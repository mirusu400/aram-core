package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// The realloc wrapper serves the same C heap as MC_knlAlloc, so every block it
// returns keeps the spare byte after the requested size that a title's
// one-past-the-end terminator relies on (issue #577).
func TestRaptorReallocKeepsSpareByteAfterRequest(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	realloc := func(address, size uint32) uint32 {
		t.Helper()
		check(t, public.CPU.WriteRegister(cpu.RegisterR0, address))
		check(t, public.CPU.WriteRegister(cpu.RegisterR1, size))
		check(t, public.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
		check(t, runtime.dispatchImport(context.Background(), raptorImportKey{
			Module: 1, Ordinal: 1063,
		}))
		result, err := public.CPU.ReadRegister(cpu.RegisterR0)
		check(t, err)
		if result == 0 {
			t.Fatalf("realloc(0x%08x, %d) failed", address, size)
		}
		if have := public.Heap.Root().Allocations[result]; have <= size {
			t.Fatalf("realloc(0x%08x, %d) block holds %d bytes, want a spare byte", address, size, have)
		}
		return result
	}
	block := realloc(0, 8)
	// The block now holds 16 bytes. Growing it to exactly that would use up
	// the spare byte, so the wrapper must move it.
	if moved := realloc(block, 16); moved == block {
		t.Fatal("realloc kept a block that has no spare byte left")
	}
}
