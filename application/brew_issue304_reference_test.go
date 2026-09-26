package application

import (
	"image"
	"testing"
)

// The authenticated title centers its 120x200 menu composition within the
// advertised device canvas. A 120x160 canvas cuts off the bottom menu items.
func TestBREWIssue304MenuFitsReportedCanvas(t *testing.T) {
	const digest = "99be6eb56702533eb0ef6f916e3f8a3b7978cb84fbee2de878856ad7a0db1649"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 300)
	frame := machine.Framebuffer()
	if got, want := frame.Bounds(), image.Rect(0, 0, 120, 200); got != want {
		t.Fatalf("menu canvas = %v, want %v", got, want)
	}
	stats, present := machine.BREWFrameStats()
	if !present || stats.PresentCount < 150 {
		t.Fatalf("menu frame stats = %+v, present=%t", stats, present)
	}
	visible := 0
	for y := 160; y < 200; y++ {
		for x := 0; x < 120; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if r != 0 || g != 0 || b != 0 {
				visible++
			}
		}
	}
	if visible < 100 {
		t.Fatalf("only %d visible pixels in the formerly clipped bottom 40 rows", visible)
	}
}
