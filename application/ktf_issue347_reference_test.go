package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

const suhoziSHA256 = "9a2d11c489dc6e26946b6af95dc58d4cece32a68fe9e96f77f36fe13b51fbe2a"

// Suhozi's shipped client places image sprites relative to the card centre,
// while its background clear and text use ordinary card coordinates. Without
// the exact-title image origin, the complete logo/menu is clipped to the
// top-left 88x100 corner (issue #347).
func TestKTFIssue347SuhoziTitleAndMenuFillCard(t *testing.T) {
	path, data := findAuthorizedPackage(t, suhoziSHA256)
	factory := NewFactory()
	factory.NewCPU = newJITCPU
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	step := func(frames int) {
		t.Helper()
		for frame := 0; frame < frames; frame++ {
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Fatalf("frame %d/%d: %v", frame, frames, err)
			}
		}
	}
	assertRightHalfPainted := func(stage string) {
		t.Helper()
		image := machine.Framebuffer()
		painted := 0
		for y := 40; y < image.Bounds().Dy(); y++ {
			for x := image.Bounds().Dx() / 2; x < image.Bounds().Dx(); x++ {
				r, g, b, _ := image.At(x, y).RGBA()
				if r != 0 || g != 0 || b != 0 {
					painted++
				}
			}
		}
		if painted < 1000 {
			t.Fatalf("%s: only %d painted pixels in the right half", stage, painted)
		}
	}

	step(2000)
	if machine.ktf.PresentCount < 100 {
		t.Fatalf("title presented only %d times", machine.ktf.PresentCount)
	}
	assertRightHalfPainted("title")
	presents := machine.ktf.PresentCount
	if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	step(5)
	if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: false}); err != nil {
		t.Fatal(err)
	}
	step(400)
	if machine.ktf.PresentCount <= presents {
		t.Fatal("the title did not repaint after opening its menu")
	}
	assertRightHalfPainted("menu")
}
