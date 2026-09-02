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

// soulCardTwoSHA256 identifies the 소울카드마스터2 package the timer deferral was
// measured against.
const soulCardTwoSHA256 = "eb1614abed708f41237181e7d80547c941faa989dcef50e9338c6f71dbb61c01"

// fuzzControls is the handset surface aram-fuzz presses, in its order. The
// sequence below is only reproducible while both agree.
var fuzzControls = []string{
	"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu",
	"back", "send", "end", "star", "hash",
	"num0", "num1", "num2", "num3", "num4",
	"num5", "num6", "num7", "num8", "num9",
}

// TestKTFTimerScheduleWaitsForATaskSlot covers issue #128. A java.util.Timer
// runs its tasks on one thread of its own and queues whatever it cannot start
// yet, so scheduling never fails on the handset for want of a thread. Here
// every scheduled TimerTask took a task slot and held it for as long as its
// run() slept, so 소울카드마스터2 - which sleeps in run() and schedules from key
// handling - filled all sixteen slots with sleeping timer tasks and died on
// the next schedule with "KTF Java task limit 16 reached".
//
// The key sequence is aram-fuzz's seed 1 at density 6, which reaches the fault
// at frame 1129 of 1200.
func TestKTFTimerScheduleWaitsForATaskSlot(t *testing.T) {
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

	random := rand.New(rand.NewSource(1))
	held := make(map[string]bool, len(fuzzControls))
	for frame := 0; frame < 1200; frame++ {
		if random.Intn(6) == 0 {
			control := fuzzControls[random.Intn(len(fuzzControls))]
			pressed := !held[control]
			held[control] = pressed
			if err := machine.QueueInput(machinecore.InputEvent{
				Control: control, Pressed: pressed,
			}); err != nil {
				t.Fatalf("frame %d queueing %s: %v", frame, control, err)
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
