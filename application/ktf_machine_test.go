package application

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFJavaCallsWaitForReusableTaskStack(t *testing.T) {
	runtime, err := ktfrt.NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runnable, err := runtime.NewHostJavaObject("java/lang/Thread")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Tasks = make([]*ktfrt.Task, ktfrt.MaxTasks)
	for index := range runtime.Tasks {
		runtime.Tasks[index] = &ktfrt.Task{}
	}

	if err := runtime.QueueJavaVirtual(runnable, "run", "()V"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.PendingJavaCalls) != 1 {
		t.Fatalf("pending Java calls = %d, want 1", len(runtime.PendingJavaCalls))
	}
	runtime.Tasks[3].Done = true
	if err := runtime.ActivatePendingJavaCalls(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.PendingJavaCalls) != 0 ||
		runtime.Tasks[3].Done ||
		len(runtime.Tasks[3].Context) == 0 {
		t.Fatalf(
			"activated Java call: pending=%d task=%+v",
			len(runtime.PendingJavaCalls),
			runtime.Tasks[3],
		)
	}
}

func TestKTFMachineKeepsInputUntilCardExists(t *testing.T) {
	runtime, err := ktfrt.NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.TickMS = 100
	machine := &Machine{
		ktf: runtime,
		input: []machinecore.InputEvent{
			{Control: "down", At: 10 * time.Millisecond},
		},
	}

	if err := machine.queueKTFInput(runtime); err != nil {
		t.Fatal(err)
	}
	if len(machine.input) != 1 || len(runtime.Tasks) != 0 {
		t.Fatalf("input=%#v tasks=%d", machine.input, len(runtime.Tasks))
	}
}

func TestKTFMachineLateInputDoesNotBackfillRepeats(t *testing.T) {
	runtime, err := ktfrt.NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	card, err := runtime.NewHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	const display = uint32(0x10004000)
	runtime.DefaultDisplay = display
	runtime.DisplayCards[display] = card
	if err := runtime.Services.Advance(
		runtime.ServiceOwner,
		2*time.Second,
	); err != nil {
		t.Fatal(err)
	}
	runtime.TickMS = 2000
	machine := &Machine{
		ktf: runtime,
		input: []machinecore.InputEvent{{
			Control: "ok",
			Pressed: true,
		}},
	}

	if err := machine.queueKTFInput(runtime); err != nil {
		t.Fatal(err)
	}
	if len(machine.input) != 0 || len(runtime.Tasks) != 1 {
		t.Fatalf("late input=%#v tasks=%d", machine.input, len(runtime.Tasks))
	}
	inputState := runtime.Services.Input.Snapshot()
	if len(inputState.Controls) != 1 ||
		inputState.Controls[0].NextRepeat !=
			int64(2500*time.Millisecond) {
		t.Fatalf("late input state = %+v", inputState)
	}
	if runtime.Services.Events.Len() != 0 {
		t.Fatalf(
			"late input left %d backfilled events",
			runtime.Services.Events.Len(),
		)
	}
}

func TestKTFMachineQueuesDueInputToDockedCard(t *testing.T) {
	runtime, err := ktfrt.NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTraceMode(ktfrt.KTFTraceFull); err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	card, err := runtime.NewHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	const display = uint32(0x10004000)
	runtime.DefaultDisplay = display
	runtime.DisplayCards[display] = card
	runtime.TickMS = 32
	machine := &Machine{
		ktf: runtime,
		input: []machinecore.InputEvent{
			{Control: "left", Pressed: true, At: 48 * time.Millisecond},
			{Control: "ok", Pressed: true, At: 32 * time.Millisecond},
		},
	}

	if err := machine.queueKTFInput(runtime); err != nil {
		t.Fatal(err)
	}
	if len(machine.input) != 1 || machine.input[0].Control != "left" {
		t.Fatalf("remaining input = %#v", machine.input)
	}
	if len(runtime.Tasks) != 1 {
		t.Fatalf("KTF tasks = %d, want 1", len(runtime.Tasks))
	}
	if runtime.Tasks[0].KeyCard != card {
		t.Fatalf(
			"KTF key task card = 0x%08x, want 0x%08x",
			runtime.Tasks[0].KeyCard,
			card,
		)
	}
	if queued, queueErr := runtime.QueueKeyEvent(false, -5); queueErr != nil {
		t.Fatal(queueErr)
	} else if queued {
		t.Fatal("overlapping card key event was queued")
	}
	runtime.Tasks[0].KeyCard = 0
	runtime.Tasks[0].WipicTimer = true
	if queued, queueErr := runtime.QueueKeyEvent(false, -5); queueErr != nil {
		t.Fatal(queueErr)
	} else if queued {
		t.Fatal("card key event overlapped a WIPI-C timer callback")
	}
	runtime.Tasks[0].KeyCard = card
	runtime.Tasks[0].WipicTimer = false
	runtime.Tasks[0].Done = true
	runtime.PaintTasks[card] = &ktfrt.Task{}
	if queued, queueErr := runtime.QueueKeyEvent(false, -5); queueErr != nil {
		t.Fatal(queueErr)
	} else if queued {
		t.Fatal("card key event overlapped a pending paint")
	}
	runtime.Tasks[0].Done = false
	delete(runtime.PaintTasks, card)
	if err := runtime.CPU.RestoreContext(runtime.Tasks[0].Context); err != nil {
		t.Fatal(err)
	}
	for register, want := range map[uint32]uint32{
		cpu.RegisterR1: card,
		cpu.RegisterR2: ktfrt.KeyPressed,
		cpu.RegisterR3: 0xfffffffb,
	} {
		got, readErr := runtime.CPU.ReadRegister(register)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got != want {
			t.Fatalf("input register r%d = 0x%08x, want 0x%08x", register, got, want)
		}
	}
	if trace := runtime.HostTrace[len(runtime.HostTrace)-1]; !strings.Contains(
		trace,
		"java_key_event:type=1:key=-5",
	) {
		t.Fatalf("input trace = %q", trace)
	}
}

func TestKTFMachineRemainsPausedWhileDockedCardCanReceiveEvents(t *testing.T) {
	for _, test := range []struct {
		name      string
		dockCard  bool
		wantState machinecore.State
	}{
		{
			name:      "event target",
			dockCard:  true,
			wantState: machinecore.StatePaused,
		},
		{
			name:      "no event target",
			wantState: machinecore.StateStopped,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := ktfrt.NewRuntime(interpreter.New(), ktf.Package{
				ClientName: "client.bin0",
				Client:     []byte{0x70, 0x47},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.CPU.Close()
			if test.dockCard {
				runtime.DefaultDisplay = 1
				runtime.DisplayCards[1] = 2
			}
			machine := &Machine{
				cpu:        runtime.CPU,
				state:      machinecore.StatePaused,
				runBudget:  DefaultRunBudget,
				ktf:        runtime,
				ktfStarted: true,
			}
			if err := machine.runKTFSlice(
				context.Background(),
				16*time.Millisecond,
			); err != nil {
				t.Fatal(err)
			}
			if machine.State() != test.wantState ||
				machine.LastResult().Reason != cpu.StopExited {
				t.Fatalf(
					"quiescent KTF state = %s, result=%+v",
					machine.State(),
					machine.LastResult(),
				)
			}
		})
	}
}

func TestKTFMachineRunsCooperativeTasksWithinOneClockQuantum(t *testing.T) {
	runtime, err := ktfrt.NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	var observed []string
	newTask := func(name string, stackIndex int) *ktfrt.Task {
		t.Helper()
		procedure := runtime.RegisterHostCall(
			"test.quantum."+name,
			func(context.Context, *ktfrt.Runtime) (uint32, error) {
				observed = append(observed, name)
				return 0, nil
			},
		)
		task, taskErr := runtime.NewTask(procedure, nil, stackIndex)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		return task
	}
	runtime.Tasks = []*ktfrt.Task{
		newTask("first", 0),
		newTask("second", 1),
	}
	runtime.DefaultDisplay = 1
	runtime.DisplayCards[1] = 2
	machine := &Machine{
		cpu:        runtime.CPU,
		state:      machinecore.StatePaused,
		runBudget:  ktfrt.RunBudgetMin,
		ktf:        runtime,
		ktfStarted: true,
	}

	if err := machine.runKTFSlice(
		context.Background(),
		16*time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "second"}; !slices.Equal(observed, want) {
		t.Fatalf("tasks run in quantum = %q, want %q", observed, want)
	}
	if got := runtime.Services.Clock.Monotonic(); got != 16*time.Millisecond {
		t.Fatalf("clock after cooperative quantum = %s, want 16ms", got)
	}
	if machine.State() != machinecore.StatePaused ||
		machine.LastResult().Reason != cpu.StopExited {
		t.Fatalf(
			"machine after cooperative quantum = %s, result=%+v",
			machine.State(),
			machine.LastResult(),
		)
	}
}
