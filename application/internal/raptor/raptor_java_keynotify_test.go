package raptor

import (
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/profile"
)

// TestRaptorJavaKeyNotifyFallsBackToTheGuestVTable pins how a card's keyNotify
// body is found when the module publishes no method metadata for the class that
// declares it. A title whose keyNotify sits on an intermediate card class the
// module leaves out of its metadata resolved nothing by name, so every keypad
// press was dropped and the input screen (e.g. 테일즈위버 막시민편's birthday
// entry) could not be driven (issue #103). The module still fills the class's
// own vtable, so the body is read from there, the same way a thread's run()
// body is (issue #79).
func TestRaptorJavaKeyNotifyFallsBackToTheGuestVTable(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java := &JavaRuntime{
		classes:     map[uint32]*raptorJavaClass{},
		ClassByName: map[string]*raptorJavaClass{},
		flatVirtual: []raptorJavaMethod{
			{className: "BaseCard", Name: "keyNotify", descriptor: "(II)Z"},
		},
	}
	runtime.Java = java

	slot, ok := raptorJavaFlatVirtualSlotByName(java, "keyNotify", "(II)Z")
	if !ok {
		t.Fatal("keyNotify has no flat vtable slot")
	}
	vtable, err := public.Heap.Allocate(slot+8, true)
	if err != nil {
		t.Fatal(err)
	}
	const keyNotifyBody = uint32(0x0009c1a5)
	if err := public.WriteU32(vtable+slot, keyNotifyBody); err != nil {
		t.Fatal(err)
	}
	card, err := public.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	holder := uint32(0x01404200)
	if err := public.WriteU32(card+4, holder); err != nil {
		t.Fatal(err)
	}
	class := &raptorJavaClass{
		Holder:      holder,
		Name:        "GameCard",
		parentName:  "BaseCard",
		guestVTable: vtable,
	}
	java.classes[holder] = class
	java.ClassByName[class.Name] = class
	java.currentCard = card

	callback, ok := runtime.JavaInputCallback(machinecore.InputEvent{
		Control: "num5",
		Pressed: true,
	})
	if !ok {
		t.Fatal("digit press was not routed to keyNotify")
	}
	if callback.Procedure != keyNotifyBody {
		t.Fatalf("keyNotify body = 0x%08x, want 0x%08x", callback.Procedure, keyNotifyBody)
	}
	wantArgs := [4]uint32{card, ktfrt.KeyPressed, uint32(profile.Key5)}
	if callback.Args != wantArgs {
		t.Fatalf("keyNotify args = %v, want %v", callback.Args, wantArgs)
	}

	// A release delivers the release event type to the same body.
	release, ok := runtime.JavaInputCallback(machinecore.InputEvent{
		Control: "num5",
		Pressed: false,
	})
	if !ok || release.Args[1] != ktfrt.KeyReleased {
		t.Fatalf("release event type = %d (ok=%v), want %d", release.Args[1], ok, ktfrt.KeyReleased)
	}

	// A class that does publish its metadata is still resolved by name, and
	// the declared body wins over the vtable slot.
	const declared = uint32(0x0000b7a5)
	class.methods = []raptorJavaDeclaredMethod{
		{Name: "keyNotify", descriptor: "(II)Z", Body: declared},
	}
	callback, ok = runtime.JavaInputCallback(machinecore.InputEvent{
		Control: "num5",
		Pressed: true,
	})
	if !ok || callback.Procedure != declared {
		t.Fatalf("declared keyNotify body = 0x%08x (ok=%v), want 0x%08x", callback.Procedure, ok, declared)
	}
}
