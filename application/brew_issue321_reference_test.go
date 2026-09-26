package application

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The exact title renders a 176x202 native surface. The former 120x160
// default cut off its right-hand title letters and the ship at the bottom.
func TestBREWIssue321TitleFitsNativeCanvas(t *testing.T) {
	const digest = "4fae0b6a37e501163f6eabbda2b400bdfd4682ae389217bc26a7dbee564cb360"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 300)
	splash := brewFrameHash(machine.Framebuffer())
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 202); got != want {
		t.Fatalf("native canvas = %v, want %v", got, want)
	}
	// The applet latches Select and consumes it on the next timer callback.
	// A press and release both delivered within one frame cancel the latch.
	if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	stepBREWReference(t, machine, 2)
	if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}); err != nil {
		t.Fatal(err)
	}
	stepBREWReference(t, machine, 60)
	frame := machine.Framebuffer()
	if got := brewFrameHash(frame); got == splash {
		t.Fatal("Select did not advance beyond the GameToilet splash")
	}
	rightVisible, bottomVisible := 0, 0
	for y := 0; y < 202; y++ {
		for x := 0; x < 176; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if r == 0xffff && g == 0xffff && b == 0xffff {
				continue
			}
			if x >= 120 && y >= 25 && y < 110 {
				rightVisible++
			}
			if y >= 160 {
				bottomVisible++
			}
		}
	}
	if rightVisible < 100 || bottomVisible < 100 {
		t.Fatalf("formerly clipped title region: right=%d bottom=%d visible pixels", rightVisible, bottomVisible)
	}
}
