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
