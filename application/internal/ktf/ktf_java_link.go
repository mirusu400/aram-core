package ktf

import (
	"context"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
)

func (r *Runtime) ensureJavaVTableIndex(
	classAddress uint32,
	vtableAddress uint32,
) (uint32, error) {
	if vtableAddress != 0 {
		r.javaVTableClasses[vtableAddress] = classAddress
	}
	if index, ok := r.javaVTables[classAddress]; ok {
		if err := r.writeJavaVTable(index, vtableAddress); err != nil {
			return 0, err
		}
		return index, nil
	}
	if len(r.javaVTables) >= 128 {
		return 0, errors.New("KTF Java vtable registry exhausted")
	}
	index := uint32(len(r.javaVTables))
	r.javaVTables[classAddress] = index
	if err := r.writeJavaVTable(index, vtableAddress); err != nil {
		return 0, err
	}
	return index, nil
}

func (r *Runtime) writeJavaVTable(index, address uint32) error {
	if index >= 128 {
		return fmt.Errorf("KTF Java vtable index %d exceeds registry", index)
	}
	if r.JvmContext == 0 {
		return nil
	}
	return r.WriteU32(r.JvmContext+12+index*4, address)
}

func (r *Runtime) rebuildHostJavaVTable(classAddress uint32) error {
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return err
	}
	hierarchy := make([]JavaClass, 0, 8)
	for depth := 0; ; depth++ {
		if depth >= 256 {
			return fmt.Errorf("Java class hierarchy for %q exceeds limit", class.Name)
		}
		hierarchy = append(hierarchy, class)
		if class.Parent == 0 {
			break
		}
		class, err = r.InspectJavaClass(class.Parent)
		if err != nil {
			return err
		}
	}
	methods := make([]JavaMethod, 0)
	for index := len(hierarchy) - 1; index >= 0; index-- {
		for _, method := range hierarchy[index].Methods {
			if _, compatibilitySlot := r.hostJavaVirtualSlots[method.Address]; compatibilitySlot {
				continue
			}
			replaced := false
			for current := range methods {
				if methods[current].Name == method.Name &&
					methods[current].Descriptor == method.Descriptor {
					methods[current] = method
					replaced = true
					break
				}
			}
			if !replaced {
				methods = append(methods, method)
			}
		}
	}
	logicalSize := uint32(len(methods))
	for _, slot := range r.hostJavaVirtualSlots {
		if size := uint32(slot) + 1; size > logicalSize {
			logicalSize = size
		}
	}
	capacity := uint32(len(methods) + 1)
	if existing := r.javaVTableCapacity[classAddress]; existing > capacity {
		capacity = existing
	}
	if capacity < logicalSize {
		capacity = ktfHostVirtualTableReserve
		for capacity < logicalSize {
			if capacity > uint32(^uint16(0))/2 {
				capacity = logicalSize
				break
			}
			capacity *= 2
		}
	}
	entries := make([]uint32, capacity)
	for index, method := range methods {
		entries[index] = method.Address
		flags, err := r.ReadU32(method.Address + 20)
		if err != nil {
			return err
		}
		if err := r.WriteU32(
			method.Address+20,
			flags&0xffff0000|uint32(index),
		); err != nil {
			return err
		}
	}
	for methodAddress, slot := range r.hostJavaVirtualSlots {
		if entries[slot] != 0 && entries[slot] != methodAddress {
			method, inspectErr := r.InspectJavaMethod(methodAddress)
			if inspectErr != nil {
				return inspectErr
			}
			compatible, hierarchyErr := r.javaClassExtends(
				classAddress,
				method.DeclaringClass,
			)
			if hierarchyErr != nil {
				return hierarchyErr
			}
			if !compatible {
				r.tracef(
					"java_compat_vtable_collision:class=%s:slot=%d:"+
						"guest=0x%08x:host=0x%08x:preserved",
					hierarchy[0].Name,
					slot,
					entries[slot],
					methodAddress,
				)
				continue
			}
		}
		entries[slot] = methodAddress
		flags, err := r.ReadU32(methodAddress + 20)
		if err != nil {
			return err
		}
		if err := r.WriteU32(
			methodAddress+20,
			flags&0xffff0000|uint32(slot),
		); err != nil {
			return err
		}
	}
	vtable, err := r.AllocateWords(uint32(len(entries)))
	if err != nil {
		return err
	}
	if err := r.writeWords(vtable, entries); err != nil {
		return err
	}
	if err := r.WriteU32(classAddress+12, vtable); err != nil {
		return err
	}
	classFlags, err := r.ReadU32(classAddress + 16)
	if err != nil {
		return err
	}
	if err := r.WriteU32(
		classAddress+16,
		classFlags&0xffff0000|logicalSize,
	); err != nil {
		return err
	}
	r.javaVTableCapacity[classAddress] = capacity
	vtableIndex, err := r.ensureJavaVTableIndex(classAddress, vtable)
	if err == nil {
		// KTF AOT code can use a class definition itself as the receiver for
		// framework-owned methods. ptr_next points at this word, so encode the
		// class vtable in the same form used by an instance fields header.
		err = r.WriteU32(classAddress+4, (vtableIndex*4)<<5)
	}
	if err == nil {
		r.tracef(
			"java_vtable:%s:class=0x%08x:slot=%d@0x%08x[%d]",
			hierarchy[0].Name,
			classAddress,
			vtableIndex,
			vtable,
			len(methods),
		)
	}
	return err
}

func (r *Runtime) buildKnlInterface() (uint32, error) {
	if r.knlInterface != 0 {
		return r.knlInterface, nil
	}
	const slotCount = 65
	slots := make([]uint32, slotCount)
	for index := range slots {
		handler := ktfNoop
		switch index {
		case 1:
			handler = ktfKernelSprintk
		case 20:
			handler = ktfKernelAllocate(false)
		case 21:
			handler = ktfKernelAllocate(true)
		case 22:
			handler = ktfKernelFree
		case 23:
			handler = ktfTotalMemory
		case 24:
			handler = ktfFreeMemory
		case 25:
			handler = ktfKernelDefineTimer
		case 26:
			handler = ktfKernelSetTimer
		case 27:
			handler = ktfKernelUnsetTimer
		case 28:
			handler = ktfKernelCurrentTime
		case 29:
			handler = ktfKernelGetSystemProperty
		case 30:
			handler = ktfKernelSetSystemProperty
		case 31:
			handler = ktfGetResourceID
		case 32:
			handler = ktfGetResource
		case 33:
			handler = ktfGetWIPICInterface
		case 36:
			handler = ktfKernelGetDLLInterface
		}
		slots[index] = r.RegisterHostCall(
			fmt.Sprintf("wipic.knl.%d", index),
			handler,
		)
	}
	address, err := r.AllocateWords(slotCount)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, slots); err != nil {
		return 0, err
	}
	r.knlInterface = address
	return address, nil
}

func (r *Runtime) buildJBInterface() (uint32, error) {
	if r.jbInterface != 0 {
		return r.jbInterface, nil
	}
	const slotCount = 13
	slots := make([]uint32, slotCount)
	for index := 1; index < slotCount; index++ {
		handler := ktfUnsupportedJavaCallback(fmt.Sprintf("java bridge slot %d", index))
		switch index {
		case 1:
			handler = ktfJavaJump(1)
		case 2:
			handler = ktfJavaJump(2)
		case 3:
			handler = ktfJavaJump(3)
		case 4:
			handler = ktfGetJavaMethod
		case 5:
			handler = ktfGetJavaField
		case 6, 7, 8, 9:
			handler = ktfNoop
		case 10:
			handler = ktfRegisterJavaClass
		case 11:
			handler = ktfRegisterJavaString
		case 12:
			handler = ktfCallNative
		}
		slots[index] = r.RegisterHostCall(
			fmt.Sprintf("java.bridge.%d", index),
			handler,
		)
	}
	address, err := r.AllocateWords(slotCount)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(address, slots); err != nil {
		return 0, err
	}
	r.jbInterface = address
	return address, nil
}

func ktfGetJavaMethod(ctx context.Context, runtime *Runtime) (uint32, error) {
	class, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	nameAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, descriptor, err := runtime.ReadJavaFullName(nameAddress)
	if err != nil {
		parameters := make([]uint32, 4)
		for index := range parameters {
			parameters[index], _ = runtime.parameter(uint32(index))
		}
		return 0, fmt.Errorf(
			"resolve Java method class=0x%08x name=0x%08x parameters=%08x: %w",
			class,
			nameAddress,
			parameters,
			err,
		)
	}
	method, err := runtime.resolveJavaMethod(class, name, descriptor)
	if err != nil {
		return 0, fmt.Errorf(
			"resolve Java method %s%s from 0x%08x: %w",
			name,
			descriptor,
			class,
			err,
		)
	}
	resolved, err := runtime.InspectJavaMethod(method)
	if err != nil {
		return 0, err
	}
	if resolved.AccessFlags&0x0008 != 0 {
		methodWords, err := runtime.ReadWords(method, 2)
		if err != nil {
			return 0, err
		}
		declaring, err := runtime.InspectJavaClass(methodWords[1])
		if err != nil {
			return 0, err
		}
		if err := runtime.ensureJavaClassInitialized(ctx, declaring); err != nil {
			return 0, err
		}
	}
	runtime.LastJavaMethod = name + descriptor
	if methodWords, methodErr := runtime.ReadWords(method, 2); methodErr == nil {
		if declaring, classErr := runtime.InspectJavaClass(
			methodWords[1],
		); classErr == nil {
			runtime.LastJavaMethod = declaring.Name + "." +
				runtime.LastJavaMethod
		}
	}
	lr, _ := runtime.CPU.ReadRegister(cpu.RegisterLR)
	runtime.tracef(
		"java_method:%s%s@0x%08x:from=0x%08x:lr=0x%08x",
		name,
		descriptor,
		method,
		class,
		lr,
	)
	return method, nil
}

func ktfGetJavaField(ctx context.Context, runtime *Runtime) (uint32, error) {
	classAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	nameAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, descriptor, err := runtime.ReadJavaFullName(nameAddress)
	if err != nil {
		return 0, err
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	field, err := runtime.ResolveJavaField(class, name, descriptor)
	if err != nil {
		return 0, err
	}
	fieldWords, err := runtime.ReadWords(field, 4)
	if err != nil {
		return 0, err
	}
	if fieldWords[0]&0x0008 != 0 {
		declaring, err := runtime.InspectJavaClass(fieldWords[1])
		if err != nil {
			return 0, err
		}
		if err := runtime.ensureJavaClassInitialized(ctx, declaring); err != nil {
			return 0, err
		}
	}
	runtime.tracef(
		"java_field:%s.%s%s@0x%08x",
		class.Name,
		name,
		descriptor,
		field,
	)
	return field, nil
}

func (r *Runtime) ReadJavaFullName(address uint32) (string, string, error) {
	if address == 0 {
		return "", "", errors.New("Java full-name pointer is null")
	}
	value, err := r.readCString(address+1, 4096)
	if err != nil {
		return "", "", err
	}
	separator := strings.IndexByte(value, '+')
	if separator < 0 {
		return "", "", fmt.Errorf("malformed Java full name %q", value)
	}
	return value[separator+1:], value[:separator], nil
}

func (r *Runtime) ResolveJavaField(
	class JavaClass,
	name string,
	descriptor string,
) (uint32, error) {
	classWords, err := r.ReadWords(class.Address, 5)
	if err != nil {
		return 0, err
	}
	descriptorWords, err := r.ReadWords(classWords[2], 9)
	if err != nil {
		return 0, err
	}
	if !strings.HasPrefix(class.Name, "[") && descriptorWords[5] != 0 {
		for index := uint32(0); index < 4096; index++ {
			field, err := r.ReadU32(descriptorWords[5] + index*4)
			if err != nil {
				return 0, err
			}
			if field == 0 {
				break
			}
			words, err := r.ReadWords(field, 4)
			if err != nil {
				return 0, err
			}
			fieldName, fieldDescriptor, err := r.ReadJavaFullName(words[2])
			if err != nil {
				return 0, err
			}
			if fieldName == name && fieldDescriptor == descriptor {
				return field, nil
			}
		}
	}
	if r.hostJavaClass[class.Address] {
		return r.addHostJavaField(class, name, descriptor)
	}
	if class.Parent != 0 {
		parent, parentErr := r.InspectJavaClass(class.Parent)
		if parentErr != nil {
			return 0, parentErr
		}
		if field, parentErr := r.ResolveJavaField(
			parent,
			name,
			descriptor,
		); parentErr == nil {
			return field, nil
		}
	}
	return 0, fmt.Errorf(
		"Java field %s.%s%s was not found",
		class.Name,
		name,
		descriptor,
	)
}

func (r *Runtime) addHostJavaField(
	class JavaClass,
	name string,
	descriptor string,
) (uint32, error) {
	if name == "" || descriptor == "" {
		return 0, errors.New("host Java field name or descriptor is empty")
	}
	fullName := append([]byte{0}, []byte(descriptor+"+"+name)...)
	nameAddress, err := r.allocateBytes(fullName, true)
	if err != nil {
		return 0, err
	}
	field, err := r.AllocateWords(4)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(field, []uint32{
		0x0009,
		class.Address,
		nameAddress,
		0,
	}); err != nil {
		return 0, err
	}
	value, err := r.hostJavaStaticFieldValue(class.Name, name)
	if err != nil {
		return 0, err
	}
	if err := r.WriteU32(field+12, value); err != nil {
		return 0, err
	}
	classWords, err := r.ReadWords(class.Address, 5)
	if err != nil {
		return 0, err
	}
	descriptorWords, err := r.ReadWords(classWords[2], 9)
	if err != nil {
		return 0, err
	}
	fields := make([]uint32, 0, 8)
	if descriptorWords[5] != 0 {
		for index := uint32(0); index < 4096; index++ {
			value, err := r.ReadU32(descriptorWords[5] + index*4)
			if err != nil {
				return 0, err
			}
			if value == 0 {
				break
			}
			fields = append(fields, value)
		}
	}
	fields = append(fields, field, 0)
	table, err := r.AllocateWords(uint32(len(fields)))
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(table, fields); err != nil {
		return 0, err
	}
	if err := r.WriteU32(classWords[2]+20, table); err != nil {
		return 0, err
	}
	return field, nil
}

func (r *Runtime) hostJavaStaticFieldValue(
	className, name string,
) (uint32, error) {
	if className == "java/lang/System" {
		switch name {
		case "in":
			if r.systemInputStream == 0 {
				instance, err := r.NewHostJavaObject("java/io/InputStream")
				if err != nil {
					return 0, err
				}
				r.systemInputStream = instance
				r.inputStreams[instance] = &ktfInputStream{}
			}
			return r.systemInputStream, nil
		case "out", "err":
			if r.systemPrintStream == 0 {
				instance, err := r.NewHostJavaObject("java/io/PrintStream")
				if err != nil {
					return 0, err
				}
				r.systemPrintStream = instance
			}
			return r.systemPrintStream, nil
		}
	}
	if className == "org/kwis/msp/lcdui/Font" {
		switch name {
		case "STYLE_BOLD":
			return 1, nil
		case "STYLE_ITALIC":
			return 2, nil
		case "STYLE_UNDERLINED":
			return 4, nil
		case "SIZE_SMALL":
			return 8, nil
		case "SIZE_LARGE":
			return 16, nil
		case "FACE_MONOSPACE":
			return 32, nil
		case "FACE_PROPORTIONAL":
			return 64, nil
		}
	}
	if className == "org/kwis/msp/lcdui/Graphics" {
		switch name {
		case "HCENTER":
			return 1, nil
		case "VCENTER":
			return 2, nil
		case "LEFT":
			return 4, nil
		case "RIGHT":
			return 8, nil
		case "TOP":
			return 16, nil
		case "BOTTOM":
			return 32, nil
		case "BASELINE":
			return 64, nil
		}
	}
	return 0, nil
}

func (r *Runtime) resolveJavaMethod(
	classOrVTable uint32,
	name string,
	descriptor string,
) (uint32, error) {
	if classAddress := r.javaVTableClasses[classOrVTable]; classAddress != 0 {
		return r.resolveJavaMethod(classAddress, name, descriptor)
	}
	first, err := r.ReadU32(classOrVTable)
	if err != nil {
		return 0, err
	}
	if first != classOrVTable+4 {
		// KTF AOT virtual-call helpers may pass an object reference directly
		// instead of its class descriptor. Object references are two words:
		// fields pointer followed by the actual class pointer. Prefer that
		// unambiguous route before treating the first word as a raw vtable.
		if classAddress, classErr := r.ReadU32(classOrVTable + 4); classErr == nil &&
			classAddress != 0 {
			if _, inspectErr := r.InspectJavaClass(classAddress); inspectErr == nil {
				return r.resolveJavaMethod(classAddress, name, descriptor)
			}
		}
		// Application class descriptors can store their parent as a one-word
		// indirection whose first word is the actual class definition. This is
		// distinct from an object reference and is common when a guest class
		// extends a framework class loaded through java_class_load.
		if first != 0 {
			if _, inspectErr := r.InspectJavaClass(first); inspectErr == nil {
				return r.resolveJavaMethod(first, name, descriptor)
			}
		}
		table := first
		// Some AOT call sites pass the vtable itself, whose first word is
		// already a Java method descriptor, while others pass a holder whose
		// first word points at that table.
		if _, methodErr := r.InspectJavaMethod(first); methodErr == nil {
			table = classOrVTable
		}
		for index := uint32(0); index < 4096; index++ {
			methodAddress, err := r.ReadU32(table + index*4)
			if err != nil {
				return 0, err
			}
			if methodAddress == 0 {
				break
			}
			method, err := r.InspectJavaMethod(methodAddress)
			if err != nil {
				referenceWords, _ := r.ReadWords(classOrVTable, 4)
				tableWords, _ := r.ReadWords(table, 4)
				return 0, fmt.Errorf(
					"inspect Java vtable method %d at 0x%08x "+
						"(reference=0x%08x words=%08x "+
						"table=0x%08x words=%08x): %w",
					index,
					methodAddress,
					classOrVTable,
					referenceWords,
					table,
					tableWords,
					err,
				)
			}
			if method.Name == name && method.Descriptor == descriptor {
				return method.Address, nil
			}
		}
		return 0, fmt.Errorf(
			"Java method %s%s is absent from vtable 0x%08x",
			name,
			descriptor,
			table,
		)
	}

	class, err := r.InspectJavaClass(classOrVTable)
	if err != nil {
		return 0, err
	}
	if method, ok := findKTFJavaMethod(class, name, descriptor); ok {
		if method.Body != 0 || method.NativeBody != 0 ||
			!r.hostJavaClass[class.Address] {
			return method.Address, nil
		}
	}
	if r.hostJavaClass[class.Address] {
		return r.addHostJavaMethod(class, name, descriptor)
	}
	if class.Parent != 0 {
		if method, parentErr := r.resolveJavaMethod(class.Parent, name, descriptor); parentErr == nil {
			return method, nil
		}
	}
	return 0, fmt.Errorf(
		"Java method %s.%s%s was not found",
		class.Name,
		name,
		descriptor,
	)
}

func (r *Runtime) addHostJavaMethod(
	class JavaClass,
	name string,
	descriptor string,
) (uint32, error) {
	stub := r.RegisterHostCall(
		fmt.Sprintf("java.method.%s.%s%s", class.Name, name, descriptor),
		HostJavaMethod(class.Name, name, descriptor),
	)
	fullName := append([]byte{0}, []byte(descriptor+"+"+name)...)
	nameAddress, err := r.allocateBytes(fullName, true)
	if err != nil {
		return 0, err
	}
	methodAddress, err := r.AllocateWords(7)
	if err != nil {
		return 0, err
	}
	accessFlags := uint16(1)
	declaredByHostSpec := false
	if spec, ok := HostJavaClassSpecs[class.Name]; ok {
		for _, method := range spec.methods {
			if method.name == name && method.descriptor == descriptor {
				accessFlags = method.access
				declaredByHostSpec = true
				break
			}
		}
	}
	body := stub
	nativeBody := uint32(0)
	if accessFlags&0x0100 != 0 {
		body = 0
		nativeBody = stub
	}
	if err := r.writeWords(methodAddress, []uint32{
		body,
		class.Address,
		nativeBody,
		nameAddress,
		0,
		uint32(accessFlags) << 16,
		0,
	}); err != nil {
		return 0, err
	}
	classWords, err := r.ReadWords(class.Address, 5)
	if err != nil {
		return 0, err
	}
	descriptorWords, err := r.ReadWords(classWords[2], 9)
	if err != nil {
		return 0, err
	}
	oldCount := uint16(descriptorWords[6])
	methods := make([]uint32, 0, int(oldCount)+2)
	for index := uint16(0); index < oldCount; index++ {
		value, err := r.ReadU32(descriptorWords[3] + uint32(index)*4)
		if err != nil {
			return 0, err
		}
		methods = append(methods, value)
	}
	methods = append(methods, methodAddress, 0)
	table, err := r.AllocateWords(uint32(len(methods)))
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(table, methods); err != nil {
		return 0, err
	}
	if err := r.WriteU32(classWords[2]+12, table); err != nil {
		return 0, err
	}
	countAndFields := descriptorWords[6]&0xffff0000 | uint32(oldCount+1)
	if err := r.WriteU32(classWords[2]+24, countAndFields); err != nil {
		return 0, err
	}
	compatibilityVirtual := !declaredByHostSpec &&
		accessFlags&(0x0002|0x0008) == 0 &&
		!strings.HasPrefix(name, "<")
	if compatibilityVirtual {
		if err := r.registerHostJavaVirtualMethod(methodAddress); err != nil {
			return 0, err
		}
	}
	if err := r.rebuildHostJavaVTable(class.Address); err != nil {
		return 0, err
	}
	if compatibilityVirtual {
		if err := r.installHostJavaVirtualMethod(methodAddress); err != nil {
			return 0, err
		}
	}
	return methodAddress, nil
}

func (r *Runtime) registerHostJavaVirtualMethod(methodAddress uint32) error {
	if _, exists := r.hostJavaVirtualSlots[methodAddress]; exists {
		return nil
	}
	if r.nextHostVirtualSlot == ^uint16(0) {
		return errors.New("KTF host Java compatibility vtable exhausted")
	}
	slot := r.nextHostVirtualSlot
	r.nextHostVirtualSlot++
	r.hostJavaVirtualSlots[methodAddress] = slot
	flags, err := r.ReadU32(methodAddress + 20)
	if err != nil {
		return err
	}
	return r.WriteU32(
		methodAddress+20,
		flags&0xffff0000|uint32(slot),
	)
}

func (r *Runtime) installHostJavaVirtualMethod(methodAddress uint32) error {
	slot, ok := r.hostJavaVirtualSlots[methodAddress]
	if !ok {
		return fmt.Errorf(
			"KTF host Java method 0x%08x has no compatibility vtable slot",
			methodAddress,
		)
	}
	for classAddress := range r.javaVTables {
		if err := r.installHostJavaVirtualMethodForClass(
			classAddress,
			methodAddress,
			slot,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) installHostJavaVirtualMethodForClass(
	classAddress uint32,
	methodAddress uint32,
	slot uint16,
) error {
	classWords, err := r.ReadWords(classAddress, 5)
	if err != nil {
		return err
	}
	required := uint32(slot) + 1
	logicalSize := classWords[4] & 0xffff
	capacity := r.javaVTableCapacity[classAddress]
	if capacity < required {
		newCapacity := ktfHostVirtualTableReserve
		requiredCapacity := max(required, logicalSize)
		for newCapacity < requiredCapacity {
			if newCapacity > uint32(^uint16(0))/2 {
				newCapacity = requiredCapacity
				break
			}
			newCapacity *= 2
		}
		entries := make([]uint32, newCapacity)
		copyCount := logicalSize
		if copyCount > capacity && capacity != 0 {
			copyCount = capacity
		}
		if copyCount != 0 {
			existing, err := r.ReadWords(classWords[3], int(copyCount))
			if err != nil {
				return err
			}
			copy(entries, existing)
		}
		vtable, err := r.AllocateWords(newCapacity)
		if err != nil {
			return err
		}
		if err := r.writeWords(vtable, entries); err != nil {
			return err
		}
		if err := r.WriteU32(classAddress+12, vtable); err != nil {
			return err
		}
		if _, err := r.ensureJavaVTableIndex(classAddress, vtable); err != nil {
			return err
		}
		r.javaVTableClasses[vtable] = classAddress
		r.javaVTableCapacity[classAddress] = newCapacity
		classWords[3] = vtable
	}
	existing, err := r.ReadU32(classWords[3] + uint32(slot)*4)
	if err != nil {
		return err
	}
	if existing != 0 && existing != methodAddress {
		className := fmt.Sprintf("0x%08x", classAddress)
		if class, inspectErr := r.InspectJavaClass(classAddress); inspectErr == nil {
			className = class.Name
		}
		method, inspectErr := r.InspectJavaMethod(methodAddress)
		if inspectErr != nil {
			return inspectErr
		}
		compatible, hierarchyErr := r.javaClassExtends(
			classAddress,
			method.DeclaringClass,
		)
		if hierarchyErr != nil {
			return hierarchyErr
		}
		if !compatible {
			r.tracef(
				"java_compat_vtable_collision:class=%s:slot=%d:"+
					"guest=0x%08x:host=0x%08x:preserved",
				className,
				slot,
				existing,
				methodAddress,
			)
			return nil
		}
	}
	if err := r.WriteU32(
		classWords[3]+uint32(slot)*4,
		methodAddress,
	); err != nil {
		return err
	}
	if logicalSize < required {
		if err := r.WriteU32(
			classAddress+16,
			classWords[4]&0xffff0000|required,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) javaClassExtends(classAddress, parentAddress uint32) (bool, error) {
	for depth := 0; classAddress != 0; depth++ {
		if depth >= 256 {
			return false, errors.New("KTF Java class hierarchy exceeds limit")
		}
		if classAddress == parentAddress {
			return true, nil
		}
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			return false, err
		}
		classAddress = class.Parent
	}
	return false, nil
}
