package application

import (
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

func newKTFInputTestRuntime(t *testing.T, display, card uint32) *ktfrt.Runtime {
	t.Helper()
	services, err := shared.NewServices(shared.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return &ktfrt.Runtime{
		Services:       services,
		DefaultDisplay: display,
		DisplayCards:   map[uint32]uint32{display: card},
	}
}

// A KTF title can remove the final card from an already-established Display
// while its Java tasks keep running. A later physical key has no card to
// receive it, so it must be consumed at the machine boundary rather than
// retained until the host input safety limit fires (issue #238).
func TestQueueKTFInputDropsDueKeyWithoutDockedCard(t *testing.T) {
	runtime := newKTFInputTestRuntime(t, 1, 0)
	machine := &Machine{input: []machinecore.InputEvent{{Control: "num2", Pressed: true}}}

	if err := machine.queueKTFInput(runtime); err != nil {
		t.Fatal(err)
	}
	if got := len(machine.input); got != 0 {
		t.Fatalf("pending input = %d, want 0 after dropping key without card", got)
	}
	if events := runtime.Services.Events.Snapshot().Events; len(events) != 0 {
		t.Fatalf("shared events = %v, key without card reached the event bus", events)
	}
}

// A card that is merely busy remains a valid input destination. Its key must
// remain pending for the existing retry path instead of being mistaken for a
// key with no destination.
func TestQueueKTFInputRetainsDueKeyForBusyDockedCard(t *testing.T) {
	const card = uint32(2)
	runtime := newKTFInputTestRuntime(t, 1, card)
	runtime.Tasks = []*ktfrt.Task{{KeyCard: card}}
	machine := &Machine{input: []machinecore.InputEvent{{Control: "num2", Pressed: true}}}

	if err := machine.queueKTFInput(runtime); err != nil {
		t.Fatal(err)
	}
	if got := len(machine.input); got != 1 {
		t.Fatalf("pending input = %d, want 1 while card key task is busy", got)
	}
}
