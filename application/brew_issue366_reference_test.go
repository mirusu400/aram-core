package application

import (
	"fmt"
	"image"
	"testing"
)

// Battle Drum X packages its menu on a 176x204 background; a 120-pixel
// framebuffer cuts off the menu border and leaves the choices shifted right.
func TestBREWIssue366BattleDrumXMenuFitsCanvas(t *testing.T) {
	const digest = "a50f7decb608272daca545a1beca126286cba8eeb8b6e230f14448393b143ca2"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 1000)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 220); got != want {
		t.Fatalf("Battle Drum X canvas = %v, want %v", got, want)
	}
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "7acd1b0021298bf3915639fea3236d3a32129051b2b21b7ec99f65333f0e78ef"; got != want {
		t.Fatalf("Battle Drum X menu frame hash=%s, want %s", got, want)
	}
}
