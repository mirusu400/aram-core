package raptor

import "testing"

// A subclass inherits every slot its parent resolved. The parent chain was
// invisible until a descriptor's parent slot could name a holder, so this pass
// had nothing to inherit from and every inherited method fell through to the
// no-op backstop: 배틀몬스터 dispatched 564 no-ops in 260 frames, which was its
// whole Jlet-side behaviour (issue #151).
func TestRaptorSubclassInheritsResolvedParentVTableSlots(t *testing.T) {
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

	const parentBody = uint32(0x00002469)
	parent := newRaptorTestClass(t, runtime, java, "app/Base", "", []raptorJavaDeclaredMethod{
		{Name: "run", descriptor: "()V", Body: parentBody},
	})
	child := newRaptorTestClass(t, runtime, java, "app/Game", parent.Name, nil)
	java.flatVirtual = []raptorJavaMethod{
		{className: parent.Name, Name: "run", descriptor: "()V"},
	}

	if err := runtime.buildRaptorJavaVTable(
		java,
		parent,
		uint32(len(java.flatVirtual)),
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.buildRaptorJavaVTable(
		java,
		child,
		uint32(len(java.flatVirtual)),
	); err != nil {
		t.Fatal(err)
	}

	slot := raptorJavaFlatVirtualSlot(0)
	inherited, err := public.ReadU32(child.vtable + slot)
	if err != nil {
		t.Fatal(err)
	}
	if inherited != parentBody {
		declared, _ := public.ReadU32(parent.vtable + slot)
		t.Fatalf("child slot = 0x%08x, want the parent's body 0x%08x (parent slot 0x%08x)",
			inherited, parentBody, declared)
	}
}

// newRaptorTestClass registers one class with a holder and descriptor in guest
// memory so the vtable builder can read it back.
func newRaptorTestClass(
	t *testing.T,
	runtime *Runtime,
	java *JavaRuntime,
	name, parentName string,
	methods []raptorJavaDeclaredMethod,
) *raptorJavaClass {
	t.Helper()
	holder, err := runtime.Public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	descriptor, err := runtime.Public.Heap.Allocate(0x4c, true)
	if err != nil || descriptor == 0 {
		t.Fatalf("allocate descriptor = 0x%08x, %v", descriptor, err)
	}
	class := &raptorJavaClass{
		Holder:     holder,
		descriptor: descriptor,
		Name:       name,
		parentName: parentName,
		methods:    methods,
	}
	java.classes[holder] = class
	java.ClassByName[name] = class
	java.classOrder = append(java.classOrder, class)
	return class
}
