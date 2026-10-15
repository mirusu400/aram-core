package interpreter

import (
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Literal machine code is executed by New(), which never uses translated
// blocks, predecoded opcodes, successor links or the generated runner.
var thumbApplicationChainCycle = []uint16{
	0x3001,         // 1000 ADDS r0,#1
	0x2802,         // 1002 CMP r0,#2
	0xd003,         // 1004 BEQ 100e
	0x3401,         // 1006 ADDS r4,#1
	0xe7fa,         // 1008 B 1000
	0x46c0, 0x46c0, // Unexecuted padding.
	0x2000, // 100e MOVS r0,#0
	0x3d01, // 1010 SUBS r5,#1
	0xe7f5, // 1012 B 1000
}

func thumbApplicationChainBudgets() []uint64 {
	budgets := make([]uint64, 64, 73)
	for i := range budgets {
		budgets[i] = uint64(i + 1)
	}
	return append(budgets, 254, 255, 256, 257, 258, 511, 512, 513, 1024)
}

func resetThumbApplicationChainOracle(t *testing.T, b *Backend, traced bool) {
	t.Helper()
	for r := uint32(0); r < cpu.RegisterPC; r++ {
		check(t, b.WriteRegister(r, 0xa5000000+r))
	}
	check(t, b.WriteRegister(cpu.RegisterR0, 0))
	check(t, b.WriteRegister(cpu.RegisterR4, 0x7fffffff))
	check(t, b.WriteRegister(cpu.RegisterR5, 0xffffffff))
	check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
	b.pcHits = nil
	check(t, b.SetPCHistoryLimit(0))
	if traced {
		b.pcHits = make(map[uint32]uint64)
		check(t, b.SetPCHistoryLimit(32))
	}
}

func assertThumbApplicationChainTwoEdges(t *testing.T, b *Backend) {
	t.Helper()
	from := b.jitBlocks[0x1000]
	if from == nil || from.countedLoop != nil || from.extraLoop != nil || from.successorGen != b.jitGen {
		t.Fatalf("ordinary two-edge entry not warmed: %+v", from)
	}
	edges := map[uint32]bool{}
	for _, next := range from.successors {
		if next != nil {
			edges[next.start] = true
		}
	}
	if !edges[0x1006] || !edges[0x100e] {
		t.Fatalf("ordinary branch did not retain both successors: %v", edges)
	}
}

func TestThumbApplicationChainEveryBudgetMatchesPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		for _, warm := range []bool{false, true} {
			for _, budget := range thumbApplicationChainBudgets() {
				t.Run(fmt.Sprintf("trace=%t/warm=%t/budget=%d", traced, warm, budget), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(thumbApplicationChainCycle...)))
						resetThumbApplicationChainOracle(t, b, traced)
					}
					if warm {
						assertThumbSMCOracleParity(t, precise, jit, 0x1000, 44)
						if !traced {
							assertThumbApplicationChainTwoEdges(t, jit)
						}
						for _, b := range []*Backend{precise, jit} {
							resetThumbApplicationChainOracle(t, b, traced)
						}
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if !traced && budget >= 22 {
						assertThumbApplicationChainTwoEdges(t, jit)
					}
				})
			}
		}
	}
}

func TestThumbApplicationChainHostRewriteInvalidatesTwoEdges(t *testing.T) {
	for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 11, 22, 255, 256, 257, 513} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise, jit := newThumbSMCOraclePair(t, false, false)
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x1000, thumbTestCode(thumbApplicationChainCycle...)))
				resetThumbApplicationChainOracle(t, b, false)
			}
			assertThumbSMCOracleParity(t, precise, jit, 0x1000, 44)
			assertThumbApplicationChainTwoEdges(t, jit)
			stale := jit.jitBlocks[0x1006]
			generation := jit.jitGen
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x1006, thumbTestCode(0x3403))) // ADD #1 -> #3.
				resetThumbApplicationChainOracle(t, b, false)
			}
			if jit.jitGen == generation {
				t.Fatal("host rewrite did not change the code generation")
			}
			assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
			if budget >= 22 {
				assertThumbApplicationChainTwoEdges(t, jit)
				for _, next := range jit.jitBlocks[0x1000].successors {
					if next == stale {
						t.Fatal("rewritten target retained a stale successor")
					}
				}
			}
		})
	}
}

// A cold successor extends jitCodeHi without changing jitGen. A writer inside
// that newly translated span must still invalidate its own remaining body.
func TestThumbApplicationChainColdSuccessorSelfModifyMatchesPrecise(t *testing.T) {
	for _, fixture := range []struct {
		name        string
		code        []uint16
		address     uint32
		replacement uint32
	}{
		{"ordinary", []uint16{0x8008, 0x4642, 0x2301, 0x2f00, 0xbe00}, 0x5004, 0x2302},
		{"bl-prefix", []uint16{0x8008, 0xf000, 0xf800, 0xbe00}, 0x5002, 0xf001},
	} {
		for _, traced := range []bool{false, true} {
			for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 7, 255, 256, 257} {
				t.Run(fmt.Sprintf("%s/trace=%t/budget=%d", fixture.name, traced, budget), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.Map(0x5000, 0x2000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
						check(t, b.WriteMemory(0x1000, thumbTestCode(0x4730))) // BX r6.
						check(t, b.WriteMemory(0x5000, thumbTestCode(fixture.code...)))
						check(t, b.WriteMemory(0x6006, thumbTestCode(0xbe00)))
						resetThumbApplicationChainOracle(t, b, traced)
						check(t, b.WriteRegister(cpu.RegisterR0, fixture.replacement))
						check(t, b.WriteRegister(cpu.RegisterR1, fixture.address))
						check(t, b.WriteRegister(cpu.RegisterR6, 0x5001))
						// Populate the private write cache before the successor is code.
						check(t, b.write16(fixture.address, fixture.code[(fixture.address-0x5000)/2], cpu.PermissionWrite))
					}
					if jit.jitBlockAt(0x1000) == nil || jit.jitBlocks[0x5000] != nil || jit.jitCodeHi >= 0x5000 {
						t.Fatal("cold-successor precondition was not established")
					}
					generation := jit.jitGen
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if budget >= 2 && jit.jitGen == generation {
						t.Fatal("newly translated successor writer did not change generation")
					}
				})
			}
		}
	}
}

func TestThumbApplicationChainFaultPrefixMatchesPrecise(t *testing.T) {
	for _, fixture := range []struct {
		name string
		code []uint16
		pc   uint32
		base uint32
	}{
		{"load", []uint16{0x3401, 0x4642, 0x6808, 0xbe00}, 0x2000, 0x4000},
		{"store", []uint16{0x3401, 0x4642, 0x6008, 0xbe00}, 0x2000, 0x4000},
		{"partial-stm", []uint16{0x3401, 0x4642, 0xc103, 0xbe00}, 0x2000, 0x2ffc},
		{"bl-suffix", []uint16{0x3401, 0xf000}, 0x2ffc, 0x4000},
	} {
		for _, traced := range []bool{false, true} {
			for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 7, 8, 255, 256, 257} {
				t.Run(fmt.Sprintf("%s/trace=%t/budget=%d", fixture.name, traced, budget), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(0x2705, 0x4730)))
						check(t, b.WriteMemory(fixture.pc, thumbTestCode(fixture.code...)))
						resetThumbApplicationChainOracle(t, b, traced)
						check(t, b.WriteRegister(cpu.RegisterR0, 0xfeedbeef))
						check(t, b.WriteRegister(cpu.RegisterR1, fixture.base))
						check(t, b.WriteRegister(cpu.RegisterR6, fixture.pc|1))
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				})
			}
		}
	}
}

func TestThumbApplicationChainModeAndStopMatchesPrecise(t *testing.T) {
	for _, modeSwitch := range []bool{false, true} {
		for _, traced := range []bool{false, true} {
			for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 7, 255, 256, 257} {
				t.Run(fmt.Sprintf("arm=%t/trace=%t/budget=%d", modeSwitch, traced, budget), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(0x2705, 0x4730)))
						end := uint16(0xbe00)
						if modeSwitch {
							end = 0x4738 // BX r7 switches into ARM.
						}
						check(t, b.WriteMemory(0x2000, thumbTestCode(0x3401, end)))
						check(t, b.WriteMemory(0x2400, armTestCode(0xe3a03007, 0xe1200070)))
						resetThumbApplicationChainOracle(t, b, traced)
						check(t, b.WriteRegister(cpu.RegisterR6, 0x2001))
						check(t, b.WriteRegister(cpu.RegisterR7, 0x2400))
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				})
			}
		}
	}
}

func TestThumbApplicationChainCanceledBatchDoesNotExecute(t *testing.T) {
	precise, jit := newThumbSMCOraclePair(t, false, false)
	for _, b := range []*Backend{precise, jit} {
		check(t, b.WriteMemory(0x1000, thumbTestCode(thumbApplicationChainCycle...)))
		resetThumbApplicationChainOracle(t, b, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := precise.Run(ctx, 0x1000, cpu.ModeThumb, 257)
	got := jit.Run(ctx, 0x1000, cpu.ModeThumb, 257)
	if got.Reason != want.Reason || got.Instructions != 0 || got.PC != want.PC || !sameError(got.Err, want.Err) {
		t.Fatalf("canceled result=%+v want=%+v", got, want)
	}
}
