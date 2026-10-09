package application

import (
	"bytes"
	"context"
	"image"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Only the exact authorized package's identity and replay timings are retained.
// This title draws its six-pixel Yes/No captions from its own bitmap font.
func TestIssue513DecisionCaptionsBelowSelection(t *testing.T) {
	path, data := findAuthorizedPackage(t, "65bace1623a7ff4e2ebb0cad652ad61ac8125da22e136531ad03f6741e81d5a8")
	for _, backend := range []string{"native", "precise"} {
		t.Run(backend, func(t *testing.T) {
			factory := NewFactory()
			var err error
			factory.NewCPU, err = ResolveCPUBackend(backend)
			check(t, err)
			factory.KTFRunBudget = DefaultKTFHandsetRunBudget
			factory.FrameRunBudget = DefaultKTFHandsetRunBudget
			created, err := factory.Create(context.Background(), machinecore.Source{
				Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			check(t, err)
			m := created.(*Machine)
			t.Cleanup(func() { _ = m.Close() })
			check(t, m.Start(context.Background()))
			for frame := 0; frame < 440; frame++ {
				control := ""
				switch frame {
				case 60, 62, 330, 332:
					control = "ok"
				case 180, 182, 210, 212, 240, 242:
					control = "down"
				}
				if control != "" {
					check(t, m.QueueInput(machinecore.InputEvent{Control: control, Pressed: frame%10 == 0}))
				}
				check(t, m.StepFrame(context.Background()))
			}
			assertIssue513Captions(t, m.Framebuffer())
			var saved bytes.Buffer
			check(t, m.SaveState(&saved))
			for _, control := range []string{"left", "right"} {
				check(t, m.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
				check(t, m.StepFrame(context.Background()))
				check(t, m.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
				for frame := 0; frame < 30; frame++ {
					check(t, m.StepFrame(context.Background()))
				}
				assertIssue513Captions(t, m.Framebuffer())
			}
			check(t, m.LoadState(bytes.NewReader(saved.Bytes())))
			check(t, m.StepFrame(context.Background()))
			assertIssue513Captions(t, m.Framebuffer())
		})
	}
}

func assertIssue513Captions(t *testing.T, frame image.Image) {
	t.Helper()
	for _, label := range []struct {
		name           string
		x1, x2, pixels int
	}{
		{"Yes", 68, 80, 25},
		{"No", 95, 105, 24},
	} {
		countWhite := func(top int) int {
			count := 0
			for y := top; y < top+6; y++ {
				for x := label.x1; x < label.x2; x++ {
					r, g, b, _ := frame.At(x, y).RGBA()
					if r == 0xffff && g == 0xffff && b == 0xffff {
						count++
					}
				}
			}
			return count
		}
		if got := countWhite(142); got != label.pixels {
			t.Errorf("%s caption below selector has %d white pixels, want %d", label.name, got, label.pixels)
		}
		if got := countWhite(126); got != 0 {
			t.Errorf("%s caption remains at button top: %d white pixels", label.name, got)
		}
	}
}
