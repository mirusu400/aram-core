package raptor

import (
	"context"
	"testing"
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
