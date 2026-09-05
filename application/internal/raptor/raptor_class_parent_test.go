package raptor

import "testing"

// A class descriptor's parent slot holds one of two things: a name string for
// a superclass the module does not define, or the **holder** of a class in the
// same module. Reading the holder as a string yields nothing, and the chain
// broke there: 배틀몬스터's launch class Game extends its obfuscated Jlet
// subclass "a" that way, so Game inherited neither startApp nor run, and the
// runtime ran the lifecycle on a second instance of the base class instead -
// throwing away every override the launch class made (issue #151).
func TestRaptorClassParentReadsAHolderAsWellAsAName(t *testing.T) {
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

	parentHolder := buildRaptorTestClass(t, runtime, java, "app/Base", 0)
	parent := java.classes[parentHolder]
	if parent == nil || parent.Name != "app/Base" {
		t.Fatalf("parent class = %#v", parent)
	}
	if parent.parentName != "org/kwis/msp/lcdui/Jlet" {
		t.Fatalf("named parent = %q, want the Jlet", parent.parentName)
	}

	childHolder := buildRaptorTestClass(t, runtime, java, "app/Game", parentHolder)
	child := java.classes[childHolder]
	if child == nil {
		t.Fatal("child class was not inspected")
	}
	if child.parentName != "app/Base" {
		t.Fatalf("holder parent = %q, want app/Base", child.parentName)
	}

	// With the chain readable, the class the launch names inherits the
	// lifecycle rather than the runtime having to find a different class.
	parent.methods = []raptorJavaDeclaredMethod{
		{Name: "startApp", descriptor: "([Ljava/lang/String;)V", Body: 0x00002468},
	}
	start, ok := runtime.RaptorJletStartApp(child)
	if !ok || start.Body != 0x00002468 {
		t.Fatalf("inherited startApp = %#v, %t", start, ok)
	}
	if resolved := runtime.ResolveRaptorJletMainClass(child); resolved != child {
		t.Fatalf("main class resolved away from the launch class to %q",
			resolved.Name)
	}
}

// buildRaptorTestClass writes one class holder and descriptor into guest
// memory. parentHolder of zero names org/kwis/msp/lcdui/Jlet by string, the
// shape a module uses for a superclass it does not define itself.
func buildRaptorTestClass(
	t *testing.T,
	runtime *Runtime,
	java *JavaRuntime,
	name string,
	parentHolder uint32,
) uint32 {
	t.Helper()
	holder, err := runtime.Public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	descriptor, err := runtime.Public.Heap.Allocate(0x4c, true)
	if err != nil || descriptor == 0 {
		t.Fatalf("allocate descriptor = 0x%08x, %v", descriptor, err)
	}
	nameAddress, err := runtime.allocateJavaCString(name)
	if err != nil {
		t.Fatal(err)
	}
	parent := parentHolder
	if parent == 0 {
		parent, err = runtime.allocateJavaCString("org/kwis/msp/lcdui/Jlet")
		if err != nil {
			t.Fatal(err)
		}
	}
	for address, value := range map[uint32]uint32{
		holder + 8:        descriptor,
		descriptor + 8:    nameAddress,
		descriptor + 0x10: parent,
	} {
		if err := runtime.Public.WriteU32(address, value); err != nil {
			t.Fatal(err)
		}
	}
	class, err := runtime.inspectRaptorJavaClass(java, holder)
	if err != nil || class == nil {
		t.Fatalf("inspect %q: %v", name, err)
	}
	return holder
}
