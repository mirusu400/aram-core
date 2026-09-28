package application

import (
	"fmt"
	"testing"
)

// The reported EZ2DJ splash assembles its logos and wordmark from subregions
// of larger IImage resources. Drawing each full resource produces overlapping
// copies and matches the broken report screenshot.
func TestBREWIssue365SplashUsesImageSourceRegions(t *testing.T) {
	const digest = "94653274c88704e2f5e9c2d4beb867fe776f79d2312e7802db65001589fb74e8"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 240)
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "ab168878b0802b3cf8c744f9dfba48dd14b797729beafe8dac8d4a4252594917"; got != want {
		t.Fatalf("EZ2DJ splash frame hash=%s, want %s", got, want)
	}
	stats, present := machine.BREWFrameStats()
	if !present || stats.PresentCount != 34 {
		t.Fatalf("EZ2DJ splash presents=%d, valid=%t, want 34 valid presents", stats.PresentCount, present)
	}
}
