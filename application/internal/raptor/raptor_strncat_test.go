package raptor

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// 냐옹타이쿤2's name entry builds its line with module-1 ordinal 1032:
// strncat(name, committed, len) for each finished syllable and
// strncat(line, preedit, len) for the one being composed. The import was
// unresolved, so the name box stayed empty whatever the player typed
// (aram-emu#19). The preedit buffer is not NUL-terminated by the IME, so the
// count must bound the copy.
func TestRaptorStrncatImportAppendsBoundedText(t *testing.T) {
	for _, module := range []uint32{1, 507} {
		t.Run(fmt.Sprintf("module%d", module), func(t *testing.T) {
			public := newPublicRuntime(t)
			runtime := &Runtime{CPU: public.CPU, Public: public}
			destination, err := public.Heap.Allocate(32, true)
			check(t, err)
			source, err := public.Heap.Allocate(16, true)
			check(t, err)
			// "가" in EUC-KR, then the stray bytes a stack buffer leaves behind.
			check(t, public.CPU.WriteMemory(destination, []byte{0xb0, 0xa1, 0}))
			check(t, public.CPU.WriteMemory(source, []byte{0xb3, 0xaa, 'x', 'y', 0}))
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: destination,
				cpu.RegisterR1: source,
				cpu.RegisterR2: 2,
				cpu.RegisterLR: guest.ReturnSentinel | 1,
			} {
				check(t, public.CPU.WriteRegister(register, value))
			}
			check(t, runtime.dispatchImport(context.Background(), raptorImportKey{
				Module: module, Ordinal: 1032,
			}))
			got, err := public.CPU.ReadRegister(cpu.RegisterR0)
			check(t, err)
			if got != destination {
				t.Fatalf("strncat returned 0x%08x, want the destination 0x%08x", got, destination)
			}
			result := make([]byte, 6)
			check(t, public.CPU.ReadMemory(destination, result))
			// "가나", terminated, and nothing of the source past the count.
			if want := []byte{0xb0, 0xa1, 0xb3, 0xaa, 0, 0}; !bytes.Equal(result, want) {
				t.Fatalf("strncat result = % x, want % x", result, want)
			}
			if public.Stats.UnimplementedCalls != 0 || public.Observed["strncat"] != 1 {
				t.Fatalf("strncat dispatch: unimplemented=%d observed=%v",
					public.Stats.UnimplementedCalls, public.Observed)
			}
		})
	}
}
