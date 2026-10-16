package application

import (
	"fmt"
	"image"
	"testing"
)

// The MBR contains 176x207 backgrounds and the reported bookshelf needs a
// 176-pixel handset width to show its full rightmost columns.
func TestBREWIssue367MinibookBookshelfFitsCanvas(t *testing.T) {
	const digest = "9d17a101d5c9e31e789698f31fb41ba5448b624eb63b37193be13b12fdbb46c0"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 1000)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 220); got != want {
		t.Fatalf("Minibook canvas = %v, want %v", got, want)
	}
	for range 3 {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, 1000)
	}
	// MeasureTextEx now agrees with the drawn Hangul advances (53a392b).
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "4d176f419ee8810db986470ab9afba0d91d51b06ebda2e45cb649deb0769b2aa"; got != want {
		t.Fatalf("Minibook bookshelf frame hash=%s, want %s", got, want)
	}
}
