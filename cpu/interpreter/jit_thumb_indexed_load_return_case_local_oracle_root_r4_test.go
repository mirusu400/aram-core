package interpreter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// These are public Run fallbacks, not direct-helper tests. The first operation
// has already been recorded at the case-local attempt; a decline must not
// duplicate it, and the faulting instruction is recorded before it can retire.
func TestThumbIndexedLoadReturnCaseLocalFallbackCountersRootR4(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		coldData, coldStack, loadFault, popFault bool
		retired                                  uint64
		pc, sp, temporary, loaded, popped, nzcv  uint32
		attempted                                int
	}{
		{"cold-data", true, false, false, false, 4, 0x1802, 0x7048, 0x2000, 0x89abcdef, 0x76540440, 0, 4},
		{"cold-stack", false, true, false, false, 4, 0x1802, 0x7048, 0x2000, 0x89abcdef, 0x76540440, 0, 4},
		{"load-fault-before-stack", false, false, true, false, 2, 0x1006, 0x7000, 0x4000, 0x55550101, 0x55550000, flagC | flagV, 3},
		{"pop-first-word-before-pc-fault", false, false, false, true, 3, 0x1008, 0x7000, 0x4000, 0x89abcdef, 0x76540000, flagC | flagV, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := indexedLoadReturnDefaultEdgeRootR2()
			if tc.loadFault || tc.popFault {
				c.index, c.base, c.sum, c.loadAddress = 0x80000000, 0x80004000, 0x4000, 0x4000
				c.sp, c.stackBase = 0x7000, 0x7000
			}
			if tc.loadFault {
				c.mapData, c.stackSize = false, 0
			}
			if tc.popFault {
				c.stackSize = 4
			}
			precise, jit, block := newIndexedLoadReturnEdgePairRootR2(t, c, flagN|flagV)
			if block.thumb[0].appOp != thumbShiftImmediate+thumbDeadFlagsOffset {
				t.Fatal("case-local fixture must start at the original dead-LSLS application tag")
			}
			if tc.coldData {
				slot := privateDataCacheIndex(c.loadAddress, cpu.PermissionRead)
				jit.privateDataCache[slot] = dataRegionCache{}
				if _, hit := cachedJITWord(&jit.privateDataCache[slot], c.loadAddress); hit {
					t.Fatal("cold-data precondition is vacuous")
				}
			}
			if tc.coldStack {
				slot := privateDataCacheIndex(c.sp, cpu.PermissionRead)
				jit.privateDataCache[slot] = dataRegionCache{}
				if _, hit := jit.privateThumbStackSpan(c.sp, 2, cpu.PermissionRead); hit {
					t.Fatal("cold-stack precondition is vacuous")
				}
			}
			for _, b := range []*Backend{precise, jit} {
				b.flags = pendingFlags{dirty: true, mask: flagC, bits: flagC}
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
			if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
				t.Fatalf("case-local public fallback got=%+v want=%+v", got, want)
			}
			reason := cpu.StopBudget
			if tc.loadFault || tc.popFault {
				reason = cpu.StopFault
				if !errors.Is(got.Err, cpu.ErrInvalidAddress) {
					t.Fatalf("literal fault error differs: %+v", got)
				}
			} else if got.Err != nil {
				t.Fatalf("cold mapped fallback must succeed: %+v", got)
			}
			if got.Reason != reason || got.Instructions != tc.retired || got.PC != tc.pc ||
				jit.mode != cpu.ModeThumb || jit.regs[cpu.RegisterSP] != tc.sp ||
				jit.regs[cpu.RegisterR2] != tc.temporary || jit.regs[cpu.RegisterR1] != tc.loaded ||
				jit.regs[cpu.RegisterR0] != tc.popped {
				t.Fatalf("literal retirement/PC/partial-register/writeback state differs: result=%+v SP=%08x r2=%08x r1=%08x r0=%08x",
					got, jit.regs[cpu.RegisterSP], jit.regs[cpu.RegisterR2], jit.regs[cpu.RegisterR1], jit.regs[cpu.RegisterR0])
			}
			// Inspect counters before any public flag/context observer. Every
			// attempted class, including a faulting load/POP, appears once.
			if collectThumbMicro {
				expected := &cpu.ThumbMicroStatistics{Blocks: map[uint32]uint64{0x1000: 1}, BlockCalls: 1}
				classes := [...]thumbInstructionClass{thumbShiftImmediate, thumbAddSubtract, thumbImmediateTransfer, thumbPop}
				for _, class := range classes[:tc.attempted] {
					expected.Classes[class]++
				}
				if actual := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(actual, expected) {
					t.Fatalf("literal first/fallback/fault micro counts got=%+v want=%+v", actual, expected)
				}
			}
			cpsr, err := jit.ReadRegister(cpu.RegisterCPSR)
			check(t, err)
			if cpsr&(flagN|flagZ|flagC|flagV) != tc.nzcv {
				t.Fatalf("literal post-ADD NZCV got=%08x want=%08x", cpsr&(flagN|flagZ|flagC|flagV), tc.nzcv)
			}
			assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
		})
	}
}
