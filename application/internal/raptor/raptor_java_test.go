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
