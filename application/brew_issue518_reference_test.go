package application

import (
	"bytes"
	"context"
	"math/rand"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Replay the reported seed exactly, and also keep stepping after its failing
// input prefix instead of letting a later generated End key close the applet.
func TestBREWIssue518RandomInput(t *testing.T) {
	path, data := findAuthorizedPackage(t, "077ede3589562150df40f9f3681160ad0be1f21d51afef08cfe196756488e962")
	controls := []string{"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu", "back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3", "num4", "num5", "num6", "num7", "num8", "num9"}
	for _, test := range []struct {
		name        string
		inputFrames int
	}{
		{"baseline", 0}, {"exact seed 319", 600}, {"continue after reported prefix", 113},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			factory := NewFactory()
			factory.AllowUntrustedBREW = true
			factory.RunBudget = DefaultKTFHandsetRunBudget
			factory.KTFRunBudget = DefaultKTFHandsetRunBudget
			factory.FrameRunBudget = DefaultKTFHandsetRunBudget
			created, err := factory.Create(ctx, machinecore.Source{Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
			if err != nil {
				t.Fatal(err)
			}
			machine := created.(*brewMachine)
			t.Cleanup(func() { _ = machine.Close() })
			if err := machine.Start(ctx); err != nil {
				t.Fatal(err)
			}
			random := rand.New(rand.NewSource(319))
			held := make(map[string]bool)
			for frame := 0; frame < 600; frame++ {
				if frame < test.inputFrames && random.Intn(4) == 0 {
					control := controls[random.Intn(len(controls))]
					held[control] = !held[control]
					if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: held[control]}); err != nil {
						t.Fatal(err)
					}
				}
				if err := machine.StepFrame(ctx); err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if machine.State() == machinecore.StateStopped {
					if test.inputFrames != 600 || frame <= 112 {
						t.Fatalf("unexpected stop at frame %d", frame)
					}
					t.Logf("reported fault passed; applet closed normally at frame %d", frame)
					return
				}
			}
			stats, _ := machine.BREWFrameStats()
			if stats.PresentCount < 100 {
				t.Fatalf("only %d guest presentations", stats.PresentCount)
			}
		})
	}
}
