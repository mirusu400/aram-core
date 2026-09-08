package raptor

import (
	"errors"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"image"
	"strconv"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestGuestHeapSharedAllocatorDoesNotOverlap(t *testing.T) {
	public := newPublicRuntime(t)
	peer := guest.Heap{CPU: public.CPU, Shared: &public.Heap}

	first, err := public.Heap.Allocate(24, true)
	if err != nil || first == 0 {
		t.Fatalf("allocate root block = 0x%08x, %v", first, err)
	}
	second, err := peer.Allocate(24, true)
	if err != nil || second == 0 {
		t.Fatalf("allocate shared block = 0x%08x, %v", second, err)
	}
	if first == second {
		t.Fatalf("root and shared heaps returned the same block 0x%08x", first)
	}
	if public.Heap.Root().Allocations[first] == 0 ||
		public.Heap.Root().Allocations[second] == 0 {
		t.Fatal("shared allocations were not recorded by the root heap")
	}
	if !peer.Release(first) {
		t.Fatal("shared heap could not release a root allocation")
	}
	reused, err := public.Heap.Allocate(24, true)
	if err != nil || reused != first {
		t.Fatalf("released root block reused at 0x%08x, %v; want 0x%08x", reused, err, first)
	}
}

// TestNewRaptorJavaObjectFieldsErrorNamesSizeAndCause exercises the field
// allocation failure path directly: it drains the guest heap to the exact
// byte, so the object header allocates but the field block after it cannot,
// and checks that the resulting error wraps errRaptorGuestHeapExhausted and
// names both the byte count and the class, rather than the old bare
// "allocate Raptor Java object fields" string that discarded the underlying
// cause (issue #200 asked for exactly this: a report that can tell a
// genuinely exhausted heap apart from an absurd field-count request).
func TestNewRaptorJavaObjectFieldsErrorNamesSizeAndCause(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	holder, err := public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	class := &raptorJavaClass{
		Holder:     holder,
		Name:       "app/Cramped",
		parentName: "java/lang/Object",
		fieldSize:  3,
	}
	java.classes[holder] = class
	java.ClassByName[class.Name] = class

	// Drain every free block but 16 bytes: Heap.Allocate rounds a request up
	// to a multiple of 8, so the 12-byte instance header actually reserves
	// 16, and leaving exactly that lets the header succeed while the field
	// block that follows finds nothing left.
	root := public.Heap.Root()
	if len(root.Free) != 1 {
		t.Fatalf("heap free list = %#v, want exactly one block for this test", root.Free)
	}
	free := root.Free[0].Size
	if free <= 16 {
		t.Fatalf("heap only has %d bytes free before the test drains it", free)
	}
	drain, err := public.Heap.Allocate(free-16, true)
	if err != nil || drain == 0 {
		t.Fatalf("drain heap = 0x%08x, %v", drain, err)
	}
	// Stage 2 of issue #200's heap-lifetime fix routes this allocation through
	// the collector's retry path, and drain is otherwise reachable from no
	// root at all - a real collection would correctly reclaim it as garbage,
	// which would let the second allocation below succeed and prove nothing.
	// Naming it from the class's own classObject field, which the stage 1
	// root walker marks, keeps the heap genuinely exhausted even after a
	// collection runs, without disturbing holder's guest memory (inspecting
	// the class re-reads that, and a garbage word there took a completely
	// different, unrelated failure path when this tried writing into it
	// instead).
	class.classObject = drain

	_, err = runtime.NewRaptorJavaObject(holder)
	if err == nil {
		t.Fatal("NewRaptorJavaObject succeeded against an exhausted heap")
	}
	if !errors.Is(err, errRaptorGuestHeapExhausted) {
		t.Fatalf("error %q does not wrap errRaptorGuestHeapExhausted", err)
	}
	wantSize := strconv.Itoa(int(class.fieldSize) * 4)
	if !strings.Contains(err.Error(), wantSize+" bytes") {
		t.Fatalf("error %q does not name the requested size (%s bytes)", err, wantSize)
	}
	if !strings.Contains(err.Error(), class.Name) {
		t.Fatalf("error %q does not name the class %q", err, class.Name)
	}
}

// TestNewRaptorJavaObjectSurvivesACollectionBetweenItsTwoAllocations covers a
// hazard stage 2 of issue #200's heap-lifetime fix introduced and this same
// package's own test suite caught before it shipped: NewRaptorJavaObject's
// instance header is not linked to anything - no vtable, no holder, no KTF
// mirror - until after the field block that follows it also allocates, so a
// collection the field allocation's retry-after-collect triggers used to see
// no root pointing at the header yet and free it, and the field allocation
// would then reclaim that exact address, aliasing the two: whichever field
// or header write happened second silently clobbered the other. The fix
// pins a heap block in java.constructing for as long as NewRaptorJavaObject
// is still building it. This drains the heap down to one legitimately
// unreferenced block a collection can and should free (so the second
// allocation genuinely needs one to succeed, proving the pin does not just
// disable collection here), and checks the finished object's header still
// names the right vtable and a field block address that is not its own.
func TestNewRaptorJavaObjectSurvivesACollectionBetweenItsTwoAllocations(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	holder, err := public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	const wantVTable = 0x10000040
	class := &raptorJavaClass{
		Holder:     holder,
		Name:       "app/Cramped",
		parentName: "java/lang/Object",
		fieldSize:  2,
		vtable:     wantVTable,
	}
	java.classes[holder] = class
	java.ClassByName[class.Name] = class

	// Drain every free block but 16 bytes, the same way the sibling test
	// above does, except this block is deliberately left unreferenced by any
	// root: it is genuine garbage, not something pinned to stand in for a
	// live object, so the field allocation below both needs and gets a real
	// collection rather than an exhausted one.
	root := public.Heap.Root()
	if len(root.Free) != 1 {
		t.Fatalf("heap free list = %#v, want exactly one block for this test", root.Free)
	}
	free := root.Free[0].Size
	if free <= 16 {
		t.Fatalf("heap only has %d bytes free before the test drains it", free)
	}
	garbage, err := public.Heap.Allocate(free-16, true)
	if err != nil || garbage == 0 {
		t.Fatalf("allocate garbage = 0x%08x, %v", garbage, err)
	}

	object, err := runtime.NewRaptorJavaObject(holder)
	check(t, err)
	if object == 0 {
		t.Fatal("NewRaptorJavaObject returned a null object against a heap a collection could still satisfy")
	}
	gotVTable, err := public.ReadU32(object)
	check(t, err)
	if gotVTable != wantVTable {
		t.Fatalf("object vtable = 0x%08x, want 0x%08x - the header word a stray "+
			"reused block would have clobbered", gotVTable, wantVTable)
	}
	gotHolder, err := public.ReadU32(object + 4)
	check(t, err)
	if gotHolder != holder {
		t.Fatalf("object holder = 0x%08x, want 0x%08x", gotHolder, holder)
	}
	fields, err := public.ReadU32(object + 8)
	check(t, err)
	if fields == 0 {
		t.Fatal("object fields pointer is null")
	}
	if fields == object {
		t.Fatalf("fields block 0x%08x aliases the object header itself", fields)
	}
	if root.Allocations[object] == 0 || root.Allocations[fields] == 0 {
		t.Fatalf("heap does not record both the header (0x%08x) and its fields "+
			"(0x%08x) as separately live", object, fields)
	}
}

func TestRaptorJavaClassDataIncludesStaticBase(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	java := &JavaRuntime{}
	class := &raptorJavaClass{
		Holder:     0x01400000,
		Name:       "example/LargeClass",
		fieldSize:  149,
		staticBase: 158,
	}

	object, err := runtime.ensureRaptorJavaClassObject(java, class)
	check(t, err)
	data, err := public.ReadU32(object + 8)
	check(t, err)
	const wantSize = uint32((158 + 8) * 4)
	if got := public.Heap.Root().Allocations[data]; got != wantSize {
		t.Fatalf("class data allocation = %d, want %d", got, wantSize)
	}
	marker, err := public.Heap.Allocate(4, true)
	if err != nil || marker == 0 {
		t.Fatalf("allocate marker = 0x%08x, %v", marker, err)
	}
	const sentinel = uint32(0xfeedface)
	check(t, public.WriteU32(marker, sentinel))
	check(t, public.WriteU32(data+162*4, 1))
	if got, err := public.ReadU32(marker); err != nil || got != sentinel {
		t.Fatalf("static field write changed following allocation to 0x%08x, %v", got, err)
	}
}

func TestRaptorJavaLinkedFieldIndexHandlesWideCompanion(t *testing.T) {
	java := &JavaRuntime{}
	java.classOrder = []*raptorJavaClass{
		{fields: []raptorJavaDeclaredField{{Name: "clock", descriptor: "J", index: 7}}},
		{fields: []raptorJavaDeclaredField{{Name: "clock", descriptor: "J", index: 67}}},
	}
	names := []string{"clock", "", ""}
	descriptors := []string{"J", "", ""}
	index, wide := raptorJavaLinkedFieldIndex(java, names, descriptors, 0, 0, false)
	if index != 7 || !wide {
		t.Fatalf("wide field = %d, wide=%t; want 7, true", index, wide)
	}
	index, wide = raptorJavaLinkedFieldIndex(java, names, descriptors, 1, index, wide)
	if index != 8 || wide {
		t.Fatalf("wide companion = %d, wide=%t; want 8, false", index, wide)
	}
	index, wide = raptorJavaLinkedFieldIndex(java, names, descriptors, 2, index, wide)
	if index != 0 || wide {
		t.Fatalf("ordinary empty field = %d, wide=%t; want 0, false", index, wide)
	}
}

func TestRaptorJavaHostClassKeepsParentAndWordSizedFields(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	class, err := runtime.ensureRaptorHostClass(java, "java/lang/StringBuffer")
	check(t, err)
	if class.parentName != "java/lang/Object" {
		t.Fatalf("StringBuffer parent = %q, want java/lang/Object", class.parentName)
	}
	hostClass, err := java.Host.InspectJavaClass(class.hostClass)
	check(t, err)
	wantWords := (uint32(hostClass.FieldSize) + 3) / 4
	if class.fieldSize != wantWords {
		t.Fatalf("StringBuffer fields = %d words, want %d", class.fieldSize, wantWords)
	}
	services, owner := java.Host.Services, java.Host.ServiceOwner
	check(t, runtime.DestroyRaptorJava())
	if runtime.Java != nil {
		t.Fatal("destroyed Raptor Java adapter remains attached")
	}
	adapter, err := services.Coordinator.Adapter(owner)
	check(t, err)
	if adapter.Lifecycle != shared.LifecycleDestroyed {
		t.Fatalf("embedded host lifecycle = %v", adapter.Lifecycle)
	}
}

func TestRaptorImportSlotsIncludeModule(t *testing.T) {
	runtime := &Runtime{
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	firstKey := raptorImportKey{Module: 100, Ordinal: 34}
	secondKey := raptorImportKey{Module: 504, Ordinal: 34}
	first, err := runtime.importStub(firstKey)
	check(t, err)
	second, err := runtime.importStub(secondKey)
	check(t, err)
	if first == second {
		t.Fatalf("different modules shared import stub 0x%08x", first)
	}
	again, err := runtime.importStub(firstKey)
	if err != nil || again != first {
		t.Fatalf("repeated import stub = 0x%08x, %v; want 0x%08x", again, err, first)
	}
	if len(runtime.importSlots) != 2 || runtime.importSlots[0] != firstKey ||
		runtime.importSlots[1] != secondKey {
		t.Fatalf("import slots = %#v", runtime.importSlots)
	}
}

func TestBuildRaptorJavaVTableUsesFixedAndFlatSlots(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	// A card subclass whose only declared method overrides an inherited flat
	// virtual, plus one flat slot the subclass does not implement.
	card, err := runtime.ensureRaptorHostClass(java, "org/kwis/msp/lcdui/Card")
	check(t, err)
	holder, err := public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	descriptor, err := public.Heap.Allocate(0x4c, true)
	if err != nil || descriptor == 0 {
		t.Fatalf("allocate descriptor = 0x%08x, %v", descriptor, err)
	}
	subclass := &raptorJavaClass{
		Holder:     holder,
		descriptor: descriptor,
		Name:       "app/Board",
		parentName: card.Name,
		methods: []raptorJavaDeclaredMethod{
			// paint's body is an ARM function (even address); its interworking
			// bit must survive into the vtable so a guest bx into it does not
			// switch the CPU into Thumb mode.
			{Name: "paint", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;)V", Body: 0x00002468},
		},
	}
	java.classes[subclass.Holder] = subclass
	java.ClassByName[subclass.Name] = subclass
	java.flatVirtual = []raptorJavaMethod{
		{className: card.Name, Name: "paint", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;)V"},
		{className: card.Name, Name: "keyNotify", descriptor: "(II)Z"},
	}

	check(t, runtime.buildRaptorJavaVTable(java, subclass, uint32(len(java.flatVirtual))))
	if subclass.vtable == 0 {
		t.Fatal("vtable was not allocated")
	}
	// Holder back-reference at +0, so guest object dispatch can recover it.
	if got, _ := public.ReadU32(subclass.vtable); got != subclass.Holder {
		t.Fatalf("vtable[0] = 0x%08x, want holder 0x%08x", got, subclass.Holder)
	}
	// Flat slot 0 dispatches to the subclass override body (declared wins), and
	// the ARM body's clear interworking bit is preserved verbatim — the builder
	// must not strip it or re-force Thumb. Flat slots start past the fixed ones
	// so the fixed pass cannot overwrite them.
	if got, _ := public.ReadU32(
		subclass.vtable + raptorJavaFlatVirtualSlot(0),
	); got != 0x00002468 {
		t.Fatalf("flat slot 0 = 0x%08x, want the ARM override body 0x00002468", got)
	}
	// Fixed Object slots are populated even though the subclass declares none.
	equals, _ := public.ReadU32(subclass.vtable + 0x10)
	if equals == 0 {
		t.Fatal("fixed Object.equals slot 0x10 was left empty")
	}
}

func TestBuildRaptorJavaVTableCopiesMethodTable(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	holder, err := public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	descriptor, err := public.Heap.Allocate(0x60, true)
	if err != nil || descriptor == 0 {
		t.Fatalf("allocate descriptor = 0x%08x, %v", descriptor, err)
	}
	// Older-SDK classes point descriptor+0x20 at a fuller method table whose
	// holder back-reference is at +0x04 and whose method bodies sit at the exact
	// vtable byte offsets the compiler-inlined call sites use (e.g. +0x48).
	methodTable, err := public.Heap.Allocate(0x60, true)
	if err != nil || methodTable == 0 {
		t.Fatalf("allocate method table = 0x%08x, %v", methodTable, err)
	}
	check(t, public.WriteU32(methodTable+0x04, holder))
	// The table is shifted 4 bytes ahead of the vtable (holder at +0x04 vs
	// +0x00), so vtable[0x48] is sourced from methodTable[0x4c].
	check(t, public.WriteU32(methodTable+0x4c, 0x00001a2c))
	check(t, public.WriteU32(descriptor+0x20, methodTable))
	leaf := &raptorJavaClass{
		Holder:     holder,
		descriptor: descriptor,
		Name:       "app/Runnable",
		parentName: "java/lang/Object",
	}
	java.classes[holder] = leaf
	java.ClassByName[leaf.Name] = leaf
	java.flatVirtual = make([]raptorJavaMethod, 24)
	for i := range java.flatVirtual {
		java.flatVirtual[i] = raptorJavaMethod{
			className: "app/Unrelated", Name: "m", descriptor: "()V",
		}
	}
	check(t, runtime.buildRaptorJavaVTable(java, leaf, uint32(len(java.flatVirtual))))
	if got, _ := public.ReadU32(leaf.vtable + 0x48); got != 0x00001a2c {
		t.Fatalf("vtable+0x48 = 0x%08x, want method-table body 0x00001a2c", got)
	}
}

func TestBuildRaptorJavaVTableCopiesInlineOwnMethods(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	holder, err := public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	descriptor, err := public.Heap.Allocate(0x60, true)
	if err != nil || descriptor == 0 {
		t.Fatalf("allocate descriptor = 0x%08x, %v", descriptor, err)
	}
	// A helper class that declares no methods through the +0x38 metadata table
	// (older-SDK layout) but carries its own virtual bodies inline at +0x2c/+0x30.
	check(t, public.WriteU32(descriptor+0x2c, 0x00001234))
	check(t, public.WriteU32(descriptor+0x30, 0x00005678))
	// A data-region pointer terminates the inline run (the metadata table field).
	check(t, public.WriteU32(descriptor+0x34, 0x01400abc))
	leaf := &raptorJavaClass{
		Holder:     holder,
		descriptor: descriptor,
		Name:       "app/Leaf",
		parentName: "java/lang/Object",
	}
	java.classes[holder] = leaf
	java.ClassByName[leaf.Name] = leaf
	// Flat entries the class neither declares nor host-implements, so the flat
	// pass leaves the own-method slots empty for the inline copy to fill.
	java.flatVirtual = make([]raptorJavaMethod, 8)
	for i := range java.flatVirtual {
		java.flatVirtual[i] = raptorJavaMethod{
			className: "app/Unrelated", Name: "m", descriptor: "()V",
		}
	}
	check(t, runtime.buildRaptorJavaVTable(java, leaf, uint32(len(java.flatVirtual))))
	if got, _ := public.ReadU32(leaf.vtable + 0x2c); got != 0x00001234 {
		t.Fatalf("vtable+0x2c = 0x%08x, want inline body 0x00001234", got)
	}
	if got, _ := public.ReadU32(leaf.vtable + 0x30); got != 0x00005678 {
		t.Fatalf("vtable+0x30 = 0x%08x, want inline body 0x00005678", got)
	}
	// The data-region pointer must not be copied as if it were a method body;
	// the slot is left empty by the inline run and then backfilled with the
	// no-op trampoline so a call through it cannot branch to address 0.
	if got, _ := public.ReadU32(leaf.vtable + 0x34); got == 0x01400abc {
		t.Fatalf("vtable+0x34 = 0x%08x, data pointer copied as a method body", got)
	}
	if got, _ := public.ReadU32(leaf.vtable + 0x34); got != java.noopStub {
		t.Fatalf("vtable+0x34 = 0x%08x, want no-op backstop 0x%08x", got, java.noopStub)
	}
}

func TestScanGuestStringAndWord(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	block, err := public.Heap.Allocate(0x400, true)
	if err != nil || block == 0 {
		t.Fatalf("allocate = 0x%08x, %v", block, err)
	}
	lo := block & ^uint32(0xfff)
	hi := lo + 0x3000
	// Preceded by a NUL so the whole-string guard accepts the match; the zeroed
	// allocation already supplies that leading NUL byte.
	strAt := block + 0x40
	check(t, public.CPU.WriteMemory(strAt, append([]byte("app/Main"), 0)))
	if got := runtime.scanGuestCString("app/Main", lo, hi); got != strAt {
		t.Fatalf("scanGuestCString = 0x%08x, want 0x%08x", got, strAt)
	}
	if got := runtime.scanGuestCString("absent/Class", lo, hi); got != 0 {
		t.Fatalf("scanGuestCString(absent) = 0x%08x, want 0", got)
	}
	wordAt := block + 0x100
	check(t, public.WriteU32(wordAt, 0xdeadbeef))
	if got := runtime.scanGuestWord(0xdeadbeef, lo, hi); got != wordAt {
		t.Fatalf("scanGuestWord = 0x%08x, want 0x%08x", got, wordAt)
	}
	if got := runtime.scanGuestWord(0x12345678, lo, hi); got != 0 {
		t.Fatalf("scanGuestWord(absent) = 0x%08x, want 0", got)
	}
}

func newPublicRuntime(t *testing.T) *wipirt.Runtime {
	t.Helper()
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, wipirt.MapRuntimeMemory(backend))
	runtime, err := wipirt.NewRuntime(backend, image.NewRGBA(image.Rect(0, 0, 16, 12)))
	check(t, err)
	check(t, backend.Map(guest.DefaultStackBase, guest.DefaultStackSize, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, backend.WriteRegister(cpu.RegisterSP, guest.DefaultStackBase+guest.DefaultStackSize-0x100))
	return runtime
}
