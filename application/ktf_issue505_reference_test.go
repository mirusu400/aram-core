package application

import (
	"bytes"
	"context"
	"math/rand"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Issue #505's seed parks Asphalt 4's paint callback in Object.wait. Input must
// still reach its card instead of filling the machine queue at frame 6142.
func TestKTFWaitingPaintDrainsAsphaltFourInput(t *testing.T) {
	path, data := findAuthorizedPackage(t, asphaltFourSHA256)
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), SHA256: asphaltFourSHA256,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))
	random := rand.New(rand.NewSource(272))
	held := make(map[string]bool, len(fuzzControls))
	for frame := 0; frame < 10000; frame++ {
		if random.Intn(4) == 0 {
			control := fuzzControls[random.Intn(len(fuzzControls))]
			held[control] = !held[control]
			if err := machine.QueueInput(machinecore.InputEvent{
				Control: control, Pressed: held[control],
			}); err != nil {
				t.Fatalf("frame %d queueing %s: %v", frame, control, err)
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	// Check that the submitted edges actually drain, rather than merely staying
	// below the safety bound during the reproducing sequence.
	for frame := 0; frame < 60 && len(machine.input) != 0; frame++ {
		check(t, machine.StepFrame(context.Background()))
	}
	if len(machine.input) != 0 {
		t.Fatalf("%d key edges remain queued after the replay", len(machine.input))
	}
}
