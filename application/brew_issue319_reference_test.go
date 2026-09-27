package application

import (
	"fmt"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Two held Select inputs reach the Continue prompt in the reported archive.
// Its final choice must read "1.예 2.아니오", not ambiguous UTF-16-looking
// syllables made from the game's packed EUC-KR AECHAR values.
func TestBREWIssue319ContinuePromptText(t *testing.T) {
	const digest = "2cfa3dff0779456f8482ccfbda740436101699186fdd9537b7a9108f8bfcedbf"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	holdSelect := func() {
		t.Helper()
		if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}); err != nil {
			t.Fatal(err)
		}
		stepBREWReference(t, machine, 60)
		if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}); err != nil {
			t.Fatal(err)
		}
	}
	stepBREWReference(t, machine, 300)
	holdSelect()
	stepBREWReference(t, machine, 300)
	holdSelect()
	stepBREWReference(t, machine, 100)
	if got := machine.runtime.EventCount(0x101); got != 2 {
		t.Fatalf("Select key presses = %d, want 2", got)
	}
	const want = "3b4f972a42e1d1f29fccadcfc5cad8c78f4d0923019b57ea8e353d20c2eaa15e"
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != want {
		t.Fatalf("Continue prompt frame hash = %s, want %s", got, want)
	}
}
