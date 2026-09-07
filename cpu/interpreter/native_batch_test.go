//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestNativeBatchBoundaryMatchesInterpreter runs a straight-line block longer
// than the space left at the end of a 256-instruction batch, across budgets
// that end before, inside, and well after that block. The native runner must
// retire exactly what the interpreter retires whether it hands the block to
// the next batch or interprets the run budget's true tail, and it must not
// stall when a batch begins with a block it cannot hold.
func TestNativeBatchBoundaryMatchesInterpreter(t *testing.T) {
	// 200 adds, then a 100-add block behind a branch so it starts a fresh
	// translated block that no 56-instruction batch remainder can hold,
	// then a counted loop back to the top and a breakpoint.
	var code []byte
	for i := 0; i < 200; i++ {
		code = append(code, 0x01, 0x30) // adds r0, #1
	}
	code = append(code, 0x00, 0xe0) // b +0 (ends the block)
	for i := 0; i < 100; i++ {
		code = append(code, 0x01, 0x31) // adds r1, #1
	}
	code = append(code, 0x01, 0x3a) // subs r2, #1
	// bne top: offset back over 302 halfwords + the pipeline offset.
	back := -(len(code)/2 + 2)
	code = append(code, byte(back&0xff), 0xd1)
	code = append(code, 0x00, 0xbe) // bkpt

	for _, budget := range []uint64{
		1, 55, 56, 57, 200, 201, 255, 256, 257, 300, 301, 302, 303,
		511, 512, 513, 600, 604, 605, 606, 900, 906, 1000, 3000,
	} {
		var results [2]cpu.Result
		var regs [2][17]uint32
		for i, make := range []func() *Backend{New, NewNativeJIT} {
			b := make()
			mustMap(t, b, 0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)
			check(t, b.WriteMemory(0x1000, code))
			check(t, b.WriteRegister(cpu.RegisterR2, 3))
			results[i] = b.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			for id := uint32(0); id < 17; id++ {
				regs[i][id] = register(t, b, id)
			}
			_ = b.Close()
		}
		if results[0].Instructions != results[1].Instructions ||
			results[0].Reason != results[1].Reason || results[0].PC != results[1].PC {
			t.Fatalf("budget %d: interpreter %+v, native %+v", budget, results[0], results[1])
		}
		if regs[0] != regs[1] {
			t.Fatalf("budget %d: registers diverged: %v vs %v", budget, regs[0], regs[1])
		}
	}
}
