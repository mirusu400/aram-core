package raptor

import (
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// Issue 525 parses level data through module-1 ordinal 1019. Returning zero
// for this import selects a nonexistent level resource and the guest
// dereferences MC_knlGetResourceID's -12 result as a pointer.
func TestRaptorAtoiImportParsesLevelNumbers(t *testing.T) {
	for _, module := range []uint32{1, 507} {
		t.Run(fmt.Sprintf("module%d", module), func(t *testing.T) {
			public := newPublicRuntime(t)
			runtime := &Runtime{CPU: public.CPU, Public: public}
			text, err := public.Heap.Allocate(64, true)
			check(t, err)
			for _, test := range []struct {
				text string
				want int32
			}{
				{"4", 4},
				{"13\r\n", 13},
				{" \t+27,42", 27},
				{"-12", -12},
				{"0017", 17},
				{"0x10", 0},
				{"", 0},
				{"level", 0},
			} {
				check(t, public.CPU.WriteMemory(text, append([]byte(test.text), 0)))
				check(t, public.CPU.WriteRegister(cpu.RegisterR0, text))
				// atoi has one argument: stale registers must not become a
				// strtol end-pointer or numeric base.
				check(t, public.CPU.WriteRegister(cpu.RegisterR1, 0xfffffff4))
				check(t, public.CPU.WriteRegister(cpu.RegisterR2, 16))
				check(t, public.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
				check(t, runtime.dispatchImport(context.Background(), raptorImportKey{
					Module: module, Ordinal: 1019,
				}))
				got, err := public.CPU.ReadRegister(cpu.RegisterR0)
				check(t, err)
				if int32(got) != test.want {
					t.Fatalf("atoi(%q) = %d, want %d", test.text, int32(got), test.want)
				}
			}
			if public.Stats.UnimplementedCalls != 0 || public.Observed["atoi"] != 8 {
				t.Fatalf("atoi dispatch: unimplemented=%d observed=%v",
					public.Stats.UnimplementedCalls, public.Observed)
			}
		})
	}
}
