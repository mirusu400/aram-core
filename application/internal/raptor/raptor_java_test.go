package raptor

import (
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"image"
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
	if err != nil {
		t.Fatal(err)
	}
	data, err := public.ReadU32(object + 8)
	if err != nil {
		t.Fatal(err)
	}
	const wantSize = uint32((158 + 8) * 4)
	if got := public.Heap.Root().Allocations[data]; got != wantSize {
		t.Fatalf("class data allocation = %d, want %d", got, wantSize)
	}
	marker, err := public.Heap.Allocate(4, true)
	if err != nil || marker == 0 {
		t.Fatalf("allocate marker = 0x%08x, %v", marker, err)
	}
	const sentinel = uint32(0xfeedface)
	if err := public.WriteU32(marker, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := public.WriteU32(data+162*4, 1); err != nil {
		t.Fatal(err)
	}
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
	index, wide := raptorJavaLinkedFieldIndex(java, "clock", "J", 0, false)
	if index != 67 || !wide {
		t.Fatalf("wide field = %d, wide=%t; want 67, true", index, wide)
	}
	index, wide = raptorJavaLinkedFieldIndex(java, "", "", index, wide)
	if index != 68 || wide {
		t.Fatalf("wide companion = %d, wide=%t; want 68, false", index, wide)
	}
	index, wide = raptorJavaLinkedFieldIndex(java, "", "", index, wide)
	if index != 0 || wide {
		t.Fatalf("ordinary empty field = %d, wide=%t; want 0, false", index, wide)
	}
}

func TestRaptorJavaHostClassKeepsParentAndWordSizedFields(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	java, err := runtime.ensureJavaRuntime()
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.ensureRaptorHostClass(java, "java/lang/StringBuffer")
	if err != nil {
		t.Fatal(err)
	}
	if class.parentName != "java/lang/Object" {
		t.Fatalf("StringBuffer parent = %q, want java/lang/Object", class.parentName)
	}
	hostClass, err := java.Host.InspectJavaClass(class.hostClass)
	if err != nil {
		t.Fatal(err)
	}
	wantWords := (uint32(hostClass.FieldSize) + 3) / 4
	if class.fieldSize != wantWords {
		t.Fatalf("StringBuffer fields = %d words, want %d", class.fieldSize, wantWords)
	}
	services, owner := java.Host.Services, java.Host.ServiceOwner
	if err := runtime.DestroyRaptorJava(); err != nil {
		t.Fatal(err)
	}
	if runtime.Java != nil {
		t.Fatal("destroyed Raptor Java adapter remains attached")
	}
	adapter, err := services.Coordinator.Adapter(owner)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.importStub(secondKey)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	// A card subclass whose only declared method overrides an inherited flat
	// virtual, plus one flat slot the subclass does not implement.
	card, err := runtime.ensureRaptorHostClass(java, "org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
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

	if err := runtime.buildRaptorJavaVTable(java, subclass, uint32(len(java.flatVirtual))); err != nil {
		t.Fatal(err)
	}
	if subclass.vtable == 0 {
		t.Fatal("vtable was not allocated")
	}
	// Holder back-reference at +0, so guest object dispatch can recover it.
	if got, _ := public.ReadU32(subclass.vtable); got != subclass.Holder {
		t.Fatalf("vtable[0] = 0x%08x, want holder 0x%08x", got, subclass.Holder)
	}
	// Flat slot 0 dispatches to the subclass override body (declared wins), and
	// the ARM body's clear interworking bit is preserved verbatim — the builder
	// must not strip it or re-force Thumb.
	if got, _ := public.ReadU32(subclass.vtable + 4); got != 0x00002468 {
		t.Fatalf("flat slot 0 = 0x%08x, want the ARM override body 0x00002468", got)
	}
	// Fixed Object slots are populated even though the subclass declares none.
	equals, _ := public.ReadU32(subclass.vtable + 0x10)
	if equals == 0 {
		t.Fatal("fixed Object.equals slot 0x10 was left empty")
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
	if err := public.CPU.WriteMemory(strAt, append([]byte("app/Main"), 0)); err != nil {
		t.Fatal(err)
	}
	if got := runtime.scanGuestCString("app/Main", lo, hi); got != strAt {
		t.Fatalf("scanGuestCString = 0x%08x, want 0x%08x", got, strAt)
	}
	if got := runtime.scanGuestCString("absent/Class", lo, hi); got != 0 {
		t.Fatalf("scanGuestCString(absent) = 0x%08x, want 0", got)
	}
	wordAt := block + 0x100
	if err := public.WriteU32(wordAt, 0xdeadbeef); err != nil {
		t.Fatal(err)
	}
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
	if err := wipirt.MapRuntimeMemory(backend); err != nil {
		t.Fatal(err)
	}
	runtime, err := wipirt.NewRuntime(backend, image.NewRGBA(image.Rect(0, 0, 16, 12)))
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Map(guest.DefaultStackBase, guest.DefaultStackSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, guest.DefaultStackBase+guest.DefaultStackSize-0x100); err != nil {
		t.Fatal(err)
	}
	return runtime
}
