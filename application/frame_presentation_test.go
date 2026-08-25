package application

import (
	"image"
	"image/color"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

// A driver polls the framebuffer every host tick, so an untouched screen must
// cost it nothing: the same immutable image comes back under the same
// sequence, and no copy is made.
func TestFramePresentationHoldsItsSequenceWhilePixelsAreUnchanged(t *testing.T) {
	machine := &Machine{frame: image.NewRGBA(image.Rect(0, 0, 4, 3))}

	first, firstSequence := machine.FramePresentation()
	if firstSequence == 0 {
		t.Fatal("the first presentation reported the never-published sequence")
	}
	second, secondSequence := machine.FramePresentation()
	if secondSequence != firstSequence {
		t.Fatalf(
			"unchanged frame sequence = %d, want %d",
			secondSequence,
			firstSequence,
		)
	}
	if second != first {
		t.Fatal("an unchanged frame was materialized again")
	}

	machine.frame.SetRGBA(1, 1, color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff})
	third, thirdSequence := machine.FramePresentation()
	if thirdSequence == firstSequence {
		t.Fatal("a redrawn frame kept its sequence")
	}
	if got := third.(*image.RGBA).RGBAAt(1, 1); got.R != 0x12 {
		t.Fatalf("redrawn pixel = %+v", got)
	}
}

// The published frame must be a snapshot. If it aliased the guest's live
// framebuffer, the next guest draw would silently rewrite a frame the driver
// still believes it holds.
func TestFramePresentationDoesNotAliasTheGuestFramebuffer(t *testing.T) {
	machine := &Machine{frame: image.NewRGBA(image.Rect(0, 0, 4, 3))}
	published, sequence := machine.FramePresentation()

	machine.frame.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	if got := published.(*image.RGBA).RGBAAt(0, 0); got.R != 0 {
		t.Fatalf("published frame followed a later guest draw: %+v", got)
	}
	if _, next := machine.FramePresentation(); next == sequence {
		t.Fatal("a guest draw after publication was missed")
	}
}

func presentedKTFMachine(t *testing.T) (*Machine, *shared.Graphics, shared.ServiceID) {
	t.Helper()
	graphics, err := shared.NewGraphics(shared.NewRegistry(16), shared.GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := graphics.CreateSurface(1, shared.SurfaceDescriptor{
		Width:  2,
		Height: 2,
		Format: shared.PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetScreen(1, surface); err != nil {
		t.Fatal(err)
	}
	machine := &Machine{
		frame: image.NewRGBA(image.Rect(0, 0, 2, 2)),
		ktf: &ktfrt.Runtime{
			Services: &shared.Services{Graphics: graphics},
		},
	}
	return machine, graphics, surface
}

// KTF presents once per committed frame whether or not the pixels moved, so
// the sequence has to follow the presented content rather than the count of
// presentations.
func TestKTFFramePresentationFollowsPresentedContent(t *testing.T) {
	machine, graphics, surface := presentedKTFMachine(t)
	if err := graphics.SetPixel(1, surface, 0, 0, shared.RGB(255, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := graphics.Present(1, surface, shared.Rectangle{}); err != nil {
		t.Fatal(err)
	}

	first, firstSequence := machine.FramePresentation()
	if got := first.(*image.RGBA).RGBAAt(0, 0); got.R != 255 {
		t.Fatalf("presented pixel = %+v", got)
	}
	repeat, repeatSequence := machine.FramePresentation()
	if repeatSequence != firstSequence || repeat != first {
		t.Fatal("a re-read of the same presented frame was materialized again")
	}

	if _, err := graphics.Present(1, surface, shared.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if _, sequence := machine.FramePresentation(); sequence != firstSequence {
		t.Fatalf(
			"presenting identical pixels moved the sequence %d -> %d",
			firstSequence,
			sequence,
		)
	}

	if err := graphics.SetPixel(1, surface, 1, 1, shared.RGB(0, 255, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := graphics.Present(1, surface, shared.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	changed, changedSequence := machine.FramePresentation()
	if changedSequence == firstSequence {
		t.Fatal("a redrawn KTF frame kept its sequence")
	}
	if got := changed.(*image.RGBA).RGBAAt(1, 1); got.G != 255 {
		t.Fatalf("redrawn KTF pixel = %+v", got)
	}
}

// Before the runtime commits anything the driver still needs a frame, but it
// must not be rebuilt on every tick either.
func TestKTFFramePresentationReusesThePreCommitPlaceholder(t *testing.T) {
	machine, _, _ := presentedKTFMachine(t)

	blank, sequence := machine.FramePresentation()
	repeat, repeatSequence := machine.FramePresentation()
	if repeat != blank || repeatSequence != sequence {
		t.Fatal("the pre-commit placeholder was rebuilt")
	}
	if got := blank.(*image.RGBA).RGBAAt(0, 0); got.A != 0xff || got.R != 0 {
		t.Fatalf("placeholder pixel = %+v", got)
	}
}
