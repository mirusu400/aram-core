package application

import (
	"fmt"
	"testing"
)

// The reported EZ2DJ splash assembles its logos and wordmark from subregions
// of larger IImage resources. Drawing each full resource produces overlapping
// copies and matches the broken report screenshot. The splash reads the screen
// size and spreads its logos to the corners of the 176x220 canvas the probe
// gives it.
func TestBREWIssue365SplashUsesImageSourceRegions(t *testing.T) {
	const digest = "94653274c88704e2f5e9c2d4beb867fe776f79d2312e7802db65001589fb74e8"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 240)
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "75e8586f54eb29c3269a4e26411a6d6322f4cf3fe46db3edb64e9ca1b4f1c93c"; got != want {
		t.Fatalf("EZ2DJ splash frame hash=%s, want %s", got, want)
	}
	stats, present := machine.BREWFrameStats()
	if !present || stats.PresentCount != 34 {
		t.Fatalf("EZ2DJ splash presents=%d, valid=%t, want 34 valid presents", stats.PresentCount, present)
	}
}
