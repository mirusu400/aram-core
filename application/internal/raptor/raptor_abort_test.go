package raptor

import (
	"bytes"
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

func TestRaptorReallocImportPreservesAllocation(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	original, err := public.Heap.Allocate(8, false)
	check(t, err)
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	check(t, public.CPU.WriteMemory(original, want))
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, original))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, 24))
	check(t, public.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))

	check(t, runtime.dispatchImport(context.Background(), raptorImportKey{
		Module: 1, Ordinal: 1063,
	}))
	replacement, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if replacement == 0 || replacement == original {
		t.Fatalf("realloc result = 0x%08x, original 0x%08x", replacement, original)
	}
	if _, ok := public.Heap.Root().Allocations[original]; ok {
		t.Fatal("realloc kept the old allocation")
	}
	got := make([]byte, len(want))
	check(t, public.CPU.ReadMemory(replacement, got))
	if !bytes.Equal(got, want) {
		t.Fatalf("realloc contents = %v, want %v", got, want)
	}
	if public.Observed["RAPTOR.realloc"] != 1 || public.Stats.UnimplementedCalls != 0 {
		t.Fatalf("realloc accounting = %+v, unimplemented=%+v", public.Stats, public.Unimplemented)
	}
}
