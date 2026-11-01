package application

import (
	"fmt"
	"image"
	"testing"
)

// The MBR contains 176x207 backgrounds and the reported bookshelf needs a
// 176-pixel handset width to show its full rightmost columns. The height is
// the handset's 207-pixel application area: a taller canvas sends the module
// down its QVGA layout (issue #558).
func TestBREWIssue367MinibookBookshelfFitsCanvas(t *testing.T) {
	const digest = "9d17a101d5c9e31e789698f31fb41ba5448b624eb63b37193be13b12fdbb46c0"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 1000)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 207); got != want {
		t.Fatalf("Minibook canvas = %v, want %v", got, want)
	}
	for range 3 {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, 1000)
	}
	// MeasureTextEx agrees with the drawn Hangul advances (53a392b), and on
	// the 207-pixel canvas the shelf header is one label and the pointing
	// hand and caption box sit inside the frame.
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "6b797fd1e5175aa65ad27a5edd5219939b2495b2c63a217621274cc6932f36a1"; got != want {
		t.Fatalf("Minibook bookshelf frame hash=%s, want %s", got, want)
	}
}
