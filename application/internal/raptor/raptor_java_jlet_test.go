package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorMainJletUsesKTFMirror(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	jlet, err := runtime.ensureRaptorHostClass(java, "org/kwis/msp/lcdui/Jlet")
	check(t, err)
	instance, err := runtime.NewRaptorJavaObject(jlet.Holder)
	check(t, err)
	mirror := java.lgtToKTF[instance]
	if mirror == 0 {
		t.Fatal("Raptor Jlet has no KTF mirror")
	}

	// The old startup handoff wrote instance here. getCurrentJlet then returned
	// a Raptor header to wrapRaptorJavaObject, which expects a KTF object and
	// fails while inspecting it as one.
	java.Host.MainJlet = instance
	if _, callErr := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className:  "org/kwis/msp/lcdui/Jlet",
		Name:       "getCurrentJlet",
		descriptor: "()Lorg/kwis/msp/lcdui/Jlet;",
		isStatic:   true,
	}); callErr == nil {
		t.Fatal("getCurrentJlet accepted a Raptor object in KTF MainJlet")
	} else {
		t.Logf("legacy Raptor-in-KTF handoff fails as expected: %v", callErr)
	}

	check(t, runtime.SetRaptorJavaMainInstance(instance))
	if java.MainInstance != instance || java.Host.MainJlet != mirror {
		t.Fatalf("main Jlet = Raptor 0x%08x KTF 0x%08x, want 0x%08x and 0x%08x",
			java.MainInstance, java.Host.MainJlet, instance, mirror)
	}
	result, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className:  "org/kwis/msp/lcdui/Jlet",
		Name:       "getCurrentJlet",
		descriptor: "()Lorg/kwis/msp/lcdui/Jlet;",
		isStatic:   true,
	})
	check(t, err)
	if result.Low != instance {
		t.Fatalf("getCurrentJlet = 0x%08x, want Raptor main 0x%08x", result.Low, instance)
	}
}

// A main Jlet must be visible to the shared collector before its constructor
// runs. 배틀몬스터 calls System.gc() from that startup path; when launch created
// the object first and bound it only after startApp, the collector reclaimed the
// still-live Raptor header and its KTF mirror. A later String allocation reused
// the header, and the next virtual call jumped to the ARM instruction word it
// found there instead of a method pointer (issue #255).
func TestRaptorMainObjectIsRootedBeforeConstructorGC(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	jlet, err := runtime.ensureRaptorHostClass(java, "org/kwis/msp/lcdui/Jlet")
	check(t, err)

	instance, err := runtime.NewRaptorJavaMainObject(jlet.Holder)
	check(t, err)
	mirror := java.lgtToKTF[instance]
	if mirror == 0 {
		t.Fatal("bound Raptor main Jlet has no KTF mirror")
	}
	// Leave no accidental CPU-register root. The launch binding must be what
	// keeps both sides of the bridge alive during an explicit constructor GC.
	for register := cpu.RegisterR0; register <= cpu.RegisterR12; register++ {
		check(t, runtime.CPU.WriteRegister(register, 0))
	}
	java.Host.CollectJavaHeapForTest()

	root := public.Heap.Root()
	if root.Allocations[instance] == 0 {
		t.Fatalf("constructor GC reclaimed Raptor main Jlet 0x%08x", instance)
	}
	if root.Allocations[mirror] == 0 {
		t.Fatalf("constructor GC reclaimed KTF main Jlet mirror 0x%08x", mirror)
	}
	if java.MainInstance != instance || java.Host.MainJlet != mirror ||
		java.lgtToKTF[instance] != mirror || java.ktfToLGT[mirror] != instance {
		t.Fatalf(
			"main Jlet binding after GC = Raptor 0x%08x KTF 0x%08x maps 0x%08x/0x%08x",
			java.MainInstance,
			java.Host.MainJlet,
			java.lgtToKTF[instance],
			java.ktfToLGT[mirror],
		)
	}
}
