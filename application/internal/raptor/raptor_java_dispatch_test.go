package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestRaptorDispatchTableAnswersTheReceiversVTable pins module-100 ordinal 100.
//
// It used to fall through to the unimplemented-import path, which resumed the
// guest with r0 = 0. The guest indexes what comes back as a dispatch table
// (`table + offset*4 + 4`) and branches to the method it finds, so a zero
// answer made it load a method address of 0 from address 4 and branch there:
// SD한국전쟁 fails with "ARM fetch at 0x00000000" at frame 113 dispatching an
// org/kwis/msf/io/Socket method this way (issue #196).
func TestRaptorDispatchTableAnswersTheReceiversVTable(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	object, err := public.Heap.Allocate(12, true)
	if err != nil || object == 0 {
		t.Fatalf("allocate object = 0x%08x, %v", object, err)
	}
	const vtable = uint32(0x1000ff80)
	check(t, public.WriteU32(object, vtable))

	check(t, public.CPU.WriteRegister(cpu.RegisterR0, object))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 100},
	))
	got, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != vtable {
		t.Fatalf("module100#100(object) = 0x%08x, want the receiver's vtable 0x%08x",
			got, vtable)
	}
	if public.Unimplemented["RAPTOR.module100#100"] != 0 {
		t.Fatal("the dispatch-table helper fell through to the anonymous import path")
	}

	// The guest's own unresolved-method path calls the same helper with a null
	// receiver on purpose; that must not read address 0 as a table.
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, 0))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 100},
	))
	got, err = public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != 0 {
		t.Fatalf("module100#100(null) = 0x%08x, want 0", got)
	}
}
