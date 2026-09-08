package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestRaptorMonitorHelpersLeaveTheObjectAlone pins that module-100 ordinals 86
// and 87 read only r0.
//
// They used to be dispatched as a getfield/putfield accessor pair whose ABI was
// assumed to be "r0 = object, r1 = value, r2 = byte offset into the field
// block". The call sites set only r0, so r1 and r2 hold whatever the caller
// left there, and ordinal 87 wrote that leftover value at that leftover offset
// into a live object's field block - at unaligned offsets too, which sliced the
// top bytes off neighbouring references. The guest then dispatched through a
// half-erased pointer and branched into its own .text (issue #211, and the
// same shape in #162/#176/#177/#196).
//
// The registers below are the ones one seed-75 frame of 훼밀리마트타이쿤 really
// left behind: r1 = 0 and r2 = 0x23, an offset that is not even word-aligned.
func TestRaptorMonitorHelpersLeaveTheObjectAlone(t *testing.T) {
	for _, ordinal := range []uint32{87, 86} {
		public := newPublicRuntime(t)
		runtime := &Runtime{
			CPU:             public.CPU,
			Public:          public,
			resolvedImports: make(map[raptorImportKey]uint64),
			importSlotByKey: make(map[raptorImportKey]uint32),
		}

		// A three-word object header whose +8 word points at a field block, the
		// layout NewRaptorJavaObject builds.
		object, err := public.Heap.Allocate(12, true)
		if err != nil || object == 0 {
			t.Fatalf("allocate object = 0x%08x, %v", object, err)
		}
		const fieldWords = 16
		fields, err := public.Heap.Allocate(fieldWords*4, true)
		if err != nil || fields == 0 {
			t.Fatalf("allocate fields = 0x%08x, %v", fields, err)
		}
		check(t, public.WriteU32(object+8, fields))
		for word := uint32(0); word < fieldWords; word++ {
			check(t, public.WriteU32(fields+word*4, 0x10039238+word))
		}

		check(t, public.CPU.WriteRegister(cpu.RegisterR0, object))
		check(t, public.CPU.WriteRegister(cpu.RegisterR1, 0))
		check(t, public.CPU.WriteRegister(cpu.RegisterR2, 0x23))

		check(t, runtime.dispatchImport(
			context.Background(),
			raptorImportKey{Module: 100, Ordinal: ordinal},
		))

		for word := uint32(0); word < fieldWords; word++ {
			got, err := public.ReadU32(fields + word*4)
			check(t, err)
			if want := 0x10039238 + word; got != want {
				t.Fatalf("module100#%d changed field word %d: 0x%08x, want 0x%08x",
					ordinal, word, got, want)
			}
		}

		name := "RAPTOR.java.monitorEnter"
		if ordinal == 87 {
			name = "RAPTOR.java.monitorExit"
		}
		if got := public.Stats.LastAPI; got != name {
			t.Fatalf("module100#%d recorded as %q, want %q", ordinal, got, name)
		}
		if got := public.Unimplemented["RAPTOR.module100#86"] +
			public.Unimplemented["RAPTOR.module100#87"]; got != 0 {
			t.Fatalf("monitor helper fell through to the anonymous import path %d times", got)
		}
	}
}
