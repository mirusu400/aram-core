//go:build aram_profile_ops

package interpreter

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbCompareBranchTailProfileCountsTwoInstructions(t *testing.T) {
	for _, registerCompare := range []bool{false, true} {
		for _, budget := range thumbApplicationChainBudgets() {
			t.Run(fmt.Sprintf("register=%t/budget%d", registerCompare, budget), func(t *testing.T) {
				precise, jit := newThumbSMCOraclePair(t, false, false)
				words := slices.Clone(thumbApplicationChainCycle)
				if registerCompare {
					words[1] = 0x4288
				}
				for _, b := range []*Backend{precise, jit} {
					check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
					resetThumbApplicationChainOracle(t, b, false)
					check(t, b.WriteRegister(1, 2))
				}
				assertThumbCompareBranchTail(t, jit, 0x1000)
				assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				want := thumbApplicationChainLiteralStatistics(budget)
				if registerCompare {
					want.Classes[thumbALU] += want.Classes[thumbCompareImmediate]
					want.ALU[10] += want.Classes[thumbCompareImmediate]
					want.Classes[thumbCompareImmediate] = 0
				}
				if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, want) {
					t.Fatalf("fused tail statistics=%+v want literal=%+v", got, want)
				}
			})
		}
	}
}

func TestThumbCompareBranchTailProfileSkipsFaultAndRewrittenTail(t *testing.T) {
	for _, smc := range []bool{false, true} {
		t.Run(fmt.Sprint(smc), func(t *testing.T) {
			precise, jit := newThumbSMCOraclePair(t, false, false)
			want := &cpu.ThumbMicroStatistics{Blocks: map[uint32]uint64{0x1000: 1}, BlockCalls: 1}
			for _, b := range []*Backend{precise, jit} {
				if smc {
					thumbCompareBranchOracleReset(t, b, []uint16{0x3401, 0x8008, 0x2a00, 0xd102, 0x2301, 0xbe00, 0x46c0, 0x2302, 0xbe00}, false)
					check(t, b.WriteRegister(0, 0x2a01))
					check(t, b.WriteRegister(1, 0x1004))
					check(t, b.WriteRegister(2, 0))
				} else {
					thumbCompareBranchOracleReset(t, b, []uint16{0x3401, 0x6808, 0x2f00, 0xd100, 0xbe00}, false)
					check(t, b.WriteRegister(1, 0x4000))
				}
			}
			want.Classes[thumbAddImmediate] = 1
			if smc {
				want.BlockCalls = 3
				want.Blocks[0x1004], want.Blocks[0x100e] = 1, 1
				want.Classes[thumbHalfwordTransfer], want.Classes[thumbCompareImmediate], want.Classes[thumbConditionalBranch] = 1, 1, 1
				want.Classes[thumbMoveImmediate], want.Classes[thumbBreakpoint] = 1, 1
			} else {
				want.Classes[thumbImmediateTransfer] = 1 // Fault attempt, not retirement.
			}
			assertThumbCompareBranchTail(t, jit, 0x1000)
			assertThumbSMCOracleParity(t, precise, jit, 0x1000, 257)
			if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, want) {
				t.Fatalf("fault/SMC tail statistics=%+v want literal=%+v", got, want)
			}
		})
	}
}
