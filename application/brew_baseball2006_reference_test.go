package application

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// 게임빌 2006프로야구 is a 176x220 title. On the generic 120x160 canvas its
// own sprite blitter computed a negative row length for a sprite past the
// right edge and memmoved 0xfffffff4 bytes (aram-fuzz 160efa4658ded845).
// The events are that finding's seed-384 key sequence.
func TestBREWBaseball2006UsesItsCanvas(t *testing.T) {
	const digest = "79a2b320c46fba8650dc21e2a525f8226d5e2592f47f02b5de77061ba19eac8f"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 220); got != want {
		t.Fatalf("2006프로야구 canvas = %v, want %v", got, want)
	}
	events := []struct {
		frame   int
		control string
		pressed bool
	}{
		{5, "ok", true}, {6, "num7", true}, {13, "num4", true}, {14, "num1", true}, {19, "up", true},
		{24, "soft-right", true}, {25, "num9", true}, {26, "back", true}, {35, "num4", false},
		{36, "num8", true}, {39, "ok", false}, {41, "num9", false}, {42, "num8", false}, {46, "up", false},
		{48, "num0", true}, {52, "soft-left", true}, {59, "send", true}, {60, "num5", true},
		{63, "back", false}, {68, "soft-left", false}, {71, "ok", true}, {75, "num3", true},
		{84, "num6", true}, {85, "num4", true}, {92, "num2", true},
	}
	next := 0
	for frame := 0; frame < 400; frame++ {
		for next < len(events) && events[next].frame == frame {
			check(t, machine.QueueInput(machinecore.InputEvent{
				Control: events[next].control,
				Pressed: events[next].pressed,
			}))
			next++
		}
		stepBREWReference(t, machine, 1)
	}
}
