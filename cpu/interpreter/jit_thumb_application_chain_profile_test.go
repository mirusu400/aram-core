//go:build aram_profile_ops

package interpreter

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Diagnostic expectations come from this literal control-flow model, not the
// translator, opcode classifier, generated executor or successor cache.
func thumbApplicationChainLiteralStatistics(budget uint64) *cpu.ThumbMicroStatistics {
	want := &cpu.ThumbMicroStatistics{Blocks: make(map[uint32]uint64)}
	pc, r0 := uint32(0x1000), uint32(0)
	for retired := uint64(0); retired < budget; retired++ {
		if pc == 0x1000 || pc == 0x1006 || pc == 0x100e || retired%runBatchInstructions == 0 {
			want.BlockCalls++
			want.Blocks[pc]++
		}
		var class thumbInstructionClass
		switch pc {
		case 0x1000:
			class, r0, pc = thumbAddImmediate, r0+1, 0x1002
		case 0x1002:
			class, pc = thumbCompareImmediate, 0x1004
		case 0x1004:
			class, pc = thumbConditionalBranch, 0x1006
			if r0 == 2 {
				pc = 0x100e
			}
		case 0x1006:
			class, pc = thumbAddImmediate, 0x1008
		case 0x1008, 0x1012:
			class, pc = thumbUnconditionalBranch, 0x1000
		case 0x100e:
			class, r0, pc = thumbMoveImmediate, 0, 0x1010
		case 0x1010:
			class, pc = thumbSubtractImmediate, 0x1012
		default:
			panic("literal chain model reached an unexpected PC")
		}
		want.Classes[class]++
	}
	return want
}

func TestThumbApplicationChainProfileCountsEveryBudget(t *testing.T) {
	for _, budget := range thumbApplicationChainBudgets() {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise, jit := newThumbSMCOraclePair(t, false, false)
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x1000, thumbTestCode(thumbApplicationChainCycle...)))
				resetThumbApplicationChainOracle(t, b, false)
			}
			assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
			got, want := jit.ExecutionStatistics().ThumbMicro, thumbApplicationChainLiteralStatistics(budget)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("statistics=%+v want=%+v", got, want)
			}
		})
	}
}

func TestThumbApplicationChainProfileFaultAndSelfModifyBoundaries(t *testing.T) {
	for _, smc := range []bool{false, true} {
		t.Run(fmt.Sprintf("smc=%t", smc), func(t *testing.T) {
			precise, jit := newThumbSMCOraclePair(t, false, false)
			want := &cpu.ThumbMicroStatistics{Blocks: make(map[uint32]uint64)}
			for _, b := range []*Backend{precise, jit} {
				resetThumbApplicationChainOracle(t, b, false)
				if smc {
					// The first block exits after STRH; a fresh block starts at
					// the rewritten MOV, then follows BX to a third BKPT block.
					check(t, b.WriteMemory(0x1000, thumbTestCode(0x8008, 0x2301, 0x4730, 0xbe00)))
					check(t, b.WriteRegister(cpu.RegisterR0, 0x2302))
					check(t, b.WriteRegister(cpu.RegisterR1, 0x1002))
					check(t, b.WriteRegister(cpu.RegisterR6, 0x1007))
				} else {
					check(t, b.WriteMemory(0x1000, thumbTestCode(0x2705, 0x4730)))
					check(t, b.WriteMemory(0x2000, thumbTestCode(0x6808, 0xbe00)))
					check(t, b.WriteRegister(cpu.RegisterR1, 0x4000))
					check(t, b.WriteRegister(cpu.RegisterR6, 0x2001))
				}
			}
			if smc {
				want.BlockCalls = 3
				want.Blocks[0x1000], want.Blocks[0x1002], want.Blocks[0x1006] = 1, 1, 1
				want.Classes[thumbHalfwordTransfer], want.Classes[thumbMoveImmediate] = 1, 1
				want.Classes[thumbHighRegister], want.Classes[thumbBreakpoint] = 1, 1
			} else {
				want.BlockCalls = 2
				want.Blocks[0x1000], want.Blocks[0x2000] = 1, 1
				want.Classes[thumbMoveImmediate], want.Classes[thumbHighRegister] = 1, 1
				want.Classes[thumbImmediateTransfer] = 1 // Faulting attempt is observed, not retired.
			}
			assertThumbSMCOracleParity(t, precise, jit, 0x1000, 257)
			if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, want) {
				t.Fatalf("statistics=%+v want=%+v", got, want)
			}
			// Reading the detached counters cannot itself record another block.
			if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, want) {
				t.Fatalf("reading statistics changed counters: %+v", got)
			}
		})
	}
}
