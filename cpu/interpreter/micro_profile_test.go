package interpreter

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbMicroProfileIsDetachedAndNotGuestState(t *testing.T) {
	b := NewJIT()
	defer b.Close()
	mapCodeAndStack(t, b)
	check(t, b.WriteMemory(0x1000, thumbTestCode(
		0x2001, // MOVS r0,#1
		0x3001, // ADDS r0,#1
		0x2802, // CMP r0,#2
		0xd1fb, // BNE 0x1000 (not taken)
	)))
	result := b.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
	if result.Err != nil || result.Instructions != 4 || result.PC != 0x1008 ||
		immediateMemoryRegisterValue(t, b, cpu.RegisterR0) != 2 {
		t.Fatalf("diagnostic execution changed guest result: %+v", result)
	}
	before, err := b.SaveContext()
	check(t, err)
	stats := b.ExecutionStatistics().ThumbMicro
	if !collectThumbMicro {
		if stats != nil {
			t.Fatal("ordinary build collected instruction diagnostics")
		}
	} else {
		if stats == nil || stats.BlockCalls != 1 || stats.Blocks[0x1000] != 1 {
			t.Fatalf("missing translated block observation: %+v", stats)
		}
		for _, class := range []thumbInstructionClass{thumbMoveImmediate, thumbAddImmediate, thumbCompareImmediate, thumbConditionalBranch} {
			if stats.Classes[class] != 1 {
				t.Fatalf("class %d count = %d, want 1", class, stats.Classes[class])
			}
		}
		stats.Classes[thumbMoveImmediate] = 99
		stats.Blocks[0x1000] = 99
		fresh := b.ExecutionStatistics().ThumbMicro
		if fresh.Classes[thumbMoveImmediate] != 1 || fresh.Blocks[0x1000] != 1 {
			t.Fatal("caller mutation changed backend diagnostics")
		}
	}
	after, err := b.SaveContext()
	check(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("reading host diagnostics changed deterministic CPU state")
	}
}
