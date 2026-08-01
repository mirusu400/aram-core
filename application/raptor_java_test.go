package application

import (
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestGuestHeapSharedAllocatorDoesNotOverlap(t *testing.T) {
	public := newPublicRuntime(t)
	peer := guestHeap{cpu: public.cpu, shared: &public.heap}

	first, err := public.heap.allocate(24, true)
	if err != nil || first == 0 {
		t.Fatalf("allocate root block = 0x%08x, %v", first, err)
	}
	second, err := peer.allocate(24, true)
	if err != nil || second == 0 {
		t.Fatalf("allocate shared block = 0x%08x, %v", second, err)
	}
	if first == second {
		t.Fatalf("root and shared heaps returned the same block 0x%08x", first)
	}
	if public.heap.root().allocations[first] == 0 ||
		public.heap.root().allocations[second] == 0 {
		t.Fatal("shared allocations were not recorded by the root heap")
	}
	if !peer.release(first) {
		t.Fatal("shared heap could not release a root allocation")
	}
	reused, err := public.heap.allocate(24, true)
	if err != nil || reused != first {
		t.Fatalf("released root block reused at 0x%08x, %v; want 0x%08x", reused, err, first)
	}
}

func TestRaptorJavaClassDataIncludesStaticBase(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &raptorRuntime{cpu: public.cpu, public: public}
	java := &raptorJavaRuntime{}
	class := &raptorJavaClass{
		holder:     0x01400000,
		name:       "example/LargeClass",
		fieldSize:  149,
		staticBase: 158,
	}

	object, err := runtime.ensureRaptorJavaClassObject(java, class)
	if err != nil {
		t.Fatal(err)
	}
	data, err := public.readU32(object + 8)
	if err != nil {
		t.Fatal(err)
	}
	const wantSize = uint32((158 + 8) * 4)
	if got := public.heap.root().allocations[data]; got != wantSize {
		t.Fatalf("class data allocation = %d, want %d", got, wantSize)
	}
	marker, err := public.heap.allocate(4, true)
	if err != nil || marker == 0 {
		t.Fatalf("allocate marker = 0x%08x, %v", marker, err)
	}
	const sentinel = uint32(0xfeedface)
	if err := public.writeU32(marker, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := public.writeU32(data+162*4, 1); err != nil {
		t.Fatal(err)
	}
	if got, err := public.readU32(marker); err != nil || got != sentinel {
		t.Fatalf("static field write changed following allocation to 0x%08x, %v", got, err)
	}
}

func TestRaptorJavaLinkedFieldIndexHandlesWideCompanion(t *testing.T) {
	java := &raptorJavaRuntime{}
	java.classOrder = []*raptorJavaClass{
		{fields: []raptorJavaDeclaredField{{name: "clock", descriptor: "J", index: 7}}},
		{fields: []raptorJavaDeclaredField{{name: "clock", descriptor: "J", index: 67}}},
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
	runtime := &raptorRuntime{cpu: public.cpu, public: public}
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
	hostClass, err := java.host.inspectJavaClass(class.hostClass)
	if err != nil {
		t.Fatal(err)
	}
	wantWords := (uint32(hostClass.FieldSize) + 3) / 4
	if class.fieldSize != wantWords {
		t.Fatalf("StringBuffer fields = %d words, want %d", class.fieldSize, wantWords)
	}
	services, owner := java.host.services, java.host.serviceOwner
	if err := runtime.destroyRaptorJava(); err != nil {
		t.Fatal(err)
	}
	if runtime.java != nil {
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
	runtime := &raptorRuntime{
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
