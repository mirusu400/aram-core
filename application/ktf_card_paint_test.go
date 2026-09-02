package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// spidermanThreeSHA256 identifies the 스파이더맨3 package. The title starts
// sixteen threads before its first frame, which is what makes it the case
// this covers.
const spidermanThreeSHA256 = "8c6821fab9693b480d3dbfcc261c588dfddc715c9901e97a298ee88c66c68b9c"

// TestKTFCardPaintWaitsForATaskSlot covers a title that died on launch. Every
// task slot was held by a thread the title had started - thirteen of them
// asleep with deadlines a few milliseconds out - so the card's paint could not
// get one and the runtime treated that as fatal. Leaving the card dirty paints
// it on a later quantum instead, once a sleeper retires.
//
// No probe sweep could see this: aram-probe stops at the first presented frame,
// and the fault arrives in the same quantum, just after it.
func TestKTFCardPaintWaitsForATaskSlot(t *testing.T) {
	path, data := findAuthorizedPackage(t, spidermanThreeSHA256)

	factory := NewFactory()
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	const frames = 600
	for frame := 0; frame < frames; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d with no input at all: %v", frame, err)
		}
	}
	// Surviving is not enough: the deferral must not have starved the card out
	// of ever painting.
	if presents := machine.ktf.PresentCount; presents < frames/4 {
		t.Fatalf("presented %d frames of %d", presents, frames)
	}
	bounds := machine.Framebuffer().Bounds()
	drawn := 0
	for y := 0; y < bounds.Dy(); y += 2 {
		for x := 0; x < bounds.Dx(); x += 2 {
			r, g, b, _ := machine.Framebuffer().At(x, y).RGBA()
			if r != 0 || g != 0 || b != 0 {
				drawn++
			}
		}
	}
	if drawn < 1000 {
		t.Fatalf("the screen holds %d non-black samples", drawn)
	}
}
