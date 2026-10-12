package application

import (
	"image"
	"testing"
)

// The reported package contains two 176x202 full-screen BMPs. The generic
// 120x160 BREW canvas showed only their upper-left corner.
func TestBREWIssue522ArtworkFitsNativeCanvas(t *testing.T) {
	const digest = "9f7366ca1902b91c359b271ee5721fdccff7e39f1ebda6849454e6f9572a3a4f"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 300)

	frame := machine.Framebuffer()
	if got, want := frame.Bounds(), image.Rect(0, 0, 176, 202); got != want {
		t.Fatalf("Milk Tycoon 2 canvas = %v, want %v", got, want)
	}
	stats, present := machine.BREWFrameStats()
	if !present || !stats.FrameValid || stats.PresentCount == 0 {
		t.Fatalf("Milk Tycoon 2 frame stats = %+v, present=%t", stats, present)
	}
	rightVisible, bottomVisible := 0, 0
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if r == 0 && g == 0 && b == 0 {
				continue
			}
			if x >= 120 {
				rightVisible++
			}
			if y >= 160 {
				bottomVisible++
			}
		}
	}
	if rightVisible < 100 || bottomVisible < 100 {
		t.Fatalf("formerly clipped artwork: right=%d bottom=%d visible pixels", rightVisible, bottomVisible)
	}
}
