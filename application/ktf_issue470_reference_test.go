package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Confession reads NUL-padded script lines, then loads the logo script and image.
func TestKTFIssue470ConfessionShowsTitle(t *testing.T) {
	path, data := findAuthorizedPackage(t, "e8aee49516f1facdca1dc1eb3a488511154c886c267d752a0ce987025e7f5eae")
	factory := NewFactory()
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
	for frame := 0; frame < 200; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	picture := machine.Framebuffer()
	coloured := 0
	bounds := picture.Bounds()
	for y := bounds.Min.Y + 20; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, _ := picture.At(x, y).RGBA()
			if red != 0 || green != 0 || blue != 0 {
				coloured++
			}
		}
	}
	if coloured < 1000 {
		t.Fatalf("title rendered only %d coloured pixels", coloured)
	}
}
