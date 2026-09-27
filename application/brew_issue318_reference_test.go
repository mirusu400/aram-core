package application

import (
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The exact game constructs an 8-bit BMP-shaped header around a caller-owned
// RGB565 screen. Without that SetupNativeImage form, it updates only black.
func TestBREWIssue318CallerBackedTitle(t *testing.T) {
	const digest = "ea023c35093c9b116c1cf08b54630304baf6df39a4bf5aa68fe4027949e4c44c"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 110)
	stats, present := machine.BREWFrameStats()
	if !present || !stats.FrameValid || stats.PresentCount < 30 || frameIsUniform(machine.Framebuffer()) {
		t.Fatalf("title never rendered: stats=%+v present=%t", stats, present)
	}
	before := brewFrameHash(machine.Framebuffer())
	if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	stepBREWReference(t, machine, 5)
	if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}); err != nil {
		t.Fatal(err)
	}
	stepBREWReference(t, machine, 60)
	if after := brewFrameHash(machine.Framebuffer()); after == before || frameIsUniform(machine.Framebuffer()) {
		t.Fatalf("title did not respond to Select: before=%x after=%x", before, after)
	}
}
