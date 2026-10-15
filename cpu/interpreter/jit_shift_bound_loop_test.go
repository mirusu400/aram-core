package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

// Assemble synthetic instructions independently of the classifier, with no
// proprietary code or assumptions about callers of a division routine.
func shiftBoundLoopProgram(value, bound uint16) []uint16 {
	return []uint16{1<<6 | value<<3 | value, 0x4000 | 10<<6 | bound<<3 | value, 0xd000 | 9<<8 | 0xfc}
}

func newGenericLoopTestBackend(t *testing.T, b *Backend, words []uint16) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = b.Close() })
	check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	check(t, b.Map(0x3000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, b.WriteMemory(0x1000, thumbTestCode(append(append([]uint16(nil), words...), 0xbe00)...)))
	resetGenericLoopRegisters(t, b, 0)
	return b
}

func resetGenericLoopRegisters(t *testing.T, b *Backend, flags uint32) {
	t.Helper()
	for register := uint32(0); register < cpu.RegisterPC; register++ {
		check(t, b.WriteRegister(register, 0x01234500+register*17))
	}
	check(t, b.WriteRegister(cpu.RegisterSP, 0x3f00))
	check(t, b.WriteRegister(cpu.RegisterPC, 0x1000))
	check(t, b.WriteRegister(cpu.RegisterCPSR, flags|uint32(processorModeSystem)|cpu.StatusThumb))
}

func assertGenericLoopResults(t *testing.T, precise, jit *Backend, want, got cpu.Result) {
	t.Helper()
	if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
		t.Fatalf("result got=%+v want=%+v", got, want)
	}
	wantContext, err := precise.SaveContext()
	check(t, err)
	gotContext, err := jit.SaveContext()
	check(t, err)
	if !bytes.Equal(wantContext, gotContext) {
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if a, b := registerValueForGenericLoop(t, precise, register), registerValueForGenericLoop(t, jit, register); a != b {
				t.Errorf("register %d got=%08x want=%08x", register, b, a)
			}
		}
		t.Fatal("architectural context differs")
	}
	if len(precise.regions) != len(jit.regions) {
		t.Fatal("mapping count differs")
	}
	for index := range precise.regions {
		if !bytes.Equal(precise.regions[index].data, jit.regions[index].data) {
			t.Fatalf("memory region %d differs", index)
		}
	}
	if !slices.Equal(precise.PCHistory(), jit.PCHistory()) || !reflect.DeepEqual(precise.pcHits, jit.pcHits) ||
		!reflect.DeepEqual(precise.PCRegisterCaptures(), jit.PCRegisterCaptures()) {
		t.Fatal("instruction trace or captures differ")
	}
}

func registerValueForGenericLoop(t *testing.T, b *Backend, register uint32) uint32 {
	t.Helper()
	value, err := b.ReadRegister(register)
	check(t, err)
	return value
}

func runGenericLoopOracle(t *testing.T, precise, jit *Backend, pc uint32, budget uint64) {
	t.Helper()
	want := precise.Run(context.Background(), pc, cpu.ModeThumb, budget)
	got := jit.Run(context.Background(), pc, cpu.ModeThumb, budget)
	assertGenericLoopResults(t, precise, jit, want, got)
}

func TestThumbShiftBoundLoopAllRegistersFlagsAndPrefixes(t *testing.T) {
	precise := newGenericLoopTestBackend(t, New(), shiftBoundLoopProgram(0, 1))
	jit := newGenericLoopTestBackend(t, NewJIT(), shiftBoundLoopProgram(0, 1))
	for value := uint16(0); value < 8; value++ {
		for bound := uint16(0); bound < 8; bound++ {
			if value == bound {
				continue
			}
			program := append(shiftBoundLoopProgram(value, bound), 0xbe00)
			check(t, precise.WriteMemory(0x1000, thumbTestCode(program...)))
			check(t, jit.WriteMemory(0x1000, thumbTestCode(program...)))
			block := jit.jitBlockAt(0x1000)
			if block == nil || block.extraLoop == nil || block.extraLoop.kind != jitShiftBoundLoop || block.end != 0x1006 {
				t.Fatalf("registers %d/%d not recognized", value, bound)
			}
			for flags := uint32(0); flags < 16; flags++ {
				for budget := uint64(1); budget <= 10; budget++ {
					for _, b := range []*Backend{precise, jit} {
						resetGenericLoopRegisters(t, b, flags<<28)
						check(t, b.WriteRegister(uint32(value), 0x40000001))
						check(t, b.WriteRegister(uint32(bound), 0x80000004))
					}
					before := jit.ExecutionStatistics()
					runGenericLoopOracle(t, precise, jit, 0x1000, budget)
					after := jit.ExecutionStatistics()
					if got := after.AcceleratedLoopInstructions - before.AcceleratedLoopInstructions; got != budget/3*3 {
						t.Fatalf("registers %d/%d flags=%x budget=%d accelerated=%d", value, bound, flags, budget, got)
					}
				}
			}
		}
	}
}

func TestThumbShiftBoundLoopZeroWrapAndUnsignedExit(t *testing.T) {
	precise := newGenericLoopTestBackend(t, New(), shiftBoundLoopProgram(6, 2))
	jit := newGenericLoopTestBackend(t, NewJIT(), shiftBoundLoopProgram(6, 2))
	for _, state := range [][2]uint32{{0, 0}, {0, ^uint32(0)}, {1, 0}, {1, 2}, {1, 0x7fffffff}, {0x40000000, 0x7fffffff}, {0x40000000, 0x80000000}, {0x80000000, 0}, {0x80000001, 3}, {^uint32(0), ^uint32(0)}} {
		for budget := uint64(1); budget <= 100; budget++ {
			for _, b := range []*Backend{precise, jit} {
				resetGenericLoopRegisters(t, b, flagC|flagV)
				check(t, b.WriteRegister(6, state[0]))
				check(t, b.WriteRegister(2, state[1]))
			}
			runGenericLoopOracle(t, precise, jit, 0x1000, budget)
		}
	}
}

// Guard declines may fill private lookup hints but must not change guest state,
// deferred flags, memory or execution counters.
func assertGenericLoopDecline(t *testing.T, b *Backend, call func() uint64) {
	t.Helper()
	registers, flags, statistics := b.regs, b.flags, b.ExecutionStatistics()
	memory := make([][]byte, len(b.regions))
	for index := range b.regions {
		memory[index] = append([]byte(nil), b.regions[index].data...)
	}
	if retired := call(); retired != 0 {
		t.Fatalf("declined call retired %d", retired)
	}
	if b.regs != registers || b.flags != flags || !reflect.DeepEqual(b.ExecutionStatistics(), statistics) {
		t.Fatal("decline changed registers, pending flags or statistics")
	}
	for index := range b.regions {
		if !bytes.Equal(memory[index], b.regions[index].data) {
			t.Fatal("decline changed memory")
		}
	}
}

func TestThumbShiftBoundLoopRequiresExactShapeAndGuards(t *testing.T) {
	words := shiftBoundLoopProgram(2, 5)
	for index := range words {
		b := newGenericLoopTestBackend(t, NewJIT(), words)
		changed := append([]uint16(nil), words...)
		changed[index] ^= 0x0100
		check(t, b.WriteMemory(0x1000, thumbTestCode(changed...)))
		if block := b.jitBlockAt(0x1000); block != nil && classifyThumbShiftBoundLoop(block) != nil {
			t.Fatalf("mutated instruction %d was accepted", index)
		}
	}
	for _, code := range [][]uint16{{0x0040, 0x4280, 0xd9fc}, {0x0052, 0x42aa, 0xd8fc}, {0x0052, 0x42aa, 0xd9fd}, {0x005a, 0x42aa, 0xd9fc}} {
		b := newGenericLoopTestBackend(t, NewJIT(), code)
		if block := b.jitBlockAt(0x1000); block != nil && classifyThumbShiftBoundLoop(block) != nil {
			t.Fatal("near-match or mutable bound was accepted")
		}
	}
	b := newGenericLoopTestBackend(t, NewJIT(), words)
	loop := classifyThumbShiftBoundLoop(b.jitBlockAt(0x1000))
	for _, observation := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		b.setNZCV(0, true, true)
		assertGenericLoopDecline(t, b, func() uint64 {
			return b.accelerateThumbShiftBoundLoop(loop, 100, observation[0], observation[1], observation[2])
		})
	}
	for _, budget := range []uint64{0, 1, 2} {
		assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbShiftBoundLoop(loop, budget, false, false, false) })
	}
	b.physicalAccess = true
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbShiftBoundLoop(loop, 100, false, false, false) })
	b.physicalAccess = false
	b.tlb = []tlbEntry{{}}
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbShiftBoundLoop(loop, 100, false, false, false) })
	b.tlb = nil
	check(t, b.WriteRegister(cpu.RegisterPC, 0x1002))
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbShiftBoundLoop(loop, 100, false, false, false) })
}

func TestThumbShiftBoundLoopFetchFaultAndCodeMutation(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		precise := newGenericLoopTestBackend(t, New(), shiftBoundLoopProgram(2, 5))
		jit := newGenericLoopTestBackend(t, NewJIT(), shiftBoundLoopProgram(2, 5))
		if jit.jitBlockAt(0x1000).extraLoop == nil {
			t.Fatal("cold loop missing")
		}
		for _, b := range []*Backend{precise, jit} {
			check(t, b.WriteRegister(2, 1))
			check(t, b.WriteRegister(5, 0xffff))
			if mutate {
				check(t, b.WriteMemory(0x1004, thumbTestCode(0xd8fc))) // BHI, not BLS.
			} else {
				// A boundary after the first shift exposes a genuine fetch fault.
				b.regions[0].size = 2
				b.regions[0].data = b.regions[0].data[:2]
				// This synthetic remap must drop the same private fetch window
				// that Map drops; it is separate from the functional I-cache.
				b.executeData = nil
				b.invalidateTranslations()
				b.invalidateInstructionWindow()
				b.clearDataCaches()
			}
		}
		runGenericLoopOracle(t, precise, jit, 0x1000, 12)
	}
}

type genericLoopSecondPollContext struct{ polls int }

func (*genericLoopSecondPollContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*genericLoopSecondPollContext) Done() <-chan struct{}       { return nil }
func (*genericLoopSecondPollContext) Value(any) any               { return nil }
func (c *genericLoopSecondPollContext) Err() error {
	c.polls++
	if c.polls >= 2 {
		return context.Canceled
	}
	return nil
}

func TestThumbGenericLoopsCancelAt256AndPreserveRawCapture(t *testing.T) {
	for _, fill := range []bool{false, true} {
		for _, traced := range []bool{false, true} {
			t.Run(fmt.Sprintf("fill=%t/trace=%t", fill, traced), func(t *testing.T) {
				words := shiftBoundLoopProgram(2, 5)
				if fill {
					words = bytePairFillLoopProgram(4, 2, 6, 5)
				}
				precise := newGenericLoopTestBackend(t, New(), words)
				jit := newGenericLoopTestBackend(t, NewJIT(), words)
				for _, b := range []*Backend{precise, jit} {
					check(t, b.WriteRegister(2, 0))
					check(t, b.WriteRegister(5, ^uint32(0)))
					if fill {
						check(t, b.WriteRegister(4, 0x3001))
					}
					if traced {
						check(t, b.SetPCHistoryLimit(1024))
					}
				}
				want := precise.Run(&genericLoopSecondPollContext{}, 0x1000, cpu.ModeThumb, 1024)
				got := jit.Run(&genericLoopSecondPollContext{}, 0x1000, cpu.ModeThumb, 1024)
				if got.Instructions != 256 || got.Reason != cpu.StopRequested {
					t.Fatalf("cancellation was not bounded to 256: %+v", got)
				}
				assertGenericLoopResults(t, precise, jit, want, got)
			})
		}
		// Do not resolve flags through ReadRegister or SaveContext between these
		// runs: recordPC intentionally captures the stored (not resolved) CPSR.
		t.Run(fmt.Sprintf("raw-capture/fill=%t", fill), func(t *testing.T) {
			words, budget := shiftBoundLoopProgram(2, 5), uint64(3)
			if fill {
				words, budget = bytePairFillLoopProgram(4, 2, 6, 5), 6
			}
			precise := newGenericLoopTestBackend(t, New(), words)
			jit := newGenericLoopTestBackend(t, NewJIT(), words)
			for _, b := range []*Backend{precise, jit} {
				// Make an unresolved final CMP observably different from the old
				// raw CPSR. Merely checking that a capture exists is not enough.
				check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagZ|flagC|flagV|cpu.StatusThumb|uint32(processorModeSystem)))
				check(t, b.WriteRegister(2, 1))
				check(t, b.WriteRegister(5, ^uint32(0)))
				if fill {
					check(t, b.WriteRegister(4, 0x3001))
				}
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			if got.PC != 0x1000 || got.PC != want.PC {
				t.Fatalf("first iteration: got=%+v want=%+v", got, want)
			}
			if precise.flags.dirty || precise.regs[cpu.RegisterCPSR]&(flagN|flagZ|flagC|flagV) == flagN|flagZ|flagC|flagV {
				t.Fatal("precise branch did not materialize a changed raw CPSR")
			}
			for _, b := range []*Backend{precise, jit} {
				check(t, b.SetPCRegisterCapture(0x1000, 1))
			}
			want = precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
			got = jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
			if len(jit.PCRegisterCaptures()) != 1 {
				t.Fatal("capture was vacuous")
			}
			assertGenericLoopResults(t, precise, jit, want, got)
		})
	}
}

func TestThumbGenericLoopsFetchFaultAtEveryPrefixAndExternalMutation(t *testing.T) {
	for _, fill := range []bool{false, true} {
		words := shiftBoundLoopProgram(2, 5)
		if fill {
			words = bytePairFillLoopProgram(4, 2, 6, 5)
		}
		for prefix := 1; prefix < len(words); prefix++ {
			precise, jit := New(), NewJIT()
			for _, b := range []*Backend{precise, jit} {
				t.Cleanup(func() { _ = b.Close() })
				check(t, b.Map(0x1000, uint32(prefix*2), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, b.Map(0x3000, 0x100, cpu.PermissionRead|cpu.PermissionWrite))
				check(t, b.WriteMemory(0x1000, thumbTestCode(words[:prefix]...)))
				resetGenericLoopRegisters(t, b, flagC|flagV)
				check(t, b.WriteRegister(2, 1))
				check(t, b.WriteRegister(4, 0x3001))
				check(t, b.WriteRegister(5, ^uint32(0)))
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 32)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 32)
			assertGenericLoopResults(t, precise, jit, want, got)
			if got.Err == nil || got.Instructions != uint64(prefix) || got.PC != 0x1000+uint32(prefix*2) || jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
				t.Fatalf("fill=%t prefix=%d fetch fault got=%+v", fill, prefix, got)
			}
		}
		for index := range words {
			precise := newGenericLoopTestBackend(t, New(), words)
			jit := newGenericLoopTestBackend(t, NewJIT(), words)
			if block := jit.jitBlockAt(0x1000); block == nil || block.extraLoop == nil {
				t.Fatal("warm candidate missing")
			}
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteRegister(2, 1))
				check(t, b.WriteRegister(4, 0x3001))
				check(t, b.WriteRegister(5, ^uint32(0)))
				check(t, b.WriteMemory(0x1000+uint32(index*2), thumbTestCode(0xbe00)))
			}
			runGenericLoopOracle(t, precise, jit, 0x1000, 32)
			if jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
				t.Fatalf("mutated signature word %d accelerated", index)
			}
		}
	}
}

func TestThumbGenericLoopsConsecutiveRunsWithoutContextSave(t *testing.T) {
	for _, fill := range []bool{false, true} {
		words := shiftBoundLoopProgram(2, 5)
		if fill {
			words = bytePairFillLoopProgram(4, 2, 6, 5)
		}
		precise := newGenericLoopTestBackend(t, New(), words)
		jit := newGenericLoopTestBackend(t, NewJIT(), words)
		for _, b := range []*Backend{precise, jit} {
			resetGenericLoopRegisters(t, b, flagN|flagZ|flagC|flagV)
			check(t, b.WriteRegister(2, 0))
			check(t, b.WriteRegister(4, 0x3001))
			check(t, b.WriteRegister(5, ^uint32(0)))
		}
		var want, got cpu.Result
		pc := uint32(0x1000)
		for _, budget := range []uint64{1, 1, 1, 2, 5, 6, 7, 8, 11, 15, 256, 1, 3, 6} {
			want = precise.Run(context.Background(), pc, cpu.ModeThumb, budget)
			got = jit.Run(context.Background(), pc, cpu.ModeThumb, budget)
			if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
				t.Fatalf("fill=%t budget=%d got=%+v want=%+v", fill, budget, got, want)
			}
			pc = want.PC
		}
		assertGenericLoopResults(t, precise, jit, want, got)
	}
}

func TestThumbGenericLoopsSystemAndTracedOracles(t *testing.T) {
	for _, fill := range []bool{false, true} {
		for _, trapped := range []bool{false, true} {
			words := shiftBoundLoopProgram(2, 5)
			if fill {
				words = bytePairFillLoopProgram(4, 2, 6, 5)
			}
			precise, jit := New(), NewJIT()
			buses := []*testSystemBus{{memory: make(map[uint32]byte)}, {memory: make(map[uint32]byte)}}
			for index, b := range []*Backend{precise, jit} {
				t.Cleanup(func() { _ = b.Close() })
				buses[index].writeRaw(0x1000, thumbTestCode(append(words, 0xbe00)...))
				check(t, b.AttachSystemBus(buses[index]))
				resetGenericLoopRegisters(t, b, flagV|flagC)
				check(t, b.WriteRegister(2, 1))
				check(t, b.WriteRegister(5, 0xffff))
				if fill {
					check(t, b.WriteRegister(4, 0x3001))
				}
				if trapped {
					check(t, b.SetExecutionTraps([]cpu.ExecutionTrap{{Mode: cpu.ModeThumb, Address: 0x1002}}))
				}
			}
			runGenericLoopOracle(t, precise, jit, 0x1000, 21)
			if !reflect.DeepEqual(buses[0].memory, buses[1].memory) {
				t.Fatal("system bus memory differs")
			}
			if jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
				t.Fatal("system loop accelerated")
			}
		}
		words := shiftBoundLoopProgram(2, 5)
		if fill {
			words = bytePairFillLoopProgram(4, 2, 6, 5)
		}
		precise := newGenericLoopTestBackend(t, New(), words)
		jit := newGenericLoopTestBackend(t, NewJIT(), words)
		for _, b := range []*Backend{precise, jit} {
			check(t, b.SetPCHistoryLimit(128))
			check(t, b.WriteRegister(2, 1))
			check(t, b.WriteRegister(5, 0xffff))
			if fill {
				check(t, b.WriteRegister(4, 0x3001))
			}
		}
		runGenericLoopOracle(t, precise, jit, 0x1000, 21)
		if jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
			t.Fatal("traced loop accelerated")
		}
	}
}
