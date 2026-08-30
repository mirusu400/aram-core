package raptor

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

const raptorJavaHostModule = ^uint32(0)

// raptorJavaHeapBase is the lowest guest address the shared heap allocates from.
// A class vtable field below it is the guest's own (unlinked) value rather than
// a vtable we built, which lives in the heap.
const raptorJavaHeapBase = uint32(0x10000000)

// raptorJavaMaxFieldOffset bounds the byte offset accepted by the getfield /
// putfield accessors. A real object field block is at most a few kilobytes; a
// larger r2 value is not a field index, so it is ignored rather than used to
// read or write an unrelated address.
const raptorJavaMaxFieldOffset = uint32(0x10000)

// raptorPrimitiveArrayElementChar maps a JVM primitive array type code (the
// atype operand of newarray) to the field-descriptor character that
// newRaptorJavaArray decodes into an element width. It returns 0 for values
// outside the primitive atype range so a non-array use of the same ordinal
// falls through to its generic behavior.
func raptorPrimitiveArrayElementChar(atype uint32) uint32 {
	switch atype {
	case 4: // T_BOOLEAN
		return 'Z'
	case 5: // T_CHAR
		return 'C'
	case 6: // T_FLOAT
		return 'F'
	case 7: // T_DOUBLE
		return 'D'
	case 8: // T_BYTE
		return 'B'
	case 9: // T_SHORT
		return 'S'
	case 10: // T_INT
		return 'I'
	case 11: // T_LONG
		return 'J'
	}
	return 0
}

const JavaTaskInstructionBudget = uint64(250_000)

type raptorJavaMethod struct {
	className  string
	Name       string
	descriptor string
	isStatic   bool
}

type raptorJavaFixedVirtualMethod struct {
	offset     uint32
	Name       string
	descriptor string
}

// raptorJavaFlatVirtualBase is the first offset the link step publishes for a
// linked (flat) virtual method, in the units the generated code uses:
// it computes the slot as vtable + offset*4 + 4.
//
// The flat entries have to start past every fixed slot below, because a
// compiler-inlined call site reads a fixed byte offset directly and the fixed
// pass writes those slots after the flat pass. Publishing offset = index*2 put
// flat entry 1 at byte 12 - java/lang/Object's hashCode slot - so the fixed
// pass overwrote it. 현영맞고2006 draws its whole screen through flat entry 1
// (org/kwis/msp/lcdui/Graphics.drawString), and every one of those calls
// dispatched to Object.hashCode instead: 92,799 of them in sixty frames, and
// nothing was ever drawn (issue #79). The highest fixed offset in the table
// below is java/lang/String's substring at 0x74, so the flat region starts at
// byte 0x7c.
// raptorJavaThreadRunSlot is the byte offset of run()V in the module's own
// vtable for a java/lang/Thread subclass, next to the start()V slot the fixed
// table already names at 0x2c.
//
// A helper class the module publishes no metadata for cannot be resolved by
// name: 현영맞고2006's TimeChecker carries neither a method nor a field table,
// so Thread.start found no run() and the thread was never scheduled - the
// game's clock never advanced and its screen transition never completed
// (issue #79). The module still fills its own vtable, so the body is read from
// there.
const raptorJavaThreadRunSlot = 0x30

const raptorJavaFlatVirtualBase = 30

// raptorJavaFlatVirtualSlot is the byte offset of one linked virtual method
// inside a vtable, matching what generated code computes from the published
// offset.
func raptorJavaFlatVirtualSlot(index uint32) uint32 {
	return (raptorJavaFlatVirtualBase+index*2)*4 + 4
}

// raptorJavaReaderVirtualMethods is the CLDC java/io/Reader vtable layout,
// shared by Reader and InputStreamReader. See raptorJavaFixedVirtualMethods.
var raptorJavaReaderVirtualMethods = []raptorJavaFixedVirtualMethod{
	{offset: 0x2c, Name: "read", descriptor: "()I"},
	{offset: 0x30, Name: "read", descriptor: "([C)I"},
	{offset: 0x34, Name: "read", descriptor: "([CII)I"},
	{offset: 0x38, Name: "skip", descriptor: "(J)J"},
	{offset: 0x3c, Name: "ready", descriptor: "()Z"},
	{offset: 0x44, Name: "mark", descriptor: "(I)V"},
	{offset: 0x48, Name: "reset", descriptor: "()V"},
	{offset: 0x4c, Name: "close", descriptor: "()V"},
}

var raptorJavaFixedVirtualMethods = map[string][]raptorJavaFixedVirtualMethod{
	"java/lang/String": {
		{offset: 0x10, Name: "equals", descriptor: "(Ljava/lang/Object;)Z"},
		{offset: 0x2c, Name: "length", descriptor: "()I"},
		{offset: 0x3c, Name: "getBytes", descriptor: "()[B"},
		// 월드장기체스 CCC uses slot 0x44 as a string switch: it compares a
		// String against successive one-character String literals and branches
		// when the result is zero. Only compareTo returns 0 on equality.
		{offset: 0x44, Name: "compareTo", descriptor: "(Ljava/lang/String;)I"},
		{offset: 0x74, Name: "substring", descriptor: "(II)Ljava/lang/String;"},
	},
	"java/lang/StringBuffer": {
		{offset: 0x14, Name: "toString", descriptor: "()Ljava/lang/String;"},
		{offset: 0x4c, Name: "append", descriptor: "(Ljava/lang/String;)Ljava/lang/StringBuffer;"},
		{offset: 0x60, Name: "append", descriptor: "(I)Ljava/lang/StringBuffer;"},
	},
	"java/lang/Thread": {
		{offset: 0x2c, Name: "start", descriptor: "()V"},
	},
	"java/util/Random": {
		// 월드장기체스 seeds its RNG: the slot-0x2c call passes a long in r1:r2.
		{offset: 0x2c, Name: "setSeed", descriptor: "(J)V"},
		{offset: 0x34, Name: "nextInt", descriptor: "()I"},
	},
	"java/util/Calendar": {
		{offset: 0x50, Name: "get", descriptor: "(I)I"},
	},
	"java/lang/Runtime": {
		// 체스마스터 SDK glue calls slot 0x38 on the cached Runtime before
		// allocating and inside its sleep(150) idle loop: that is gc().
		{offset: 0x2c, Name: "freeMemory", descriptor: "()J"},
		{offset: 0x30, Name: "totalMemory", descriptor: "()J"},
		{offset: 0x34, Name: "exit", descriptor: "(I)V"},
		{offset: 0x38, Name: "gc", descriptor: "()V"},
	},
	"java/lang/Class": {
		// CLDC order after static forName: newInstance, isInstance,
		// isAssignableFrom, isInterface, isArray, getName,
		// getResourceAsStream. 체스마스터 SDK glue builds a resource name with
		// StringBuffer and calls slot 0x44 on a Class with the String.
		{offset: 0x34, Name: "isAssignableFrom", descriptor: "(Ljava/lang/Class;)Z"},
		{offset: 0x40, Name: "getName", descriptor: "()Ljava/lang/String;"},
		{
			offset:     0x44,
			Name:       "getResourceAsStream",
			descriptor: "(Ljava/lang/String;)Ljava/io/InputStream;",
		},
	},
	"java/util/Vector": {
		// Slot order mirrors the host spec declaration order; 월드장기체스 CCC
		// constructs Vectors and calls slot 0x44 expecting an int back: size().
		{offset: 0x2c, Name: "addElement", descriptor: "(Ljava/lang/Object;)V"},
		{offset: 0x30, Name: "elementAt", descriptor: "(I)Ljava/lang/Object;"},
		{offset: 0x34, Name: "setElementAt", descriptor: "(Ljava/lang/Object;I)V"},
		{offset: 0x38, Name: "removeElementAt", descriptor: "(I)V"},
		{offset: 0x3c, Name: "removeElement", descriptor: "(Ljava/lang/Object;)Z"},
		{offset: 0x40, Name: "removeAllElements", descriptor: "()V"},
		{offset: 0x44, Name: "size", descriptor: "()I"},
		{offset: 0x48, Name: "capacity", descriptor: "()I"},
		{offset: 0x4c, Name: "isEmpty", descriptor: "()Z"},
		{offset: 0x50, Name: "contains", descriptor: "(Ljava/lang/Object;)Z"},
		{offset: 0x54, Name: "indexOf", descriptor: "(Ljava/lang/Object;)I"},
		{offset: 0x58, Name: "copyInto", descriptor: "([Ljava/lang/Object;)V"},
		{offset: 0x5c, Name: "elements", descriptor: "()Ljava/util/Enumeration;"},
	},
	"java/io/InputStream": {
		// CLDC order: read(), read([B), read([BII), skip, available, close,
		// mark, markSupported, reset. 체스마스터 calls slot 0x3c on the stream
		// from getResourceAsStream before sizing its buffer: available().
		{offset: 0x2c, Name: "read", descriptor: "()I"},
		{offset: 0x30, Name: "read", descriptor: "([B)I"},
		{offset: 0x34, Name: "read", descriptor: "([BII)I"},
		{offset: 0x38, Name: "skip", descriptor: "(J)J"},
		{offset: 0x3c, Name: "available", descriptor: "()I"},
		{offset: 0x40, Name: "close", descriptor: "()V"},
		{offset: 0x44, Name: "mark", descriptor: "(I)V"},
		{offset: 0x4c, Name: "reset", descriptor: "()V"},
	},
	// A Reader declares read(), read(char[]), read(char[],int,int), skip,
	// ready, markSupported, mark, reset, close in that order, so its slots line
	// up from 0x2c exactly as java/io/InputStream's do. 현영맞고2006 reads its
	// tutorial and manual text with reader.read(buffer) at slot 0x30 and closes
	// at slot 0x4c; unresolved, both fell through to the no-op backstop, the
	// buffer stayed empty, and the title built a String over a negative range
	// (issue #79). Both the abstract and the concrete class carry the layout,
	// because a wrapper typed as either one is dispatched through it.
	"java/io/Reader":            raptorJavaReaderVirtualMethods,
	"java/io/InputStreamReader": raptorJavaReaderVirtualMethods,
	"org/kwis/msp/lcdui/Card": {
		// A Card is a full-screen Displayable; its dimension getters occupy the
		// Object-region bytes 0x14/0x18 in the KWIS vtable, not Object.toString/
		// notify. 서든어택포켓's Card-subclass constructor sizes a back buffer with
		// Image.createImage(getWidth()..) via these slots; without the entries
		// the calls fell through to the Object.toString host stub and the guest
		// divided the returned String pointer into a garbage image size and
		// faulted. Returning the real screen size makes createImage succeed.
		{offset: 0x14, Name: "getWidth", descriptor: "()I"},
		{offset: 0x18, Name: "getHeight", descriptor: "()I"},
	},
	// The CLDC Object slots occupy bytes 4..0x28 of every vtable; the prebuilt
	// tables shipped in module .data leave exactly ten leading slots empty and
	// start their own methods at 0x2c. String.equals at 0x10 and
	// StringBuffer.toString at 0x14 (an Object.toString override) confirm the
	// order.
	"java/lang/Object": {
		{offset: 0x08, Name: "getClass", descriptor: "()Ljava/lang/Class;"},
		{offset: 0x0c, Name: "hashCode", descriptor: "()I"},
		{offset: 0x10, Name: "equals", descriptor: "(Ljava/lang/Object;)Z"},
		{offset: 0x14, Name: "toString", descriptor: "()Ljava/lang/String;"},
		{offset: 0x18, Name: "notify", descriptor: "()V"},
		{offset: 0x1c, Name: "notifyAll", descriptor: "()V"},
		{offset: 0x28, Name: "wait", descriptor: "()V"},
	},
}

type raptorJavaDeclaredMethod struct {
	Name       string
	descriptor string
	Body       uint32
	flags      uint16
}

type raptorJavaDeclaredField struct {
	Name       string
	descriptor string
	index      uint32
}

type raptorJavaClass struct {
	Holder     uint32
	descriptor uint32
	Name       string
	parentName string
	fieldSize  uint32
	staticBase uint32
	vtable     uint32
	// guestVTable is the dispatch table the module built for this class, kept
	// after the runtime replaces vtable with its own. See raptorJavaThreadRun.
	guestVTable uint32
	methods     []raptorJavaDeclaredMethod
	fields      []raptorJavaDeclaredField
	hostClass   uint32
	classObject uint32
}

// raptorJavaTaskStackSize is the stack one started Java thread gets, and
// raptorJavaTaskStackTop is where the first one's stack ends.
//
// Every thread used to start with the same stack pointer, so a title running
// two at once had them writing over each other's frames. It only stayed hidden
// while a second thread could not be scheduled at all (issue #79).
const (
	raptorJavaTaskStackTop  = guest.DefaultStackBase + guest.DefaultStackSize - 0x20000
	raptorJavaTaskStackSize = uint32(0x20000)
	raptorJavaTaskStackMax  = 6
)

// RaptorJavaTaskStack is the stack pointer thread index gets. Threads past the
// supported count share the last stack, which is what every thread did before.
func RaptorJavaTaskStack(index int) uint32 {
	if index < 0 {
		index = 0
	}
	if index >= raptorJavaTaskStackMax {
		index = raptorJavaTaskStackMax - 1
	}
	return raptorJavaTaskStackTop - uint32(index)*raptorJavaTaskStackSize
}

type JavaTask struct {
	Target    uint32
	Procedure uint32
	// Stack is where this thread's stack starts, so concurrently scheduled
	// threads do not write over each other. See RaptorJavaTaskStack.
	Stack   uint32
	Context []byte
	Done    bool
	// WakeAtMS is the monotonic millisecond this thread's Thread.sleep ends.
	// The scheduler skips the task until the clock reaches it.
	WakeAtMS uint64
}

// JavaClass is an exported alias so callers outside this package (the machine
// launch path) can name the class type returned by ResolveRaptorJletMainClass.
type JavaClass = raptorJavaClass

type JavaRuntime struct {
	Host *ktfrt.Runtime

	classes     map[uint32]*raptorJavaClass
	ClassByName map[string]*raptorJavaClass
	classOrder  []*raptorJavaClass
	hostMethods map[uint32]raptorJavaMethod
	nextMethod  uint32
	// activeTask is the Java thread the machine is currently running, so a
	// Thread.sleep from guest code parks that thread.
	activeTask *JavaTask
	// primitiveArrays maps a KTF array mirror to its element width, for the
	// arrays whose elements the bridge copies across a host call. See
	// syncRaptorArrayArguments.
	primitiveArrays map[uint32]uint32
	// syncScratch is the reusable staging buffer those copies move through.
	syncScratch []byte
	// noopStub is a single cached host trampoline reused to fill vtable
	// own-method slots that no source (flat/fixed/inline/table) resolved. It
	// keeps a call through an otherwise-null slot from branching to address 0.
	noopStub uint32
	// arrayVTable is a shared all-no-op vtable installed in every array header
	// so a virtual dispatch on an array reference returns 0 instead of branching
	// to address 0 through the array's zero header.
	arrayVTable uint32

	flatVirtual  []raptorJavaMethod
	lgtToKTF     map[uint32]uint32
	ktfToLGT     map[uint32]uint32
	initializing map[uint32]bool
	scratch      uint32
	jarPath      uint32

	// fieldOffsets and fieldNames remember where the linker published the
	// module's field-offset table and which fields it covers, so a class that
	// only arrives after the link can still have its fields resolved. See
	// resolveRaptorJavaFieldOffsets.
	fieldOffsets uint32
	fieldNames   uint32
	fieldCount   uint32

	LaunchRequested bool
	MainClass       string
	MainInstance    uint32
	currentCard     uint32
	dirtyCards      map[uint32]bool
	threadTargets   []uint32
	Tasks           []*JavaTask
	// nextTask rotates the thread scheduler. See NextRunnableJavaTask.
	nextTask int
}

// NextRunnableJavaTask picks the next started thread to run, rotating so every
// one of them gets a slice.
//
// The scheduler used to take the first task that was not done, so a title whose
// main loop never returns starved every other thread it started. 현영맞고2006
// starts Hcvs.run() and then TimeChecker.run(); the first never returns, so the
// clock thread it runs against never got a single instruction (issue #79).
func (r *Runtime) NextRunnableJavaTask() *JavaTask {
	java := r.Java
	if java == nil || len(java.Tasks) == 0 {
		return nil
	}
	now := uint64(0)
	if r.Public != nil {
		now = r.Public.TickMS
	}
	for offset := 0; offset < len(java.Tasks); offset++ {
		index := (java.nextTask + offset) % len(java.Tasks)
		task := java.Tasks[index]
		if task.Done || task.WakeAtMS > now {
			// A thread parked by Thread.sleep is not runnable until the
			// handset clock reaches its wake time. See sleepRaptorJavaTask.
			continue
		}
		java.nextTask = (index + 1) % len(java.Tasks)
		return task
	}
	return nil
}

func (r *Runtime) ensureJavaRuntime() (*JavaRuntime, error) {
	if r.Java != nil {
		return r.Java, nil
	}
	host, err := ktfrt.NewRuntimeForProfile(
		r.CPU,
		ktf.Package{
			JARName:   r.Pkg.JARName,
			Client:    []byte{0},
			Files:     r.Pkg.Files,
			Resources: r.Pkg.Resources,
		},
		r.Public.Frame,
		ProfileID+"/java",
		// The Raptor Java host shares the public runtime's fallback font; the
		// public WIPI runtime already applied the machine's selection.
		r.Public.FallbackFontName(),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Raptor Java Host: %w", err)
	}
	// Raptor and its Java adapter share one guest address space. Delegate every
	// host allocation to the public runtime's allocator so copying the heap's
	// slice header cannot create independently advancing, overlapping free
	// lists.
	host.Heap = guest.Heap{CPU: r.CPU, Shared: &r.Public.Heap}
	host.Mapped = true
	host.DeferThreads = false
	// The Raptor AOT-Java bridge cannot deliver a host-raised Java exception to
	// a guest catch block, so a first-run read-only open of a not-yet-written
	// private config file must not fault the machine.
	host.LenientMissingRead = true
	// The AOT compiler inlines StringBuffer.setLength(0) as a native write the
	// KTF StringBuffer handler never sees, so a builder reused after toString()
	// must be reset at the next append or it keeps accumulating (놈3 builds
	// every resource name in one buffer and would miss every lookup after the
	// first, stalling the load).
	host.ConsumeStringBufferOnToString = true
	scratch, err := r.Public.Heap.Allocate(16*4, true)
	if err != nil || scratch == 0 {
		return nil, errors.New("allocate Raptor Java call scratch")
	}
	java := &JavaRuntime{
		Host:         host,
		classes:      make(map[uint32]*raptorJavaClass),
		ClassByName:  make(map[string]*raptorJavaClass),
		hostMethods:  make(map[uint32]raptorJavaMethod),
		nextMethod:   1,
		lgtToKTF:     make(map[uint32]uint32),
		ktfToLGT:     make(map[uint32]uint32),
		initializing: make(map[uint32]bool),
		dirtyCards:   make(map[uint32]bool),
		scratch:      scratch,
		MainClass:    r.Pkg.Descriptor.MainClass,
	}
	r.Java = java
	return java, nil
}

func (r *Runtime) DestroyRaptorJava() error {
	if r == nil || r.Java == nil {
		return nil
	}
	host := r.Java.Host
	r.Java = nil
	if host == nil || host.Services == nil {
		return nil
	}
	adapter, err := host.Services.Coordinator.Adapter(host.ServiceOwner)
	if err != nil || adapter.Lifecycle == shared.LifecycleDestroyed {
		return err
	}
	return host.Services.Coordinator.Transition(
		host.ServiceOwner,
		shared.LifecycleDestroyed,
		host.Services.Clock.Monotonic(),
		nil,
	)
}

func (r *Runtime) importStub(key raptorImportKey) (uint32, error) {
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

// raptorJavaNoopStub returns a cached Thumb host trampoline whose dispatch is a
// no-op returning 0, used to backfill unresolved vtable own-method slots.
func (r *Runtime) raptorJavaNoopStub(java *JavaRuntime) (uint32, error) {
	if java.noopStub != 0 {
		return java.noopStub, nil
	}
	stub, err := r.registerJavaHostMethod(raptorJavaMethod{Name: "<noop>"})
	if err != nil {
		return 0, err
	}
	// Host trampolines are Thumb code; force the interworking bit.
	java.noopStub = stub | 1
	return java.noopStub, nil
}

// raptorJavaArrayVTable lazily builds the shared no-op vtable installed in array
// headers. Slot +0x00 is the class holder (0 for arrays); every method slot from
// +0x04 up holds the no-op stub, so a virtual dispatch on an array reference
// returns 0 rather than branching to address 0.
func (r *Runtime) raptorJavaArrayVTable(java *JavaRuntime) (uint32, error) {
	if java.arrayVTable != 0 {
		return java.arrayVTable, nil
	}
	noop, err := r.raptorJavaNoopStub(java)
	if err != nil {
		return 0, err
	}
	const arrayVTableSize = 0x200
	vtable, err := r.Public.Heap.Allocate(arrayVTableSize, true)
	if err != nil || vtable == 0 {
		return 0, errors.New("allocate Raptor Java array vtable")
	}
	for offset := uint32(0x04); offset < arrayVTableSize; offset += 4 {
		if err := r.Public.WriteU32(vtable+offset, noop); err != nil {
			return 0, err
		}
	}
	java.arrayVTable = vtable
	return java.arrayVTable, nil
}

func (r *Runtime) registerJavaHostMethod(method raptorJavaMethod) (uint32, error) {
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

func (r *Runtime) dispatchJavaImport(
	ctx context.Context,
	key raptorImportKey,
) (guest.WIPIReturn, string, bool, error) {
	if key.Module == raptorJavaHostModule {
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.host", true, err
		}
		method, ok := java.hostMethods[key.Ordinal]
		if !ok {
			return guest.WIPIReturn{}, "RAPTOR.java.host", true,
				fmt.Errorf("unknown Java host method %d", key.Ordinal)
		}
		result, err := r.callJavaHostMethod(ctx, method)
		return result, "RAPTOR.java." + method.className + "." +
			method.Name + method.descriptor, true, err
	}
	if key.Module == 508 || key.Module == 511 || key.Module == 513 {
		if key.Ordinal == 3 {
			return guest.WIPIReturn{}, fmt.Sprintf("RAPTOR.Java.module%d.init", key.Module), true, nil
		}
		return guest.WIPIReturn{}, "", false, nil
	}
	if key.Module == 504 {
		switch key.Ordinal {
		case 22:
			return guest.WIPIReturn{}, "RAPTOR.lgte.setProperty", true, nil
		case 23:
			java, err := r.ensureJavaRuntime()
			if err != nil {
				return guest.WIPIReturn{}, "RAPTOR.lgte.getJARPath", true, err
			}
			if java.jarPath == 0 {
				java.jarPath, err = r.allocateJavaCString(r.Pkg.JARName)
				if err != nil {
					return guest.WIPIReturn{}, "RAPTOR.lgte.getJARPath", true, err
				}
			}
			target, err := r.CPU.ReadRegister(cpu.RegisterR2)
			if err == nil && target != 0 {
				err = r.Public.WriteU32(target, java.jarPath)
			}
			return guest.WIPIReturn{}, "RAPTOR.lgte.getJARPath", true, err
		}
		return guest.WIPIReturn{}, "", false, nil
	}
	if key.Module != 100 {
		return guest.WIPIReturn{}, "", false, nil
	}
	switch key.Ordinal {
	case 3:
		_, err := r.ensureJavaRuntime()
		return guest.WIPIReturn{}, "RAPTOR.Java.initialize", true, err
	case 7:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.registerClasses", true, err
		}
		classes, readErr := r.CPU.ReadRegister(cpu.RegisterR0)
		if readErr == nil {
			readErr = r.registerRaptorJavaClasses(java, classes)
		}
		return guest.WIPIReturn{Low: classes}, "RAPTOR.java.registerClasses", true, readErr
	case 9:
		data, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.stringLiteral", true, err
		}
		length, err := r.CPU.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.stringLiteral", true, err
		}
		cache, err := r.CPU.ReadRegister(cpu.RegisterR3)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.stringLiteral", true, err
		}
		value, err := r.raptorJavaStringLiteral(data, length, cache)
		return guest.WIPIReturn{Low: value}, "RAPTOR.Java.stringLiteral", true, err
	case 20:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.linkClasses", true, err
		}
		return guest.WIPIReturn{}, "RAPTOR.Java.linkClasses", true,
			r.linkRaptorJavaClasses(java)
	case 11, 12, 13:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.loadClass", true, err
		}
		holder, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.loadClass", true, err
		}
		if key.Ordinal == 13 {
			class := r.raptorJavaClassForObject(java, holder)
			if class == nil {
				return guest.WIPIReturn{}, "RAPTOR.Java.initializeClass", true,
					fmt.Errorf("unknown Raptor Java class object 0x%08x", holder)
			}
			initializer, readErr := r.CPU.ReadRegister(cpu.RegisterR1)
			if readErr != nil {
				return guest.WIPIReturn{}, "RAPTOR.Java.initializeClass", true, readErr
			}
			if java.initializing[holder] {
				return guest.WIPIReturn{Low: holder}, "RAPTOR.Java.initializeClass", true, nil
			}
			java.initializing[holder] = true
			defer delete(java.initializing, holder)
			if initializer != 0 && r.Public.InvokeSync != nil {
				if _, invokeErr := r.Public.InvokeSync(ctx, wipirt.GuestCallback{
					Procedure: initializer,
					Args:      [4]uint32{holder},
				}); invokeErr != nil {
					return guest.WIPIReturn{}, "RAPTOR.Java.initializeClass", true, invokeErr
				}
			}
			if writeErr := r.writeRaptorJavaClassState(class, 5); writeErr != nil {
				return guest.WIPIReturn{}, "RAPTOR.Java.initializeClass", true, writeErr
			}
			return guest.WIPIReturn{Low: holder}, "RAPTOR.Java.initializeClass", true, nil
		}
		if holder != 0 {
			class, inspectErr := r.inspectRaptorJavaClass(java, holder)
			if inspectErr != nil {
				return guest.WIPIReturn{}, "RAPTOR.java.loadClass", true, inspectErr
			}
			if class.vtable == 0 && len(java.flatVirtual) != 0 {
				if buildErr := r.buildRaptorJavaVTable(
					java,
					class,
					uint32(len(java.flatVirtual)),
				); buildErr != nil {
					return guest.WIPIReturn{}, "RAPTOR.java.loadClass", true, buildErr
				}
			}
			var linked [2]byte
			binary.LittleEndian.PutUint16(linked[:], 3)
			if writeErr := r.CPU.WriteMemory(class.descriptor+0x1a, linked[:]); writeErr != nil {
				return guest.WIPIReturn{}, "RAPTOR.java.loadClass", true, writeErr
			}
			if key.Ordinal == 12 {
				object, objectErr := r.ensureRaptorJavaClassObject(java, class)
				return guest.WIPIReturn{Low: object}, "RAPTOR.java.loadClass", true, objectErr
			}
		}
		return guest.WIPIReturn{Low: holder}, "RAPTOR.java.loadClass", true, nil
	case 130:
		return guest.WIPIReturn{}, "RAPTOR.java.setJARPath", true, nil
	case 131:
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.launch", true, err
		}
		arguments, readErr := r.CPU.ReadRegister(cpu.RegisterR3)
		if readErr == nil && arguments != 0 {
			mainName, pointerErr := r.Public.ReadU32(arguments)
			if pointerErr == nil && mainName != 0 {
				if value, stringErr := r.Public.ReadCString(mainName); stringErr == nil && len(value) != 0 {
					java.MainClass = string(value)
				}
			}
		}
		java.LaunchRequested = true
		return guest.WIPIReturn{}, "RAPTOR.Java.launch", true, nil
	case 14:
		size, err := r.CPU.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.alloc", true, err
		}
		// A primitive array (new byte[]/short[]/int[]) resolves its element type
		// through this ordinal with r2 = the JVM atype code (T_BOOLEAN=4 ..
		// T_LONG=11). The result is passed straight to newArray (ordinal 16) as
		// its element operand, so return the element-descriptor char the array
		// allocator decodes to the correct element width. Without this the value
		// was a heap pointer, and newRaptorJavaArray treated every primitive
		// array as a 4-byte object array — a byte[] read buffer became 4x too
		// wide and never lined up with the guest's byte-stride accesses.
		if elementChar := raptorPrimitiveArrayElementChar(size); elementChar != 0 {
			return guest.WIPIReturn{Low: elementChar}, "RAPTOR.Java.arrayType", true, nil
		}
		address, err := r.Public.Heap.Allocate(size, true)
		return guest.WIPIReturn{Low: address}, "RAPTOR.java.alloc", true, err
	case 15:
		class, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.new", true, err
		}
		instance, err := r.NewRaptorJavaObject(class)
		return guest.WIPIReturn{Low: instance}, "RAPTOR.Java.new", true, err
	case 16:
		element, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.newArray", true, err
		}
		count, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.newArray", true, err
		}
		array, err := r.newRaptorJavaArray(element, count)
		return guest.WIPIReturn{Low: array}, "RAPTOR.Java.newArray", true, err
	case 17:
		element, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.newMultiArray", true, err
		}
		dimensionsPtr, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.newMultiArray", true, err
		}
		dimensions, err := r.CPU.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.newMultiArray", true, err
		}
		array, err := r.newRaptorJavaMultiArray(element, dimensionsPtr, dimensions)
		return guest.WIPIReturn{Low: array}, "RAPTOR.Java.newMultiArray", true, err
	case 18:
		return r.checkRaptorJavaType()
	case 225:
		// 체스마스터 SDK glue feeds this result straight into Java.new while
		// tokenizing a string into new String objects: the String class.
		java, err := r.ensureJavaRuntime()
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.stringClass", true, err
		}
		class, err := r.ensureRaptorHostClass(java, "java/lang/String")
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.Java.stringClass", true, err
		}
		return guest.WIPIReturn{Low: class.Holder}, "RAPTOR.Java.stringClass", true, nil
	case 97, 250:
		array, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.arrayStore", true, err
		}
		index, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.arrayStore", true, err
		}
		value, err := r.CPU.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.arrayStore", true, err
		}
		return guest.WIPIReturn{}, "RAPTOR.java.arrayStore", true,
			r.storeRaptorJavaArray(array, index, value)
	case 86: // getfield-style accessor: r0=object, r2=byte offset into fields.
		// Only small offsets are treated as field-block indices; a large r2 is
		// not a field offset (some call sites pass an unrelated reference), so
		// it falls through to the safe zero return the unimplemented path also
		// gives, rather than reading an out-of-range address.
		obj, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.getField", true, err
		}
		off, _ := r.CPU.ReadRegister(cpu.RegisterR2)
		var value uint32
		if obj != 0 && off < raptorJavaMaxFieldOffset {
			if body, rerr := r.Public.ReadU32(obj + 8); rerr == nil && body != 0 {
				value, _ = r.Public.ReadU32(body + off)
			}
		}
		return guest.WIPIReturn{Low: value}, "RAPTOR.java.getField", true, nil
	case 87: // putfield-style accessor: r0=object, r1=value, r2=byte offset.
		obj, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "RAPTOR.java.putField", true, err
		}
		value, _ := r.CPU.ReadRegister(cpu.RegisterR1)
		off, _ := r.CPU.ReadRegister(cpu.RegisterR2)
		if obj != 0 && off < raptorJavaMaxFieldOffset {
			if body, rerr := r.Public.ReadU32(obj + 8); rerr == nil && body != 0 {
				_ = r.Public.WriteU32(body+off, value)
			}
		}
		return guest.WIPIReturn{}, "RAPTOR.java.putField", true, nil
	}
	return guest.WIPIReturn{}, "", false, nil
}

func (r *Runtime) raptorJavaStringLiteral(
	address, length, cache uint32,
) (uint32, error) {
	if cache != 0 {
		value, err := r.Public.ReadU32(cache)
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
		if err := r.CPU.ReadMemory(address, data); err != nil {
			return 0, err
		}
	}
	units := make([]uint16, length)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(data[index*2:])
	}
	value, err := r.NewRaptorJavaString(string(utf16.Decode(units)))
	if err != nil {
		return 0, err
	}
	if cache != 0 {
		if err := r.Public.WriteU32(cache, value); err != nil {
			return 0, err
		}
	}
	return value, nil
}

func (r *Runtime) allocateJavaCString(value string) (uint32, error) {
	address, err := r.Public.Heap.Allocate(uint32(len(value)+1), true)
	if err != nil || address == 0 {
		return 0, errors.New("allocate Raptor Java string")
	}
	if err := r.CPU.WriteMemory(address, append([]byte(value), 0)); err != nil {
		return 0, err
	}
	return address, nil
}

func (r *Runtime) registerRaptorJavaClasses(
	java *JavaRuntime,
	address uint32,
) error {
	count, err := r.Public.ReadU32(address)
	if err != nil {
		return err
	}
	if count > 4096 {
		return fmt.Errorf("Raptor Java public class count %d exceeds limit", count)
	}
	for index := uint32(0); index < count; index++ {
		holder, err := r.Public.ReadU32(address + 8 + index*4)
		if err != nil {
			return err
		}
		// Some registration tables carry reserved null slots ahead of the real
		// holders (체스마스터 passes count=4 with the first slot empty).
		if holder == 0 {
			continue
		}
		if _, err := r.inspectRaptorJavaClass(java, holder); err != nil {
			return err
		}
	}
	return nil
}

// RecoverUnregisteredMainClass links the Jlet subclass named by the manifest
// when an older-SDK Raptor build omitted it from registerClasses. Those builds
// publish only helper classes through the public registration table, yet the
// main Clet descriptor still lives in the module image with a valid holder,
// method table, and startApp body. The class name string sits in the module's
// read-only data; the descriptor stores a pointer to it at +8 and each holder
// stores a pointer to its descriptor at +8, so the name locates the descriptor
// and the descriptor locates the holder. Returns nil without error when no
// matching descriptor exists (a genuinely absent main class stays a failure).
func (r *Runtime) RecoverUnregisteredMainClass(
	java *JavaRuntime,
	name string,
) (*raptorJavaClass, error) {
	if name == "" {
		return nil, nil
	}
	// Class names are stored slash-separated in the descriptor, but a manifest
	// main class may arrive dot-separated; try both spellings.
	candidates := []string{name}
	if slashed := strings.ReplaceAll(name, ".", "/"); slashed != name {
		candidates = append(candidates, slashed)
	}
	for _, candidate := range candidates {
		if class := java.ClassByName[candidate]; class != nil {
			return class, nil
		}
	}
	var (
		nameAddr uint32
		spelling string
	)
	for _, candidate := range candidates {
		// The class-name pool can sit well past the first megabyte of the
		// module image (간호사타이쿤2 stores it near 0x00176000), so scan the whole
		// low image; scanGuestCString skips unmapped pages, so the wider bound
		// only costs a page walk when the name is absent.
		if addr := r.scanGuestCString(candidate, 0x00001000, 0x00400000); addr != 0 {
			nameAddr, spelling = addr, candidate
			break
		}
	}
	if nameAddr == 0 {
		return nil, nil
	}
	descriptor := r.scanGuestWord(nameAddr, 0x01000000, 0x01500000)
	if descriptor < 8 {
		return nil, nil
	}
	descriptor -= 8
	holder := r.scanGuestWord(descriptor, 0x01000000, 0x01500000)
	if holder < 8 {
		return nil, nil
	}
	holder -= 8
	class, err := r.inspectRaptorJavaClass(java, holder)
	if err != nil {
		return nil, err
	}
	if class == nil || class.Name != spelling {
		return nil, nil
	}
	// Publish under the requested spelling too so the caller's lookup by the
	// manifest name resolves after a dot/slash normalization.
	if class.Name != name {
		java.ClassByName[name] = class
	}
	// Link the recovered class's virtual table exactly as linkClasses does for
	// the public set so its startApp can dispatch inherited virtuals.
	if class.vtable == 0 && len(java.flatVirtual) > 0 {
		if err := r.buildRaptorJavaVTable(java, class, uint32(len(java.flatVirtual))); err != nil {
			return nil, err
		}
	}
	return class, nil
}

// scanGuestCString returns the guest address of a NUL-terminated copy of want
// within [start, end), or 0. It reads a page at a time so unmapped gaps in the
// module image are skipped instead of aborting the whole scan.
func (r *Runtime) scanGuestCString(want string, start, end uint32) uint32 {
	target := append([]byte(want), 0)
	const page = 0x1000
	buf := make([]byte, page+uint32(len(target)))
	for base := start; base < end; base += page {
		n := uint32(len(buf))
		if base+n > end {
			n = end - base
		}
		if r.CPU.ReadMemory(base, buf[:n]) != nil {
			continue
		}
		for i := 0; i+len(target) <= int(n); i++ {
			if buf[i] != target[0] {
				continue
			}
			if string(buf[i:i+len(target)]) != string(target) {
				continue
			}
			if i == 0 || buf[i-1] == 0 {
				return base + uint32(i)
			}
		}
	}
	return 0
}

// scanGuestWord returns the guest address of the first 4-byte-aligned word equal
// to value within [start, end), or 0.
func (r *Runtime) scanGuestWord(value, start, end uint32) uint32 {
	const page = 0x1000
	buf := make([]byte, page)
	for base := start; base < end; base += page {
		n := uint32(len(buf))
		if base+n > end {
			n = end - base
		}
		if r.CPU.ReadMemory(base, buf[:n]) != nil {
			continue
		}
		for i := 0; i+4 <= int(n); i += 4 {
			w := uint32(buf[i]) | uint32(buf[i+1])<<8 |
				uint32(buf[i+2])<<16 | uint32(buf[i+3])<<24
			if w == value {
				return base + uint32(i)
			}
		}
	}
	return 0
}

func (r *Runtime) inspectRaptorJavaClass(
	java *JavaRuntime,
	holder uint32,
) (*raptorJavaClass, error) {
	if class := java.classes[holder]; class != nil {
		return class, nil
	}
	descriptor, err := r.Public.ReadU32(holder + 8)
	if err != nil || descriptor == 0 {
		return nil, fmt.Errorf("inspect Raptor Java class holder 0x%08x", holder)
	}
	nameAddress, err := r.Public.ReadU32(descriptor + 8)
	if err != nil {
		return nil, err
	}
	nameBytes, err := r.Public.ReadCString(nameAddress)
	if err != nil || len(nameBytes) == 0 {
		return nil, fmt.Errorf("inspect Raptor Java class name at 0x%08x", nameAddress)
	}
	parentAddress, _ := r.Public.ReadU32(descriptor + 0x10)
	parentBytes, _ := r.Public.ReadCString(parentAddress)
	fieldWord, _ := r.Public.ReadU32(descriptor + 0x18)
	staticBase, _ := r.Public.ReadU32(descriptor + 0x48)
	if staticBase > 0xffff {
		return nil, fmt.Errorf("Raptor Java class %q static base %d exceeds limit", string(nameBytes), staticBase)
	}
	vtable, _ := r.Public.ReadU32(descriptor + 0x0c)
	guestVTable := vtable
	class := &raptorJavaClass{
		Holder:      holder,
		descriptor:  descriptor,
		Name:        string(nameBytes),
		parentName:  string(parentBytes),
		fieldSize:   fieldWord & 0xffff,
		staticBase:  staticBase,
		vtable:      vtable,
		guestVTable: guestVTable,
	}
	java.classes[holder] = class
	java.ClassByName[class.Name] = class
	java.classOrder = append(java.classOrder, class)
	methods, _ := r.Public.ReadU32(descriptor + 0x38)
	if methods != 0 {
		methodCount, readErr := r.Public.ReadU32(methods)
		if readErr != nil || methodCount > 4096 {
			return nil, fmt.Errorf("inspect Raptor Java methods for %q", class.Name)
		}
		class.methods = make([]raptorJavaDeclaredMethod, 0, methodCount)
		for index := uint32(0); index < methodCount; index++ {
			record := methods + 4 + index*28
			methodNameAddress, _ := r.Public.ReadU32(record + 4)
			methodTypeAddress, _ := r.Public.ReadU32(record + 8)
			flags, _ := r.Public.ReadU32(record + 12)
			body, _ := r.Public.ReadU32(record + 20)
			methodName, nameErr := r.Public.ReadCString(methodNameAddress)
			methodType, typeErr := r.Public.ReadCString(methodTypeAddress)
			if nameErr != nil || typeErr != nil || len(methodName) == 0 {
				continue
			}
			class.methods = append(class.methods, raptorJavaDeclaredMethod{
				Name:       string(methodName),
				descriptor: string(methodType),
				Body:       body,
				flags:      uint16(flags),
			})
		}
	}
	fields, _ := r.Public.ReadU32(descriptor + 0x3c)
	if fields != 0 {
		fieldCount, readErr := r.Public.ReadU32(fields)
		if readErr != nil || fieldCount > 4096 {
			return nil, fmt.Errorf("inspect Raptor Java fields for %q", class.Name)
		}
		class.fields = make([]raptorJavaDeclaredField, 0, fieldCount)
		for index := uint32(0); index < fieldCount; index++ {
			record := fields + 4 + index*20
			fieldNameAddress, _ := r.Public.ReadU32(record + 4)
			fieldTypeAddress, _ := r.Public.ReadU32(record + 8)
			fieldIndex, _ := r.Public.ReadU32(record + 16)
			fieldName, nameErr := r.Public.ReadCString(fieldNameAddress)
			fieldType, typeErr := r.Public.ReadCString(fieldTypeAddress)
			if nameErr != nil || typeErr != nil || len(fieldName) == 0 {
				continue
			}
			class.fields = append(class.fields, raptorJavaDeclaredField{
				Name:       string(fieldName),
				descriptor: string(fieldType),
				index:      fieldIndex,
			})
		}
	}
	// A class that arrives after linkClasses brings field indices the published
	// table could not have carried, so the table is resolved again.
	if len(class.fields) != 0 && java.fieldOffsets != 0 {
		if err := r.resolveRaptorJavaFieldOffsets(java); err != nil {
			return nil, err
		}
	}
	return class, nil
}

func (r *Runtime) readRaptorJavaArguments(count int) ([]uint32, error) {
	arguments := make([]uint32, count)
	for index := range arguments {
		if index < 4 {
			value, err := r.CPU.ReadRegister(cpu.RegisterR0 + uint32(index))
			if err != nil {
				return nil, err
			}
			arguments[index] = value
			continue
		}
		stack, err := r.CPU.ReadRegister(cpu.RegisterSP)
		if err != nil {
			return nil, err
		}
		value, err := r.Public.ReadU32(stack + uint32(index-4)*4)
		if err != nil {
			return nil, err
		}
		arguments[index] = value
	}
	return arguments, nil
}

func (r *Runtime) linkRaptorJavaClasses(java *JavaRuntime) error {
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
	classCount, err := r.Public.ReadU32(importedClasses)
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
		nameAddress, _ := r.Public.ReadU32(record)
		nameBytes, nameErr := r.Public.ReadCString(nameAddress)
		if nameErr != nil || len(nameBytes) == 0 {
			return fmt.Errorf("inspect Raptor imported Java class %d", index)
		}
		staticFieldRange, _ := r.Public.ReadU32(record + 8)
		virtualRange, _ := r.Public.ReadU32(record + 12)
		staticMethodRange, _ := r.Public.ReadU32(record + 20)
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
	// The virtual offset table runs to the next linker-filled table in the zero
	// section; SDK revisions order those tables differently, and in the older
	// layout the virtual table is last, bounded only by the section end.
	virtualEnd := uint32(0)
	for _, boundary := range []uint32{
		fieldOffsets, staticFieldOffsets, staticMethodOffsets, arguments[9],
	} {
		if boundary > virtualMethodOffsets && (virtualEnd == 0 || boundary < virtualEnd) {
			virtualEnd = boundary
		}
	}
	if virtualEnd == 0 {
		if bss, ok := r.Pkg.Image.ZeroSection(); ok &&
			virtualMethodOffsets >= bss.Address &&
			virtualMethodOffsets < bss.Address+bss.Size {
			virtualEnd = bss.Address + bss.Size
		}
	}
	virtualCount := uint32(0)
	if virtualEnd > virtualMethodOffsets {
		virtualCount = (virtualEnd - virtualMethodOffsets) / 2
	}
	if virtualCount < maxVirtual || virtualCount > 4096 {
		return fmt.Errorf("invalid Raptor Java virtual method table size %d", virtualCount)
	}
	// The boundary can overshoot the true method count (alignment padding, or
	// the open-ended older layout), so trim trailing entries with no name.
	effectiveCount := maxVirtual
	names := make([]string, virtualCount)
	descriptors := make([]string, virtualCount)
	for index := uint32(0); index < virtualCount; index++ {
		nameAddress, _ := r.Public.ReadU32(virtualMethods + index*8)
		typeAddress, _ := r.Public.ReadU32(virtualMethods + index*8 + 4)
		name, _ := r.Public.ReadCString(nameAddress)
		descriptor, _ := r.Public.ReadCString(typeAddress)
		names[index], descriptors[index] = string(name), string(descriptor)
		if len(name) != 0 && len(descriptor) != 0 {
			effectiveCount = index + 1
		}
	}
	java.flatVirtual = make([]raptorJavaMethod, effectiveCount)
	for index := uint32(0); index < effectiveCount; index++ {
		java.flatVirtual[index] = raptorJavaMethod{
			Name: names[index], descriptor: descriptors[index],
		}
		// Generated code computes vtable + offset*4 + 4; the offsets are
		// published from raptorJavaFlatVirtualBase so the flat entries land
		// past every fixed slot.
		var encoded [2]byte
		binary.LittleEndian.PutUint16(
			encoded[:],
			uint16(raptorJavaFlatVirtualBase+index*2),
		)
		if err := r.CPU.WriteMemory(virtualMethodOffsets+index*2, encoded[:]); err != nil {
			return err
		}
	}
	for _, entry := range imports {
		for offset := uint32(0); offset < uint32(entry.virtualCount); offset++ {
			index := uint32(entry.virtualStart) + offset
			java.flatVirtual[index].className = entry.class.Name
		}
		if err := r.buildRaptorJavaVTable(java, entry.class, effectiveCount); err != nil {
			return err
		}
		for offset := uint32(0); offset < uint32(entry.staticMethodCount); offset++ {
			index := uint32(entry.staticMethodStart) + offset
			method := raptorJavaMethod{className: entry.class.Name, isStatic: true}
			nameAddress, _ := r.Public.ReadU32(staticMethods + index*8)
			typeAddress, _ := r.Public.ReadU32(staticMethods + index*8 + 4)
			name, _ := r.Public.ReadCString(nameAddress)
			descriptor, _ := r.Public.ReadCString(typeAddress)
			method.Name, method.descriptor = string(name), string(descriptor)
			if offset < 2 || method.Name == "" {
				method.Name = "<class>"
				method.descriptor = "()V"
			}
			if method.Name == "<init>" {
				method.isStatic = false
			}
			stub, err := r.registerJavaHostMethod(method)
			if err != nil {
				return err
			}
			if err := r.Public.WriteU32(staticMethodOffsets+index*4, stub|1); err != nil {
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
		if err := r.CPU.WriteMemory(staticFieldOffsets+index*2, encoded[:]); err != nil {
			return err
		}
		_, _ = r.Public.ReadU32(staticFields + index*8)
	}
	if bss, ok := r.Pkg.Image.ZeroSection(); ok &&
		fieldOffsets >= bss.Address && fieldOffsets < bss.Address+bss.Size {
		// The field-offset table runs to the next linker-filled table, not the
		// end of the zero section. Older SDK layouts place the resolved
		// static-method pointer table (and the other offset tables) *after*
		// fieldOffsets in .bss; overrunning to the section end here would
		// overwrite those freshly written function pointers with field indices
		// (메이플스토리2007 crashed constructing its main class because its
		// staticMethodOffsets table was clobbered right after linkClasses).
		fieldEnd := bss.Address + bss.Size
		for _, boundary := range []uint32{
			staticFieldOffsets, virtualMethodOffsets, staticMethodOffsets, arguments[9],
		} {
			if boundary > fieldOffsets && boundary < fieldEnd {
				fieldEnd = boundary
			}
		}
		fieldCount := (fieldEnd - fieldOffsets) / 2
		if fieldCount > 4096 {
			return fmt.Errorf("invalid Raptor Java field table size %d", fieldCount)
		}
		java.fieldOffsets = fieldOffsets
		java.fieldNames = fields
		java.fieldCount = fieldCount
		if err := r.resolveRaptorJavaFieldOffsets(java); err != nil {
			return err
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

func (r *Runtime) ensureRaptorHostClass(
	java *JavaRuntime,
	name string,
) (*raptorJavaClass, error) {
	if class := java.ClassByName[name]; class != nil {
		return class, nil
	}
	hostClass, err := java.Host.EnsureJavaClass(name)
	if err != nil {
		return nil, err
	}
	hostInfo, err := java.Host.InspectJavaClass(hostClass)
	if err != nil {
		return nil, err
	}
	parentName := ""
	if hostInfo.Parent != 0 {
		parent, parentErr := java.Host.InspectJavaClass(hostInfo.Parent)
		if parentErr != nil {
			return nil, parentErr
		}
		parentName = parent.Name
	}
	fieldWords := (uint32(hostInfo.FieldSize) + 3) / 4
	holder, err := r.Public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		return nil, errors.New("allocate Raptor Java host class holder")
	}
	descriptor, err := r.Public.Heap.Allocate(0x4c, true)
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
	if err := r.Public.WriteU32(holder+8, descriptor); err != nil {
		return nil, err
	}
	for offset, value := range map[uint32]uint32{
		0: 0x21, 8: nameAddress, 0x10: parentAddress,
		0x18: fieldWords,
	} {
		if err := r.Public.WriteU32(descriptor+offset, value); err != nil {
			return nil, err
		}
	}
	class := &raptorJavaClass{
		Holder: holder, descriptor: descriptor, Name: name,
		parentName: parentName, fieldSize: fieldWords,
		hostClass: hostClass,
	}
	java.classes[holder] = class
	java.ClassByName[name] = class
	java.classOrder = append(java.classOrder, class)
	return class, nil
}

// resolveRaptorJavaFieldOffsets publishes an index for every field the module
// names, into the table its generated code reads a field's slot from.
//
// It is separated from linkClasses because a module may register the classes
// that own those fields *after* it links. 현영맞고2006 registers nine
// field-less helper classes, links, and only then loads Hcvs - the Card
// subclass that owns 378 of the module's 370 named fields. Resolving once at
// link time left every entry zero, so each of that title's getfields read slot
// zero: its paint() read what it believed was its Graphics field, found the
// unrelated object living in slot zero, took the "already painting" early exit
// and drew nothing, so the title booted to a black screen and never repainted
// (issue #79).
func (r *Runtime) resolveRaptorJavaFieldOffsets(java *JavaRuntime) error {
	if java.fieldOffsets == 0 || java.fieldCount == 0 {
		return nil
	}
	previousFieldIndex := uint32(0)
	previousFieldWide := false
	for index := uint32(0); index < java.fieldCount; index++ {
		nameAddress, _ := r.Public.ReadU32(java.fieldNames + index*8)
		typeAddress, _ := r.Public.ReadU32(java.fieldNames + index*8 + 4)
		name, _ := r.Public.ReadCString(nameAddress)
		descriptor, _ := r.Public.ReadCString(typeAddress)
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
		if err := r.CPU.WriteMemory(java.fieldOffsets+index*2, encoded[:]); err != nil {
			return err
		}
	}
	return nil
}

func raptorJavaLinkedFieldIndex(
	java *JavaRuntime,
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
			if declared.Name == name && declared.descriptor == descriptor {
				fieldIndex = declared.index
				break
			}
		}
	}
	return fieldIndex, wide
}

func (r *Runtime) buildRaptorJavaVTable(
	java *JavaRuntime,
	class *raptorJavaClass,
	count uint32,
) error {
	if count == 0 {
		return nil
	}
	// Resolve the class's +0x20 method table and its holder shift up front. It
	// is the fully-linked virtual dispatch table, and compiler-inlined call
	// sites read fixed byte offsets from the vtable (e.g. 놈3 calls vtable+0x88)
	// that can lie past the flat-metadata slot count. Size the vtable to the
	// table's real method extent so those slots exist and get filled; a vtable
	// truncated to count*8+8 leaves them null and the guest branches to 0.
	methodTable, holderOffset := uint32(0), uint32(0)
	if mt, tableErr := r.Public.ReadU32(class.descriptor + 0x20); tableErr == nil &&
		mt >= 0x01000000 {
		// The holder back-reference sits within a variable-size header (observed
		// at +0x04 for some classes, +0x0c for others). The runtime vtable stores
		// the holder at +0x00, so vtable[offset] == methodTable[offset+holderOffset].
		for probe := uint32(4); probe <= 0x20; probe += 4 {
			if candidate, _ := r.Public.ReadU32(mt + probe); candidate == class.Holder {
				methodTable, holderOffset = mt, probe
				break
			}
		}
	}
	// Guest vtables hold the class holder at +0 followed by 4-byte slots.
	// Linked flat entries live at raptorJavaFlatVirtualSlot(index) (matching
	// the offsets the link step publishes); SDK classes additionally pin
	// methods at fixed byte offsets that compiler-inlined call sites hardcode.
	vtableSize := raptorJavaFlatVirtualSlot(count) + 4
	if methodTable != 0 {
		// Walk the table's method region (contiguous code pointers in the low
		// image, interspersed with zero gaps) until it ends at a class-data
		// pointer (>= 0x01000000) or a hard cap, tracking the last real method.
		lastCode := uint32(0)
		for offset := uint32(0x2c); offset < 0x400; offset += 4 {
			value, readErr := r.Public.ReadU32(methodTable + offset + holderOffset)
			if readErr != nil || value >= 0x01000000 {
				break
			}
			if value != 0 {
				lastCode = offset
			}
		}
		if extent := lastCode + 4; extent > vtableSize {
			vtableSize = extent
		}
	}
	vtable, err := r.Public.Heap.Allocate(vtableSize, true)
	if err != nil || vtable == 0 {
		return errors.New("allocate Raptor Java vtable")
	}
	if err := r.Public.WriteU32(vtable, class.Holder); err != nil {
		return err
	}
	for index, method := range java.flatVirtual {
		procedure := uint32(0)
		if method.Name != "" && r.raptorClassImplements(java, class, method.className) {
			stub, stubErr := r.registerJavaHostMethod(raptorJavaMethod{
				className: method.className, Name: method.Name,
				descriptor: method.descriptor,
			})
			if stubErr != nil {
				return stubErr
			}
			// Host trampolines are Thumb code; force the interworking bit.
			procedure = stub | 1
		}
		for _, declared := range class.methods {
			if declared.Name == method.Name && declared.descriptor == method.descriptor {
				// The declared body pointer already carries the ARM/Thumb
				// interworking bit; preserve it so a bx into an ARM method body
				// does not switch the CPU into Thumb mode.
				procedure = declared.Body
				break
			}
		}
		if procedure != 0 {
			if err := r.Public.WriteU32(
				vtable+raptorJavaFlatVirtualSlot(uint32(index)),
				procedure,
			); err != nil {
				return err
			}
		}
	}
	// SDK slots are inherited: apply fixed layouts root-first along the class
	// chain so a subclass's own fixed entries override its ancestors', and an
	// application override declared for a fixed slot dispatches to its Body.
	var chain []*raptorJavaClass
	for walk, depth := class, 0; walk != nil && depth < 256; depth++ {
		chain = append(chain, walk)
		walk = java.ClassByName[walk.parentName]
	}
	fixedNames := []string{"java/lang/Object"}
	for index := len(chain) - 1; index >= 0; index-- {
		fixedNames = append(fixedNames, chain[index].Name)
	}
	for _, fixedName := range fixedNames {
		for _, method := range raptorJavaFixedVirtualMethods[fixedName] {
			procedure := uint32(0)
			for _, declaring := range chain {
				if declared, found := DeclaredMethod(declaring, method.Name, method.descriptor); found && declared.Body != 0 {
					// Preserve the declared body's interworking bit (ARM or Thumb).
					procedure = declared.Body
					break
				}
			}
			if procedure == 0 {
				registered, registerErr := r.registerJavaHostMethod(raptorJavaMethod{
					className: fixedName,
					Name:      method.Name, descriptor: method.descriptor,
				})
				if registerErr != nil {
					return registerErr
				}
				// Host trampolines are Thumb code.
				procedure = registered | 1
			}
			if err := r.Public.WriteU32(vtable+method.offset, procedure); err != nil {
				return err
			}
		}
	}
	// The guest lays each class's own virtual method bodies inline in the
	// descriptor starting at +0x2c (right after the Object slots), and
	// compiler-inlined call sites read vtable+0x2c, +0x30, ... directly. The
	// flat/fixed passes above populate slots only for methods discovered via the
	// +0x38 metadata table, which older-SDK helper classes omit entirely — so
	// copy the descriptor's inline bodies into the still-empty own-method slots
	// at their matching offsets. Descriptor code pointers live in the low image;
	// the trailing metadata/table fields point into the class-data region and
	// terminate the run, as does a zero slot.
	for offset := uint32(0x2c); offset < vtableSize; offset += 4 {
		body, readErr := r.Public.ReadU32(class.descriptor + offset)
		if readErr != nil || body == 0 || body >= 0x01000000 {
			break
		}
		existing, _ := r.Public.ReadU32(vtable + offset)
		if existing != 0 {
			continue
		}
		if err := r.Public.WriteU32(vtable+offset, body); err != nil {
			return err
		}
	}
	// Older-SDK classes also carry a fuller per-class method table pointed to by
	// descriptor+0x20 (its holder back-reference sits at +0x04, and method bodies
	// occupy the exact vtable byte offsets the compiler-inlined call sites use,
	// including inherited/interface slots the inline block above does not cover,
	// e.g. 월드장기체스 CCC calls vtable+0x48). When that table belongs to this class,
	// copy its code entries into still-empty vtable slots at matching offsets.
	// Only empty slots are filled and only guest-image code pointers are copied,
	// so Object stubs, declared bodies, and the inline block are all preserved.
	// The table and its holder shift were resolved above to size the vtable.
	if methodTable != 0 {
		// The +0x20 table is the class's fully-linked method vtable and is
		// authoritative for the slots it fills, so it overrides the inline
		// descriptor block copied above (which can carry a different method
		// for the same slot). Its Object-slot region is zero, so the host
		// Object stubs and flat slots below 0x2c survive (zero is skipped).
		for offset := uint32(0x2c); offset < vtableSize; offset += 4 {
			body, readErr := r.Public.ReadU32(methodTable + offset + holderOffset)
			if readErr != nil || body == 0 || body >= 0x01000000 {
				continue
			}
			if err := r.Public.WriteU32(vtable+offset, body); err != nil {
				return err
			}
		}
	}
	// Backstop any own-method slot no source resolved. Host classes (String,
	// collections) and unlinked helpers expose more virtual methods than the
	// fixed/table sources cover; a compiler-inlined call to such a slot would
	// otherwise load 0 and branch to address 0 (놈3 calls String.vtable+0x88).
	// A no-op trampoline keeps the guest running; slots the title never calls
	// are unaffected, so working titles cannot regress.
	// Cover every method slot from the first (index 0 at +0x04), not only the
	// own-method region at +0x2c: a class can dispatch a low/Object-region slot
	// (e.g. spiderman3's class "d" calls index 0 -> vtable+0x04) that neither the
	// flat, fixed, nor table passes resolved, leaving 0 and branching to address
	// 0. Only still-empty slots are filled, so Object stubs, declared bodies, the
	// inline block, and the +0x20 table all survive and working titles that call
	// a populated slot are unaffected. Slot +0x00 is the class holder, not a
	// method, so start at +0x04.
	if noop, noopErr := r.raptorJavaNoopStub(java); noopErr == nil && noop != 0 {
		for offset := uint32(0x04); offset < vtableSize; offset += 4 {
			existing, readErr := r.Public.ReadU32(vtable + offset)
			if readErr != nil || existing != 0 {
				continue
			}
			if err := r.Public.WriteU32(vtable+offset, noop); err != nil {
				return err
			}
		}
	}
	class.vtable = vtable
	if err := r.Public.WriteU32(class.descriptor+0x0c, vtable); err != nil {
		return err
	}
	return r.SyncRaptorJavaVTables(java)
}

// SyncRaptorJavaVTables publishes linked class tables to host objects that
// were mirrored before the imported Java class graph finished linking.
func (r *Runtime) SyncRaptorJavaVTables(java *JavaRuntime) error {
	for instance := range java.lgtToKTF {
		holder, err := r.Public.ReadU32(instance + 4)
		if err != nil {
			return err
		}
		class := java.classes[holder]
		if class == nil || class.vtable == 0 {
			continue
		}
		if err := r.Public.WriteU32(instance, class.vtable); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) raptorClassImplements(
	java *JavaRuntime,
	class *raptorJavaClass,
	wanted string,
) bool {
	if wanted == "" {
		return false
	}
	for depth := 0; class != nil && depth < 256; depth++ {
		if class.Name == wanted {
			return true
		}
		if spec, ok := ktfrt.HostJavaClassSpecs[class.parentName]; ok {
			for name := class.parentName; name != ""; {
				if name == wanted {
					return true
				}
				next, exists := ktfrt.HostJavaClassSpecs[name]
				if !exists {
					break
				}
				name = next.Parent
			}
			_ = spec
			return false
		}
		class = java.ClassByName[class.parentName]
	}
	return false
}

func (r *Runtime) JavaInputCallback(
	event machinecore.InputEvent,
) (wipirt.GuestCallback, bool) {
	if r.Java == nil || r.Java.currentCard == 0 {
		return wipirt.GuestCallback{}, false
	}
	key, ok := guest.InputKeyCode(event.Control)
	if !ok {
		return wipirt.GuestCallback{}, false
	}
	class := r.raptorJavaClassForObject(r.Java, r.Java.currentCard)
	for depth := 0; class != nil && depth < 256; depth++ {
		method, found := DeclaredMethod(class, "keyNotify", "(II)Z")
		if found && method.Body != 0 {
			eventType := ktfrt.KeyReleased
			if event.Pressed {
				eventType = ktfrt.KeyPressed
			}
			return wipirt.GuestCallback{
				Procedure: method.Body,
				Args: [4]uint32{
					r.Java.currentCard,
					eventType,
					uint32(int32(key)),
				},
			}, true
		}
		class = r.Java.ClassByName[class.parentName]
	}
	return wipirt.GuestCallback{}, false
}

func (r *Runtime) wrapRaptorJavaObject(
	java *JavaRuntime,
	mirror uint32,
) (uint32, error) {
	if instance := java.ktfToLGT[mirror]; instance != 0 {
		return instance, nil
	}
	words, err := java.Host.ReadWords(mirror, 2)
	if err != nil {
		return 0, err
	}
	hostClass, err := java.Host.InspectJavaClass(words[1])
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
	instance, err := r.Public.Heap.Allocate(12, true)
	if err != nil || instance == 0 {
		return 0, errors.New("allocate Raptor Java host wrapper")
	}
	// An array the host returns (DataBase.selectRecord, String.getBytes) needs
	// a body the AOT can read: its length word followed by the elements, not
	// the one-word field block a plain object gets. Without it every such array
	// looked empty to the guest and indexing one threw out of bounds.
	bodyWords := max(uint32(4), class.fieldSize*4)
	mirrorBody, count, element, primitive, isArray := java.Host.ArrayShape(mirror)
	if isArray {
		if count > maxRaptorArraySyncElements {
			return 0, fmt.Errorf(
				"Raptor Java host array length %d exceeds limit", count,
			)
		}
		bodyWords = 4 + count*element
	}
	fields, err := r.Public.Heap.Allocate(bodyWords, true)
	if err != nil || fields == 0 {
		return 0, errors.New("allocate Raptor Java host wrapper fields")
	}
	if isArray {
		if err := r.Public.WriteU32(fields, count); err != nil {
			return 0, err
		}
		if primitive {
			r.noteRaptorPrimitiveArray(java, mirror, element)
		}
		if primitive && count != 0 {
			buffer := make([]byte, count*element)
			if err := r.CPU.ReadMemory(mirrorBody, buffer); err == nil {
				if err := r.CPU.WriteMemory(fields+4, buffer); err != nil {
					return 0, err
				}
			}
		}
	}
	for offset, value := range map[uint32]uint32{
		0: class.vtable, 4: class.Holder, 8: fields,
	} {
		if err := r.Public.WriteU32(instance+offset, value); err != nil {
			return 0, err
		}
	}
	java.lgtToKTF[instance] = mirror
	java.ktfToLGT[mirror] = instance
	if class.vtable != 0 {
		if err := r.Public.WriteU32(instance, class.vtable); err != nil {
			return 0, err
		}
	}
	return instance, nil
}

func (r *Runtime) NewRaptorJavaString(value string) (uint32, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	mirror, err := java.Host.NewJavaString(value)
	if err != nil {
		return 0, err
	}
	return r.wrapRaptorJavaObject(java, mirror)
}

func (r *Runtime) NewRaptorJavaReferenceArray(
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
	body, err := r.Public.ReadU32(array + 8)
	if err != nil {
		return 0, err
	}
	for index, element := range elements {
		if err := r.Public.WriteU32(body+4+uint32(index)*4, element); err != nil {
			return 0, err
		}
	}
	mirror := java.lgtToKTF[array]
	mirrorWords, err := java.Host.ReadWords(mirror, 2)
	if err != nil {
		return 0, err
	}
	for index, element := range elements {
		if mapped := java.lgtToKTF[element]; mapped != 0 {
			element = mapped
		}
		if err := java.Host.WriteU32(
			mirrorWords[0]+8+uint32(index)*4,
			element,
		); err != nil {
			return 0, err
		}
	}
	return array, nil
}

// ResolveRaptorJletMainClass returns the class that should receive the Jlet
// lifecycle. The manifest / launch-supplied main class is authoritative when it
// declares startApp(String[]), but obfuscated or launcher-wrapped titles name a
// helper as the main class while the real Jlet subclass has an obfuscated name
// (배틀몬스터: manifest MClass "Jp", launch names "Game" — a bare helper — while the
// only class extending org/kwis/msp/lcdui/Jlet with a startApp body is "a"). When
// the requested class has no startApp and exactly one registered class extends
// Jlet with a real startApp, use that class instead. Ambiguity (zero or several)
// keeps the requested class so the caller reports the original precise failure.
func (r *Runtime) ResolveRaptorJletMainClass(requested *raptorJavaClass) *raptorJavaClass {
	if requested == nil {
		return requested
	}
	if m, ok := DeclaredMethod(requested, "startApp", "([Ljava/lang/String;)V"); ok && m.Body != 0 {
		return requested
	}
	java := r.Java
	if java == nil {
		return requested
	}
	var candidate *raptorJavaClass
	count := 0
	for _, class := range java.ClassByName {
		m, ok := DeclaredMethod(class, "startApp", "([Ljava/lang/String;)V")
		if !ok || m.Body == 0 {
			continue
		}
		if !r.raptorClassExtendsJlet(java, class) {
			continue
		}
		candidate = class
		count++
	}
	if count == 1 {
		return candidate
	}
	return requested
}

func (r *Runtime) raptorClassExtendsJlet(java *JavaRuntime, class *raptorJavaClass) bool {
	for walk, depth := class, 0; walk != nil && depth < 256; depth++ {
		if walk.parentName == "org/kwis/msp/lcdui/Jlet" || walk.Name == "org/kwis/msp/lcdui/Jlet" {
			return true
		}
		walk = java.ClassByName[walk.parentName]
	}
	return false
}

func DeclaredMethod(
	class *raptorJavaClass,
	name, descriptor string,
) (raptorJavaDeclaredMethod, bool) {
	for _, method := range class.methods {
		if method.Name == name && method.descriptor == descriptor {
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
