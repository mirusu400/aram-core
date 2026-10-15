package interpreter

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Synthetic assembly, independent of the production matcher. The earliest
// entry is the three-instruction prefix, not the later right-shift block.
func quotientRefineLoopProgram(quotient, divisor, remainder, original uint16) []uint16 {
	return []uint16{
		0x1800 | quotient<<6 | quotient<<3 | quotient, // ADDS q,q,q.
		0x4000 | 10<<6 | original<<3 | divisor,        // CMP divisor,original.
		0xd000 | 6,                                    // BEQ exit (after all ten words).
		0x0800 | 1<<6 | divisor<<3 | divisor,          // LSRS divisor,divisor,#1.
		0x4000 | 10<<6 | divisor<<3 | remainder,       // CMP remainder,divisor.
		0xd000 | 3<<8 | 0xf9,                          // BCC earliest entry.
		0x4000 | 5<<6 | quotient<<3 | quotient,        // ADCS q,q.
		0x1800 | 1<<9 | divisor<<6 | remainder<<3 | remainder,
		0x4000 | 10<<6 | original<<3 | divisor,
		0xd000 | 1<<8 | 0xf8, // BNE the right-shift block.
	}
}

func resetQuotientRefineState(t *testing.T, b *Backend, registers [4]uint16, values [4]uint32, flags uint32) {
	t.Helper()
	resetGenericLoopRegisters(t, b, flags)
	for index, register := range registers {
		check(t, b.WriteRegister(uint32(register), values[index]))
	}
}

func TestThumbQuotientRefineLoopAllRegistersAndBudgetCuts(t *testing.T) {
	precise := newGenericLoopTestBackend(t, New(), quotientRefineLoopProgram(0, 1, 2, 3))
	jit := newGenericLoopTestBackend(t, NewJIT(), quotientRefineLoopProgram(0, 1, 2, 3))
	for q := uint16(0); q < 8; q++ {
		for d := uint16(0); d < 8; d++ {
			for r := uint16(0); r < 8; r++ {
				for o := uint16(0); o < 8; o++ {
					if q == d || q == r || q == o || d == r || d == o || r == o {
						continue
					}
					program := append(quotientRefineLoopProgram(q, d, r, o), 0xbe00)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000, thumbTestCode(program...)))
					}
					block := jit.jitBlockAt(0x1000)
					if block == nil || block.extraLoop == nil || block.extraLoop.kind != jitQuotientRefineLoop || block.end != 0x1014 || len(block.thumb) != 3 {
						t.Fatalf("registers %v not recognized with exact decoded dependency span", [4]uint16{q, d, r, o})
					}
					for budget := uint64(1); budget <= 31; budget++ {
						for _, b := range []*Backend{precise, jit} {
							resetQuotientRefineState(t, b, [4]uint16{q, d, r, o}, [4]uint32{0x80000001, 16, 9, 1}, uint32(q+d+r+o)%16<<28)
						}
						before := jit.ExecutionStatistics().AcceleratedLoopInstructions
						runGenericLoopOracle(t, precise, jit, 0x1000, budget)
						if budget >= 3 && jit.ExecutionStatistics().AcceleratedLoopInstructions == before {
							t.Fatalf("registers %v budget=%d did not accelerate the initial prefix", [4]uint16{q, d, r, o}, budget)
						}
					}
				}
			}
		}
	}
}

func TestThumbQuotientRefineLoopFlagsZeroOddUnreachableAndWrap(t *testing.T) {
	registers := [4]uint16{7, 2, 5, 0}
	precise := newGenericLoopTestBackend(t, New(), quotientRefineLoopProgram(registers[0], registers[1], registers[2], registers[3]))
	jit := newGenericLoopTestBackend(t, NewJIT(), quotientRefineLoopProgram(registers[0], registers[1], registers[2], registers[3]))
	for flags := uint32(0); flags < 16; flags++ {
		for _, quotient := range []uint32{0, 1, 0x7fffffff, 0x80000000, ^uint32(0)} {
			for _, state := range [][3]uint32{{0, 0, 0}, {0, ^uint32(0), 0}, {1, 0, 1}, {4, 0, 1}, {4, 2, 1}, {4, ^uint32(0), 1}, {0, 0, 1}, {1, 0, 3}, {5, 3, 3}, {7, 1, 2}, {0x80000000, 0x7fffffff, 1}, {^uint32(0), ^uint32(0), 1}, {0x80000001, 0x80000000, 1}, {^uint32(0), ^uint32(0), ^uint32(0)}, {1, 0, ^uint32(0)}} {
				for budget := uint64(1); budget <= 40; budget++ {
					for _, b := range []*Backend{precise, jit} {
						resetQuotientRefineState(t, b, registers, [4]uint32{quotient, state[0], state[1], state[2]}, flags<<28)
					}
					runGenericLoopOracle(t, precise, jit, 0x1000, budget)
				}
			}
		}
	}
}

func TestThumbQuotientRefineLoopExactPathRetirementAndDecline(t *testing.T) {
	for _, test := range []struct {
		name                   string
		values                 [4]uint32
		budget, retired, units uint64
		want                   [4]uint32
		pc                     uint32
	}{
		{"zero-equality-prefix", [4]uint32{0x80000001, 0, 99, 0}, 3, 3, 1, [4]uint32{2, 0, 99, 0}, 0x1014},
		{"nonzero-equality-prefix", [4]uint32{^uint32(0), 5, 1, 5}, 30, 3, 1, [4]uint32{0xfffffffe, 5, 1, 5}, 0x1014},
		{"low-short", [4]uint32{1, 4, 0, 1}, 8, 3, 1, [4]uint32{2, 4, 0, 1}, 0x1006},
		{"low-complete", [4]uint32{1, 4, 0, 1}, 9, 9, 2, [4]uint32{4, 2, 0, 1}, 0x1006},
		{"high-short", [4]uint32{1, 4, 2, 1}, 9, 3, 1, [4]uint32{2, 4, 2, 1}, 0x1006},
		{"high-complete", [4]uint32{1, 4, 2, 1}, 10, 10, 2, [4]uint32{5, 2, 0, 1}, 0x1006},
		{"low-then-low-exit", [4]uint32{1, 4, 0, 1}, 15, 15, 3, [4]uint32{8, 1, 0, 1}, 0x1014},
		{"high-then-low-exit", [4]uint32{1, 4, 2, 1}, 16, 16, 3, [4]uint32{10, 1, 0, 1}, 0x1014},
		{"adc-wrap-carry-from-cmp", [4]uint32{^uint32(0), 4, 2, 1}, 10, 10, 2, [4]uint32{0xfffffffd, 2, 0, 1}, 0x1006},
	} {
		t.Run(test.name, func(t *testing.T) {
			precise := newGenericLoopTestBackend(t, New(), quotientRefineLoopProgram(0, 1, 2, 3))
			jit := newGenericLoopTestBackend(t, NewJIT(), quotientRefineLoopProgram(0, 1, 2, 3))
			for _, b := range []*Backend{precise, jit} {
				resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, test.values, flagN|flagZ|flagV)
			}
			loop := jit.classifyThumbQuotientRefineLoop(jit.jitBlockAt(0x1000))
			if retired := jit.accelerateThumbQuotientRefineLoop(loop, test.budget, false, false, false); retired != test.retired {
				t.Fatalf("retired %d want %d", retired, test.retired)
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, test.retired)
			assertGenericLoopResults(t, precise, jit, want, cpu.Result{Reason: cpu.StopBudget, Instructions: test.retired, PC: jit.regs[cpu.RegisterPC]})
			for index, value := range test.want {
				if got := registerValueForGenericLoop(t, jit, uint32(index)); got != value {
					t.Fatalf("register %d got %08x want %08x", index, got, value)
				}
			}
			if jit.regs[cpu.RegisterPC] != test.pc || jit.ExecutionStatistics().AcceleratedLoopIterations != test.units || jit.ExecutionStatistics().AcceleratedLoopInstructions != test.retired {
				t.Fatal("path PC or counter accounting differs")
			}
		})
	}
	b := newGenericLoopTestBackend(t, NewJIT(), quotientRefineLoopProgram(0, 1, 2, 3))
	loop := b.classifyThumbQuotientRefineLoop(b.jitBlockAt(0x1000))
	for _, observation := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		b.setNZCV(0, true, true)
		assertGenericLoopDecline(t, b, func() uint64 {
			return b.accelerateThumbQuotientRefineLoop(loop, 100, observation[0], observation[1], observation[2])
		})
	}
	for budget := uint64(0); budget < 3; budget++ {
		assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbQuotientRefineLoop(loop, budget, false, false, false) })
	}
	b.physicalAccess = true
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbQuotientRefineLoop(loop, 100, false, false, false) })
	b.physicalAccess = false
	b.tlb = []tlbEntry{{}}
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbQuotientRefineLoop(loop, 100, false, false, false) })
	b.tlb = nil
	check(t, b.WriteRegister(cpu.RegisterPC, 0x1006))
	assertGenericLoopDecline(t, b, func() uint64 { return b.accelerateThumbQuotientRefineLoop(loop, 100, false, false, false) })
}

func TestThumbQuotientRefineLoopRejectsNearMatchesAndObserverLookahead(t *testing.T) {
	words := quotientRefineLoopProgram(0, 1, 2, 3)
	for index := range words {
		changed := append([]uint16(nil), words...)
		changed[index] ^= 0x0100
		b := newGenericLoopTestBackend(t, NewJIT(), changed)
		if block := b.jitBlockAt(0x1000); block != nil && block.extraLoop != nil && block.extraLoop.kind == jitQuotientRefineLoop {
			t.Fatalf("mutated instruction %d accepted", index)
		}
	}
	for _, index := range []int{2, 5, 9} {
		changed := append([]uint16(nil), words...)
		changed[index]++
		b := newGenericLoopTestBackend(t, NewJIT(), changed)
		if block := b.jitBlockAt(0x1000); block != nil && block.extraLoop != nil && block.extraLoop.kind == jitQuotientRefineLoop {
			t.Fatalf("wrong branch target %d accepted", index)
		}
	}
	for _, registers := range [][4]uint16{{0, 0, 2, 3}, {0, 1, 0, 3}, {0, 1, 2, 0}, {0, 1, 1, 3}, {0, 1, 2, 1}, {0, 1, 2, 2}} {
		b := newGenericLoopTestBackend(t, NewJIT(), quotientRefineLoopProgram(registers[0], registers[1], registers[2], registers[3]))
		if block := b.jitBlockAt(0x1000); block != nil && block.extraLoop != nil && block.extraLoop.kind == jitQuotientRefineLoop {
			t.Fatalf("aliased registers %v accepted", registers)
		}
	}
	b := newGenericLoopTestBackend(t, NewJIT(), words)
	block := b.jitBlockAt(0x1000)
	for _, guard := range []string{"physical", "tlb", "system"} {
		b.executeData = nil
		switch guard {
		case "physical":
			b.physicalAccess = true
		case "tlb":
			b.tlb = []tlbEntry{{}}
		case "system":
			b.systemBus = &testSystemBus{}
		}
		if b.classifyThumbQuotientRefineLoop(block) != nil || b.executeData != nil {
			t.Fatalf("%s guard performed extra forward fetches", guard)
		}
		b.physicalAccess, b.tlb, b.systemBus = false, nil, nil
	}
	wrapped := &jitBlock{start: 0xffffffec, thumb: append([]thumbMicroInstr(nil), block.thumb...)}
	if b.classifyThumbQuotientRefineLoop(wrapped) != nil {
		t.Fatal("unrepresentable decoded dependency end accepted")
	}
}

func TestThumbQuotientRefineLoopForwardFetchFaultIsNotSpeculativeExecution(t *testing.T) {
	words := quotientRefineLoopProgram(0, 1, 2, 3)
	for present := 3; present < 10; present++ {
		for _, state := range [][4]uint32{{1, 4, ^uint32(0), 1}, {1, 4, 0, 1}, {0x80000001, 0, 9, 0}} {
			for _, budget := range []uint64{1, 2, 3, 4, 32} {
				precise, jit := New(), NewJIT()
				for _, b := range []*Backend{precise, jit} {
					t.Cleanup(func() { _ = b.Close() })
					check(t, b.Map(0x1000, uint32(present*2), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					check(t, b.Map(0x1014, 2, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					check(t, b.WriteMemory(0x1000, thumbTestCode(words[:present]...)))
					check(t, b.WriteMemory(0x1014, thumbTestCode(0xbe00)))
					resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, state, flagC|flagV)
				}
				want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
				got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
				assertGenericLoopResults(t, precise, jit, want, got)
				if jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
					t.Fatal("incomplete decoded dependency was accelerated")
				}
				if budget <= 3 && got.Err != nil {
					t.Fatal("lookahead fetch fault escaped before executing the missing word")
				}
				if budget == 32 && (state[1] == state[3] || present >= 6 && state[2] == 0) && got.Err != nil {
					t.Fatalf("unexecuted forward fetch fault escaped: present=%d state=%v got=%+v", present, state, got)
				}
				if budget == 32 && state[2] == ^uint32(0) && (got.Err == nil || got.Instructions != uint64(present)) {
					t.Fatalf("actual path fetch fault prefix differs: present=%d got=%+v", present, got)
				}
			}
		}
	}
}

func TestThumbQuotientRefineLoopOwnsEveryDecodedWordAcrossCodePages(t *testing.T) {
	words := quotientRefineLoopProgram(0, 1, 2, 3)
	for _, start := range []uint32{0x1000, 0x1ffa} {
		for index := range words {
			precise, jit := New(), NewJIT()
			for _, b := range []*Backend{precise, jit} {
				t.Cleanup(func() { _ = b.Close() })
				check(t, b.Map(start, 22, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, b.WriteMemory(start, thumbTestCode(append(append([]uint16(nil), words...), 0xbe00)...)))
				resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, [4]uint32{1, 4, ^uint32(0), 1}, flagV)
				check(t, b.WriteRegister(cpu.RegisterPC, start))
			}
			block := jit.jitBlockAt(start)
			if block == nil || block.extraLoop == nil || block.extraLoop.kind != jitQuotientRefineLoop || block.end != start+20 || len(block.thumb) != 3 || len(jit.jitBlocks) != 1 {
				t.Fatal("earliest block does not exclusively own the complete decoded dependency")
			}
			if !jit.hasJITCodePages(start+6, 14) {
				t.Fatal("forward words were not marked as translated code")
			}
			generation := jit.jitGen
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(start+uint32(index)*2, thumbTestCode(0xbe00)))
			}
			if jit.jitGen == generation {
				t.Fatalf("word %d on start %x did not invalidate earliest metadata", index, start)
			}
			if _, retained := jit.jitBlocks[start]; retained {
				t.Fatal("mutated dependency retained earliest decoded metadata")
			}
			runGenericLoopOracle(t, precise, jit, start, 32)
			if jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
				t.Fatal("mutated signature accelerated")
			}
		}
	}
}

func TestThumbQuotientRefineLoopRestartMutationAndRawCapture(t *testing.T) {
	words := quotientRefineLoopProgram(0, 1, 2, 3)
	for _, budget := range []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 15, 16, 17, 256} {
		for _, state := range [][4]uint32{{^uint32(0), 4, 0, 1}, {^uint32(0), 4, ^uint32(0), 0x80000000}, {^uint32(0), 0, 0, 1}} {
			precise := newGenericLoopTestBackend(t, New(), words)
			jit := newGenericLoopTestBackend(t, NewJIT(), words)
			for _, b := range []*Backend{precise, jit} {
				resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, state, flagN|flagZ|flagC|flagV)
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
			if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
				t.Fatalf("first cut %d got=%+v want=%+v", budget, got, want)
			}
			// No CPSR read, context save, or flag materialization is allowed here.
			if want.Reason != cpu.StopBudget {
				continue
			}
			if budget >= 3 && precise.regs[cpu.RegisterCPSR]&(flagN|flagZ|flagC|flagV) == flagN|flagZ|flagC|flagV {
				t.Fatal("raw capture fixture did not change precise stored flags")
			}
			for _, b := range []*Backend{precise, jit} {
				check(t, b.SetPCRegisterCapture(want.PC, 1))
			}
			want = precise.Run(context.Background(), want.PC, cpu.ModeThumb, 1)
			got = jit.Run(context.Background(), got.PC, cpu.ModeThumb, 1)
			if len(jit.PCRegisterCaptures()) != 1 {
				t.Fatal("capture was vacuous")
			}
			assertGenericLoopResults(t, precise, jit, want, got)
		}
	}
	for _, cut := range []uint64{3, 4, 6, 9, 10, 13} {
		for index := range words {
			precise := newGenericLoopTestBackend(t, New(), words)
			jit := newGenericLoopTestBackend(t, NewJIT(), words)
			for _, b := range []*Backend{precise, jit} {
				resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, [4]uint32{1, 16, 9, 1}, flagN|flagV)
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, cut)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, cut)
			if want.PC != got.PC {
				t.Fatal("restart PC differs")
			}
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x1000+uint32(index)*2, thumbTestCode(0xbe00)))
			}
			// Keep pending flags intact across both the write and the subsequent Run.
			want = precise.Run(context.Background(), want.PC, cpu.ModeThumb, 64)
			got = jit.Run(context.Background(), got.PC, cpu.ModeThumb, 64)
			assertGenericLoopResults(t, precise, jit, want, got)
		}
	}
	precise := newGenericLoopTestBackend(t, New(), words)
	jit := newGenericLoopTestBackend(t, NewJIT(), words)
	for _, b := range []*Backend{precise, jit} {
		resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, [4]uint32{0x80000000, 1, 0, 3}, flagN|flagZ|flagC|flagV)
	}
	pc := uint32(0x1000)
	var want, got cpu.Result
	for _, budget := range []uint64{1, 1, 1, 2, 6, 7, 9, 10, 15, 256, 1, 3, 7} {
		want = precise.Run(context.Background(), pc, cpu.ModeThumb, budget)
		got = jit.Run(context.Background(), pc, cpu.ModeThumb, budget)
		if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
			t.Fatalf("consecutive cut %d got=%+v want=%+v", budget, got, want)
		}
		pc = want.PC
	}
	assertGenericLoopResults(t, precise, jit, want, got)
}

func TestThumbQuotientRefineLoopCancelSystemTraceAndCacheGuards(t *testing.T) {
	words := quotientRefineLoopProgram(0, 1, 2, 3)
	for _, traced := range []bool{false, true} {
		precise := newGenericLoopTestBackend(t, New(), words)
		jit := newGenericLoopTestBackend(t, NewJIT(), words)
		for _, b := range []*Backend{precise, jit} {
			resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, [4]uint32{0x80000000, 5, 3, 3}, flagV)
			if traced {
				check(t, b.SetPCHistoryLimit(1024))
			}
		}
		want := precise.Run(&genericLoopSecondPollContext{}, 0x1000, cpu.ModeThumb, 1024)
		got := jit.Run(&genericLoopSecondPollContext{}, 0x1000, cpu.ModeThumb, 1024)
		if got.Instructions != 256 || got.Reason != cpu.StopRequested {
			t.Fatalf("256 cancellation got=%+v", got)
		}
		assertGenericLoopResults(t, precise, jit, want, got)
		if traced && jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
			t.Fatal("traced loop accelerated")
		}
	}
	for _, trapped := range []bool{false, true} {
		precise, jit := New(), NewJIT()
		buses := []*testSystemBus{{memory: make(map[uint32]byte)}, {memory: make(map[uint32]byte)}}
		for index, b := range []*Backend{precise, jit} {
			t.Cleanup(func() { _ = b.Close() })
			buses[index].writeRaw(0x1000, thumbTestCode(append(append([]uint16(nil), words...), 0xbe00)...))
			check(t, b.AttachSystemBus(buses[index]))
			resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, [4]uint32{1, 4, 2, 1}, flagN|flagV)
			if trapped {
				check(t, b.SetExecutionTraps([]cpu.ExecutionTrap{{Mode: cpu.ModeThumb, Address: 0x1006}}))
			}
		}
		runGenericLoopOracle(t, precise, jit, 0x1000, 32)
		if !reflect.DeepEqual(buses[0].memory, buses[1].memory) || jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
			t.Fatal("system bus memory or acceleration differs")
		}
	}
	for _, budget := range []uint64{1, 3, 9, 10, 32} {
		// Exercise the functional cache through its system-bus contract. The
		// private no-lookahead guard itself is checked independently above.
		precise, jit := New(), NewJIT()
		buses := []*testSystemBus{{memory: make(map[uint32]byte)}, {memory: make(map[uint32]byte)}}
		for index, b := range []*Backend{precise, jit} {
			t.Cleanup(func() { _ = b.Close() })
			buses[index].writeRaw(0x1000, thumbTestCode(append(append([]uint16(nil), words...), 0xbe00)...))
			check(t, b.AttachSystemBus(buses[index]))
			resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, [4]uint32{1, 4, 2, 1}, flagC|flagV)
			b.setCP15Control(1 << 12)
		}
		runGenericLoopOracle(t, precise, jit, 0x1000, budget)
		if block := jit.jitBlockAt(0x1000); block != nil && block.extraLoop != nil && block.extraLoop.kind == jitQuotientRefineLoop {
			t.Fatal("functional instruction cache performed forward recognition")
		}
		if jit.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
			t.Fatal("I-cache loop accelerated")
		}
	}
}

func TestThumbQuotientRefineLoopProfileAccounting(t *testing.T) {
	for _, test := range []struct {
		values [4]uint32
		budget uint64
	}{
		{[4]uint32{1, 0, 9, 0}, 3}, // Initial equality still retires three virtual ops.
		{[4]uint32{1, 4, 0, 1}, 8}, // Prefix accelerated, low path partially interpreted.
		{[4]uint32{1, 4, 2, 1}, 9}, // Prefix accelerated, high path partially interpreted.
		{[4]uint32{1, 4, 0, 1}, 9},
		{[4]uint32{1, 4, 2, 1}, 10},
		{[4]uint32{1, 0, 0, 1}, 256},
	} {
		t.Run(fmt.Sprintf("values=%v/budget=%d", test.values, test.budget), func(t *testing.T) {
			jit := newGenericLoopTestBackend(t, NewJIT(), quotientRefineLoopProgram(0, 1, 2, 3))
			resetQuotientRefineState(t, jit, [4]uint16{0, 1, 2, 3}, test.values, flagN|flagZ|flagC|flagV)
			result := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, test.budget)
			stats := jit.ExecutionStatistics()
			if result.Err != nil || result.Instructions != test.budget || stats.AcceleratedLoopInstructions < 3 {
				t.Fatalf("retirement got=%+v stats=%+v", result, stats)
			}
			if !collectThumbMicro {
				if stats.ThumbMicro != nil {
					t.Fatal("normal build retained micro diagnostics")
				}
				return
			}
			var actualInstructions, trackedBlocks uint64
			if stats.ThumbMicro != nil {
				for _, count := range stats.ThumbMicro.Classes {
					actualInstructions += count
				}
				for _, count := range stats.ThumbMicro.Blocks {
					trackedBlocks += count
				}
				if trackedBlocks+stats.ThumbMicro.UntrackedBlockCalls != stats.ThumbMicro.BlockCalls {
					t.Fatal("block map diagnostic accounting differs")
				}
			}
			if actualInstructions+stats.AcceleratedLoopInstructions != result.Instructions {
				t.Fatalf("initial prefix or path was omitted/double-counted: actual=%d virtual=%d retired=%d", actualInstructions, stats.AcceleratedLoopInstructions, result.Instructions)
			}
		})
	}
}

func TestThumbQuotientRefineLoopChainedEntryAndUnmappedExit(t *testing.T) {
	words := quotientRefineLoopProgram(0, 1, 2, 3)
	// The ordinary application chain must return to the cold dispatcher before
	// entering an accelerated successor, rather than consuming its short prefix.
	program := append([]uint16{0x2001, 0xe005, 0xbe00, 0xbe00, 0xbe00, 0xbe00, 0xbe00, 0xbe00}, words...)
	precise := newGenericLoopTestBackend(t, New(), program)
	jit := newGenericLoopTestBackend(t, NewJIT(), program)
	for budget := uint64(1); budget <= 36; budget++ {
		for _, b := range []*Backend{precise, jit} {
			resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, [4]uint32{99, 16, 9, 1}, flagN|flagV)
		}
		before := jit.ExecutionStatistics().AcceleratedLoopInstructions
		runGenericLoopOracle(t, precise, jit, 0x1000, budget)
		if budget >= 5 && jit.ExecutionStatistics().AcceleratedLoopInstructions == before {
			t.Fatal("ordinary chain consumed an accelerated successor without using its dispatcher")
		}
	}
	for _, values := range [][4]uint32{{1, 0, 9, 0}, {1, 4, 0, 1}, {1, 4, 2, 1}} {
		precise, jit := New(), NewJIT()
		for _, b := range []*Backend{precise, jit} {
			t.Cleanup(func() { _ = b.Close() })
			check(t, b.Map(0x1000, 20, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
			resetQuotientRefineState(t, b, [4]uint16{0, 1, 2, 3}, values, flagC|flagV)
		}
		want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 32)
		got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 32)
		assertGenericLoopResults(t, precise, jit, want, got)
		if got.Err == nil || got.PC != 0x1014 || jit.ExecutionStatistics().AcceleratedLoopInstructions != got.Instructions {
			t.Fatalf("unmapped fallthrough did not retain all completed virtual instructions: %+v", got)
		}
	}
}
