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
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "549661ce691aaf87979c9042997e4f92b9a4c07a55663ce35f1c54e2846c6fe7"; got != want {
		t.Fatalf("Top Gun 2 menu frame hash=%s, want %s", got, want)
	}
	stats, presented := machine.BREWFrameStats()
	if !presented || !stats.FrameValid || stats.PresentCount < 160 {
		t.Fatalf("Top Gun 2 presents=%d valid=%v presented=%v", stats.PresentCount, stats.FrameValid, presented)
	}
}
