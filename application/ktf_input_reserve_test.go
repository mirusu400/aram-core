package application

import (
	"bytes"
	"context"
	"math/rand"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// TestKTFInputReserveDrainsSoulCardTwo covers issue #245. 소울카드마스터2
// schedules enough sleeping TimerTasks to occupy each ordinary KTF task stack.
// A handset still dispatches a physical key to its foreground card; keeping one
// task stack for that dispatcher prevents the host-side input queue from
// growing to its 1,024-event safety bound.
func TestKTFInputReserveDrainsSoulCardTwo(t *testing.T) {
	path, data := findAuthorizedPackage(t, soulCardTwoSHA256)

	factory := NewFactory()
	factory.NewCPU = func() cpu.Backend { return interpreter.New() }
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
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

	random := rand.New(rand.NewSource(99))
	held := make(map[string]bool, len(fuzzControls))
	for frame := 0; frame < 10_000; frame++ {
		if random.Intn(4) == 0 {
			control := fuzzControls[random.Intn(len(fuzzControls))]
			pressed := !held[control]
			held[control] = pressed
			if err := machine.QueueInput(machinecore.InputEvent{
				Control: control,
				Pressed: pressed,
			}); err != nil {
				t.Fatalf("frame %d queueing %s: %v", frame, control, err)
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
