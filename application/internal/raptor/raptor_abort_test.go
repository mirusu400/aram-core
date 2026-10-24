package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorAbortImportStopsInsteadOfReturning(t *testing.T) {
	for _, module := range []uint32{1, 507} {
		public := newPublicRuntime(t)
		runtime := &Runtime{CPU: public.CPU, Public: public}
		check(t, public.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
		check(t, runtime.dispatchImport(context.Background(), raptorImportKey{
			Module: module, Ordinal: 1061,
		}))
		if !public.ExitRequested {
			t.Fatalf("module %d abort returned without requesting exit", module)
		}
		if got := public.Unimplemented["RAPTOR.module1#1061"] +
			public.Unimplemented["RAPTOR.module507#1061"]; got != 0 {
			t.Fatalf("module %d abort remained unimplemented", module)
		}
		if got := public.Observed["MC_knlExit"]; got != 1 {
			t.Fatalf("module %d abort exit calls = %d, want 1", module, got)
		}
	}
}
