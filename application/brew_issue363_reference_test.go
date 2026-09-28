package application

import (
	"fmt"
	"image"
	"testing"
)

// The reported game's 176-pixel menu and centered board are clipped when the
// generic 120x160 BREW canvas is used.
func TestBREWIssue363UsesFullHandsetCanvas(t *testing.T) {
	const digest = "06369c8db97fb4e28165297ac2557c0de050dfb6986ea429d3411ffedfa891b9"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 300)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 220); got != want {
		t.Fatalf("handset canvas = %v, want %v", got, want)
	}
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 120)
	tapBREWControl(t, machine, "num2")
	stepBREWReference(t, machine, 120)
	for range 2 {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, 120)
	}
	const want = "19f066504a84689cfa1d5e34749076a1d98100330cfd022001fa3dace696e054"
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != want {
		t.Fatalf("centered game board frame hash = %s, want %s", got, want)
	}
}
