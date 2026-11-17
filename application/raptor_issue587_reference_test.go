package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// 액션퍼즐패밀리4 reads both the display height and its primary framebuffer's
// height, and moves every screen down by the difference. While the framebuffer
// answered 296 rows of a 320-row display, each screen started below a blank
// 24-row strip and its last 24 rows fell off the display: the row of
// characters and the soft-key labels were cut (issue #587). Every Clet gets
// the whole screen now, so the two heights agree.
//
// The opening notice paints a white page over the whole surface. Its first 24
// rows stayed black before the fix.
func TestRaptorIssue587NoticeFillsTheWholeScreen(t *testing.T) {
	path, data := findAuthorizedPackage(t, "0b8c8f62e5b6364eca377468087adfdcfe580ea479c373c3e48bbae34bac5888")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	factory := NewFactory()
	factory.NewCPU = fastestCPUFactory()
	factory.RunBudget = DefaultHandsetRunBudget
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(ctx))

	for frame := 0; frame < 200; frame++ {
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	screen := machine.Framebuffer()
	bounds := screen.Bounds()
	if bounds.Dx() != 240 || bounds.Dy() != 320 {
		t.Fatalf("screen = %dx%d, want 240x320", bounds.Dx(), bounds.Dy())
	}
	for _, y := range []int{0, 23, 296, 319} {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := screen.At(x, y).RGBA()
			if r>>8 < 0xf0 || g>>8 < 0xf0 || b>>8 < 0xf0 {
				t.Fatalf("pixel (%d,%d) = %02x%02x%02x, want the notice's white page", x, y, r>>8, g>>8, b>>8)
			}
		}
	}
}
