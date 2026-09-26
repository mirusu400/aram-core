package application

import (
	"fmt"
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The game's loading animation draws narrow frame-only rectangles before it
// reaches the reported white screen. Preserve that distinct IDisplay behavior
// while the later title rendering is investigated separately.
func TestBREWIssue315LoadingFrameOutline(t *testing.T) {
	const digest = "85179e4a9c0bac57c3e89587cd0d47b16b74968a5622a48a292bfd93f7bff60b"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 6)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 120, 160); got != want {
		t.Fatalf("generic BREW canvas = %v, want %v", got, want)
	}
	stats, present := machine.BREWFrameStats()
	if !present || stats.PresentCount < 2 {
		t.Fatalf("BREW frame stats = %+v, present=%t; expected loading frame", stats, present)
	}
	if frameIsUniform(machine.Framebuffer()) {
		t.Fatal("frame-only loading rectangle was not rendered")
	}
}

// The game uses SetupNativeImage's reallocation flag to decide whether to
// draw its decoded BMPs. A cleared flag leaves the title white and the menu
// as plain color blocks even though the bitmaps themselves were decoded.
func TestBREWIssue315NativeBitmapTitleAndMenu(t *testing.T) {
	const digest = "85179e4a9c0bac57c3e89587cd0d47b16b74968a5622a48a292bfd93f7bff60b"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 3000)
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "ac8d3c4e0010dec79e2630dbfd1cde3a17da46db7f5ec707450c16b2ec2fbfa7"; got != want {
		t.Fatalf("MR 2004 title frame hash=%s, want %s", got, want)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	stepBREWReference(t, machine, 2)
	if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}); err != nil {
		t.Fatal(err)
	}
	stepBREWReference(t, machine, 100)
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "826f5ab41646202899231b2975e9ea1a069fe17b2af929c9b513bff03ca4c03f"; got != want {
		t.Fatalf("MR 2004 car menu frame hash=%s, want %s", got, want)
	}
}
