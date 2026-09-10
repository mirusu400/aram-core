package ktf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func newMNTestRuntime(t testing.TB) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     make([]byte, 0x4000),
	})
	check(t, err)
	t.Cleanup(func() { _ = runtime.CPU.Close() })
	check(t, runtime.MapImageAndHost())
	return runtime
}

func TestKTFPrepareMNContextPublishesLoaderABIFields(t *testing.T) {
	runtime := newMNTestRuntime(t)
	const got = ImageBase + 0x3000
	check(t, runtime.prepareMNContext(got))

	if runtime.mnGOT != got || runtime.mnContext == 0 || runtime.JvmContext == 0 {
		t.Fatalf(
			"MN context = got 0x%08x context 0x%08x JVM 0x%08x",
			runtime.mnGOT,
			runtime.mnContext,
			runtime.JvmContext,
		)
	}
	if got := readU32(t, runtime, ImageBase+8*4); got != runtime.mnContext {
		t.Fatalf("descriptor context = 0x%08x, want 0x%08x", got, runtime.mnContext)
	}
	if got := readU32(t, runtime, runtime.mnContext+mnContextJVMField); got != runtime.JvmContext {
		t.Fatalf("MN JVM context = 0x%08x, want 0x%08x", got, runtime.JvmContext)
	}
	stack := readU32(t, runtime, runtime.mnContext+mnContextStackField)
	if stack == 0 {
		t.Fatal("MN call-out stack was not published")
	}
}

func TestKTFMNInterfaceSlot8ThrowsByClassName(t *testing.T) {
	runtime := newMNTestRuntime(t)
	address, err := runtime.ensureMNInterface()
	check(t, err)
	slot := readU32(t, runtime, address+8*4)
	host, ok := runtime.hostCalls[slot&^1]
	if !ok || host.name != "mn.throw" {
		t.Fatalf("MN slot 8 = 0x%08x/%q", slot, host.name)
	}
	name, err := runtime.allocateBytes([]byte("java/lang/NullPointerException"), true)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, name))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 0))
	_, err = host.handler(context.Background(), runtime)
	var unhandled *ktfUnhandledJavaException
	if !errors.As(err, &unhandled) || runtime.LastJavaThrowName != "java/lang/NullPointerException" {
		t.Fatalf("MN slot 8 error = %v, throw = %q", err, runtime.LastJavaThrowName)
	}
}

func TestKTFMNResolveMemberAndInvokePreserveScratchArguments(t *testing.T) {
	runtime := newMNTestRuntime(t)
	class := ensureClass(t, runtime, "java/lang/String")
	member, err := runtime.allocateBytes(
		append([]byte{'H'}, []byte("(II)Ljava/lang/String;+substring")...),
		true,
	)
	check(t, err)
	stack := allocWords(t, runtime, 6)
	check(t, runtime.writeWords(stack, []uint32{1, 3, 0, 0, 0, 0}))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, class))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, member))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, stack))
	handle, err := ktfMNResolveMember(context.Background(), runtime)
	check(t, err)
	if handle == 0 || readU32(t, runtime, stack)&1 == 0 {
		t.Fatalf("resolved member = handle 0x%08x entry 0x%08x", handle, readU32(t, runtime, stack))
	}

	receiver := newJavaString(t, runtime, "abcd")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, handle))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, receiver))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	value, err := ktfMNInvoke(context.Background(), runtime)
	check(t, err)
	if got := runtime.javaStringValue(value); got != "bc" {
		t.Fatalf("MN substring result = %q, want bc", got)
	}
	if got, _ := runtime.CPU.ReadRegister(cpu.RegisterSP); got != stack+8 {
		t.Fatalf("MN instance helper SP = 0x%08x, want 0x%08x", got, stack+8)
	}
	if _, ok := runtime.mnCallFrames[stack]; ok {
		t.Fatal("MN resolved-member scratch frame was not consumed")
	}
}

func TestKTFMNStaticInvokeRunsThreadSleepWithWideArgument(t *testing.T) {
	runtime := newMNTestRuntime(t)
	class := ensureClass(t, runtime, "java/lang/Thread")
	handle, err := runtime.resolveJavaMethod(class, "sleep", "(J)V")
	check(t, err)
	stack := allocWords(t, runtime, 6)
	check(t, runtime.writeWords(stack, []uint32{0, 0xfeed, 0, 0, 0, 0}))
	runtime.DeferThreads = true
	runtime.TickMS = 25
	task := &Task{}
	runtime.activeTask = task
	t.Cleanup(func() { runtime.activeTask = nil })
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, handle))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 150))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	_, err = ktfMNInvokeStatic(context.Background(), runtime)
	check(t, err)
	if task.WakeAtMS != 175 || !runtime.yieldRequested {
		t.Fatalf("MN Thread.sleep = wake %d yield %t", task.WakeAtMS, runtime.yieldRequested)
	}
	if got, _ := runtime.CPU.ReadRegister(cpu.RegisterSP); got != stack+8 {
		t.Fatalf("MN static helper SP = 0x%08x, want 0x%08x", got, stack+8)
	}
}

func defineMNCallMethod(
	t testing.TB,
	runtime *Runtime,
	name, descriptor string,
	handler ktfHostHandler,
) (uint32, uint32) {
	t.Helper()
	entry := runtime.RegisterHostCall("test.mn."+name, handler)
	fullName, err := runtime.allocateBytes(
		append([]byte{0}, []byte(descriptor+"+"+name)...),
		true,
	)
	check(t, err)
	method := allocWords(t, runtime, 7)
	check(t, runtime.writeWords(method, []uint32{
		entry,
		0,
		entry,
		fullName,
		0,
		0,
		0,
	}))
	inspected, err := runtime.InspectJavaMethod(method)
	check(t, err)
	runtime.rememberMNCallMethod(inspected)
	return method, entry
}

func captureMNParameters(target *[]uint32, count int) ktfHostHandler {
	return func(_ context.Context, runtime *Runtime) (uint32, error) {
		values := make([]uint32, count)
		for index := range values {
			value, err := runtime.parameter(uint32(index))
			if err != nil {
				return 0, err
			}
			values[index] = value
		}
		*target = values
		return 0, nil
	}
}

func requireMNParameters(t testing.TB, got, want []uint32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("MN parameters = %08x, want %08x", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("MN parameters = %08x, want %08x", got, want)
		}
	}
}

func TestKTFMNInvokeDirectEntriesUseDescriptorArity(t *testing.T) {
	runtime := newMNTestRuntime(t)

	var instanceCall []uint32
	_, instanceEntry := defineMNCallMethod(
		t,
		runtime,
		"instanceArity",
		"(IJI)V",
		captureMNParameters(&instanceCall, 6),
	)
	instanceStack := allocWords(t, runtime, 4)
	check(t, runtime.writeWords(instanceStack, []uint32{0x11, 0x22, 0x33, 0x44}))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, instanceEntry))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 0xcafe))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, instanceStack))
	_, err := ktfMNInvoke(context.Background(), runtime)
	check(t, err)
	requireMNParameters(
		t,
		instanceCall,
		[]uint32{0, 0xcafe, 0x11, 0x22, 0x33, 0x44},
	)

	cell := allocWords(t, runtime, 1)
	check(t, runtime.WriteU32(cell, instanceEntry))
	target, parameterWords, err := runtime.resolveMNCallTarget(cell, nil)
	check(t, err)
	if target != instanceEntry || parameterWords != 4 {
		t.Fatalf(
			"MN member cell = target 0x%08x words %d, want 0x%08x/4",
			target,
			parameterWords,
			instanceEntry,
		)
	}

	var staticCall []uint32
	_, staticEntry := defineMNCallMethod(
		t,
		runtime,
		"staticArity",
		"(IJI)V",
		captureMNParameters(&staticCall, 5),
	)
	staticStack := allocWords(t, runtime, 3)
	check(t, runtime.writeWords(staticStack, []uint32{0x22, 0x33, 0x44}))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, staticEntry))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 0x11))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, staticStack))
	_, err = ktfMNInvokeStatic(context.Background(), runtime)
	check(t, err)
	requireMNParameters(t, staticCall, []uint32{0, 0x11, 0x22, 0x33, 0x44})

	var noArgumentCall []uint32
	_, noArgumentEntry := defineMNCallMethod(
		t,
		runtime,
		"noArguments",
		"()V",
		captureMNParameters(&noArgumentCall, 2),
	)
	noArgumentStack := allocWords(t, runtime, 2)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, noArgumentEntry))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 0xbeef))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, noArgumentStack))
	_, err = ktfMNInvoke(context.Background(), runtime)
	check(t, err)
	requireMNParameters(t, noArgumentCall, []uint32{0, 0xbeef})
}

func TestKTFMNInvokeRejectsBadStackWithoutLeakingScratchFrame(t *testing.T) {
	runtime := newMNTestRuntime(t)
	_, entry := defineMNCallMethod(
		t,
		runtime,
		"badStack",
		"()V",
		captureMNParameters(new([]uint32), 2),
	)
	const stack = 0xfffffff8
	runtime.mnCallFrames = map[uint32]mnCallFrame{
		stack: {parameterWords: 0},
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, entry))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 1))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	if _, err := ktfMNInvoke(context.Background(), runtime); err == nil {
		t.Fatal("MN invocation with an unmapped stack succeeded")
	}
	if _, ok := runtime.mnCallFrames[stack]; ok {
		t.Fatal("MN invocation leaked its consumed scratch frame")
	}
}

func TestKTFMNNewTaskRestoresGOTAndContextRegisters(t *testing.T) {
	runtime := newMNTestRuntime(t)
	runtime.mnGOT = ImageBase + 0x3000
	runtime.mnContext = allocWords(t, runtime, mnContextWords)
	task, err := runtime.NewTask(ImageBase|1, nil, 0)
	check(t, err)
	check(t, runtime.restoreTaskContext(task))
	got, err := runtime.CPU.ReadRegister(cpu.RegisterR10)
	check(t, err)
	contextAddress, err := runtime.CPU.ReadRegister(cpu.RegisterR11)
	check(t, err)
	if got != runtime.mnGOT || contextAddress != runtime.mnContext {
		t.Fatalf(
			"MN task registers = r10 0x%08x r11 0x%08x, want 0x%08x/0x%08x",
			got,
			contextAddress,
			runtime.mnGOT,
			runtime.mnContext,
		)
	}
}

type mnLayoutMethod struct {
	name       string
	descriptor string
	slot       uint16
	body       uint32
}

func defineMNLayoutClass(
	t testing.TB,
	runtime *Runtime,
	object, descriptorAddress uint32,
	name string,
	parent uint32,
	fieldSize uint16,
	methods []mnLayoutMethod,
) (uint32, []uint32) {
	t.Helper()
	nameAddress, err := runtime.allocateBytes([]byte(name), true)
	check(t, err)
	methodList := allocWords(t, runtime, uint32(len(methods)+1))
	methodAddresses := make([]uint32, 0, len(methods))
	for index, spec := range methods {
		fullName, err := runtime.allocateBytes(
			append([]byte{0}, []byte(spec.descriptor+"+"+spec.name)...),
			true,
		)
		check(t, err)
		method := allocWords(t, runtime, 7)
		check(t, runtime.writeWords(method, []uint32{
			spec.body,
			object,
			0,
			fullName,
			0,
			uint32(spec.slot),
			0,
		}))
		check(t, runtime.WriteU32(methodList+uint32(index)*4, method))
		methodAddresses = append(methodAddresses, method)
	}
	field := uint32(0)
	fieldList := uint32(0)
	if fieldSize != 0 {
		fieldName, err := runtime.allocateBytes(append([]byte{0}, []byte("I+value")...), true)
		check(t, err)
		field = allocWords(t, runtime, 4)
		fieldList = allocWords(t, runtime, 2)
		check(t, runtime.writeWords(field, []uint32{1, object, fieldName, 0}))
		check(t, runtime.writeWords(fieldList, []uint32{field, 0}))
	}
	check(t, runtime.writeWords(descriptorAddress, []uint32{
		nameAddress,
		0,
		parent,
		methodList,
		0,
		fieldList,
		uint32(fieldSize)<<16 | uint32(len(methods)),
		0x21,
		0,
	}))
	check(t, runtime.writeWords(object, []uint32{object + 4, 0, descriptorAddress, 0, 0}))
	return field, methodAddresses
}

func TestKTFMNClassLayoutsLinkParentFirstAndOnlyOnce(t *testing.T) {
	runtime := newMNTestRuntime(t)
	const (
		parent           = ImageBase + 0x100
		parentDescriptor = ImageBase + 0x180
		child            = ImageBase + 0x200
		childDescriptor  = ImageBase + 0x280
	)
	parentField, parentMethods := defineMNLayoutClass(
		t,
		runtime,
		parent,
		parentDescriptor,
		"test/Parent",
		0,
		4,
		[]mnLayoutMethod{{name: "value", descriptor: "()I", slot: 2, body: ImageBase | 1}},
	)
	childField, childMethods := defineMNLayoutClass(
		t,
		runtime,
		child,
		childDescriptor,
		"test/Child",
		parent,
		8,
		[]mnLayoutMethod{
			{name: "value", descriptor: "()I", slot: 2, body: ImageBase + 2 | 1},
			{name: "extra", descriptor: "()V", slot: 4, body: ImageBase + 4 | 1},
		},
	)
	check(t, runtime.linkMNClassLayouts([]uint32{child, parent}))
	parentClass := inspectClass(t, runtime, parent)
	childClass := inspectClass(t, runtime, child)
	if parentClass.FieldSize != 4 || childClass.FieldSize != 12 {
		t.Fatalf("MN field sizes = parent %d child %d", parentClass.FieldSize, childClass.FieldSize)
	}
	if readU32(t, runtime, parentField+12) != 0 || readU32(t, runtime, childField+12) != 4 {
		t.Fatalf(
			"MN field offsets = parent %d child %d",
			readU32(t, runtime, parentField+12),
			readU32(t, runtime, childField+12),
		)
	}
	if got := readU32(t, runtime, parentClass.VTable+2*4); got != parentMethods[0] {
		t.Fatalf("parent vtable[2] = 0x%08x", got)
	}
	if got := readU32(t, runtime, childClass.VTable+2*4); got != childMethods[0] {
		t.Fatalf("child override vtable[2] = 0x%08x", got)
	}
	if got := readU32(t, runtime, childClass.VTable+4*4); got != childMethods[1] {
		t.Fatalf("child vtable[4] = 0x%08x", got)
	}
	linkedVTable := childClass.VTable
	check(t, runtime.linkMNClassLayouts([]uint32{parent, child}))
	childClass = inspectClass(t, runtime, child)
	if childClass.FieldSize != 12 || readU32(t, runtime, childField+12) != 4 || childClass.VTable != linkedVTable {
		t.Fatalf(
			"relinked child = size %d offset %d vtable 0x%08x, want 12/4/0x%08x",
			childClass.FieldSize,
			readU32(t, runtime, childField+12),
			childClass.VTable,
			linkedVTable,
		)
	}
}

func TestKTFMNClassLayoutRejectsInvalidFieldTables(t *testing.T) {
	t.Run("offset outside declaring footprint", func(t *testing.T) {
		runtime := newMNTestRuntime(t)
		const (
			parent           = ImageBase + 0x100
			parentDescriptor = ImageBase + 0x180
			child            = ImageBase + 0x200
			childDescriptor  = ImageBase + 0x280
		)
		defineMNLayoutClass(t, runtime, parent, parentDescriptor, "test/Parent", 0, 4, nil)
		field, _ := defineMNLayoutClass(
			t,
			runtime,
			child,
			childDescriptor,
			"test/Child",
			parent,
			4,
			nil,
		)
		check(t, runtime.WriteU32(field+12, 4))
		err := runtime.linkMNClassLayouts([]uint32{child, parent})
		if err == nil || !strings.Contains(err.Error(), "exceeds own footprint") {
			t.Fatalf("MN invalid field offset error = %v", err)
		}
		if got := readU32(t, runtime, field+12); got != 4 {
			t.Fatalf("MN invalid field offset mutated to %d", got)
		}
	})

	t.Run("missing terminator", func(t *testing.T) {
		runtime := newMNTestRuntime(t)
		const (
			parent           = ImageBase + 0x100
			parentDescriptor = ImageBase + 0x180
			object           = ImageBase + 0x200
			descriptor       = ImageBase + 0x280
		)
		defineMNLayoutClass(t, runtime, parent, parentDescriptor, "test/Parent", 0, 4, nil)
		field, _ := defineMNLayoutClass(
			t,
			runtime,
			object,
			descriptor,
			"test/Fields",
			parent,
			4,
			nil,
		)
		table := allocWords(t, runtime, 4096)
		entries := make([]uint32, 4096)
		for index := range entries {
			entries[index] = field
		}
		check(t, runtime.writeWords(table, entries))
		check(t, runtime.WriteU32(descriptor+5*4, table))
		err := runtime.linkMNClassLayouts([]uint32{object, parent})
		if err == nil || !strings.Contains(err.Error(), "field table exceeds") {
			t.Fatalf("MN unterminated field table error = %v", err)
		}
	})
}

func TestKTFMNClassLayoutRejectsUnrepresentableVTable(t *testing.T) {
	runtime := newMNTestRuntime(t)
	const (
		object     = ImageBase + 0x100
		descriptor = ImageBase + 0x180
	)
	defineMNLayoutClass(
		t,
		runtime,
		object,
		descriptor,
		"test/HugeVTable",
		0,
		0,
		[]mnLayoutMethod{{
			name:       "value",
			descriptor: "()I",
			slot:       ^uint16(0),
			body:       ImageBase | 1,
		}},
	)
	err := runtime.linkMNClassLayouts([]uint32{object})
	if err == nil || !strings.Contains(err.Error(), "vtable size") {
		t.Fatalf("MN oversized vtable error = %v", err)
	}
}

func TestKTFMNArrayCallbacksAllocatePrimitiveAndNestedArrays(t *testing.T) {
	runtime := newMNTestRuntime(t)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, 10*4))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 3))
	array, err := ktfMNPrimitiveArrayNew(context.Background(), runtime)
	check(t, err)
	fields := readU32(t, runtime, array)
	if got := readU32(t, runtime, fields+4); got != 3 {
		t.Fatalf("MN int[] length = %d, want 3", got)
	}

	class := ensureClass(t, runtime, "[[I")
	sizes := allocWords(t, runtime, 2)
	check(t, runtime.writeWords(sizes, []uint32{2, 3}))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, class))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 2))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, sizes))
	nested, err := ktfMNMultiArrayNew(context.Background(), runtime)
	check(t, err)
	nestedFields := readU32(t, runtime, nested)
	if got := readU32(t, runtime, nestedFields+4); got != 2 {
		t.Fatalf("MN int[][] length = %d, want 2", got)
	}
	for index := uint32(0); index < 2; index++ {
		child := readU32(t, runtime, nestedFields+8+index*4)
		childFields := readU32(t, runtime, child)
		if got := readU32(t, runtime, childFields+4); got != 3 {
			t.Fatalf("MN int[][] child %d length = %d, want 3", index, got)
		}
	}
}

func TestKTFMNEmbeddedStringUsesCompactHeader(t *testing.T) {
	runtime := newMNTestRuntime(t)
	source := newJavaString(t, runtime, "MN")
	characters, err := runtime.readJavaFieldWord(source, 0)
	check(t, err)
	const (
		object = ImageBase + 0x300
		fields = ImageBase + 0x320
		header = 0x280
	)
	check(t, runtime.writeWords(object, []uint32{fields, header}))
	check(t, runtime.writeWords(fields, []uint32{header, characters, 0, 2}))
	// Mapping has already finished. Marking the package relocatable here selects
	// the MN compact-String validation without asking the test fixture to carry a
	// complete relocatable load descriptor.
	runtime.Pkg.Relocations = []uint32{0}
	value, ok := runtime.readGuestJavaString(object)
	if !ok || value != "MN" {
		t.Fatalf("MN embedded String = %q, %t", value, ok)
	}

	const (
		nonStringObject = ImageBase + 0x340
		nonStringFields = ImageBase + 0x360
		nonStringHeader = 0x300
	)
	check(t, runtime.writeWords(
		nonStringObject,
		[]uint32{nonStringFields, nonStringHeader},
	))
	check(t, runtime.writeWords(
		nonStringFields,
		[]uint32{nonStringHeader, characters, 0, 2},
	))
	if value, ok := runtime.readGuestJavaString(nonStringObject); ok {
		t.Fatalf("MN non-String compact object decoded as %q", value)
	}
}

// TestKTFRegisterMNClassesStopsAtUnrelocatedSentinelRecord reproduces the
// gamearchive corpus crash: a module's class list does not always end at a
// null pointer. Some builds (every KTF WIPI1.1-era title probed) close it
// with a record whose fields were never relocated - a small offset sits
// where a class descriptor pointer belongs. Dereferencing it read guest
// address 0x508 and crashed the whole bootstrap. The walk has to recognize
// that shape and stop instead.
func TestKTFRegisterMNClassesStopsAtUnrelocatedSentinelRecord(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     make([]byte, 4096),
	})
	check(t, err)
	t.Cleanup(func() { _ = runtime.CPU.Close() })
	check(t, runtime.MapImageAndHost())

	name, err := runtime.allocateBytes(append([]byte("Real"), 0), true)
	check(t, err)
	descriptor := allocWords(t, runtime, 9)
	if err := runtime.writeWords(descriptor, []uint32{
		name, 0, 0, 0, 0, 0, 0, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}

	// Two class records laid out in the image the way a module carries them:
	// object := record - 4, and the record itself is read as five words
	// starting at record. The real class comes first; the sentinel follows
	// it, the same order the corpus titles carry it in.
	const (
		object      = ImageBase + 0x100
		record      = object + 4
		sentinel    = ImageBase + 0x200
		sentinelRec = sentinel + 4
		// Past every mapped region: the corpus sentinel's own "next" field,
		// which must end the walk rather than be dereferenced.
		outsideImage = 0x00080000
		// Not a pointer to anything: the corpus sentinel's own descriptor
		// field, which must not be dereferenced either.
		unrelocatedOffset = 0x500
	)
	moduleTable := uint32(record - mnModuleTableClasses)

	fields := []struct {
		address uint32
		value   uint32
	}{
		{object, 0x19},             // tag (unused by the walk itself)
		{object + 4, 0},            // record's own first word
		{object + 8, descriptor},   // class descriptor pointer
		{object + 12, 0},           // reserved
		{object + 16, 0},           // flags
		{object + 20, sentinelRec}, // next record

		{sentinel, 0x80000000},            // sentinel tag
		{sentinel + 4, 0},                 // record's own first word
		{sentinel + 8, unrelocatedOffset}, // never a real descriptor
		{sentinel + 12, 0},
		{sentinel + 16, 0},
		{sentinel + 20, outsideImage}, // next: outside every mapped region
	}
	for _, field := range fields {
		check(t, runtime.WriteU32(field.address, field.value))
	}

	if err := runtime.registerMNClasses(moduleTable, 0); err != nil {
		t.Fatalf("registerMNClasses returned %v, want the sentinel skipped and the real class registered", err)
	}
}
