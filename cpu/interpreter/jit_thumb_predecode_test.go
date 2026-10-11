package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Use the precise decoder as an independent oracle for every encoding whose
// operands the Thumb micro executor can predecode. Aliased registers, shifts
// beyond a word and arithmetic boundaries must retain both results and flags.
func TestThumbMicroPredecodeMatchesPrecise(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		mapCodeAndStack(t, b)
		t.Cleanup(func() { _ = b.Close() })
	}
	values := []uint32{0, 1, 31, 32, 255, 0x7fffffff, 0x80000000, 0xffffffff}
	ctx := context.Background()
	for raw := uint32(0); raw < 0x4800; raw++ {
		for trial := 0; trial < 4; trial++ {
			var results [2]cpu.Result
			for index, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x1000, thumbTestCode(uint16(raw))))
				for r := uint32(0); r < cpu.RegisterPC; r++ {
					check(t, b.WriteRegister(r, values[(int(r)+trial*2)%len(values)]))
				}
				flags := uint32(processorModeSystem)
				if trial&1 != 0 {
					flags |= flagN | flagC | flagV
				}
				if trial&2 != 0 {
					flags |= flagZ
				}
				check(t, b.WriteRegister(cpu.RegisterCPSR, flags))
				results[index] = b.Run(ctx, 0x1000, cpu.ModeThumb, 1)
			}
			if results[0].Err != nil || results[1].Err != nil ||
				results[0].Reason != results[1].Reason ||
				results[0].Instructions != results[1].Instructions {
				t.Fatalf("opcode %#04x trial %d: precise=%+v jit=%+v", raw, trial, results[0], results[1])
			}
			for r := uint32(0); r <= cpu.RegisterCPSR; r++ {
				want := immediateMemoryRegisterValue(t, precise, r)
				if got := immediateMemoryRegisterValue(t, jit, r); got != want {
					t.Fatalf("opcode %#04x trial %d register %d: got %#x want %#x", raw, trial, r, got, want)
				}
			}
		}
	}
}
