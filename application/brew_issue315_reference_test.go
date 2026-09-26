package application

import "testing"

// The game's loading animation draws narrow frame-only rectangles before it
// reaches the reported white screen. Preserve that distinct IDisplay behavior
// while the later title rendering is investigated separately.
func TestBREWIssue315LoadingFrameOutline(t *testing.T) {
	const digest = "85179e4a9c0bac57c3e89587cd0d47b16b74968a5622a48a292bfd93f7bff60b"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 6)
	stats, present := machine.BREWFrameStats()
	if !present || stats.PresentCount < 2 {
		t.Fatalf("BREW frame stats = %+v, present=%t; expected loading frame", stats, present)
	}
	if frameIsUniform(machine.Framebuffer()) {
		t.Fatal("frame-only loading rectangle was not rendered")
	}
}
