package application

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

const raptorJavaHostModule = ^uint32(0)

const raptorJavaTaskInstructionBudget = uint64(250_000)

type raptorJavaMethod struct {
	className  string
	name       string
	descriptor string
	isStatic   bool
}

type raptorJavaFixedVirtualMethod struct {
	offset     uint32
	name       string
	descriptor string
}

var raptorJavaFixedVirtualMethods = map[string][]raptorJavaFixedVirtualMethod{
	"java/lang/String": {
		{offset: 0x10, name: "equals", descriptor: "(Ljava/lang/Object;)Z"},
		{offset: 0x2c, name: "length", descriptor: "()I"},
		{offset: 0x3c, name: "getBytes", descriptor: "()[B"},
		{offset: 0x74, name: "substring", descriptor: "(II)Ljava/lang/String;"},
	},
	"java/lang/StringBuffer": {
		{offset: 0x14, name: "toString", descriptor: "()Ljava/lang/String;"},
		{offset: 0x4c, name: "append", descriptor: "(Ljava/lang/String;)Ljava/lang/StringBuffer;"},
		{offset: 0x60, name: "append", descriptor: "(I)Ljava/lang/StringBuffer;"},
	},
	"java/lang/Thread": {
		{offset: 0x2c, name: "start", descriptor: "()V"},
	},
	"java/util/Random": {
		{offset: 0x34, name: "nextInt", descriptor: "()I"},
	},
	"java/util/Calendar": {
		{offset: 0x50, name: "get", descriptor: "(I)I"},
	},
}

type raptorJavaDeclaredMethod struct {
	name       string
	descriptor string
	body       uint32
	flags      uint16
}

type raptorJavaDeclaredField struct {
	name       string
	descriptor string
	index      uint32
}

type raptorJavaClass struct {
	holder      uint32
	descriptor  uint32
	name        string
	parentName  string
	fieldSize   uint32
	staticBase  uint32
	vtable      uint32
	methods     []raptorJavaDeclaredMethod
	fields      []raptorJavaDeclaredField
	hostClass   uint32
	classObject uint32
}

type raptorJavaTask struct {
	target    uint32
	procedure uint32
	context   []byte
	done      bool
}

type raptorJavaRuntime struct {
	host *ktfRuntime

	classes     map[uint32]*raptorJavaClass
	classByName map[string]*raptorJavaClass
	classOrder  []*raptorJavaClass
	hostMethods map[uint32]raptorJavaMethod
	nextMethod  uint32

	flatVirtual  []raptorJavaMethod
	lgtToKTF     map[uint32]uint32
	ktfToLGT     map[uint32]uint32
	initializing map[uint32]bool
	scratch      uint32
	jarPath      uint32

	launchRequested bool
	mainClass       string
	mainInstance    uint32
	currentCard     uint32
	dirtyCards      map[uint32]bool
	threadTargets   []uint32
	tasks           []*raptorJavaTask
}

func (r *raptorRuntime) ensureJavaRuntime() (*raptorJavaRuntime, error) {
	if r.java != nil {
		return r.java, nil
	}
	host, err := newKTFRuntimeForProfile(
		r.cpu,
		ktf.Package{
			JARName:   r.pkg.JARName,
			Client:    []byte{0},
			Files:     r.pkg.Files,
			Resources: r.pkg.Resources,
		},
		r.public.frame,
		raptorProfileID+"/java",
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Raptor Java host: %w", err)
	}
	// Raptor and its Java adapter share one guest address space. Delegate every
	// host allocation to the public runtime's allocator so copying the heap's
	// slice header cannot create independently advancing, overlapping free
	// lists.
	host.heap = guestHeap{cpu: r.cpu, shared: &r.public.heap}
	host.mapped = true
	host.deferThreads = false
	scratch, err := r.public.heap.allocate(16*4, true)
	if err != nil || scratch == 0 {
		return nil, errors.New("allocate Raptor Java call scratch")
	}
	java := &raptorJavaRuntime{
		host:         host,
		classes:      make(map[uint32]*raptorJavaClass),
		classByName:  make(map[string]*raptorJavaClass),
		hostMethods:  make(map[uint32]raptorJavaMethod),
		nextMethod:   1,
		lgtToKTF:     make(map[uint32]uint32),
		ktfToLGT:     make(map[uint32]uint32),
		initializing: make(map[uint32]bool),
		dirtyCards:   make(map[uint32]bool),
		scratch:      scratch,
		mainClass:    r.pkg.Descriptor.MainClass,
	}
	r.java = java
	return java, nil
}

func (r *raptorRuntime) destroyRaptorJava() error {
	if r == nil || r.java == nil {
		return nil
	}
	host := r.java.host
	r.java = nil
	if host == nil || host.services == nil {
		return nil
	}
	adapter, err := host.services.Coordinator.Adapter(host.serviceOwner)
	if err != nil || adapter.Lifecycle == shared.LifecycleDestroyed {
		return err
	}
	return host.services.Coordinator.Transition(
		host.serviceOwner,
		shared.LifecycleDestroyed,
		host.services.Clock.Monotonic(),
		nil,
	)
}

func (r *raptorRuntime) importStub(key raptorImportKey) (uint32, error) {
	if slot, ok := r.importSlotByKey[key]; ok {
		return raptorImportStubBase + slot*4, nil
	}
	if len(r.importSlots) >= int(raptorImportStubSize/4) {
		return 0, errors.New("Raptor import table exceeds trampoline range")
	}
	slot := uint32(len(r.importSlots))
	r.importSlots = append(r.importSlots, key)
	r.importSlotByKey[key] = slot
	return raptorImportStubBase + slot*4, nil
}

func (r *raptorRuntime) registerJavaHostMethod(method raptorJavaMethod) (uint32, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	id := java.nextMethod
	java.nextMethod++
	java.hostMethods[id] = method
	key := raptorImportKey{Module: raptorJavaHostModule, Ordinal: id}
	stub, err := r.importStub(key)
	// Synthetic Java methods are part of the resolved trampoline graph even
	// though the guest linker did not request them one by one.
	if err == nil {
		r.resolvedImports[key] = 1
	}
	return stub, err
}

func (r *raptorRuntime) dispatchJavaImport(
	ctx context.Context,
	key raptorImportKey,
) (wipiReturn, string, bool, error) {
	if key.Module == raptorJavaHostModule {
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.host", true, err
		}
		method, ok := java.hostMethods[key.Ordinal]
		if !ok {
			return wipiReturn{}, "RAPTOR.java.host", true,
				fmt.Errorf("unknown Java host method %d", key.Ordinal)
		}
		result, err := r.callJavaHostMethod(ctx, method)
		return result, "RAPTOR.java." + method.className + "." +
			method.name + method.descriptor, true, err
	}
	if key.Module == 508 || key.Module == 511 || key.Module == 513 {
		if key.Ordinal == 3 {
			return wipiReturn{}, fmt.Sprintf("RAPTOR.java.module%d.init", key.Module), true, nil
		}
		return wipiReturn{}, "", false, nil
	}
	if key.Module == 504 {
		switch key.Ordinal {
		case 22:
			return wipiReturn{}, "RAPTOR.lgte.setProperty", true, nil
		case 23:
			java, err := r.ensureJavaRuntime()
			if err != nil {
				return wipiReturn{}, "RAPTOR.lgte.getJARPath", true, err
			}
			if java.jarPath == 0 {
				java.jarPath, err = r.allocateJavaCString(r.pkg.JARName)
				if err != nil {
					return wipiReturn{}, "RAPTOR.lgte.getJARPath", true, err
				}
			}
			target, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err == nil && target != 0 {
				err = r.public.writeU32(target, java.jarPath)
			}
			return wipiReturn{}, "RAPTOR.lgte.getJARPath", true, err
		}
		return wipiReturn{}, "", false, nil
	}
	if key.Module != 100 {
		return wipiReturn{}, "", false, nil
	}
	switch key.Ordinal {
	case 3:
		_, err := r.ensureJavaRuntime()
		return wipiReturn{}, "RAPTOR.java.initialize", true, err
	case 7:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.registerClasses", true, err
		}
		classes, readErr := r.cpu.ReadRegister(cpu.RegisterR0)
		if readErr == nil {
			readErr = r.registerRaptorJavaClasses(java, classes)
		}
		return wipiReturn{low: classes}, "RAPTOR.java.registerClasses", true, readErr
	case 9:
		data, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.stringLiteral", true, err
		}
		length, err := r.cpu.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.stringLiteral", true, err
		}
		cache, err := r.cpu.ReadRegister(cpu.RegisterR3)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.stringLiteral", true, err
		}
		value, err := r.raptorJavaStringLiteral(data, length, cache)
		return wipiReturn{low: value}, "RAPTOR.java.stringLiteral", true, err
	case 20:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.linkClasses", true, err
		}
		return wipiReturn{}, "RAPTOR.java.linkClasses", true,
			r.linkRaptorJavaClasses(java)
	case 11, 12, 13:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.loadClass", true, err
		}
		holder, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.loadClass", true, err
		}
		if key.Ordinal == 13 {
			class := r.raptorJavaClassForObject(java, holder)
			if class == nil {
				return wipiReturn{}, "RAPTOR.java.initializeClass", true,
					fmt.Errorf("unknown Raptor Java class object 0x%08x", holder)
			}
			initializer, readErr := r.cpu.ReadRegister(cpu.RegisterR1)
			if readErr != nil {
				return wipiReturn{}, "RAPTOR.java.initializeClass", true, readErr
			}
			if java.initializing[holder] {
				return wipiReturn{low: holder}, "RAPTOR.java.initializeClass", true, nil
			}
			java.initializing[holder] = true
			defer delete(java.initializing, holder)
			if initializer != 0 && r.public.invokeSync != nil {
				if _, invokeErr := r.public.invokeSync(ctx, wipiGuestCallback{
					procedure: initializer,
					args:      [4]uint32{holder},
				}); invokeErr != nil {
					return wipiReturn{}, "RAPTOR.java.initializeClass", true, invokeErr
				}
			}
			if writeErr := r.writeRaptorJavaClassState(class, 5); writeErr != nil {
				return wipiReturn{}, "RAPTOR.java.initializeClass", true, writeErr
			}
			return wipiReturn{low: holder}, "RAPTOR.java.initializeClass", true, nil
		}
		if holder != 0 {
			class, inspectErr := r.inspectRaptorJavaClass(java, holder)
			if inspectErr != nil {
				return wipiReturn{}, "RAPTOR.java.loadClass", true, inspectErr
			}
			if class.vtable == 0 && len(java.flatVirtual) != 0 {
				if buildErr := r.buildRaptorJavaVTable(
					java,
					class,
					uint32(len(java.flatVirtual)),
				); buildErr != nil {
					return wipiReturn{}, "RAPTOR.java.loadClass", true, buildErr
				}
			}
			var linked [2]byte
			binary.LittleEndian.PutUint16(linked[:], 3)
			if writeErr := r.cpu.WriteMemory(class.descriptor+0x1a, linked[:]); writeErr != nil {
				return wipiReturn{}, "RAPTOR.java.loadClass", true, writeErr
			}
			if key.Ordinal == 12 {
				object, objectErr := r.ensureRaptorJavaClassObject(java, class)
				return wipiReturn{low: object}, "RAPTOR.java.loadClass", true, objectErr
			}
		}
		return wipiReturn{low: holder}, "RAPTOR.java.loadClass", true, nil
	case 130:
		return wipiReturn{}, "RAPTOR.java.setJARPath", true, nil
	case 131:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.launch", true, err
		}
		arguments, readErr := r.cpu.ReadRegister(cpu.RegisterR3)
		if readErr == nil && arguments != 0 {
			mainName, pointerErr := r.public.readU32(arguments)
			if pointerErr == nil && mainName != 0 {
				if value, stringErr := r.public.readCString(mainName); stringErr == nil && len(value) != 0 {
					java.mainClass = string(value)
				}
			}
		}
		java.launchRequested = true
		return wipiReturn{}, "RAPTOR.java.launch", true, nil
	case 14:
		size, err := r.cpu.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.alloc", true, err
		}
		address, err := r.public.heap.allocate(size, true)
		return wipiReturn{low: address}, "RAPTOR.java.alloc", true, err
	case 15:
		class, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.new", true, err
		}
		instance, err := r.newRaptorJavaObject(class)
		return wipiReturn{low: instance}, "RAPTOR.java.new", true, err
	case 16:
		element, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.newArray", true, err
		}
		count, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.newArray", true, err
		}
		array, err := r.newRaptorJavaArray(element, count)
		return wipiReturn{low: array}, "RAPTOR.java.newArray", true, err
	case 18:
		return r.checkRaptorJavaType()
	case 97, 250:
		array, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.arrayStore", true, err
		}
		index, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.arrayStore", true, err
		}
		value, err := r.cpu.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return wipiReturn{}, "RAPTOR.java.arrayStore", true, err
		}
		return wipiReturn{}, "RAPTOR.java.arrayStore", true,
			r.storeRaptorJavaArray(array, index, value)
	}
	return wipiReturn{}, "", false, nil
}

func (r *raptorRuntime) raptorJavaStringLiteral(
	address, length, cache uint32,
) (uint32, error) {
	if cache != 0 {
		value, err := r.public.readU32(cache)
		if err != nil {
			return 0, err
		}
		if value != 0 {
			return value, nil
		}
	}
	if length > 1<<20 {
		return 0, fmt.Errorf("Raptor Java string length %d exceeds limit", length)
	}
	data := make([]byte, length*2)
	if len(data) != 0 {
		if address == 0 {
			return 0, errors.New("Raptor Java string data is null")
		}
		if err := r.cpu.ReadMemory(address, data); err != nil {
			return 0, err
		}
	}
	units := make([]uint16, length)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(data[index*2:])
	}
	value, err := r.newRaptorJavaString(string(utf16.Decode(units)))
	if err != nil {
		return 0, err
	}
	if cache != 0 {
		if err := r.public.writeU32(cache, value); err != nil {
			return 0, err
		}
	}
	return value, nil
}

func (r *raptorRuntime) allocateJavaCString(value string) (uint32, error) {
	address, err := r.public.heap.allocate(uint32(len(value)+1), true)
	if err != nil || address == 0 {
		return 0, errors.New("allocate Raptor Java string")
	}
	if err := r.cpu.WriteMemory(address, append([]byte(value), 0)); err != nil {
		return 0, err
	}
	return address, nil
}

func (r *raptorRuntime) registerRaptorJavaClasses(
	java *raptorJavaRuntime,
	address uint32,
) error {
	count, err := r.public.readU32(address)
	if err != nil {
		return err
	}
	if count > 4096 {
		return fmt.Errorf("Raptor Java public class count %d exceeds limit", count)
	}
	for index := uint32(0); index < count; index++ {
		holder, err := r.public.readU32(address + 8 + index*4)
		if err != nil {
			return err
		}
		if _, err := r.inspectRaptorJavaClass(java, holder); err != nil {
			return err
		}
	}
	return nil
}

func (r *raptorRuntime) inspectRaptorJavaClass(
	java *raptorJavaRuntime,
	holder uint32,
) (*raptorJavaClass, error) {
	if class := java.classes[holder]; class != nil {
		return class, nil
	}
	descriptor, err := r.public.readU32(holder + 8)
	if err != nil || descriptor == 0 {
		return nil, fmt.Errorf("inspect Raptor Java class holder 0x%08x", holder)
	}
	nameAddress, err := r.public.readU32(descriptor + 8)
	if err != nil {
		return nil, err
	}
	nameBytes, err := r.public.readCString(nameAddress)
	if err != nil || len(nameBytes) == 0 {
		return nil, fmt.Errorf("inspect Raptor Java class name at 0x%08x", nameAddress)
	}
	parentAddress, _ := r.public.readU32(descriptor + 0x10)
	parentBytes, _ := r.public.readCString(parentAddress)
	fieldWord, _ := r.public.readU32(descriptor + 0x18)
	staticBase, _ := r.public.readU32(descriptor + 0x48)
	if staticBase > 0xffff {
		return nil, fmt.Errorf("Raptor Java class %q static base %d exceeds limit", string(nameBytes), staticBase)
	}
	vtable, _ := r.public.readU32(descriptor + 0x0c)
	class := &raptorJavaClass{
		holder:     holder,
		descriptor: descriptor,
		name:       string(nameBytes),
		parentName: string(parentBytes),
		fieldSize:  fieldWord & 0xffff,
		staticBase: staticBase,
		vtable:     vtable,
	}
	java.classes[holder] = class
	java.classByName[class.name] = class
	java.classOrder = append(java.classOrder, class)
	methods, _ := r.public.readU32(descriptor + 0x38)
	if methods != 0 {
		methodCount, readErr := r.public.readU32(methods)
		if readErr != nil || methodCount > 4096 {
			return nil, fmt.Errorf("inspect Raptor Java methods for %q", class.name)
		}
		class.methods = make([]raptorJavaDeclaredMethod, 0, methodCount)
		for index := uint32(0); index < methodCount; index++ {
			record := methods + 4 + index*28
			methodNameAddress, _ := r.public.readU32(record + 4)
			methodTypeAddress, _ := r.public.readU32(record + 8)
			flags, _ := r.public.readU32(record + 12)
			body, _ := r.public.readU32(record + 20)
			methodName, nameErr := r.public.readCString(methodNameAddress)
			methodType, typeErr := r.public.readCString(methodTypeAddress)
			if nameErr != nil || typeErr != nil || len(methodName) == 0 {
				continue
			}
			class.methods = append(class.methods, raptorJavaDeclaredMethod{
				name:       string(methodName),
				descriptor: string(methodType),
				body:       body,
				flags:      uint16(flags),
			})
		}
	}
	fields, _ := r.public.readU32(descriptor + 0x3c)
	if fields != 0 {
		fieldCount, readErr := r.public.readU32(fields)
		if readErr != nil || fieldCount > 4096 {
			return nil, fmt.Errorf("inspect Raptor Java fields for %q", class.name)
		}
		class.fields = make([]raptorJavaDeclaredField, 0, fieldCount)
		for index := uint32(0); index < fieldCount; index++ {
			record := fields + 4 + index*20
			fieldNameAddress, _ := r.public.readU32(record + 4)
			fieldTypeAddress, _ := r.public.readU32(record + 8)
			fieldIndex, _ := r.public.readU32(record + 16)
			fieldName, nameErr := r.public.readCString(fieldNameAddress)
			fieldType, typeErr := r.public.readCString(fieldTypeAddress)
			if nameErr != nil || typeErr != nil || len(fieldName) == 0 {
				continue
			}
			class.fields = append(class.fields, raptorJavaDeclaredField{
				name:       string(fieldName),
				descriptor: string(fieldType),
				index:      fieldIndex,
			})
		}
	}
	return class, nil
}

func (r *raptorRuntime) readRaptorJavaArguments(count int) ([]uint32, error) {
	arguments := make([]uint32, count)
	for index := range arguments {
		if index < 4 {
			value, err := r.cpu.ReadRegister(cpu.RegisterR0 + uint32(index))
			if err != nil {
				return nil, err
			}
			arguments[index] = value
			continue
		}
		stack, err := r.cpu.ReadRegister(cpu.RegisterSP)
		if err != nil {
			return nil, err
		}
		value, err := r.public.readU32(stack + uint32(index-4)*4)
		if err != nil {
			return nil, err
		}
		arguments[index] = value
	}
	return arguments, nil
}

func (r *raptorRuntime) linkRaptorJavaClasses(java *raptorJavaRuntime) error {
	arguments, err := r.readRaptorJavaArguments(11)
	if err != nil {
		return err
	}
	importedClasses := arguments[0]
	fields := arguments[1]
	staticFields := arguments[2]
	virtualMethods := arguments[3]
	staticMethods := arguments[5]
	staticFieldOffsets := arguments[7]
	fieldOffsets := arguments[6]
	virtualMethodOffsets := arguments[8]
	staticMethodOffsets := arguments[10]
	classCount, err := r.public.readU32(importedClasses)
	if err != nil || classCount > 4096 {
		return fmt.Errorf("inspect Raptor imported Java classes")
	}
	type importedClass struct {
		class             *raptorJavaClass
		virtualStart      uint16
		virtualCount      uint16
		staticMethodStart uint16
		staticMethodCount uint16
		staticFieldStart  uint16
		staticFieldCount  uint16
	}
	imports := make([]importedClass, 0, classCount)
	maxVirtual, maxStaticMethod, maxStaticField := uint32(0), uint32(0), uint32(0)
	for index := uint32(0); index < classCount; index++ {
		record := importedClasses + 4 + index*24
		nameAddress, _ := r.public.readU32(record)
		nameBytes, nameErr := r.public.readCString(nameAddress)
		if nameErr != nil || len(nameBytes) == 0 {
			return fmt.Errorf("inspect Raptor imported Java class %d", index)
		}
		staticFieldRange, _ := r.public.readU32(record + 8)
		virtualRange, _ := r.public.readU32(record + 12)
		staticMethodRange, _ := r.public.readU32(record + 20)
		class, err := r.ensureRaptorHostClass(java, string(nameBytes))
		if err != nil {
			return err
		}
		entry := importedClass{
			class:             class,
			staticFieldStart:  uint16(staticFieldRange),
			staticFieldCount:  uint16(staticFieldRange >> 16),
			virtualStart:      uint16(virtualRange),
			virtualCount:      uint16(virtualRange >> 16),
			staticMethodStart: uint16(staticMethodRange),
			staticMethodCount: uint16(staticMethodRange >> 16),
		}
		imports = append(imports, entry)
		maxVirtual = max(maxVirtual, uint32(entry.virtualStart)+uint32(entry.virtualCount))
		maxStaticMethod = max(maxStaticMethod, uint32(entry.staticMethodStart)+uint32(entry.staticMethodCount))
		maxStaticField = max(maxStaticField, uint32(entry.staticFieldStart)+uint32(entry.staticFieldCount))
	}
	// The generated output tables themselves give the complete virtual-method
	// count, including methods declared by the application after its imports.
	virtualCount := uint32(0)
	if staticMethodOffsets > virtualMethodOffsets {
		virtualCount = (staticMethodOffsets - virtualMethodOffsets) / 2
	}
	if virtualCount < maxVirtual || virtualCount > 4096 {
		return fmt.Errorf("invalid Raptor Java virtual method table size %d", virtualCount)
	}
	java.flatVirtual = make([]raptorJavaMethod, virtualCount)
	for index := uint32(0); index < virtualCount; index++ {
		nameAddress, _ := r.public.readU32(virtualMethods + index*8)
		typeAddress, _ := r.public.readU32(virtualMethods + index*8 + 4)
		name, _ := r.public.readCString(nameAddress)
		descriptor, _ := r.public.readCString(typeAddress)
		java.flatVirtual[index] = raptorJavaMethod{
			name: string(name), descriptor: string(descriptor),
		}
		var encoded [2]byte
		binary.LittleEndian.PutUint16(encoded[:], uint16(index*2))
		if err := r.cpu.WriteMemory(virtualMethodOffsets+index*2, encoded[:]); err != nil {
			return err
		}
	}
	for _, entry := range imports {
		for offset := uint32(0); offset < uint32(entry.virtualCount); offset++ {
			index := uint32(entry.virtualStart) + offset
			java.flatVirtual[index].className = entry.class.name
		}
		if err := r.buildRaptorJavaVTable(java, entry.class, virtualCount); err != nil {
			return err
		}
		for offset := uint32(0); offset < uint32(entry.staticMethodCount); offset++ {
			index := uint32(entry.staticMethodStart) + offset
			method := raptorJavaMethod{className: entry.class.name, isStatic: true}
			nameAddress, _ := r.public.readU32(staticMethods + index*8)
			typeAddress, _ := r.public.readU32(staticMethods + index*8 + 4)
			name, _ := r.public.readCString(nameAddress)
			descriptor, _ := r.public.readCString(typeAddress)
			method.name, method.descriptor = string(name), string(descriptor)
			if offset < 2 || method.name == "" {
				method.name = "<class>"
				method.descriptor = "()V"
			}
			if method.name == "<init>" {
				method.isStatic = false
			}
			stub, err := r.registerJavaHostMethod(method)
			if err != nil {
				return err
			}
			if err := r.public.writeU32(staticMethodOffsets+index*4, stub|1); err != nil {
				return err
			}
		}
	}
	// Imported static fields are represented as signed offsets by the AOT ABI.
	// The three fields used by this family are Font constants; keeping their
	// indices stable lets the companion value table be addressed correctly.
	for index := uint32(0); index < maxStaticField; index++ {
		var encoded [2]byte
		binary.LittleEndian.PutUint16(encoded[:], uint16(index))
		if err := r.cpu.WriteMemory(staticFieldOffsets+index*2, encoded[:]); err != nil {
			return err
		}
		_, _ = r.public.readU32(staticFields + index*8)
	}
	if bss, ok := r.pkg.Image.ZeroSection(); ok &&
		fieldOffsets >= bss.Address && fieldOffsets < bss.Address+bss.Size {
		fieldCount := (bss.Address + bss.Size - fieldOffsets) / 2
		if fieldCount > 4096 {
			return fmt.Errorf("invalid Raptor Java field table size %d", fieldCount)
		}
		previousFieldIndex := uint32(0)
		previousFieldWide := false
		for index := uint32(0); index < fieldCount; index++ {
			nameAddress, _ := r.public.readU32(fields + index*8)
			typeAddress, _ := r.public.readU32(fields + index*8 + 4)
			name, _ := r.public.readCString(nameAddress)
			descriptor, _ := r.public.readCString(typeAddress)
			fieldIndex, wide := raptorJavaLinkedFieldIndex(
				java,
				string(name),
				string(descriptor),
				previousFieldIndex,
				previousFieldWide,
			)
			previousFieldIndex, previousFieldWide = fieldIndex, wide
			var encoded [2]byte
			binary.LittleEndian.PutUint16(encoded[:], uint16(fieldIndex))
			if err := r.cpu.WriteMemory(fieldOffsets+index*2, encoded[:]); err != nil {
				return err
			}
		}
	}
	for _, class := range java.classes {
		if class.hostClass == 0 {
			if err := r.buildRaptorJavaVTable(java, class, virtualCount); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *raptorRuntime) ensureRaptorHostClass(
	java *raptorJavaRuntime,
	name string,
) (*raptorJavaClass, error) {
	if class := java.classByName[name]; class != nil {
		return class, nil
	}
	hostClass, err := java.host.ensureJavaClass(name)
	if err != nil {
		return nil, err
	}
	hostInfo, err := java.host.inspectJavaClass(hostClass)
	if err != nil {
		return nil, err
	}
	parentName := ""
	if hostInfo.Parent != 0 {
		parent, parentErr := java.host.inspectJavaClass(hostInfo.Parent)
		if parentErr != nil {
			return nil, parentErr
		}
		parentName = parent.Name
	}
	fieldWords := (uint32(hostInfo.FieldSize) + 3) / 4
	holder, err := r.public.heap.allocate(12, true)
	if err != nil || holder == 0 {
		return nil, errors.New("allocate Raptor Java host class holder")
	}
	descriptor, err := r.public.heap.allocate(0x4c, true)
	if err != nil || descriptor == 0 {
		return nil, errors.New("allocate Raptor Java host class descriptor")
	}
	nameAddress, err := r.allocateJavaCString(name)
	if err != nil {
		return nil, err
	}
	parentAddress, err := r.allocateJavaCString(parentName)
	if err != nil {
		return nil, err
	}
	if err := r.public.writeU32(holder+8, descriptor); err != nil {
		return nil, err
	}
	for offset, value := range map[uint32]uint32{
		0: 0x21, 8: nameAddress, 0x10: parentAddress,
		0x18: fieldWords,
	} {
		if err := r.public.writeU32(descriptor+offset, value); err != nil {
			return nil, err
		}
	}
	class := &raptorJavaClass{
		holder: holder, descriptor: descriptor, name: name,
		parentName: parentName, fieldSize: fieldWords,
		hostClass: hostClass,
	}
	java.classes[holder] = class
	java.classByName[name] = class
	java.classOrder = append(java.classOrder, class)
	return class, nil
}

func raptorJavaLinkedFieldIndex(
	java *raptorJavaRuntime,
	name, descriptor string,
	previousIndex uint32,
	previousWide bool,
) (uint32, bool) {
	wide := descriptor == "J" || descriptor == "D"
	if name == "" && descriptor == "" && previousWide {
		// The AOT field table emits an unnamed companion entry for the second
		// word of every long or double instance field.
		return previousIndex + 1, false
	}
	fieldIndex := uint32(0)
	// Class registration order is stable. Several obfuscated applications use
	// the same short field name in more than one class, so ranging over the
	// holder map would otherwise make linking depend on Go's map iteration.
	for _, class := range java.classOrder {
		for _, declared := range class.fields {
			if declared.name == name && declared.descriptor == descriptor {
				fieldIndex = declared.index
				break
			}
		}
	}
	return fieldIndex, wide
}

func (r *raptorRuntime) buildRaptorJavaVTable(
	java *raptorJavaRuntime,
	class *raptorJavaClass,
	count uint32,
) error {
	if count == 0 {
		return nil
	}
	vtable, err := r.public.heap.allocate(count*8, true)
	if err != nil || vtable == 0 {
		return errors.New("allocate Raptor Java vtable")
	}
	for index, method := range java.flatVirtual {
		procedure := uint32(0)
		if method.name != "" && r.raptorClassImplements(java, class, method.className) {
			procedure, err = r.registerJavaHostMethod(raptorJavaMethod{
				className: method.className, name: method.name,
				descriptor: method.descriptor,
			})
			if err != nil {
				return err
			}
		}
		for _, declared := range class.methods {
			if declared.name == method.name && declared.descriptor == method.descriptor {
				procedure = declared.body &^ 1
				break
			}
		}
		if procedure != 0 {
			if err := r.public.writeU32(vtable+uint32(index)*8+4, procedure|1); err != nil {
				return err
			}
		}
	}
	for _, method := range raptorJavaFixedVirtualMethods[class.name] {
		procedure, registerErr := r.registerJavaHostMethod(raptorJavaMethod{
			className: class.name,
			name:      method.name, descriptor: method.descriptor,
		})
		if registerErr != nil {
			return registerErr
		}
		if err := r.public.writeU32(vtable+method.offset, procedure|1); err != nil {
			return err
		}
	}
	class.vtable = vtable
	if err := r.public.writeU32(class.descriptor+0x0c, vtable); err != nil {
		return err
	}
	return r.syncRaptorJavaVTables(java)
}

// syncRaptorJavaVTables publishes linked class tables to host objects that
// were mirrored before the imported Java class graph finished linking.
func (r *raptorRuntime) syncRaptorJavaVTables(java *raptorJavaRuntime) error {
	for instance := range java.lgtToKTF {
		holder, err := r.public.readU32(instance + 4)
		if err != nil {
			return err
		}
		class := java.classes[holder]
		if class == nil || class.vtable == 0 {
			continue
		}
		if err := r.public.writeU32(instance, class.vtable); err != nil {
			return err
		}
	}
	return nil
}

func (r *raptorRuntime) raptorClassImplements(
	java *raptorJavaRuntime,
	class *raptorJavaClass,
	wanted string,
) bool {
	if wanted == "" {
		return false
	}
	for depth := 0; class != nil && depth < 256; depth++ {
		if class.name == wanted {
			return true
		}
		if spec, ok := ktfHostJavaClassSpecs[class.parentName]; ok {
			for name := class.parentName; name != ""; {
				if name == wanted {
					return true
				}
				next, exists := ktfHostJavaClassSpecs[name]
				if !exists {
					break
				}
				name = next.parent
			}
			_ = spec
			return false
		}
		class = java.classByName[class.parentName]
	}
	return false
}

func (r *raptorRuntime) newRaptorJavaObject(holder uint32) (uint32, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	class := r.raptorJavaClassForObject(java, holder)
	if class != nil {
		holder = class.holder
	}
	class, err = r.inspectRaptorJavaClass(java, holder)
	if err != nil {
		if host := java.classes[holder]; host != nil {
			class = host
		} else {
			return 0, err
		}
	}
	if class.vtable == 0 && len(java.flatVirtual) != 0 {
		if err := r.buildRaptorJavaVTable(java, class, uint32(len(java.flatVirtual))); err != nil {
			return 0, err
		}
	}
	instance, err := r.public.heap.allocate(12, true)
	if err != nil || instance == 0 {
		return 0, errors.New("allocate Raptor Java object")
	}
	fields, err := r.public.heap.allocate(max(uint32(4), class.fieldSize*4), true)
	if err != nil || fields == 0 {
		return 0, errors.New("allocate Raptor Java object fields")
	}
	if err := r.public.writeU32(instance, class.vtable); err != nil {
		return 0, err
	}
	if err := r.public.writeU32(instance+4, holder); err != nil {
		return 0, err
	}
	if err := r.public.writeU32(instance+8, fields); err != nil {
		return 0, err
	}
	hostName := class.name
	if class.hostClass == 0 {
		hostName = class.parentName
	}
	for hostName != "" && java.classByName[hostName] != nil &&
		java.classByName[hostName].hostClass == 0 {
		hostName = java.classByName[hostName].parentName
	}
	if hostName == "" {
		hostName = "java/lang/Object"
	}
	hostClass, err := java.host.ensureJavaClass(hostName)
	if err != nil {
		return 0, err
	}
	hostInfo, err := java.host.inspectJavaClass(hostClass)
	if err != nil {
		return 0, err
	}
	mirror, err := java.host.newJavaInstanceForClass(hostInfo)
	if err != nil {
		return 0, err
	}
	java.lgtToKTF[instance] = mirror
	java.ktfToLGT[mirror] = instance
	if class.vtable != 0 {
		if err := r.public.writeU32(instance, class.vtable); err != nil {
			return 0, err
		}
	}
	return instance, nil
}

func (r *raptorRuntime) ensureRaptorJavaClassObject(
	java *raptorJavaRuntime,
	class *raptorJavaClass,
) (uint32, error) {
	if class.classObject != 0 {
		return class.classObject, nil
	}
	object, err := r.public.heap.allocate(12, true)
	if err != nil || object == 0 {
		return 0, errors.New("allocate Raptor Java class object")
	}
	// Class data includes eight VM bookkeeping words followed by storage whose
	// high-water mark is described independently from instance field size.
	// AOT code addresses static fields through staticBase; ignoring it lets a
	// large class overwrite the allocation that follows its class object.
	dataWords := max(class.fieldSize, class.staticBase) + 8
	data, err := r.public.heap.allocate(dataWords*4, true)
	if err != nil || data == 0 {
		return 0, errors.New("allocate Raptor Java class data")
	}
	if err := r.public.writeU32(object+4, class.holder); err != nil {
		return 0, err
	}
	if err := r.public.writeU32(object+8, data); err != nil {
		return 0, err
	}
	class.classObject = object
	if err := r.writeRaptorJavaClassState(class, 3); err != nil {
		return 0, err
	}
	return object, nil
}

func (r *raptorRuntime) writeRaptorJavaClassState(
	class *raptorJavaClass,
	state uint16,
) error {
	if class.classObject == 0 {
		return errors.New("Raptor Java class object is absent")
	}
	data, err := r.public.readU32(class.classObject + 8)
	if err != nil {
		return err
	}
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], state)
	return r.cpu.WriteMemory(data+0x10, encoded[:])
}

func (r *raptorRuntime) raptorJavaClassForObject(
	java *raptorJavaRuntime,
	object uint32,
) *raptorJavaClass {
	if object == 0 {
		return nil
	}
	if class := java.classes[object]; class != nil {
		return class
	}
	for _, class := range java.classes {
		if class.classObject != 0 && class.classObject == object {
			return class
		}
	}
	holder, err := r.public.readU32(object + 4)
	if err != nil {
		return nil
	}
	return java.classes[holder]
}

func (r *raptorRuntime) newRaptorJavaArray(element, count uint32) (uint32, error) {
	if count > 1<<24 {
		return 0, fmt.Errorf("Raptor Java array length %d exceeds limit", count)
	}
	elementSize := uint32(4)
	if element != 0 && element <= 0x100 {
		switch byte(element) {
		case 'Z', 'B':
			elementSize = 1
		case 'C', 'S':
			elementSize = 2
		case 'J', 'D':
			elementSize = 8
		}
	}
	instance, err := r.public.heap.allocate(12, true)
	if err != nil || instance == 0 {
		return 0, errors.New("allocate Raptor Java array")
	}
	body, err := r.public.heap.allocate(4+count*elementSize, true)
	if err != nil || body == 0 {
		return 0, errors.New("allocate Raptor Java array body")
	}
	if err := r.public.writeU32(instance+8, body); err != nil {
		return 0, err
	}
	if err := r.public.writeU32(body, count); err != nil {
		return 0, err
	}
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	className := "[Ljava/lang/Object;"
	if element != 0 && element <= 0x100 {
		className = "[" + string(byte(element))
	}
	mirror, err := java.host.newJavaArray(className, count, elementSize)
	if err != nil {
		return 0, err
	}
	java.lgtToKTF[instance] = mirror
	java.ktfToLGT[mirror] = instance
	return instance, nil
}

func (r *raptorRuntime) storeRaptorJavaArray(
	array, index, value uint32,
) error {
	if array == 0 {
		return errors.New("Raptor Java array is null")
	}
	body, err := r.public.readU32(array + 8)
	if err != nil || body == 0 {
		return errors.New("Raptor Java array body is null")
	}
	length, err := r.public.readU32(body)
	if err != nil {
		return err
	}
	if index >= length {
		return fmt.Errorf("Raptor Java array index %d exceeds length %d", index, length)
	}
	if err := r.public.writeU32(body+4+index*4, value); err != nil {
		return err
	}
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return err
	}
	if mirror := java.lgtToKTF[array]; mirror != 0 {
		mirrorWords, readErr := java.host.readWords(mirror, 2)
		if readErr != nil {
			return readErr
		}
		if mapped := java.lgtToKTF[value]; mapped != 0 {
			value = mapped
		}
		return java.host.writeU32(mirrorWords[0]+8+index*4, value)
	}
	return nil
}

func (r *raptorRuntime) checkRaptorJavaType() (wipiReturn, string, bool, error) {
	instance, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return wipiReturn{}, "RAPTOR.java.checkType", true, err
	}
	if instance == 0 {
		return wipiReturn{}, "RAPTOR.java.checkType", true, nil
	}
	return wipiReturn{low: 1}, "RAPTOR.java.checkType", true, nil
}

func (r *raptorRuntime) callJavaHostMethod(
	ctx context.Context,
	method raptorJavaMethod,
) (wipiReturn, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return wipiReturn{}, err
	}
	if method.name == "<class>" {
		class := java.classByName[method.className]
		if class == nil {
			return wipiReturn{}, fmt.Errorf(
				"Raptor Java host class %q is absent",
				method.className,
			)
		}
		return wipiReturn{low: class.holder}, nil
	}
	argumentCount := raptorJavaDescriptorArgumentCount(method.descriptor)
	if !method.isStatic {
		argumentCount++
	}
	arguments, err := r.readRaptorJavaArguments(argumentCount)
	if err != nil {
		return wipiReturn{}, err
	}
	if !method.isStatic && len(arguments) != 0 {
		receiver := arguments[0]
		switch method.className + "." + method.name + method.descriptor {
		case "org/kwis/msp/lcdui/Card.repaint()V",
			"org/kwis/msp/lcdui/Card.repaint(IIII)V":
			java.dirtyCards[receiver] = true
			return wipiReturn{}, nil
		case "org/kwis/msp/lcdui/Card.serviceRepaints()V":
			if !java.dirtyCards[receiver] {
				return wipiReturn{}, nil
			}
			delete(java.dirtyCards, receiver)
			return wipiReturn{}, r.paintRaptorJavaCard(ctx, java, receiver)
		case "org/kwis/msp/lcdui/Display.getDockedCard()Lorg/kwis/msp/lcdui/Card;":
			return wipiReturn{low: java.currentCard}, nil
		case "org/kwis/msp/lcdui/Display.pushCard(Lorg/kwis/msp/lcdui/Card;)V":
			if len(arguments) < 2 {
				return wipiReturn{}, errors.New("Raptor Java pushCard has no card")
			}
			java.currentCard = arguments[1]
			if java.currentCard == 0 {
				return wipiReturn{}, nil
			}
			return wipiReturn{}, r.paintRaptorJavaCard(ctx, java, java.currentCard)
		case "java/lang/Thread.start()V":
			target := uint32(0)
			if mirror := java.lgtToKTF[receiver]; mirror != 0 {
				target = java.ktfToLGT[java.host.threadTargets[mirror]]
			}
			if target == 0 {
				target = receiver
			}
			java.threadTargets = append(java.threadTargets, target)
			class := r.raptorJavaClassForObject(java, target)
			for depth := 0; class != nil && depth < 256; depth++ {
				if run, found := raptorDeclaredMethod(class, "run", "()V"); found && run.body != 0 {
					java.tasks = append(java.tasks, &raptorJavaTask{
						target: target, procedure: run.body,
					})
					break
				}
				class = java.classByName[class.parentName]
			}
			return wipiReturn{}, nil
		}
	}
	for index, value := range arguments {
		if mirror := java.lgtToKTF[value]; mirror != 0 {
			arguments[index] = mirror
		}
	}
	data := make([]byte, len(arguments)*4)
	for index, value := range arguments {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	if err := r.cpu.WriteMemory(java.scratch, data); err != nil {
		return wipiReturn{}, err
	}
	parameterBase := java.host.nativeParameterBase
	java.host.nativeParameterBase = java.scratch
	value, callErr := ktfHostJavaMethod(
		method.className,
		method.name,
		method.descriptor,
	)(ctx, java.host)
	java.host.nativeParameterBase = parameterBase
	if callErr != nil {
		return wipiReturn{}, callErr
	}
	if raptorJavaDescriptorReturnsReference(method.descriptor) && value != 0 {
		value, err = r.wrapRaptorJavaObject(java, value)
		if err != nil {
			return wipiReturn{}, err
		}
	}
	return wipiReturn{low: value, high: java.host.javaReturnHigh}, nil
}

func (r *raptorRuntime) paintRaptorJavaCard(
	ctx context.Context,
	java *raptorJavaRuntime,
	card uint32,
) error {
	class := r.raptorJavaClassForObject(java, card)
	var paint raptorJavaDeclaredMethod
	found := false
	for depth := 0; class != nil && depth < 256; depth++ {
		if paint, found = raptorDeclaredMethod(
			class,
			"paint",
			"(Lorg/kwis/msp/lcdui/Graphics;)V",
		); found {
			break
		}
		class = java.classByName[class.parentName]
	}
	if !found || paint.body == 0 {
		return nil
	}
	graphicsMirror, err := java.host.ensureScreenGraphics()
	if err != nil {
		return err
	}
	java.host.resetScreenGraphics(graphicsMirror)
	graphics, err := r.wrapRaptorJavaObject(java, graphicsMirror)
	if err != nil {
		return err
	}
	if r.public.invokeSync == nil {
		return errors.New("Raptor Java callback bridge is unavailable")
	}
	if _, err := r.public.invokeSync(ctx, wipiGuestCallback{
		procedure: paint.body,
		args:      [4]uint32{card, graphics},
	}); err != nil {
		return err
	}
	return java.host.recordPresentation()
}

func (m *Machine) stepRaptorJavaTask(ctx context.Context) (cpu.Result, bool, error) {
	runtime := m.raptor
	if runtime == nil || runtime.java == nil {
		return cpu.Result{}, false, nil
	}
	java := runtime.java
	var task *raptorJavaTask
	for _, candidate := range java.tasks {
		if !candidate.done {
			task = candidate
			break
		}
	}
	if task == nil {
		return cpu.Result{}, false, nil
	}
	outer, err := m.cpu.SaveContext()
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	defer func() { _ = m.cpu.RestoreContext(outer) }()
	if len(task.context) == 0 {
		for register := cpu.RegisterR0; register <= cpu.RegisterR12; register++ {
			if err := m.cpu.WriteRegister(register, 0); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
			}
		}
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0:   task.target,
			cpu.RegisterSP:   DefaultStackBase + DefaultStackSize - 0x20000,
			cpu.RegisterLR:   returnSentinel | 1,
			cpu.RegisterPC:   task.procedure &^ 1,
			cpu.RegisterCPSR: modeStatus(task.procedure),
		} {
			if err := m.cpu.WriteRegister(register, value); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
			}
		}
	} else if err := m.cpu.RestoreContext(task.context); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	pc, err := m.cpu.ReadRegister(cpu.RegisterPC)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	status, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, true, err
	}
	mode := cpu.ModeARM
	if status&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	result := m.runWIPISlice(
		ctx,
		pc,
		mode,
		raptorJavaTaskInstructionBudget,
		true,
	)
	if result.Err != nil {
		registers := make([]uint32, cpu.RegisterR12+1)
		for register := range registers {
			registers[register], _ = m.cpu.ReadRegister(uint32(register))
		}
		sp, _ := m.cpu.ReadRegister(cpu.RegisterSP)
		lr, _ := m.cpu.ReadRegister(cpu.RegisterLR)
		result.Err = fmt.Errorf(
			"%w (r0-r12=%08x sp=%08x lr=%08x)",
			result.Err,
			registers,
			sp,
			lr,
		)
		return result, true, result.Err
	}
	if result.Reason == cpu.StopBreakpoint && result.PC >= 2 &&
		result.PC-2 == returnSentinel {
		task.done = true
		return result, true, nil
	}
	task.context, err = m.cpu.SaveContext()
	if err != nil {
		result.Reason = cpu.StopFault
		result.Err = err
		return result, true, err
	}
	return result, true, nil
}

func (r *raptorRuntime) raptorJavaInputCallback(
	event machinecore.InputEvent,
) (wipiGuestCallback, bool) {
	if r.java == nil || r.java.currentCard == 0 {
		return wipiGuestCallback{}, false
	}
	key, ok := inputKeyCode(event.Control)
	if !ok {
		return wipiGuestCallback{}, false
	}
	class := r.raptorJavaClassForObject(r.java, r.java.currentCard)
	for depth := 0; class != nil && depth < 256; depth++ {
		method, found := raptorDeclaredMethod(class, "keyNotify", "(II)Z")
		if found && method.body != 0 {
			eventType := ktfKeyReleased
			if event.Pressed {
				eventType = ktfKeyPressed
			}
			return wipiGuestCallback{
				procedure: method.body,
				args: [4]uint32{
					r.java.currentCard,
					eventType,
					uint32(int32(key)),
				},
			}, true
		}
		class = r.java.classByName[class.parentName]
	}
	return wipiGuestCallback{}, false
}

func (r *raptorRuntime) wrapRaptorJavaObject(
	java *raptorJavaRuntime,
	mirror uint32,
) (uint32, error) {
	if instance := java.ktfToLGT[mirror]; instance != 0 {
		return instance, nil
	}
	words, err := java.host.readWords(mirror, 2)
	if err != nil {
		return 0, err
	}
	hostClass, err := java.host.inspectJavaClass(words[1])
	if err != nil {
		return 0, err
	}
	class, err := r.ensureRaptorHostClass(java, hostClass.Name)
	if err != nil {
		return 0, err
	}
	if class.vtable == 0 && len(java.flatVirtual) != 0 {
		if err := r.buildRaptorJavaVTable(java, class, uint32(len(java.flatVirtual))); err != nil {
			return 0, err
		}
	}
	instance, err := r.public.heap.allocate(12, true)
	if err != nil || instance == 0 {
		return 0, errors.New("allocate Raptor Java host wrapper")
	}
	fields, err := r.public.heap.allocate(max(uint32(4), class.fieldSize*4), true)
	if err != nil || fields == 0 {
		return 0, errors.New("allocate Raptor Java host wrapper fields")
	}
	for offset, value := range map[uint32]uint32{
		0: class.vtable, 4: class.holder, 8: fields,
	} {
		if err := r.public.writeU32(instance+offset, value); err != nil {
			return 0, err
		}
	}
	java.lgtToKTF[instance] = mirror
	java.ktfToLGT[mirror] = instance
	if class.vtable != 0 {
		if err := r.public.writeU32(instance, class.vtable); err != nil {
			return 0, err
		}
	}
	return instance, nil
}

func (r *raptorRuntime) newRaptorJavaString(value string) (uint32, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	mirror, err := java.host.newJavaString(value)
	if err != nil {
		return 0, err
	}
	return r.wrapRaptorJavaObject(java, mirror)
}

func (r *raptorRuntime) newRaptorJavaReferenceArray(
	elements []uint32,
) (uint32, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	array, err := r.newRaptorJavaArray(0x100, uint32(len(elements)))
	if err != nil {
		return 0, err
	}
	body, err := r.public.readU32(array + 8)
	if err != nil {
		return 0, err
	}
	for index, element := range elements {
		if err := r.public.writeU32(body+4+uint32(index)*4, element); err != nil {
			return 0, err
		}
	}
	mirror := java.lgtToKTF[array]
	mirrorWords, err := java.host.readWords(mirror, 2)
	if err != nil {
		return 0, err
	}
	for index, element := range elements {
		if mapped := java.lgtToKTF[element]; mapped != 0 {
			element = mapped
		}
		if err := java.host.writeU32(
			mirrorWords[0]+8+uint32(index)*4,
			element,
		); err != nil {
			return 0, err
		}
	}
	return array, nil
}

func (m *Machine) startRaptorJava(ctx context.Context) error {
	runtime := m.raptor
	java := runtime.java
	if java == nil || !java.launchRequested || java.mainInstance != 0 {
		return nil
	}
	class := java.classByName[java.mainClass]
	if class == nil {
		return fmt.Errorf("Raptor Java main class %q was not registered", java.mainClass)
	}
	instance, err := runtime.newRaptorJavaObject(class.holder)
	if err != nil {
		return fmt.Errorf("allocate Raptor Java main class %q: %w", class.name, err)
	}
	constructor, ok := raptorDeclaredMethod(class, "<init>", "()V")
	if !ok || constructor.body == 0 {
		return fmt.Errorf("Raptor Java main class %q has no default constructor", class.name)
	}
	result, _, err := m.invokeWIPICallback(ctx, wipiGuestCallback{
		procedure: constructor.body,
		args:      [4]uint32{instance},
	})
	if err != nil {
		return fmt.Errorf(
			"construct Raptor Java main class %q at PC 0x%08x after %d instructions: %w",
			class.name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	values := []string{class.name, "", "true", "true"}
	strings := make([]uint32, len(values))
	for index, value := range values {
		strings[index], err = runtime.newRaptorJavaString(value)
		if err != nil {
			return err
		}
	}
	arguments, err := runtime.newRaptorJavaReferenceArray(strings)
	if err != nil {
		return err
	}
	start, ok := raptorDeclaredMethod(
		class,
		"startApp",
		"([Ljava/lang/String;)V",
	)
	if !ok || start.body == 0 {
		return fmt.Errorf("Raptor Java main class %q has no startApp(String[])", class.name)
	}
	result, _, err = m.invokeWIPICallback(ctx, wipiGuestCallback{
		procedure: start.body,
		args:      [4]uint32{instance, arguments},
	})
	if err != nil {
		return fmt.Errorf(
			"start Raptor Java main class %q at PC 0x%08x after %d instructions: %w",
			class.name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	java.mainInstance = instance
	return runtime.syncRaptorJavaVTables(java)
}

func raptorDeclaredMethod(
	class *raptorJavaClass,
	name, descriptor string,
) (raptorJavaDeclaredMethod, bool) {
	for _, method := range class.methods {
		if method.name == name && method.descriptor == descriptor {
			return method, true
		}
	}
	return raptorJavaDeclaredMethod{}, false
}

func raptorJavaDescriptorArgumentCount(descriptor string) int {
	start := strings.IndexByte(descriptor, '(')
	end := strings.IndexByte(descriptor, ')')
	if start < 0 || end < start {
		return 0
	}
	count := 0
	for index := start + 1; index < end; count++ {
		switch descriptor[index] {
		case 'L':
			separator := strings.IndexByte(descriptor[index:end], ';')
			if separator < 0 {
				return count
			}
			index += separator + 1
		case '[':
			for index < end && descriptor[index] == '[' {
				index++
			}
			if index < end && descriptor[index] == 'L' {
				separator := strings.IndexByte(descriptor[index:end], ';')
				if separator < 0 {
					return count
				}
				index += separator + 1
			} else {
				index++
			}
		default:
			index++
		}
	}
	return count
}

func raptorJavaDescriptorReturnsReference(descriptor string) bool {
	end := strings.LastIndexByte(descriptor, ')')
	return end >= 0 && end+1 < len(descriptor) &&
		(descriptor[end+1] == 'L' || descriptor[end+1] == '[')
}
