package application

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// newKTFQuantumMachine builds the smallest KTF machine a presentation quantum
// can be driven against: one mapped image, one task, and the shared services.
func newKTFQuantumMachine(t *testing.T) *Machine {
	t.Helper()
	drawBuffer := image.NewRGBA(image.Rect(0, 0, 2, 1))
	runtime, err := ktfrt.NewRuntimeForProfile(
		interpreter.New(),
		ktf.Package{
			ClientName: "client.bin0",
			// Thumb "b ." - a woken task spins until it runs out of budget
			// instead of wandering into unmapped memory.
			Client: []byte{0xFE, 0xE7},
		},
		drawBuffer,
		ktfrt.ProfileID,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTraceMode(ktfrt.KTFTraceFull); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.CPU.Close() })
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	machine := &Machine{
		frame:        drawBuffer,
		ktf:          runtime,
		cpu:          runtime.CPU,
		state:        machinecore.StateReady,
		runBudget:    ktfrt.RunBudgetMin,
		ktfRunBudget: ktfrt.RunBudgetMin,
		ktfStarted:   true,
	}
	// Park the clock on a whole millisecond so deadline arithmetic in the test
	// is exact rather than depending on where the previous quantum landed.
	if err := runtime.Services.Advance(
		runtime.ServiceOwner,
		17*time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	runtime.TickMS = 17
	return machine
}

func ktfQuantumSteps(runtime *ktfrt.Runtime) []string {
	var steps []string
	for _, entry := range runtime.HostTrace {
		if strings.HasPrefix(entry, "ktf_quantum_step:") {
			steps = append(steps, entry)
		}
	}
	return steps
}

// TestKTFQuantumStopsOnASleepingTaskDeadline pins the fix for a title whose own
// speed setting had no effect: the mirrored tick only moved once per quantum,
// so a wait the guest asked to be five milliseconds long lasted a whole
// sixteen-millisecond quantum and every in-game speed rounded to the same
// frame rate.
func TestKTFQuantumStopsOnASleepingTaskDeadline(t *testing.T) {
	machine := newKTFQuantumMachine(t)
	runtime := machine.ktf
	task, err := runtime.NewTask(ktfrt.ImageBase|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	task.WakeAtMS = runtime.TickMS + 5
	runtime.Tasks = []*ktfrt.Task{task}
	runtime.HostTrace = nil

	before := runtime.Services.Clock.Monotonic()
	if err := machine.runKTFSlice(context.Background(), ktfrt.FrameDuration); err != nil {
		t.Fatal(err)
	}
	if advanced := runtime.Services.Clock.Monotonic() - before; advanced != ktfrt.FrameDuration {
		t.Fatalf("quantum advanced %s, want %s", advanced, ktfrt.FrameDuration)
	}
	steps := ktfQuantumSteps(runtime)
	if len(steps) != 2 ||
		!strings.HasPrefix(steps[0], "ktf_quantum_step:advance_ms=5:tick_ms=22") {
		t.Fatalf("quantum steps = %v, want the guest to wake five milliseconds in", steps)
	}
	if task.WakeAtMS != 0 {
		t.Fatalf("task wake deadline after the quantum = %d, want it cleared", task.WakeAtMS)
	}
}

// TestKTFQuantumKeepsWholeAdvanceWithoutADeadline guards the common case: a
// title that is not waiting on a timer still receives the quantum in one piece,
// so the fix cannot cost a service advance per frame for every other title.
func TestKTFQuantumKeepsWholeAdvanceWithoutADeadline(t *testing.T) {
	for _, test := range []struct {
		name string
		wake uint64
	}{
		{name: "runnable", wake: 0},
		{name: "sleeping past the quantum", wake: 17 + 40},
		{name: "parked forever", wake: ^uint64(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			machine := newKTFQuantumMachine(t)
			runtime := machine.ktf
			task, err := runtime.NewTask(ktfrt.ImageBase|1, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			task.WakeAtMS = test.wake
			runtime.Tasks = []*ktfrt.Task{task}
			runtime.HostTrace = nil

			before := runtime.Services.Clock.Monotonic()
			if err := machine.runKTFSlice(
				context.Background(),
				ktfrt.FrameDuration,
			); err != nil {
				t.Fatal(err)
			}
			if advanced := runtime.Services.Clock.Monotonic() - before; advanced != ktfrt.FrameDuration {
				t.Fatalf("quantum advanced %s, want %s", advanced, ktfrt.FrameDuration)
			}
			if steps := ktfQuantumSteps(runtime); len(steps) != 0 {
				t.Fatalf("quantum steps = %v, want the whole quantum in one advance", steps)
			}
		})
	}
}
