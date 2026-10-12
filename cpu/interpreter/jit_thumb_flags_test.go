package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// The precise executor independently evaluates every flag write. Exercise
// dead-write translations at every possible budget cut, with carry-consuming
// ALU instructions, aliases, arithmetic boundaries, tracing, and fault exits.
func TestThumbDeadFlagsMatchPreciseAtEveryExit(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		mapCodeAndStack(t, b)
		t.Cleanup(func() { _ = b.Close() })
	}
	values := [...]uint32{0, 1, 31, 32, 255, 0x7fffffff, 0x80000000, 0xffffffff}
	seed := uint32(0xdeadf1a6)
	for trial := 0; trial < 128; trial++ {
		program := make([]uint16, 10)
		for index := range program[:8] {
			seed = seed*1664525 + 1013904223
			program[index] = uint16(seed % 0x4700)
			if program[index]&0xfc00 == 0x4400 && program[index]&0x0087 == 0x0087 {
				program[index] &^= 0x0080 // Keep randomized high-register writes off PC.
			}
		}
		program[8], program[9] = 0x2f00, 0xbe00 // CMP r7,#0; BKPT
		if trial&1 != 0 {
			// An unmapped load must expose the flags before it, even though
			// the suffix would have overwritten every flag on success.
			program[4] = 0x6838 // LDR r0,[r7]
		}
		for _, b := range []*Backend{precise, jit} {
			check(t, b.WriteMemory(0x1000, thumbTestCode(program...)))
		}
		for traced := 0; traced < 2; traced++ {
			if traced != 0 {
				jit.pcHits = make(map[uint32]uint64)
			} else {
				jit.pcHits = nil
			}
			for budget := uint64(1); budget <= uint64(len(program)); budget++ {
				var results [2]cpu.Result
				for index, b := range []*Backend{precise, jit} {
					for r := uint32(0); r < cpu.RegisterPC; r++ {
						check(t, b.WriteRegister(r, values[(int(r)+trial)%len(values)]))
					}
					check(t, b.WriteRegister(cpu.RegisterR7, 0xfffff000))
					check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(trial&15)<<28|uint32(processorModeSystem)))
					results[index] = b.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
				}
				want, got := results[0], results[1]
				if (want.Err == nil) != (got.Err == nil) || want.Reason != got.Reason ||
					want.Instructions != got.Instructions || want.PC != got.PC {
					t.Fatalf("trial %d trace %d budget %d: precise=%+v jit=%+v", trial, traced, budget, want, got)
				}
				for r := uint32(0); r <= cpu.RegisterCPSR; r++ {
					wantValue := immediateMemoryRegisterValue(t, precise, r)
					if gotValue := immediateMemoryRegisterValue(t, jit, r); gotValue != wantValue {
						t.Fatalf("trial %d trace %d budget %d register %d: got %#x want %#x; code=%#v", trial, traced, budget, r, gotValue, wantValue, program)
					}
				}
			}
		}
	}
}

func TestThumbDeadFlagsDoNotCrossFaultBarrier(t *testing.T) {
	instructions := []thumbMicroInstr{
		{raw: 0x3001, op: thumbAddImmediate},
		{raw: 0x6808, op: thumbImmediateTransfer},
		{raw: 0x3101, op: thumbAddImmediate},
		{raw: 0x3201, op: thumbAddImmediate},
		{raw: 0x3301, op: thumbAddImmediate},
	}
	markDeadThumbFlags(instructions)
	if instructions[0].appOp != thumbAddImmediate || instructions[4].appOp != thumbAddImmediate {
		t.Fatal("flags at a fault/budget exit were removed")
	}
	if instructions[2].appOp != thumbAddImmediate+thumbDeadFlagsOffset || instructions[3].appOp != thumbAddImmediate+thumbDeadFlagsOffset {
		t.Fatal("overwritten non-faulting flag writes were retained")
	}
}
