package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbIndexedLoadReturnAlreadyCanceledDoesNotExecuteRootR3(t *testing.T) {
	c := indexedLoadReturnDefaultEdgeRootR2()
	precise, jit, block := newIndexedLoadReturnEdgePairRootR2(t, c, flagN|flagV)
	// The block is already classified and both private reads are warm. Poison
	// only its host dispatch tags, leaving the guest code and fused metadata
	// intact, so an accidentally entered fused or ordinary path is observable.
	for index := range block.thumb {
		block.thumb[index].appOp = thumbInstructionClass(255)
	}
	for _, b := range []*Backend{precise, jit} {
		b.flags = pendingFlags{dirty: true, mask: flagC, bits: flagC}
	}
	regs, pending, mode := jit.regs, jit.flags, jit.mode
	cache, stats, generation := jit.privateDataCache, jit.ExecutionStatistics(), jit.jitGen
	memory := make([][]byte, len(jit.regions))
	for index := range jit.regions {
		memory[index] = append([]byte(nil), jit.regions[index].data...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := precise.Run(ctx, 0x1000, cpu.ModeThumb, 4)
	got := jit.Run(ctx, 0x1000, cpu.ModeThumb, 4)
	if got.Reason != cpu.StopRequested || got.Instructions != 0 || got.PC != 0x1000 || !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("already-canceled fused batch executed guest work: %+v", got)
	}
	if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
		t.Fatalf("canceled caller result got=%+v want=%+v", got, want)
	}
	// Inspect the lazy representation before SaveContext can resolve flags.
	if regs != jit.regs || pending != jit.flags || mode != jit.mode || generation != jit.jitGen ||
		!reflect.DeepEqual(cache, jit.privateDataCache) || !reflect.DeepEqual(stats, jit.ExecutionStatistics()) {
		t.Fatal("already-canceled batch changed raw state, private cache, generation or execution counters")
	}
	for index := range jit.regions {
		if !bytes.Equal(memory[index], jit.regions[index].data) {
			t.Fatalf("already-canceled batch changed memory region %d", index)
		}
	}
	assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
}

func TestThumbIndexedLoadReturnCodeRewriteInvalidatesMetadataRootR3(t *testing.T) {
	c := indexedLoadReturnDefaultEdgeRootR2()
	precise, jit, old := newIndexedLoadReturnEdgePairRootR2(t, c, flagC|flagV)
	generation := jit.jitGen
	// Assemble a synthetic LDRB r1,[r2,#0] independently. This changes the
	// observable r1 result from a full word to its low byte while retaining
	// the same PCs, register roles, total instruction count and POP return.
	var replacement [2]byte
	binary.LittleEndian.PutUint16(replacement[:], 0x7800|2<<3|1)
	for _, b := range []*Backend{precise, jit} {
		check(t, b.WriteMemory(0x1004, replacement[:]))
	}
	if jit.jitGen == generation {
		t.Fatal("write into the translated four-op span did not advance generation")
	}
	fresh := jit.jitBlockAt(0x1000)
	if fresh == nil || fresh == old || fresh.indexedLoadReturn != nil || len(fresh.thumb) != 4 || fresh.end != 0x1008 {
		t.Fatalf("rewritten byte load retained stale indexed metadata or changed span: %+v", fresh)
	}
	if fresh.thumb[2].op != thumbImmediateTransfer || fresh.thumb[2].appOp != thumbAppImmediateTransferBase+3 {
		t.Fatal("fresh translation did not decode the byte-load application opcode")
	}
	regs, pending, stats := jit.regs, jit.flags, jit.ExecutionStatistics()
	if jit.tryThumbApplicationIndexedLoadReturn(fresh) {
		t.Fatal("rewritten non-word-load block entered the indexed-four executor")
	}
	if regs != jit.regs || pending != jit.flags || !reflect.DeepEqual(stats, jit.ExecutionStatistics()) {
		t.Fatal("declined rewritten metadata changed raw state or counters")
	}
	want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
	got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
	if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
		t.Fatalf("rewritten caller result got=%+v want=%+v", got, want)
	}
	if got.Err != nil || got.Instructions != 4 || got.Reason != cpu.StopBudget || jit.regs[cpu.RegisterR1] != uint32(byte(c.loadValue)) {
		t.Fatalf("rewritten LDRB result is stale: result=%+v r1=%08x want=%02x", got, jit.regs[cpu.RegisterR1], byte(c.loadValue))
	}
	assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
}

func TestThumbIndexedLoadReturnStackRewriteUsesDynamicReturnRootR3(t *testing.T) {
	for returnBits := uint32(0); returnBits < 4; returnBits++ {
		t.Run(string(rune('0'+returnBits)), func(t *testing.T) {
			c := indexedLoadReturnDefaultEdgeRootR2()
			c.popMask, c.sp, c.returnTarget = 1, 0x7040, 0x1801
			precise, jit, block := newIndexedLoadReturnEdgePairRootR2(t, c, flagN|flagC)
			generation, metadata := jit.jitGen, block.indexedLoadReturn
			// POP {r0,pc} reads r0 at 0x7040 and the return at 0x7044.
			// Change only private stack data after translating the code.
			target := uint32(0x1880) | returnBits
			var replacement [4]byte
			binary.LittleEndian.PutUint32(replacement[:], target)
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x7044, replacement[:]))
			}
			if jit.jitGen != generation || jit.jitBlockAt(0x1000) != block || block.indexedLoadReturn != metadata {
				t.Fatal("non-executable stack-data write unnecessarily invalidated code metadata")
			}
			// The ordinary app switch cannot execute these poisoned host tags.
			// Successful execution therefore proves the real caller used the
			// warm fused path after the stack word changed, not a scalar fallback.
			for index := 1; index < len(block.thumb); index++ {
				block.thumb[index].appOp = thumbInstructionClass(255)
			}
			before := jit.ExecutionStatistics()
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
			if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
				t.Fatalf("dynamic return caller got=%+v want=%+v", got, want)
			}
			expectedPC, expectedMode := target&^uint32(3), cpu.ModeARM
			if target&1 != 0 {
				expectedPC, expectedMode = target&^uint32(1), cpu.ModeThumb
			}
			if got.Err != nil || got.Instructions != 4 || got.PC != expectedPC || jit.mode != expectedMode ||
				jit.regs[cpu.RegisterSP] != 0x7048 || jit.regs[cpu.RegisterR1] != c.loadValue {
				t.Fatalf("fused return cached old stack data or lost mode/writeback: result=%+v mode=%v sp=%08x r1=%08x", got, jit.mode, jit.regs[cpu.RegisterSP], jit.regs[cpu.RegisterR1])
			}
			after := jit.ExecutionStatistics()
			if after.TranslatedBlocks != before.TranslatedBlocks || after.TranslationInvalidations != before.TranslationInvalidations ||
				after.AcceleratedLoopIterations != before.AcceleratedLoopIterations || after.AcceleratedLoopInstructions != before.AcceleratedLoopInstructions {
				t.Fatal("dynamic return changed translation or virtual-loop counters")
			}
			if collectThumbMicro {
				expected := &cpu.ThumbMicroStatistics{Blocks: map[uint32]uint64{0x1000: 1}, BlockCalls: 1}
				for _, class := range []thumbInstructionClass{thumbShiftImmediate, thumbAddSubtract, thumbImmediateTransfer, thumbPop} {
					expected.Classes[class]++
				}
				if !reflect.DeepEqual(after.ThumbMicro, expected) {
					t.Fatalf("dynamic return must record one block/four original classes: got=%+v want=%+v", after.ThumbMicro, expected)
				}
			}
			assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
		})
	}
}
