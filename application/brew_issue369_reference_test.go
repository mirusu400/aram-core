package application

import (
	"fmt"
	"image"
	"testing"
)

// Top Gun 2 creates a transient file during startup before loading its resources.
func TestBREWIssue369TopGun2StartsAndOpensMenu(t *testing.T) {
	const digest = "43392c5a657ac36aca605a3cde727bd817b5672594be55b8470bacdb924468f1"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 800)
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 400)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 220); got != want {
		t.Fatalf("Top Gun 2 canvas = %v, want %v", got, want)
	}
	// AEE_RO_OLDMASK now removes the RGB565 magenta key (53a392b).
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "599581af8306b8ac14a2fa8318c29b7c5ce6f8d4816f1ea390914566b16a641e"; got != want {
		t.Fatalf("Top Gun 2 menu frame hash=%s, want %s", got, want)
	}
	for y := 80; y < 145; y++ {
		for x := 35; x < 120; x++ {
			r, g, b, _ := machine.Framebuffer().At(x, y).RGBA()
			if r == 0xffff && g == 0 && b == 0xffff {
				t.Fatalf("transparent magenta was copied into menu at %d,%d", x, y)
			}
		}
	}
	stats, presented := machine.BREWFrameStats()
	if !presented || !stats.FrameValid || stats.PresentCount < 160 {
		t.Fatalf("Top Gun 2 presents=%d valid=%v presented=%v", stats.PresentCount, stats.FrameValid, presented)
	}
}
