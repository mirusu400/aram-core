package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Alternate two blocks on different code pages, then replace the second one.
// A retained successor must never execute a translation from the old generation.
func TestThumbJITSuccessorDropsReplacedTarget(t *testing.T) {
	for _, create := range []func() *Backend{New, NewJIT} {
		b := create()
		for _, address := range []uint32{0x1000, 0x2000} {
			check(t, b.Map(address, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		}
		check(t, b.WriteMemory(0x1000, thumbTestCode(0x2001, 0x4710))) // MOVS r0,#1; BX r2
		check(t, b.WriteMemory(0x2000, thumbTestCode(
			0x3101, // ADDS r1,#1
			0x2903, // CMP r1,#3
			0xd000, // BEQ 0x2008
			0x4718, // BX r3
			0xbe00, // BKPT
		)))
		check(t, b.WriteRegister(cpu.RegisterR2, 0x2001))
		check(t, b.WriteRegister(cpu.RegisterR3, 0x1001))
		result := b.Run(context.Background(), 0x1000, cpu.ModeThumb, 17)
		if result.Err != nil || result.Instructions != 17 || result.PC != 0x2008 ||
			immediateMemoryRegisterValue(t, b, cpu.RegisterR1) != 3 {
			t.Fatalf("initial alternating blocks: %+v", result)
		}
		check(t, b.WriteMemory(0x2000, thumbTestCode(0x3102))) // ADDS r1,#2
		check(t, b.WriteRegister(cpu.RegisterR1, 0))
		result = b.Run(context.Background(), 0x1000, cpu.ModeThumb, 12)
		if result.Err != nil || result.Instructions != 12 || result.PC != 0x1000 ||
			immediateMemoryRegisterValue(t, b, cpu.RegisterR1) != 4 {
			t.Fatalf("replaced alternating block: %+v", result)
		}
		check(t, b.Close())
	}
}

func BenchmarkThumbJITSuccessors(b *testing.B) {
	backend := NewJIT()
	b.Cleanup(func() { _ = backend.Close() })
	for _, address := range []uint32{0x1000, 0x2000} {
		check(b, backend.Map(address, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	}
	// Keep the branch targets out of the arithmetic destinations.
	check(b, backend.WriteMemory(0x1000, thumbTestCode(0x3001, 0x3102, 0x4720)))
	check(b, backend.WriteMemory(0x2000, thumbTestCode(0x3203, 0x3304, 0x4728)))
	check(b, backend.WriteRegister(cpu.RegisterR4, 0x2001))
	check(b, backend.WriteRegister(cpu.RegisterR5, 0x1001))
	const budget = uint64(100_000)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		result := backend.Run(ctx, 0x1000, cpu.ModeThumb, budget)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget {
			b.Fatalf("run result = %+v", result)
		}
	}
	b.ReportMetric(float64(b.N)*float64(budget)/b.Elapsed().Seconds(), "guest-insn/s")
}

func TestThumbJITSuccessorDropsTargetChangedDuringRun(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		for _, address := range []uint32{0x1000, 0x2000} {
			check(t, b.Map(address, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		}
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.WriteMemory(0x1000, thumbTestCode(0x3001, 0x4720))) // ADDS r0,#1; BX r4
		check(t, b.WriteMemory(0x2000, thumbTestCode(0x2802, 0xd005, 0x4728)))
		check(t, b.WriteMemory(0x2010, thumbTestCode(0x803e, 0x4728))) // STRH r6,[r7]; BX r5
		check(t, b.WriteRegister(cpu.RegisterR4, 0x2001))
		check(t, b.WriteRegister(cpu.RegisterR5, 0x1001))
		check(t, b.WriteRegister(cpu.RegisterR6, 0x3005)) // Replace ADD #1 with ADD #5.
		check(t, b.WriteRegister(cpu.RegisterR7, 0x1000))
	}
	assertThumbFillParity(t, precise, jit, 0x1000, 64)
	if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterR0); got <= 7 {
		t.Fatalf("guest did not execute the replacement code: r0=%d", got)
	}
}

func TestThumbJITNonBranchHighRegistersRemainInBlock(t *testing.T) {
	program := thumbTestCode(0x4640, 0x4681, 0x4448, 0x4548, 0x3001, 0xe7f9)
	for budget := uint64(1); budget <= 24; budget++ {
		precise, jit := New(), NewJIT()
		for _, b := range []*Backend{precise, jit} {
			mapCodeAndStack(t, b)
			check(t, b.WriteMemory(0x1000, program))
			check(t, b.WriteRegister(cpu.RegisterR8, 0x80000000))
			check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
			t.Cleanup(func() { _ = b.Close() })
		}
		if block := jit.jitBlockAt(0x1000); block == nil || len(block.thumb) != 6 {
			t.Fatal("non-branching high-register operations split the translated block")
		}
		assertThumbFillParity(t, precise, jit, 0x1000, budget)
	}
}
