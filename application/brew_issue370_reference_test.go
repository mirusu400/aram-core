package application

import (
	"fmt"
	"image"
	"testing"
)

// Tengai's PAD contains 176x204 full-screen art, which needs the 176x220
// handset canvas to keep the title and main menu within view.
func TestBREWIssue370TengaiTitleAndMenuFitCanvas(t *testing.T) {
	const digest = "01f8fe8784138792822df9306127279f038813ea62c00385d256b77b61a025cf"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 1000)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 220); got != want {
		t.Fatalf("Tengai canvas = %v, want %v", got, want)
	}
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "84321b0b6bc30896c5b2b5b101efbb80ea6a884c4eace9d820fb26adbc148279"; got != want {
		t.Fatalf("Tengai title frame hash=%s, want %s", got, want)
	}
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 1000)
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "f8bccbf1b06426de184cfb84c7205943d1f63ad855aaef345bfd5b498bcb69bd"; got != want {
		t.Fatalf("Tengai menu frame hash=%s, want %s", got, want)
	}
}
