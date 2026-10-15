package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Assemble STRB / ADDS / STRB / ADDS / CMP / BLS independently of the matcher.
func bytePairFillLoopProgram(destination, low, high, bound uint16) []uint16 {
	return []uint16{
		0x6000 | 1<<12 | destination<<3 | low,
		0x3000 | destination<<8 | 1,
		0x6000 | 1<<12 | destination<<3 | high,
		0x3000 | destination<<8 | 1,
		0x4000 | 10<<6 | bound<<3 | destination,
		0xd000 | 9<<8 | 0xf9,
	}
}

func TestThumbBytePairFillLoopAllRegistersAndPrefixes(t *testing.T) {
	precise := newGenericLoopTestBackend(t, New(), bytePairFillLoopProgram(4, 2, 6, 5))
	jit := newGenericLoopTestBackend(t, NewJIT(), bytePairFillLoopProgram(4, 2, 6, 5))
	// All immutable operands may alias each other, but none may alias the updated
	// destination. Exercise every allowed renaming, including low == high == bound.
	for destination := uint16(0); destination < 8; destination++ {
		for bound := uint16(0); bound < 8; bound++ {
			for low := uint16(0); low < 8; low++ {
				for high := uint16(0); high < 8; high++ {
					if destination == low || destination == high || destination == bound {
						continue
					}
					program := append(bytePairFillLoopProgram(destination, low, high, bound), 0xbe00)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(program...)))
					}
					block := jit.jitBlockAt(0x1000)
					if block == nil || block.extraLoop == nil || block.extraLoop.kind != jitBytePairFillLoop || block.end != 0x100c {
						t.Fatalf("registers dst=%d low=%d high=%d bound=%d not recognized", destination, low, high, bound)
					}
					for budget := uint64(1); budget <= 13; budget++ {
						for _, b := range []*Backend{precise, jit} {
							resetGenericLoopRegisters(t, b, flagN|flagV)
							check(t, b.WriteRegister(uint32(low), 0xdeadbe27))
							check(t, b.WriteRegister(uint32(high), 0xcafebed8))
							check(t, b.WriteRegister(uint32(bound), 0x3005))
							check(t, b.WriteRegister(uint32(destination), 0x3001))
							check(t, b.WriteMemory(0x3000, bytes.Repeat([]byte{0x91}, 16)))
						}
						before := jit.ExecutionStatistics()
						runGenericLoopOracle(t, precise, jit, 0x1000, budget)
						after := jit.ExecutionStatistics()
						if got := after.AcceleratedLoopInstructions - before.AcceleratedLoopInstructions; got != budget/6*6 {
							t.Fatalf("dst=%d low=%d high=%d bound=%d budget=%d accelerated=%d", destination, low, high, bound, budget, got)
						}
					}
				}
			}
		}
	}
}

func TestThumbBytePairFillLoopFlagsUnsignedInclusiveAndFiniteSpan(t *testing.T) {
	precise := newGenericLoopTestBackend(t, New(), bytePairFillLoopProgram(4, 2, 6, 5))
	jit := newGenericLoopTestBackend(t, NewJIT(), bytePairFillLoopProgram(4, 2, 6, 5))
	for flags := uint32(0); flags < 16; flags++ {
		for _, destination := range []uint32{0x3000, 0x3001} {
			for _, bound := range []uint32{destination - 1, destination, destination + 1, destination + 2, destination + 3, destination + 4, ^uint32(0)} {
				for budget := uint64(1); budget <= 25; budget++ {
					for _, b := range []*Backend{precise, jit} {
						resetGenericLoopRegisters(t, b, flags<<28)
						check(t, b.WriteRegister(2, 0xabcdef12))
						check(t, b.WriteRegister(6, 0x789abcd3))
						check(t, b.WriteRegister(4, destination))
						check(t, b.WriteRegister(5, bound))
						check(t, b.WriteMemory(0x3000, bytes.Repeat([]byte{0x65}, 32)))
					}
					runGenericLoopOracle(t, precise, jit, 0x1000, budget)
				}
			}
		}
	}
	// A complete inclusive do-while writes exactly three pairs when end = dst+4.
	for _, b := range []*Backend{precise, jit} {
		resetGenericLoopRegisters(t, b, 0)
		check(t, b.WriteRegister(2, 0x12))
		check(t, b.WriteRegister(6, 0xd3))
		check(t, b.WriteRegister(4, 0x3001))
		check(t, b.WriteRegister(5, 0x3005))
		check(t, b.WriteMemory(0x3000, bytes.Repeat([]byte{0x65}, 10)))
	}
	runGenericLoopOracle(t, precise, jit, 0x1000, 18)
	var got [10]byte
	check(t, jit.ReadMemory(0x3000, got[:]))
	if want := []byte{0x65, 0x12, 0xd3, 0x12, 0xd3, 0x12, 0xd3, 0x65, 0x65, 0x65}; !bytes.Equal(got[:], want) {
		t.Fatalf("inclusive finite span got %x want %x", got, want)
	}
	if pc := registerValueForGenericLoop(t, jit, cpu.RegisterPC); pc != 0x100c {
		t.Fatalf("completed loop PC = %08x", pc)
	}
}

func TestThumbBytePairFillLoopFaultPrefixesAndSplitRegions(t *testing.T) {
	for _, test := range []struct {
		name        string
		destination uint32
		mapExtra    func(*testing.T, *Backend)
		faultAfter  uint64
	}{
		{"first-byte-unmapped", 0x5000, nil, 0},
		{"first-byte-read-only", 0x5000, func(t *testing.T, b *Backend) { check(t, b.Map(0x5000, 2, cpu.PermissionRead)) }, 0},
		{"second-byte-unmapped", 0x3fff, nil, 2},
		{"second-byte-read-only", 0x5000, func(t *testing.T, b *Backend) {
			check(t, b.Map(0x5000, 1, cpu.PermissionRead|cpu.PermissionWrite))
			check(t, b.Map(0x5001, 1, cpu.PermissionRead))
		}, 2},
		{"two-writable-regions", 0x5000, func(t *testing.T, b *Backend) {
			check(t, b.Map(0x5000, 1, cpu.PermissionRead|cpu.PermissionWrite))
			check(t, b.Map(0x5001, 1, cpu.PermissionRead|cpu.PermissionWrite))
		}, 6},
		{"progress-before-second-byte-fault", 0x3ffd, nil, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			for budget := uint64(1); budget <= 19; budget++ {
				precise := newGenericLoopTestBackend(t, New(), bytePairFillLoopProgram(4, 2, 6, 5))
				jit := newGenericLoopTestBackend(t, NewJIT(), bytePairFillLoopProgram(4, 2, 6, 5))
				for _, b := range []*Backend{precise, jit} {
					if test.mapExtra != nil {
						test.mapExtra(t, b)
					}
					check(t, b.WriteRegister(2, 0x12))
					check(t, b.WriteRegister(6, 0xd3))
					check(t, b.WriteRegister(4, test.destination))
					check(t, b.WriteRegister(5, ^uint32(0)))
				}
				want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
				got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
				assertGenericLoopResults(t, precise, jit, want, got)
				if budget > test.faultAfter && (got.Err == nil || got.Instructions != test.faultAfter) {
					t.Fatalf("fault prefix got %+v want retired %d", got, test.faultAfter)
				}
				if test.faultAfter == 2 && budget >= 3 {
					var first [1]byte
					check(t, jit.ReadMemory(test.destination, first[:]))
					if first[0] != 0x12 || registerValueForGenericLoop(t, jit, 4) != test.destination+1 || got.PC != 0x1006 {
						t.Fatalf("second-byte fault lost first store/ADDS: %+v first=%x", got, first)
					}
				}
				if test.destination == 0x3ffd && budget >= 9 && jit.ExecutionStatistics().AcceleratedLoopInstructions != 6 {
					t.Fatal("safe first iteration was not retired before declining the faulting iteration")
				}
			}
		})
	}
}

func TestThumbBytePairFillLoopWrapAndSignedBoundary(t *testing.T) {
	for _, destination := range []uint32{0xfffffffd, 0xfffffffe, 0xffffffff, 0x7fffffff, 0x80000000} {
		for _, bound := range []uint32{0, destination, ^uint32(0)} {
			for budget := uint64(1); budget <= 25; budget++ {
				precise := newGenericLoopTestBackend(t, New(), bytePairFillLoopProgram(4, 2, 6, 5))
				jit := newGenericLoopTestBackend(t, NewJIT(), bytePairFillLoopProgram(4, 2, 6, 5))
				for _, b := range []*Backend{precise, jit} {
					check(t, b.Map(0xfffff000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
					check(t, b.Map(0, 0x100, cpu.PermissionRead|cpu.PermissionWrite))
					check(t, b.Map(0x7ffff000, 0x2000, cpu.PermissionRead|cpu.PermissionWrite))
					check(t, b.WriteRegister(2, 0x12345678))
					check(t, b.WriteRegister(6, 0xabcdef09))
					check(t, b.WriteRegister(4, destination))
					check(t, b.WriteRegister(5, bound))
				}
				runGenericLoopOracle(t, precise, jit, 0x1000, budget)
			}
		}
	}
}

func TestThumbBytePairFillLoopRequiresExactShapeAndGuards(t *testing.T) {
	words := bytePairFillLoopProgram(4, 2, 6, 5)
	for index := range words {
		changed := append([]uint16(nil), words...)
		changed[index] ^= 0x0100
		b := newGenericLoopTestBackend(t, NewJIT(), changed)
		if block := b.jitBlockAt(0x1000); block != nil && classifyThumbBytePairFillLoop(block) != nil {
			t.Fatalf("mutated instruction %d was accepted", index)
		}
	}
	for _, registers := range [][4]uint16{{4, 4, 6, 5}, {4, 2, 4, 5}, {4, 2, 6, 4}} {
		b := newGenericLoopTestBackend(t, NewJIT(), bytePairFillLoopProgram(registers[0], registers[1], registers[2], registers[3]))
		if block := b.jitBlockAt(0x1000); block != nil && classifyThumbBytePairFillLoop(block) != nil {
			t.Fatal("updated operand was incorrectly considered invariant")
		}
	}
	b := newGenericLoopTestBackend(t, NewJIT(), words)
	loop := classifyThumbBytePairFillLoop(b.jitBlockAt(0x1000))
	check(t, b.WriteRegister(4, 0x3001))
	check(t, b.WriteRegister(5, ^uint32(0)))
	for _, observation := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		b.setNZCV(0, true, true)
		assertGenericLoopDecline(t, b, func() uint64 {
			return b.accelerateThumbBytePairFillLoop(loop, 100, observation[0], observation[1], observation[2])
		})
	}
	for budget := uint64(0); budget < 6; budget++ {
		assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbBytePairFillLoop(loop, budget, false, false, false) })
	}
	b.physicalAccess = true
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbBytePairFillLoop(loop, 100, false, false, false) })
	b.physicalAccess = false
	b.tlb = []tlbEntry{{}}
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbBytePairFillLoop(loop, 100, false, false, false) })
	b.tlb = nil
	check(t, b.WriteRegister(cpu.RegisterPC, 0x1002))
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbBytePairFillLoop(loop, 100, false, false, false) })
	check(t, b.WriteRegister(cpu.RegisterPC, 0x1000))
	for _, destination := range []uint32{0x3fff, 0x5000, 0xfffffffe, 0xffffffff} {
		check(t, b.WriteRegister(4, destination))
		assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbBytePairFillLoop(loop, 100, false, false, false) })
	}
	// End-PC wrap is unsupported even if the conditional target wraps correctly.
	shift := &jitBlock{start: 0xfffffffa, thumb: make([]thumbMicroInstr, 3)}
	for index, raw := range shiftBoundLoopProgram(2, 5) {
		shift.thumb[index].raw = raw
	}
	fill := &jitBlock{start: 0xfffffff4, thumb: make([]thumbMicroInstr, 6)}
	for index, raw := range words {
		fill.thumb[index].raw = raw
	}
	if classifyThumbShiftBoundLoop(shift) != nil || classifyThumbBytePairFillLoop(fill) != nil {
		t.Fatal("unrepresentable fallthrough PC was accepted")
	}
}

func TestThumbBytePairFillLoopCodePagesAndMutation(t *testing.T) {
	for _, translated := range []bool{false, true} {
		precise := newGenericLoopTestBackend(t, New(), bytePairFillLoopProgram(4, 2, 6, 5))
		jit := newGenericLoopTestBackend(t, NewJIT(), bytePairFillLoopProgram(4, 2, 6, 5))
		for _, b := range []*Backend{precise, jit} {
			// The owned image is executable data. Translation, not execute permission
			// alone, determines whether direct stores would miss an invalidation.
			b.regions[1].permissions |= cpu.PermissionExecute
			b.clearDataCaches()
			check(t, b.WriteMemory(0x3000, thumbTestCode(0xbe00)))
			check(t, b.WriteRegister(2, 0x12))
			check(t, b.WriteRegister(6, 0xd3))
			check(t, b.WriteRegister(4, 0x3001))
			check(t, b.WriteRegister(5, 0x3005))
		}
		if translated {
			_ = jit.jitBlockAt(0x3000)
		}
		loop := classifyThumbBytePairFillLoop(jit.jitBlockAt(0x1000))
		if translated {
			assertGenericLoopDecline(t, jit, func() uint64 { return jit.accelerateThumbBytePairFillLoop(loop, 6, false, false, false) })
		}
		runGenericLoopOracle(t, precise, jit, 0x1000, 18)
		accelerated := jit.ExecutionStatistics().AcceleratedLoopInstructions
		if !translated && accelerated != 18 {
			t.Fatalf("executable data without a code page accelerated %d, want 18", accelerated)
		}
	}
	for _, destination := range []uint32{0x1000, 0x1001, 0x1002, 0x1003, 0x100a, 0x100b} {
		for budget := uint64(1); budget <= 25; budget++ {
			precise := newGenericLoopTestBackend(t, New(), bytePairFillLoopProgram(4, 2, 6, 5))
			jit := newGenericLoopTestBackend(t, NewJIT(), bytePairFillLoopProgram(4, 2, 6, 5))
			_ = jit.jitBlockAt(0x1000)
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteRegister(2, 0))
				check(t, b.WriteRegister(6, 0xbe))
				check(t, b.WriteRegister(4, destination))
				check(t, b.WriteRegister(5, 0x1010))
			}
			runGenericLoopOracle(t, precise, jit, 0x1000, budget)
			if jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
				t.Fatal("current code alias accelerated")
			}
		}
	}
}

// This independently exercises the terminal contract of the cold dispatcher.
// An alias fallback may stop, fault or switch mode without a stop reason.
func TestThumbBytePairFillLoopAliasFallbackIsTerminal(t *testing.T) {
	for _, raw := range []uint16{0xbe00, 0x4710, 0x6818} { // BKPT, BX r2, LDR r0,[r3].
		for _, traced := range []bool{false, true} {
			for _, wrapped := range []bool{false, true} {
				for _, budget := range []uint64{1, 16} {
					t.Run(fmt.Sprintf("instruction=%04x/trace=%t/wrap=%t/budget=%d", raw, traced, wrapped, budget), func(t *testing.T) {
						precise, jit := New(), NewJIT()
						start, destination := uint32(0x1000), uint32(0x1000)
						if wrapped {
							start, destination = 0, ^uint32(0)
						}
						for _, b := range []*Backend{precise, jit} {
							t.Cleanup(func() { _ = b.Close() })
							check(t, b.Map(start, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
							check(t, b.WriteMemory(start, thumbTestCode(raw, 0xbe00)))
							resetGenericLoopRegisters(t, b, flagC|flagV)
							check(t, b.WriteRegister(cpu.RegisterPC, start))
							check(t, b.WriteRegister(2, 0x2000))
							check(t, b.WriteRegister(3, 0x5000))
							check(t, b.WriteRegister(4, destination))
							if traced {
								check(t, b.SetPCHistoryLimit(16))
							}
						}
						extra := &jitExtraLoop{kind: jitBytePairFillLoop, bytePairFill: jitThumbBytePairFillLoop{start: start, destination: 4, low: 2, high: 6, bound: 5}}
						wantN, wantReason, wantErr := precise.runThumb(budget)
						gotN, done, gotReason, gotErr := jit.runThumbExtraLoop(extra, budget, false, false, traced)
						sameReason := gotReason == nil && wantReason == nil || gotReason != nil && wantReason != nil && *gotReason == *wantReason
						if !done || gotN != wantN || !sameReason || !sameError(gotErr, wantErr) || jit.mode != precise.mode {
							t.Fatalf("fallback got=(%d,%t,%v,%v,%v) want=(%d,true,%v,%v,%v)", gotN, done, gotReason, gotErr, jit.mode, wantN, wantReason, wantErr, precise.mode)
						}
						assertGenericLoopResults(t, precise, jit, cpu.Result{Instructions: wantN, PC: precise.regs[cpu.RegisterPC]}, cpu.Result{Instructions: gotN, PC: jit.regs[cpu.RegisterPC]})
					})
				}
			}
		}
	}
}

func TestThumbGenericLoopsProfileAccounting(t *testing.T) {
	for _, fill := range []bool{false, true} {
		words, iteration := shiftBoundLoopProgram(2, 5), uint64(3)
		if fill {
			words, iteration = bytePairFillLoopProgram(4, 2, 6, 5), 6
		}
		jit := newGenericLoopTestBackend(t, NewJIT(), words)
		check(t, jit.WriteRegister(2, 0))
		check(t, jit.WriteRegister(5, ^uint32(0)))
		if fill {
			check(t, jit.WriteRegister(4, 0x3001))
		}
		result := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 256)
		stats := jit.ExecutionStatistics()
		if result.Err != nil || result.Instructions != 256 || stats.AcceleratedLoopIterations != 256/iteration || stats.AcceleratedLoopInstructions != 256/iteration*iteration {
			t.Fatalf("fill=%t retirement got=%+v statistics=%+v", fill, result, stats)
		}
		if !collectThumbMicro {
			if stats.ThumbMicro != nil {
				t.Fatal("ordinary build unexpectedly retained per-instruction diagnostics")
			}
			continue
		}
		if stats.ThumbMicro == nil {
			t.Fatal("partial iteration was not profiled")
		}
		var actualInstructions, trackedBlocks uint64
		for _, count := range stats.ThumbMicro.Classes {
			actualInstructions += count
		}
		for _, count := range stats.ThumbMicro.Blocks {
			trackedBlocks += count
		}
		if actualInstructions+stats.AcceleratedLoopInstructions != 256 || trackedBlocks+stats.ThumbMicro.UntrackedBlockCalls != stats.ThumbMicro.BlockCalls {
			t.Fatalf("fill=%t diagnostic accounting differs: instructions=%d accelerated=%d micro=%+v", fill, actualInstructions, stats.AcceleratedLoopInstructions, stats.ThumbMicro)
		}
	}
}
