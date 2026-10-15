package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func thumbCompareBranchOracleReset(t *testing.T, b *Backend, words []uint16, traced bool) {
	t.Helper()
	check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
	resetThumbApplicationChainOracle(t, b, traced)
	for r := uint32(0); r < 8; r++ {
		check(t, b.WriteRegister(r, 0x80000000+r*0x11111111))
	}
}

func assertThumbCompareBranchTail(t *testing.T, b *Backend, pc uint32) *jitBlock {
	t.Helper()
	block := b.jitBlockAt(pc)
	if block == nil || block.compareBranchTail == nil || block.countedLoop != nil || block.extraLoop != nil {
		t.Fatalf("not an ordinary compare/branch full block: %+v", block)
	}
	return block
}

func TestThumbCompareBranchTailClassifiesOnlyExactEncodings(t *testing.T) {
	for raw := uint16(0x4000); raw < 0x4400; raw++ {
		block := &jitBlock{start: 0x1000, end: 0x1004, thumb: []thumbMicroInstr{
			{pc: 0x1000, raw: raw, op: thumbALU, appOp: thumbALU},
			{pc: 0x1002, raw: 0xd001, op: thumbConditionalBranch, appOp: thumbConditionalBranch},
		}}
		got := classifyThumbCompareBranchTail(block)
		if (got != nil) != (raw&0xffc0 == 0x4280) {
			t.Fatalf("ALU encoding %04x classification=%+v", raw, got)
		}
		if got != nil && (!got.register || got.left != uint8(raw)&7 || got.right != uint32(raw>>3)&7 || got.target != 0x1008) {
			t.Fatalf("ALU encoding %04x operands=%+v", raw, got)
		}
		if len(block.thumb) != 2 || block.end != 0x1004 || block.thumb[0].raw != raw {
			t.Fatal("tail classification changed the original slice/span")
		}
	}
	for raw := uint16(0x2800); raw < 0x3000; raw++ {
		block := &jitBlock{start: 0x1000, end: 0x1004, thumb: []thumbMicroInstr{
			{pc: 0x1000, raw: raw, op: thumbCompareImmediate, appOp: thumbCompareImmediate},
			{pc: 0x1002, raw: 0xd001, op: thumbConditionalBranch, appOp: thumbConditionalBranch},
		}}
		got := classifyThumbCompareBranchTail(block)
		if got == nil || got.register || got.left != uint8(raw>>8)&7 || got.right != uint32(raw&255) {
			t.Fatalf("CMP immediate %04x operands=%+v", raw, got)
		}
	}
	for _, mutate := range []func(*jitBlock){
		func(b *jitBlock) { b.thumb = b.thumb[:1] },
		func(b *jitBlock) { b.thumb[0].op = thumbALU },
		func(b *jitBlock) { b.thumb[0].appOp += thumbDeadFlagsOffset },
		func(b *jitBlock) {
			b.thumb[0].raw = 0x4580
			b.thumb[0].op, b.thumb[0].appOp = thumbHighRegister, thumbHighRegister
		},
		func(b *jitBlock) { b.thumb[0].pc++ },
		func(b *jitBlock) { b.thumb[1].pc++ },
		func(b *jitBlock) { b.thumb[1].op = thumbUnsupported },
		func(b *jitBlock) { b.thumb[1].appOp = thumbUnsupported },
		func(b *jitBlock) { b.thumb[1].raw = 0xde00 },
		func(b *jitBlock) { b.thumb[1].raw = 0xdf00 },
		func(b *jitBlock) { b.thumb[1].raw = 0xe000 },
		func(b *jitBlock) { b.end++ },
		func(b *jitBlock) { b.thumb[0].pc, b.thumb[1].pc, b.end = 0xfffffffc, 0xfffffffe, 0 },
	} {
		block := &jitBlock{start: 0x1000, end: 0x1004, thumb: []thumbMicroInstr{
			{pc: 0x1000, raw: 0x2800, op: thumbCompareImmediate, appOp: thumbCompareImmediate},
			{pc: 0x1002, raw: 0xd001, op: thumbConditionalBranch, appOp: thumbConditionalBranch},
		}}
		mutate(block)
		if got := classifyThumbCompareBranchTail(block); got != nil {
			t.Fatalf("accepted an invalid internal tail tuple: %+v", block)
		}
	}
}

func TestThumbCompareBranchTailAllCompareEncodingsMatchPrecise(t *testing.T) {
	precise, jit := newThumbSMCOraclePair(t, false, false)
	for _, base := range []uint16{0x2800, 0x4280} {
		count := uint16(0x800)
		if base == 0x4280 {
			count = 64
		}
		for encoding := uint16(0); encoding < count; encoding++ {
			raw := base | encoding
			for budget := uint64(1); budget <= 3; budget++ {
				for _, b := range []*Backend{precise, jit} {
					thumbCompareBranchOracleReset(t, b, []uint16{raw, 0xd001, 0xbe00, 0x46c0, 0xbe00}, false)
				}
				assertThumbCompareBranchTail(t, jit, 0x1000)
				assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
			}
		}
	}
}

// Inspect raw CPSR/pending storage before any public API can resolve flags and
// hide a fused-path omission. New() independently executes the literal pair.
func TestThumbCompareBranchTailMaterializesRawFlags(t *testing.T) {
	for _, raw := range []uint16{0x2800, 0x28ff, 0x4288, 0x4280} {
		for condition := uint16(0); condition < 14; condition++ {
			for _, operands := range [][2]uint32{{0, 0}, {1, 0}, {0, 1}, {0x7fffffff, 0xffffffff}, {0x80000000, 1}, {0xffffffff, 0x7fffffff}} {
				t.Run(fmt.Sprintf("%04x/cond%d/%x-%x", raw, condition, operands[0], operands[1]), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, false)
					for _, b := range []*Backend{precise, jit} {
						thumbCompareBranchOracleReset(t, b, []uint16{raw, 0xd001 | condition<<8}, false)
						check(t, b.WriteRegister(0, operands[0]))
						check(t, b.WriteRegister(1, operands[1]))
						b.setNZCV(0, true, true) // A pending predecessor must be overwritten.
					}
					want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 2)
					block := assertThumbCompareBranchTail(t, jit, 0x1000)
					jit.mode, jit.regs[cpu.RegisterPC] = cpu.ModeThumb, 0x1000
					jit.setModeFlag()
					n, reason, err := jit.executeThumbApplicationMicroBlocks(block, 2)
					if n != 2 || reason != nil || err != nil || want.Instructions != 2 || want.Err != nil {
						t.Fatalf("full tail result n=%d reason=%v err=%v precise=%+v", n, reason, err, want)
					}
					if jit.flags != (pendingFlags{}) || jit.regs != precise.regs {
						t.Fatalf("unmaterialized or different raw state flags=%+v CPSR=%08x want=%08x", jit.flags, jit.regs[cpu.RegisterCPSR], precise.regs[cpu.RegisterCPSR])
					}
				})
			}
		}
	}
}

func TestThumbCompareBranchTailEveryBudgetAndResumeMatchesPrecise(t *testing.T) {
	for _, registerCompare := range []bool{false, true} {
		for _, traced := range []bool{false, true} {
			for _, warm := range []bool{false, true} {
				for _, budget := range thumbApplicationChainBudgets() {
					t.Run(fmt.Sprintf("register=%t/trace=%t/warm=%t/budget%d", registerCompare, traced, warm, budget), func(t *testing.T) {
						precise, jit := newThumbSMCOraclePair(t, false, traced)
						words := slices.Clone(thumbApplicationChainCycle)
						if registerCompare {
							words[1] = 0x4288
						}
						for _, b := range []*Backend{precise, jit} {
							check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
							resetThumbApplicationChainOracle(t, b, traced)
							check(t, b.WriteRegister(1, 2))
						}
						assertThumbCompareBranchTail(t, jit, 0x1000)
						if warm {
							assertThumbSMCOracleParity(t, precise, jit, 0x1000, 44)
							if !traced {
								assertThumbApplicationChainTwoEdges(t, jit)
							}
						}
						// Resume the same architectural state across a cut before CMP,
						// between CMP/Bcond, and on subsequent Backend.Run batches.
						for _, part := range []uint64{1, 1, 1, budget} {
							pc := precise.regs[cpu.RegisterPC]
							if !warm && part == 1 && pc == 0 {
								pc = 0x1000
							}
							assertThumbSMCOracleParity(t, precise, jit, pc, part)
						}
					})
				}
			}
		}
	}
}

func TestThumbCompareBranchTailPrefixFaultsMatchPrecise(t *testing.T) {
	for _, fixture := range []struct {
		name            string
		codeSize        uint32
		words           []uint16
		base, sp, value uint32
	}{
		{"load-fault", 10, []uint16{0x3401, 0x6808, 0x2f00, 0xd100, 0xbe00}, 0x4000, 0x2800, 0},
		{"store-fault", 10, []uint16{0x3401, 0x6008, 0x2f00, 0xd100, 0xbe00}, 0x4000, 0x2800, 0},
		{"partial-stm-smc-fault", 12, []uint16{0x3401, 0x3502, 0xc103, 0x2f00, 0xd100, 0xbe00}, 0x1008, 0x2800, 0xbe00d000},
		{"partial-push-smc-fault", 12, []uint16{0x3401, 0x3502, 0xb403, 0x2f00, 0xd100, 0xbe00}, 0x1008, 0x1010, 0xbe00d000},
	} {
		for _, traced := range []bool{false, true} {
			for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 255, 256, 257} {
				t.Run(fmt.Sprintf("%s/trace=%t/budget%d", fixture.name, traced, budget), func(t *testing.T) {
					precise, jit := newThumbPCOraclePair(t, fixture.codeSize, traced)
					for _, b := range []*Backend{precise, jit} {
						thumbCompareBranchOracleReset(t, b, fixture.words, traced)
						check(t, b.WriteRegister(0, fixture.value))
						check(t, b.WriteRegister(1, fixture.base))
						check(t, b.WriteRegister(cpu.RegisterSP, fixture.sp))
					}
					assertThumbCompareBranchTail(t, jit, 0x1000)
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				})
			}
		}
	}
}

func TestThumbCompareBranchTailSelfModifyMatchesPrecise(t *testing.T) {
	for _, rewrite := range []struct {
		address uint32
		raw     uint16
	}{{0x1004, 0x2a01}, {0x1006, 0xd002}, {0x1006, 0xd100}, {0x1006, 0xde00}, {0x1006, 0xbe00}} {
		for _, traced := range []bool{false, true} {
			for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 7, 255, 256, 257} {
				t.Run(fmt.Sprintf("%x=%04x/trace=%t/budget%d", rewrite.address, rewrite.raw, traced, budget), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, traced)
					for _, b := range []*Backend{precise, jit} {
						thumbCompareBranchOracleReset(t, b, []uint16{0x3401, 0x8008, 0x2a00, 0xd102, 0x2301, 0xbe00, 0x46c0, 0x2302, 0xbe00}, traced)
						check(t, b.WriteRegister(0, uint32(rewrite.raw)))
						check(t, b.WriteRegister(1, rewrite.address))
						check(t, b.WriteRegister(2, 0))
					}
					original := assertThumbCompareBranchTail(t, jit, 0x1000)
					if original.end != 0x1008 || len(original.thumb) != 4 {
						t.Fatal("tail lost original SMC span")
					}
					generation := jit.jitGen
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if budget >= 2 && jit.jitGen == generation {
						t.Fatal("tail rewrite did not invalidate its code generation")
					}
				})
			}
		}
	}
}

func TestThumbCompareBranchTailHostRewriteTwoEdgesMatchesPrecise(t *testing.T) {
	for _, rewrite := range []struct {
		address uint32
		raw     uint16
	}{{0x1002, 0x2803}, {0x1004, 0xd103}, {0x1004, 0xd005}} {
		for _, budget := range []uint64{1, 2, 3, 4, 11, 22, 255, 256, 257, 513} {
			t.Run(fmt.Sprintf("%x=%04x/budget%d", rewrite.address, rewrite.raw, budget), func(t *testing.T) {
				precise, jit := newThumbSMCOraclePair(t, false, false)
				for _, b := range []*Backend{precise, jit} {
					check(t, b.WriteMemory(0x1000, thumbTestCode(thumbApplicationChainCycle...)))
					resetThumbApplicationChainOracle(t, b, false)
				}
				assertThumbSMCOracleParity(t, precise, jit, 0x1000, 44)
				assertThumbApplicationChainTwoEdges(t, jit)
				stale := jit.jitBlocks[0x1000]
				generation := jit.jitGen
				for _, b := range []*Backend{precise, jit} {
					check(t, b.WriteMemory(rewrite.address, thumbTestCode(rewrite.raw)))
					resetThumbApplicationChainOracle(t, b, false)
				}
				if jit.jitGen == generation {
					t.Fatal("host tail rewrite left generation unchanged")
				}
				assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				if jit.jitBlocks[0x1000] == stale {
					t.Fatal("host tail rewrite retained old metadata")
				}
			})
		}
	}
}

func TestThumbCompareBranchTailCancellationMatchesPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		precise, jit := newThumbSMCOraclePair(t, false, traced)
		for _, b := range []*Backend{precise, jit} {
			check(t, b.WriteMemory(0x1000, thumbTestCode(thumbApplicationChainCycle...)))
			resetThumbApplicationChainOracle(t, b, traced)
		}
		want := precise.Run(&thumbPCSecondPollContext{}, 0x1000, cpu.ModeThumb, 1024)
		got := jit.Run(&thumbPCSecondPollContext{}, 0x1000, cpu.ModeThumb, 1024)
		if got.Instructions != 256 || got.Reason != want.Reason || got.PC != want.PC || !sameError(got.Err, want.Err) {
			t.Fatalf("batch cancellation got=%+v want=%+v", got, want)
		}
		wantState, err := precise.SaveContext()
		check(t, err)
		gotState, err := jit.SaveContext()
		check(t, err)
		if !bytes.Equal(gotState, wantState) || !slices.Equal(jit.PCHistory(), precise.PCHistory()) || !reflect.DeepEqual(jit.pcHits, precise.pcHits) {
			t.Fatal("cancellation changed state or per-instruction observations")
		}
	}
}

func TestThumbCompareBranchTailObserverAndPhysicalGuardsMatchPrecise(t *testing.T) {
	for _, observer := range []string{"history", "hits", "capture", "physical", "system-trap"} {
		for _, budget := range []uint64{1, 2, 3, 4, 255, 256, 257} {
			t.Run(fmt.Sprintf("%s/budget%d", observer, budget), func(t *testing.T) {
				precise, jit := newThumbSMCOraclePair(t, observer == "system-trap", false)
				for _, b := range []*Backend{precise, jit} {
					check(t, b.WriteMemory(0x1000, thumbTestCode(thumbApplicationChainCycle...)))
					resetThumbApplicationChainOracle(t, b, false)
					switch observer {
					case "history":
						check(t, b.SetPCHistoryLimit(32))
					case "hits":
						b.pcHits = make(map[uint32]uint64)
					case "capture":
						check(t, b.SetPCRegisterCapture(0x1004, 32))
					case "physical":
						b.physicalAccess = true
					case "system-trap":
						check(t, b.SetExecutionTraps([]cpu.ExecutionTrap{{Address: 0x1004, Mode: cpu.ModeThumb}}))
					}
				}
				assertThumbCompareBranchTail(t, jit, 0x1000)
				assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				if !reflect.DeepEqual(jit.PCRegisterCaptures(), precise.PCRegisterCaptures()) {
					t.Fatal("tail changed a per-instruction raw register capture")
				}
			})
		}
	}
}

func TestThumbCompareBranchTailPCReadingPrefixMatchesPrecise(t *testing.T) {
	for _, start := range []uint32{0x1000, 0x1002} {
		for _, traced := range []bool{false, true} {
			for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 7, 255, 256, 257} {
				t.Run(fmt.Sprintf("start%x/trace=%t/budget%d", start, traced, budget), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, traced)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(start, thumbTestCode(0x467a, 0xa300, 0x429a, 0xd001, 0xbe00, 0x46c0, 0xbe00)))
						resetThumbApplicationChainOracle(t, b, traced)
					}
					assertThumbCompareBranchTail(t, jit, start)
					assertThumbSMCOracleParity(t, precise, jit, start, budget)
				})
			}
		}
	}
}

func TestThumbCompareBranchTailEarlierControlFlowMatchesPrecise(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		words []uint16
		r6    uint32
		arm   bool
	}{
		{"bkpt", []uint16{0xbe00, 0x2800, 0xd001}, 0x2001, false},
		{"unsupported", []uint16{0xde00, 0x2800, 0xd001}, 0x2001, false},
		{"mov-pc", []uint16{0x46b7, 0x2800, 0xd001}, 0x2001, false},
		{"add-pc", []uint16{0x44b7, 0x2800, 0xd001}, 0x0ffc, false},
		{"bx-arm", []uint16{0x4730, 0x2800, 0xd001}, 0x2000, true},
		{"blx-lr-arm", []uint16{0x47f0, 0x2800, 0xd001}, 0x2000, true},
		{"pop-pc-arm", []uint16{0xbd00, 0x2800, 0xd001}, 0x2000, true},
		{"bl-then-tail", []uint16{0xf000, 0xf800, 0x2800, 0xd001, 0xbe00, 0x46c0, 0xbe00}, 0x2001, false},
		{"invalid-bl-suffix", []uint16{0xf000, 0x2000, 0x2800, 0xd001}, 0x2001, false},
	} {
		for _, traced := range []bool{false, true} {
			for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 255, 256, 257} {
				t.Run(fmt.Sprintf("%s/trace=%t/budget%d", fixture.name, traced, budget), func(t *testing.T) {
					precise, jit := newThumbSMCOraclePair(t, false, traced)
					for _, b := range []*Backend{precise, jit} {
						thumbCompareBranchOracleReset(t, b, fixture.words, traced)
						check(t, b.WriteRegister(6, fixture.r6))
						if fixture.name == "blx-lr-arm" {
							check(t, b.WriteRegister(cpu.RegisterLR, fixture.r6))
						}
						check(t, b.WriteRegister(0, 0))
						check(t, b.WriteRegister(cpu.RegisterSP, 0x2100))
						check(t, b.WriteMemory(0x2100, armTestCode(0x2000)))
						if fixture.arm {
							check(t, b.WriteMemory(0x2000, armTestCode(0xe1200070)))
						} else {
							check(t, b.WriteMemory(0x2000, thumbTestCode(0xbe00)))
						}
					}
					if block := jit.jitBlockAt(0x1000); block != nil && block.compareBranchTail != nil {
						t.Fatal("tail classifier crossed an earlier control-flow boundary")
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				})
			}
		}
	}
}

func TestThumbCompareBranchTailWrappedTargetsMatchPrecise(t *testing.T) {
	for _, start := range []uint32{0, 0xfffffff0} {
		for _, branch := range []uint16{0xd080, 0xd07f, 0xd180, 0xd17f} {
			for _, value := range []uint32{0, 1} {
				for budget := uint64(1); budget <= 2; budget++ {
					t.Run(fmt.Sprintf("%x/%04x/r0%d/budget%d", start, branch, value, budget), func(t *testing.T) {
						precise, jit := New(), NewJIT()
						for _, b := range []*Backend{precise, jit} {
							t.Cleanup(func() { _ = b.Close() })
							check(t, b.Map(0, 16, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
							check(t, b.Map(0xffffffe0, 32, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
							check(t, b.WriteMemory(start, thumbTestCode(0x2800, branch)))
							check(t, b.WriteRegister(0, value))
						}
						assertThumbCompareBranchTail(t, jit, start)
						assertThumbSMCOracleParity(t, precise, jit, start, budget)
					})
				}
			}
		}
	}
}

func TestThumbCompareBranchTailColdSuccessorSelfModifyMatchesPrecise(t *testing.T) {
	for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 255, 256, 257} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise, jit := newThumbSMCOraclePair(t, false, false)
			for _, b := range []*Backend{precise, jit} {
				check(t, b.Map(0x5000, 32, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, b.WriteMemory(0x1000, thumbTestCode(0x4730)))
				check(t, b.WriteMemory(0x5000, thumbTestCode(0x8008, 0x2a00, 0xd102, 0x2301, 0xbe00, 0x46c0, 0x2302, 0xbe00)))
				resetThumbApplicationChainOracle(t, b, false)
				check(t, b.WriteRegister(6, 0x5001))
				check(t, b.WriteRegister(0, 0x2a01))
				check(t, b.WriteRegister(1, 0x5002))
				check(t, b.WriteRegister(2, 0))
			}
			if jit.jitBlockAt(0x1000) == nil || jit.jitBlocks[0x5000] != nil || jit.jitCodeHi >= 0x5000 {
				t.Fatal("successor is not cold")
			}
			assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
		})
	}
}
