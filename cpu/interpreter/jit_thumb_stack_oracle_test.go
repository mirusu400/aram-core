package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func newThumbStackApplicationOracle(t *testing.T, permission cpu.Permissions) (*Backend, *Backend) {
	t.Helper()
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.Map(0x3000, 0x80, permission))
	}
	return precise, jit
}

func resetThumbStackApplicationOracle(t *testing.T, precise, jit *Backend, instruction uint16, sp uint32, alignment, warm, traced bool) int {
	t.Helper()
	program := []uint16{0x3401, instruction} // ADDS r4,#1; PUSH/POP
	if instruction&0xfe00 == 0xb400 {
		program = append(program, 0x4642, 0x2f00, 0xbe00) // MOV r2,r8; CMP r7,#0; BKPT
	}
	for _, b := range []*Backend{precise, jit} {
		check(t, b.WriteMemory(0x1000, thumbTestCode(program...)))
		for register := uint32(0); register < cpu.RegisterPC; register++ {
			check(t, b.WriteRegister(register, 0x55000000+register*0x101))
		}
		check(t, b.WriteRegister(cpu.RegisterSP, sp))
		check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
		b.cp15.control &^= 2
		if alignment {
			b.cp15.control |= 2
		}
		b.pcHits, b.pcHistory = nil, nil
		b.pcHistoryNext = 0
		if traced {
			b.pcHits = make(map[uint32]uint64)
			b.pcHistory = make([]uint32, 16)
		}
		for offset := 0; offset < len(b.regions[1].data); offset += 4 {
			// Both Thumb and ARM return targets are exercised. No following
			// instruction executes when the POP consumes the complete budget.
			binary.LittleEndian.PutUint32(b.regions[1].data[offset:], uint32(0x1800+offset)|uint32(offset>>2&1))
		}
		b.clearDataCaches()
		if warm {
			b.cacheData(&b.regions[1], 0x3000, cpu.PermissionRead)
			b.cacheData(&b.regions[1], 0x3000, cpu.PermissionWrite)
		}
	}
	// A single nonterminal opcode with budget=1 tests only the guarded
	// partial executor. Insist that the full-budget case reaches the generated
	// application executor, with no independently recognized accelerator.
	block := jit.jitBlockAt(0x1000)
	if block == nil || len(block.thumb) != len(program) || block.countedLoop != nil || block.extraLoop != nil {
		t.Fatalf("not an ordinary complete application block: %+v", block)
	}
	return len(program)
}

func TestThumbStackApplicationAllMasksMatchPrecise(t *testing.T) {
	precise, jit := newThumbStackApplicationOracle(t, cpu.PermissionRead|cpu.PermissionWrite)
	for _, opcode := range []uint16{0xb400, 0xbc00} {
		for mask := uint16(0); mask < 0x200; mask++ {
			t.Run(fmt.Sprintf("%04x", opcode|mask), func(t *testing.T) {
				for _, alignment := range []bool{false, true} {
					for _, full := range []bool{false, true} {
						length := resetThumbStackApplicationOracle(t, precise, jit, opcode|mask, 0x3040+uint32(mask&3), alignment, true, false)
						budget := uint64(1)
						if full {
							budget = uint64(length)
						}
						assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
					}
				}
			})
		}
	}
}

func TestThumbStackApplicationFaultsAndBudgetCutsMatchPrecise(t *testing.T) {
	for _, permission := range []cpu.Permissions{cpu.PermissionRead, cpu.PermissionWrite, cpu.PermissionRead | cpu.PermissionWrite} {
		precise, jit := newThumbStackApplicationOracle(t, permission)
		for _, instruction := range []uint16{0xb403, 0xb530, 0xb5ff, 0xbc03, 0xbd30, 0xbdff} {
			for _, sp := range []uint32{0x3000, 0x3001, 0x3004, 0x307c, 0x3080, 0, 0xfffffffc} {
				for _, warm := range []bool{false, true} {
					for _, traced := range []bool{false, true} {
						t.Run(fmt.Sprintf("%04x/sp=%x/perm=%d/warm=%t/trace=%t", instruction, sp, permission, warm, traced), func(t *testing.T) {
							for budget := uint64(1); budget <= 5; budget++ {
								length := resetThumbStackApplicationOracle(t, precise, jit, instruction, sp, false, warm, traced)
								if budget > uint64(length) {
									break
								}
								assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
							}
						})
					}
				}
			}
		}
	}
}

func TestThumbStackTopAddressApplicationMatchesPrecise(t *testing.T) {
	for _, test := range []struct {
		instruction uint16
		sp          uint32
	}{
		{0xb4ff, 0},          // The complete push ends exactly at 2^32.
		{0xb403, 4},          // The first store succeeds; the wrapped second faults.
		{0xbcff, 0xffffffe0}, // Complete pop writeback wraps to zero.
		{0xbd01, 0xfffffffc}, // The first load succeeds; the PC load at zero faults.
	} {
		t.Run(fmt.Sprintf("%04x/sp=%x", test.instruction, test.sp), func(t *testing.T) {
			precise, jit := New(), NewJIT()
			for _, b := range []*Backend{precise, jit} {
				t.Cleanup(func() { _ = b.Close() })
				check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, b.Map(0xffffffe0, 32, cpu.PermissionRead|cpu.PermissionWrite))
			}
			for _, traced := range []bool{false, true} {
				for budget := uint64(1); budget <= 5; budget++ {
					length := resetThumbStackApplicationOracle(t, precise, jit, test.instruction, test.sp, false, false, traced)
					if budget > uint64(length) {
						break
					}
					for _, b := range []*Backend{precise, jit} {
						b.cacheData(&b.regions[1], 0xffffffe0, cpu.PermissionRead)
						b.cacheData(&b.regions[1], 0xffffffe0, cpu.PermissionWrite)
					}
					assertThumbSMCOracleParity(t, precise, jit, 0x1000, budget)
				}
			}
		})
	}
}
