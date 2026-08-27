package application

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func TestMinigameResumedFrameKeepsDrainingServiceEvents(t *testing.T) {
	machine := newSyntheticMachine(t)
	const (
		entry    = uint32(0x04000000)
		dataBase = uint32(0x05000000)
	)
	if err := machine.cpu.Map(
		entry,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.Map(
		dataBase,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		t.Fatal(err)
	}
	// The first five lifecycle calls increment the counter and return. The
	// sixth frame event enters a permanent loop, forcing every later host frame
	// to resume at an instruction-budget boundary.
	if err := machine.cpu.WriteMemory(entry, []byte{
		0x03, 0x49, // ldr r1, [pc, #12] -> dataBase
		0x0a, 0x68, // ldr r2, [r1]
		0x01, 0x32, // adds r2, #1
		0x0a, 0x60, // str r2, [r1]
		0x05, 0x2a, // cmp r2, #5
		0x00, 0xd8, // bhi loop
		0x70, 0x47, // bx lr
		0xfe, 0xe7, // loop: b loop
		0x00, 0x00, 0x00, 0x05,
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := minigame.NewRuntime(
		machine.cpu,
		machine.frame,
		machine.wipi,
		dataBase,
		0x1000,
		entry|1,
	)
	if err != nil {
		t.Fatal(err)
	}
	machine.minigame = runtime
	machine.frameRunBudget = 1
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	for frame := range 1100 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("resumed minigame frame %d: %v", frame, err)
		}
		if queued := machine.wipi.Services.Events.Len(); queued > 16 {
			t.Fatalf(
				"service event queue grew to %d during resumed frame %d",
				queued,
				frame,
			)
		}
	}
	if machine.State() == machinecore.StateFaulted {
		t.Fatalf("resumed minigame frame faulted: %+v", machine.LastResult())
	}
	if result := machine.LastResult(); result.Reason != cpu.StopBudget ||
		result.Instructions != 1 {
		t.Fatalf("resumed minigame result = %+v", result)
	}
}
