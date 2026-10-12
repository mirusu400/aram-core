package interpreter

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbJITBranchLinkPairsMatchPrecise(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		check(t, b.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		t.Cleanup(func() { _ = b.Close() })
	}
	for _, pc := range []uint32{0x1400, 0x1402} {
		for _, high := range []uint16{0, 1, 0x3ff, 0x400, 0x7ff} {
			for _, suffix := range []uint16{0xf800, 0xf801, 0xf802, 0xfbff, 0xfc00, 0xffff, 0xe800, 0xe802, 0xeffe, 0xe801, 0x4770} {
				for traced := 0; traced < 2; traced++ {
					jit.pcHits = nil
					if traced != 0 {
						jit.pcHits = make(map[uint32]uint64)
					}
					var results [2]cpu.Result
					for index, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(pc, thumbTestCode(0xf000|high, suffix)))
						check(t, b.WriteRegister(cpu.RegisterLR, 0xdeadbeef))
						check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagZ|flagC|flagV|uint32(processorModeSystem)))
						results[index] = b.Run(context.Background(), pc, cpu.ModeThumb, 1)
					}
					if results[0].Reason != results[1].Reason || results[0].Instructions != results[1].Instructions ||
						results[0].PC != results[1].PC || fmt.Sprint(results[0].Err) != fmt.Sprint(results[1].Err) {
						t.Fatalf("pc=%#x prefix=%#x suffix=%#x trace=%d: precise=%+v jit=%+v", pc, high, suffix, traced, results[0], results[1])
					}
					for _, r := range []uint32{cpu.RegisterPC, cpu.RegisterLR, cpu.RegisterCPSR} {
						if got, want := immediateMemoryRegisterValue(t, jit, r), immediateMemoryRegisterValue(t, precise, r); got != want {
							t.Fatalf("prefix=%#x suffix=%#x register=%d: got %#x want %#x", high, suffix, r, got, want)
						}
					}
				}
			}
		}
	}
}

func TestThumbJITBranchLinkSuffixFaultPreservesState(t *testing.T) {
	for _, suffixPermission := range []cpu.Permissions{0, cpu.PermissionRead, cpu.PermissionRead | cpu.PermissionExecute} {
		precise, jit := New(), NewJIT()
		var results [2]cpu.Result
		for index, b := range []*Backend{precise, jit} {
			check(t, b.Map(0x1000, 2, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, b.WriteMemory(0x1000, thumbTestCode(0xf000)))
			if suffixPermission != 0 {
				check(t, b.Map(0x1002, 2, suffixPermission|cpu.PermissionWrite))
				check(t, b.WriteMemory(0x1002, thumbTestCode(0xf800)))
			}
			t.Cleanup(func() { _ = b.Close() })
			check(t, b.WriteRegister(cpu.RegisterLR, 0xdeadbeef))
			results[index] = b.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
		}
		if results[0].Reason != results[1].Reason || results[0].Instructions != results[1].Instructions ||
			results[0].PC != results[1].PC || fmt.Sprint(results[0].Err) != fmt.Sprint(results[1].Err) {
			t.Fatalf("suffix permission %d: precise=%+v jit=%+v", suffixPermission, results[0], results[1])
		}
		if got, want := immediateMemoryRegisterValue(t, jit, cpu.RegisterLR), immediateMemoryRegisterValue(t, precise, cpu.RegisterLR); got != want {
			t.Fatalf("suffix fault changed link register: got %#x want %#x", got, want)
		}
	}
}

func TestThumbJITBranchLinkSuffixChangeWithinBlockIsFetched(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		mapCodeAndStack(t, b)
		t.Cleanup(func() { _ = b.Close() })
		// The first instruction replaces the suffix of the branch that
		// follows it. The pair still retires once and uses the new target.
		check(t, b.WriteMemory(0x1000, thumbTestCode(0x8008, 0xf000, 0xf800)))
		check(t, b.WriteRegister(cpu.RegisterR0, 0xf801))
		check(t, b.WriteRegister(cpu.RegisterR1, 0x1004))
	}
	assertThumbFillParity(t, precise, jit, 0x1000, 2)
	if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterPC); got != 0x1008 {
		t.Fatalf("branch used an old suffix: pc=%#x", got)
	}
}

func TestThumbJITBranchLinkSuffixInstructionCacheMatchesPrecise(t *testing.T) {
	for traced := 0; traced < 2; traced++ {
		precise, jit := New(), NewJIT()
		const pc = uint32(0x101e) // The suffix occupies a different cache line.
		const suffix = pc + 2
		const conflict = suffix + instructionCacheSets*instructionCacheLineSize
		for _, b := range []*Backend{precise, jit} {
			check(t, b.Map(0x1000, 0x200, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, b.Map(conflict, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, b.WriteMemory(pc, thumbTestCode(0xf000, 0xf800)))
			check(t, b.WriteMemory(conflict, thumbTestCode(0x2000, 0xbe00)))
			b.setCP15Control(1 << 12)
			if traced != 0 {
				b.pcHits = make(map[uint32]uint64)
			}
			t.Cleanup(func() { _ = b.Close() })
		}
		run := func(stage string, wantPC uint32) {
			t.Helper()
			var results [2]cpu.Result
			for index, b := range []*Backend{precise, jit} {
				results[index] = b.Run(context.Background(), pc, cpu.ModeThumb, 1)
			}
			for _, result := range results {
				if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 1 || result.PC != wantPC {
					t.Fatalf("%s trace=%d expected pc=%#x: precise=%+v jit=%+v", stage, traced, wantPC, results[0], results[1])
				}
			}
			for r := uint32(0); r <= cpu.RegisterCPSR; r++ {
				if got, want := immediateMemoryRegisterValue(t, jit, r), immediateMemoryRegisterValue(t, precise, r); got != want {
					t.Fatalf("%s register %d: got %#x want %#x", stage, r, got, want)
				}
			}
			if !reflect.DeepEqual(precise.pcHits, jit.pcHits) {
				t.Fatalf("%s changed instruction trace: precise=%v jit=%v", stage, precise.pcHits, jit.pcHits)
			}
		}
		run("initial cross-line fetch", pc+4)
		for _, b := range []*Backend{precise, jit} {
			check(t, b.WriteMemory(suffix, thumbTestCode(0xf801)))
		}
		run("backing write retains cached suffix", pc+4)
		for _, b := range []*Backend{precise, jit} {
			check(t, b.writeCP15(7, 5, 1, suffix))
		}
		run("suffix line invalidated", pc+6)
		for _, b := range []*Backend{precise, jit} {
			// Diagnostic cache mutations need not advance the line serial.
			// A suffix must still be fetched, rather than retained in an op.
			entry := b.instructionCacheEntry(suffix)
			entry.line[0] = 2
		}
		run("resident suffix changes without serial", pc+8)
		for _, b := range []*Backend{precise, jit} {
			check(t, b.WriteMemory(suffix, thumbTestCode(0xf803)))
			_, err := b.fetch16(conflict)
			check(t, err)
		}
		run("suffix eviction reloads backing bytes", pc+10)
	}
}

func TestThumbJITBranchLinkWholeSystemTrapsAndTraceMatchPrecise(t *testing.T) {
	for _, target := range []uint32{0x1004, 0x1008} {
		precise, jit := New(), NewJIT()
		var results [2]cpu.Result
		for index, b := range []*Backend{precise, jit} {
			bus := &testSystemBus{memory: make(map[uint32]byte)}
			code := thumbTestCode(0xf000, 0xf800|uint16((target-0x1004)/2))
			for offset, value := range code {
				bus.memory[0x1000+uint32(offset)] = value
			}
			check(t, b.AttachSystemBus(bus))
			check(t, b.SetExecutionTraps([]cpu.ExecutionTrap{
				{Address: 0x1002, Mode: cpu.ModeThumb}, // The suffix is not a boundary.
				{Address: target, Mode: cpu.ModeThumb},
			}))
			b.pcHits = make(map[uint32]uint64)
			t.Cleanup(func() { _ = b.Close() })
			results[index] = b.Run(context.Background(), 0x1000, cpu.ModeThumb, 2)
		}
		for _, result := range results {
			if result.Err != nil || result.Reason != cpu.StopExecutionTrap || result.Instructions != 1 || result.PC != target {
				t.Fatalf("target=%#x: precise=%+v jit=%+v", target, results[0], results[1])
			}
		}
		if !reflect.DeepEqual(precise.pcHits, jit.pcHits) || jit.pcHits[0x1000] != 1 || jit.pcHits[0x1002] != 0 {
			t.Fatalf("BL pair changed trace boundaries: precise=%v jit=%v", precise.pcHits, jit.pcHits)
		}
	}
}
