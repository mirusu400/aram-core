package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Replay issue 517 through the ordinary factory, without patching the guest.
// Presentation alone also accepted the original empty startup notice and white
// intro screen, so require both a visible title and a distinct menu after input.
func TestRaptorIssue517ReachesTitleAndMenu(t *testing.T) {
	path, data := findAuthorizedPackage(t, "70d709c40e10541fe54a43501a5859045a0e6c454706500e6665c74c7c8fd52f")
	for _, backend := range []struct {
		name   string
		create CPUFactory
	}{
		{"jit", newJITCPU}, {"precise", newPreciseCPU},
	} {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			factory := NewFactory()
			factory.NewCPU = backend.create
			factory.FrameRunBudget = DefaultHandsetRunBudget
			factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
			created, err := factory.Create(ctx, machinecore.Source{Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
			if err != nil {
				t.Fatal(err)
			}
			machine := created.(*Machine)
			t.Cleanup(func() { _ = machine.Close() })
			if err := machine.Start(ctx); err != nil {
				t.Fatal(err)
			}
			var title [32]byte
			for frame := 1; frame <= 1800; frame++ {
				if frame >= 300 && (frame%300 == 0 || frame%300 == 10) {
					if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: frame%300 == 0}); err != nil {
						t.Fatal(err)
					}
				}
				if err := machine.StepFrame(ctx); err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if frame == 1200 || frame == 1800 {
					coloured := 0
					for y := 24; y < machine.frame.Bounds().Dy(); y += 4 {
						for x := 0; x < machine.frame.Bounds().Dx(); x += 4 {
							pixel := machine.frame.RGBAAt(x, y)
							if pixel.R != pixel.G || pixel.G != pixel.B {
								coloured++
							}
						}
					}
					if coloured < 1000 {
						t.Fatalf("frame %d: only %d coloured scene samples", frame, coloured)
					}
					current := sha256.Sum256(machine.frame.Pix)
					if frame == 1200 {
						title = current
					} else if current == title {
						t.Fatal("confirmation did not advance the title to the menu")
					}
				}
			}
		})
	}
}
