package application

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The opening map uses 16x16 BitBlt tiles from a guest-owned indexed BMP,
// rather than a 16-bit IBitmap object. The reported white field is the result
// of silently skipping those tile blits.
func TestBREWIssue320MapTilesRender(t *testing.T) {
	const digest = "8889d81a531985b823c10490da0d39203db3a5cf563c15c0a4c0e30a6bbdf0a3"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 300)
	press := func(control string, hold int) {
		t.Helper()
		if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}); err != nil {
			t.Fatal(err)
		}
		stepBREWReference(t, machine, hold)
		if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}); err != nil {
			t.Fatal(err)
		}
		stepBREWReference(t, machine, 10)
	}
	press("select", 60) // Title to main menu.
	stepBREWReference(t, machine, 100)
	press("select", 5) // Start game; the menu is initially open.
	stepBREWReference(t, machine, 100)
	for range 5 {
		press("right", 5)
	}
	press("select", 5) // Blue arrow returns to the map.
	stepBREWReference(t, machine, 300)
	frame := machine.Framebuffer()
	if got, want := frame.Bounds(), image.Rect(0, 0, 120, 160); got != want {
		t.Fatalf("map canvas = %v, want %v", got, want)
	}
	stats, present := machine.BREWFrameStats()
	if !present || stats.PresentCount < 56 {
		t.Fatalf("map frame stats = %+v, present=%t", stats, present)
	}
	painted := 0
	for y := 0; y < 120; y++ {
		for x := 0; x < 60; x++ {
			red, green, blue, _ := frame.At(x, y).RGBA()
			if red < 0xf000 || green < 0xf000 || blue < 0xf000 {
				painted++
			}
		}
	}
	if painted < 1000 {
		t.Fatalf("only %d map pixels painted in the formerly white field", painted)
	}
}
