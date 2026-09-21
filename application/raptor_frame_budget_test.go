package application

import (
	"context"
	"testing"

	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorCallbackBudgetOverrideAndInheritance(t *testing.T) {
	for _, allowance := range []uint64{0, 17, 64} {
		m := newSyntheticMachine(t)
		const callback = uint32(0x04000000)
		check(t, m.cpu.Map(callback, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, m.cpu.WriteMemory(callback, []byte{
			0x00, 0x20, // movs r0, #0
			0x01, 0x30, // adds r0, #1
			0x0a, 0x28, // cmp r0, #10
			0xfc, 0xd1, // bne loop
			0x70, 0x47, // bx lr
		}))
		m.frameRunBudget = 3
		m.raptorFrameRunBudget = allowance
		m.raptor = &raptorrt.Runtime{CPU: m.cpu, Public: m.wipi, Started: true, Clet: raptorrt.Clet{Paint: callback | 1}}
		check(t, m.StepFrame(context.Background()))
		if allowance == 64 {
			if len(m.raptor.CallbackTasks) != 0 {
				t.Fatal("callback did not finish within its explicit allowance")
			}
		} else {
			want := allowance
			if want == 0 {
				want = 3
			}
			if got := m.LastResult(); got.Reason != cpu.StopBudget || got.Instructions != want {
				t.Fatalf("allowance %d: result = %+v, want budget stop after %d instructions", allowance, got, want)
			}
			drainRaptorCallbackTasks(t, m)
		}
		if m.frameRunBudget != 3 {
			t.Fatal("Raptor allowance changed the generic budget")
		}
	}
}
