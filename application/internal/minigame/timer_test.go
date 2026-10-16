package minigame

import (
	"context"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"testing"
	"time"
)

func TestOEMTimerUsesGuestClockAndIsConsumedOnce(t *testing.T) {
	backend := interpreter.New()
	defer backend.Close()
	for _, region := range []struct{ base, size uint32 }{{0x1000, 0x2000}, {guest.ReturnSentinel, 4}} {
		if err := backend.Map(region.base, region.size, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.WriteMemory(0x1000, []byte{0x70, 0x47}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(guest.ReturnSentinel, []byte{0, 0xbe}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterSP: 0x2000, cpu.RegisterR1: 0, cpu.RegisterR2: 1, cpu.RegisterR3: 50, cpu.RegisterPC: guest.ReturnSentinel,
	} {
		if err := backend.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &Runtime{cpu: backend, entry: 0x1001, stage: 5}
	runtime.AdvanceClock(100 * time.Millisecond)
	if _, err := runtime.dispatch(eadsServiceCall{id: 0x164, slot: 0x0c}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		clock            uint32
		wantInstructions bool
	}{{149, false}, {150, true}, {151, false}} {
		runtime.AdvanceClock(time.Duration(test.clock) * time.Millisecond)
		result, completed, err := runtime.StepFrame(context.Background(), 64)
		if err != nil {
			t.Fatal(err)
		}
		if !completed || (result.Instructions != 0) != test.wantInstructions {
			t.Fatalf("clock=%d result=%+v completed=%t", test.clock, result, completed)
		}
		if test.wantInstructions && result.Event != 0x504 {
			t.Fatalf("timer 0 dispatched %#x, want 0x504", result.Event)
		}
	}
}
