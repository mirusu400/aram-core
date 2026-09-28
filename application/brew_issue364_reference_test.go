package application

import (
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The reported title opens a version choice after four select presses. Choosing
// the full version reaches a map whose next select calls AEEStdLib WSTRCAT.
func TestBREWIssue364FullVersionAdvancesPastWideStringAppend(t *testing.T) {
	const digest = "321556e7aab52bad476b38eb9e7ec55f3c3dd7b2be5ef8bff5d7d8b5b2e1c849"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 180)
	for range 4 {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, 120)
	}
	tapBREWControl(t, machine, "num2")
	stepBREWReference(t, machine, 120)
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 120)
	before := brewFrameHash(machine.Framebuffer())
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 120)
	if after := brewFrameHash(machine.Framebuffer()); after == before {
		t.Fatal("the full-version map did not advance after select")
	}
	if got := machine.State(); got != machinecore.StateRunning {
		t.Fatalf("machine state = %s, want running", got)
	}
}
