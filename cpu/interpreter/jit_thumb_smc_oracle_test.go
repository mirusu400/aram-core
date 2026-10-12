package interpreter

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func newThumbSMCOraclePair(t *testing.T, wholeSystem, traced bool) (*Backend, *Backend) {
	t.Helper()
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		if wholeSystem {
			check(t, b.AttachSystemBus(&directTestSystemBus{base: 0x1000, data: make([]byte, 0x2000)}))
		} else {
			check(t, b.Map(0x1000, 0x2000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		}
		check(t, b.WriteRegister(cpu.RegisterR8, 0x89abcdef))
		check(t, b.WriteRegister(cpu.RegisterR4, 0x7fffffff))
		check(t, b.WriteRegister(cpu.RegisterR5, 0xffffffff))
		check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
		if traced {
			b.pcHits = make(map[uint32]uint64)
			b.pcHistory = make([]uint32, 16)
		}
	}
	return precise, jit
}

func assertThumbSMCOracleParity(t *testing.T, precise, jit *Backend, pc uint32, budget uint64) {
	t.Helper()
	assertThumbFillParity(t, precise, jit, pc, budget)
	if !reflect.DeepEqual(precise.pcHits, jit.pcHits) {
		t.Fatalf("instruction trace differs: precise=%v jit=%v", precise.pcHits, jit.pcHits)
	}
	if precise.systemBus != nil {
		want, got := make([]byte, 0x2000), make([]byte, 0x2000)
		check(t, precise.ReadMemory(0x1000, want))
		check(t, jit.ReadMemory(0x1000, got))
		if !bytes.Equal(want, got) {
			t.Fatal("physical code/data bytes differ")
		}
	}
}

// The precise executor fetches the prefix after STRH, independently of the
// translated executor. Refetching only the BL suffix cannot satisfy this oracle.
func TestThumbJITBranchPrefixSelfModifyMatchesPrecise(t *testing.T) {
	for _, replacement := range []uint16{0xf001, 0x2302, 0xde00} {
		for _, wholeSystem := range []bool{false, true} {
			for _, traced := range []bool{false, true} {
				for budget := uint64(1); budget <= 4; budget++ {
					t.Run(fmt.Sprintf("prefix=%04x/system=%t/trace=%t/budget=%d", replacement, wholeSystem, traced, budget), func(t *testing.T) {
						precise, jit := newThumbSMCOraclePair(t, wholeSystem, traced)
						for _, b := range []*Backend{precise, jit} {
							check(t, b.WriteMemory(0x1000, thumbTestCode(0x8008, 0xf000, 0xf800, 0xbe00)))
							check(t, b.WriteMemory(0x2006, thumbTestCode(0xbe00)))
							check(t, b.WriteRegister(cpu.RegisterR0, uint32(replacement)))
							check(t, b.WriteRegister(cpu.RegisterR1, 0x1002))
							if wholeSystem {
								check(t, b.SetExecutionTraps([]cpu.ExecutionTrap{
									{Address: 0x1004, Mode: cpu.ModeThumb}, // Not a boundary while it is a BL suffix.
									{Address: 0x2006, Mode: cpu.ModeThumb},
								}))
							}
						}
						generation := jit.jitGen
						assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
						if jit.jitGen == generation {
							t.Fatal("prefix write did not invalidate the translated code generation")
						}
					})
				}
			}
		}
	}
}

// Every translated writer can replace a not-yet-executed opcode beyond a
// non-branching high-register instruction. Pure ALU prefixes also exercise flag
// liveness at the new write-boundary exit, including every truncated budget.
func TestThumbJITHighRegisterSelfModifyMatchesPrecise(t *testing.T) {
	for _, writer := range []struct {
		name string
		raw  uint16
		sp   uint32
	}{
		{"str-register", 0x5088, 0x2800},
		{"strh-register", 0x5288, 0x2800},
		{"strb-register", 0x5488, 0x2800},
		{"str-immediate", 0x6008, 0x2800},
		{"strb-immediate", 0x7008, 0x2800},
		{"strh-immediate", 0x8008, 0x2800},
		{"str-stack", 0x9000, 0x1008},
		{"push", 0xb401, 0x100c},
		{"stm", 0xc101, 0x2800},
	} {
		for _, wholeSystem := range []bool{false, true} {
			for _, traced := range []bool{false, true} {
				for budget := uint64(1); budget <= 8; budget++ {
					t.Run(fmt.Sprintf("%s/system=%t/trace=%t/budget=%d", writer.name, wholeSystem, traced, budget), func(t *testing.T) {
						precise, jit := newThumbSMCOraclePair(t, wholeSystem, traced)
						for _, b := range []*Backend{precise, jit} {
							check(t, b.WriteMemory(0x1000, thumbTestCode(
								0x3401, 0x3502, // ADDS r4,#1; ADDS r5,#2
								writer.raw, 0x4642, // Store; MOV r2,r8
								0x2301, 0x2f00, 0xbe00, // MOVS r3,#1; CMP r7,#0; BKPT
							)))
							check(t, b.WriteRegister(cpu.RegisterR0, 0x2f002302))
							check(t, b.WriteRegister(cpu.RegisterR1, 0x1008))
							check(t, b.WriteRegister(cpu.RegisterR2, 0))
							check(t, b.WriteRegister(cpu.RegisterSP, writer.sp))
							if wholeSystem {
								check(t, b.SetExecutionTraps([]cpu.ExecutionTrap{{Address: 0x100c, Mode: cpu.ModeThumb}}))
							}
						}
						generation := jit.jitGen
						assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
						if budget >= 3 && jit.jitGen == generation {
							t.Fatal("writer did not invalidate the translated code generation")
						}
					})
				}
			}
		}
	}
}

// The first store invalidates the current block, but a second store in the
// same instruction faults. Do not turn that partial fault into a retired PUSH
// or STM, and do not commit its final SP/base writeback before the fault.
func TestThumbJITSelfModifyMultiStoreFaultMatchesPrecise(t *testing.T) {
	for _, writer := range []uint16{0xb403, 0xc203} { // PUSH {r0,r1}; STM r2!,{r0,r1}
		for _, traced := range []bool{false, true} {
			for budget := uint64(1); budget <= 5; budget++ {
				t.Run(fmt.Sprintf("writer=%04x/trace=%t/budget=%d", writer, traced, budget), func(t *testing.T) {
					precise, jit := New(), NewJIT()
					for _, b := range []*Backend{precise, jit} {
						t.Cleanup(func() { _ = b.Close() })
						check(t, b.Map(0x1000, 8, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
						check(t, b.WriteMemory(0x1000, thumbTestCode(0x3401, writer, 0x4643, 0x2501)))
						check(t, b.WriteRegister(cpu.RegisterR0, 0x25024643))
						check(t, b.WriteRegister(cpu.RegisterR1, 0xdeadbeef))
						check(t, b.WriteRegister(cpu.RegisterR2, 0x1004))
						check(t, b.WriteRegister(cpu.RegisterR4, 0x7fffffff))
						check(t, b.WriteRegister(cpu.RegisterSP, 0x100c))
						check(t, b.WriteRegister(cpu.RegisterCPSR, flagC|uint32(processorModeSystem)))
						if traced {
							b.pcHits = make(map[uint32]uint64)
							b.pcHistory = make([]uint32, 16)
						}
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterSP); got != 0x100c {
						t.Fatalf("fault committed stack writeback: %#x", got)
					}
					if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterR2); got != 0x1004 {
						t.Fatalf("fault committed base writeback: %#x", got)
					}
				})
			}
		}
	}
}

// The exclusive end 2^32 cannot be represented by jitBlock.end. Keeping such a
// block wraps its invalidation span and can retain a prefix after a host rewrite.
func TestThumbJITTopAddressRewriteMatchesPrecise(t *testing.T) {
	for _, test := range []struct {
		name      string
		pc        uint32
		code      []byte
		lowSuffix []byte
		rewritePC uint32
		rewrite   []byte
		budget    uint64
	}{
		{"bl-endpoint", 0xfffffffc, thumbTestCode(0xf000, 0xf800), nil, 0xfffffffc, thumbTestCode(0xf001), 1},
		{"bl-wrapping-suffix", 0xfffffffe, thumbTestCode(0xf000), thumbTestCode(0xf800), 0xfffffffe, thumbTestCode(0xf001), 1},
		{"single-endpoint", 0xfffffffe, thumbTestCode(0x2301), nil, 0xfffffffe, thumbTestCode(0x2302), 1},
		{"high-register-endpoint", 0xfffffffa, thumbTestCode(0x4642, 0x2301, 0xbe00), nil, 0xfffffffc, thumbTestCode(0x2302), 3},
	} {
		for _, traced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/trace=%t", test.name, traced), func(t *testing.T) {
				precise, jit := New(), NewJIT()
				for _, b := range []*Backend{precise, jit} {
					t.Cleanup(func() { _ = b.Close() })
					check(t, b.Map(test.pc, uint32(len(test.code)), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					check(t, b.WriteMemory(test.pc, test.code))
					if test.lowSuffix != nil {
						check(t, b.Map(0, uint32(len(test.lowSuffix)), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
						check(t, b.WriteMemory(0, test.lowSuffix))
					}
					check(t, b.WriteRegister(cpu.RegisterR8, 0x89abcdef))
					check(t, b.WriteRegister(cpu.RegisterCPSR, flagC|flagV|uint32(processorModeSystem)))
					if traced {
						b.pcHits = make(map[uint32]uint64)
						b.pcHistory = make([]uint32, 16)
					}
				}
				assertThumbSMCOracleParity(t, precise, jit, test.pc, test.budget)
				for _, b := range []*Backend{precise, jit} {
					check(t, b.WriteMemory(test.rewritePC, test.rewrite))
				}
				assertThumbSMCOracleParity(t, precise, jit, test.pc, test.budget)
			})
		}
	}
}

func TestThumbJITTopLineInstructionCacheMatchesPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		t.Run(fmt.Sprintf("trace=%t", traced), func(t *testing.T) {
			precise, jit := New(), NewJIT()
			for _, b := range []*Backend{precise, jit} {
				t.Cleanup(func() { _ = b.Close() })
				check(t, b.Map(0xffffffe0, 32, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, b.WriteMemory(0xfffffffa, thumbTestCode(0x4642, 0x2301, 0xbe00)))
				check(t, b.WriteRegister(cpu.RegisterR8, 0x89abcdef))
				b.setCP15Control(1 << 12)
				if traced {
					b.pcHits = make(map[uint32]uint64)
					b.pcHistory = make([]uint32, 16)
				}
			}
			assertThumbSMCOracleParity(t, precise, jit, 0xfffffffa, 3)
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0xfffffffc, thumbTestCode(0x2302)))
			}
			assertThumbSMCOracleParity(t, precise, jit, 0xfffffffa, 3)
			if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterR3); got != 1 {
				t.Fatalf("backing write replaced resident instruction: r3=%d", got)
			}
			for _, b := range []*Backend{precise, jit} {
				check(t, b.writeCP15(7, 5, 1, 0xfffffffc))
			}
			assertThumbSMCOracleParity(t, precise, jit, 0xfffffffa, 3)
			if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterR3); got != 2 {
				t.Fatalf("invalidation retained old instruction: r3=%d", got)
			}
		})
	}
}
