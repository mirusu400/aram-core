package minigame

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestFrameEventResumesAfterInstructionBudget(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x30, // adds r0, #1
		0xfd, 0xe7, // b 0x1000
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterPC, guest.ReturnSentinel); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		cpu:   backend,
		entry: 0x1001,
		stage: 5,
	}

	first, completed, err := runtime.StepFrame(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if completed || first.Instructions != 4 || len(runtime.Stats.Events) != 1 {
		t.Fatalf("first frame slice = %+v, completed=%t", first, completed)
	}
	if got, err := backend.ReadRegister(cpu.RegisterR0); err != nil || got != FrameEvent+2 {
		t.Fatalf("r0 after first slice = %d, err=%v; want %d", got, err, FrameEvent+2)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, 99); err != nil {
		t.Fatal(err)
	}

	second, completed, err := runtime.StepFrame(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if completed || second.Instructions != 4 || len(runtime.Stats.Events) != 2 {
		t.Fatalf("second frame slice = %+v, completed=%t", second, completed)
	}
	if got, err := backend.ReadRegister(cpu.RegisterR0); err != nil || got != FrameEvent+4 {
		t.Fatalf("r0 after resumed slice = %d, err=%v; want %d", got, err, FrameEvent+4)
	}
	if got, err := backend.ReadRegister(cpu.RegisterR1); err != nil || got != 99 {
		t.Fatalf("r1 after resumed slice = %d, err=%v; want 99", got, err)
	}
}
