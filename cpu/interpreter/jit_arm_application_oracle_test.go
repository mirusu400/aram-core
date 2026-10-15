package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func armApplicationCode(words ...uint32) []byte {
	data := make([]byte, len(words)*4)
	for index, word := range words {
		binary.LittleEndian.PutUint32(data[index*4:], word)
	}
	return data
}

func armApplicationOraclePair(t *testing.T, traced bool) (*Backend, *Backend) {
	t.Helper()
	precise, jit := New(), NewJIT()
	for _, backend := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = backend.Close() })
		mapCodeAndStack(t, backend)
		check(t, backend.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
		if traced {
			backend.pcHits = make(map[uint32]uint64)
			backend.pcHistory = make([]uint32, 32)
		}
	}
	return precise, jit
}

// New() fetches and decodes every instruction independently of ARM JIT blocks.
func assertARMApplicationOracle(t *testing.T, precise, jit *Backend, pc uint32, budget uint64) {
	t.Helper()
	want := precise.Run(context.Background(), pc, cpu.ModeARM, budget)
	got := jit.Run(context.Background(), pc, cpu.ModeARM, budget)
	if got.Reason != want.Reason || got.Instructions != want.Instructions || got.PC != want.PC || !sameError(got.Err, want.Err) {
		t.Fatalf("result=%+v want=%+v", got, want)
	}
	for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
		if got, want := armOracleRegisterValue(t, jit, register), armOracleRegisterValue(t, precise, register); got != want {
			t.Fatalf("register %d=%#x want=%#x", register, got, want)
		}
	}
	for index, region := range precise.regions {
		if !bytes.Equal(region.data, jit.regions[index].data) {
			t.Fatalf("memory differs at %#x", region.address)
		}
	}
	if !reflect.DeepEqual(precise.pcHits, jit.pcHits) || !reflect.DeepEqual(precise.PCHistory(), jit.PCHistory()) {
		t.Fatal("instruction trace differs")
	}
}

func armOracleRegisterValue(t *testing.T, backend *Backend, id uint32) uint32 {
	t.Helper()
	value, err := backend.ReadRegister(id)
	check(t, err)
	return value
}

func TestARMApplicationSelfModifyMatchesPrecise(t *testing.T) {
	for _, writer := range []uint32{0xe5810000, 0xe5c10000, 0xe1c100b0, 0xe8a10001} {
		for _, traced := range []bool{false, true} {
			for budget := uint64(1); budget <= 5; budget++ {
				t.Run(fmt.Sprintf("writer=%08x/trace=%t/budget=%d", writer, traced, budget), func(t *testing.T) {
					precise, jit := armApplicationOraclePair(t, traced)
					for _, backend := range []*Backend{precise, jit} {
						check(t, backend.WriteMemory(0x1000, armApplicationCode(writer, 0xe1a02008, 0xe3a03001, 0xe1200070)))
						check(t, backend.WriteRegister(cpu.RegisterR0, 0xe3a03002))
						check(t, backend.WriteRegister(cpu.RegisterR1, 0x1008))
						check(t, backend.WriteRegister(cpu.RegisterR8, 0x89abcdef))
					}
					assertARMApplicationOracle(t, precise, jit, 0x1000, budget)
				})
			}
		}
	}
}

func TestARMApplicationTopAddressRewriteMatchesPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		t.Run(fmt.Sprintf("trace=%t", traced), func(t *testing.T) {
			precise, jit := armApplicationOraclePair(t, traced)
			for _, backend := range []*Backend{precise, jit} {
				check(t, backend.Map(0xfffffffc, 4, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, backend.WriteMemory(0xfffffffc, armApplicationCode(0xe3a03001)))
			}
			assertARMApplicationOracle(t, precise, jit, 0xfffffffc, 1)
			for _, backend := range []*Backend{precise, jit} {
				check(t, backend.WriteMemory(0xfffffffc, armApplicationCode(0xe3a03002)))
			}
			assertARMApplicationOracle(t, precise, jit, 0xfffffffc, 1)
		})
	}
}

func TestARMApplicationRegisterOffsetsMatchPrecise(t *testing.T) {
	for _, shift := range []uint32{0, 1, 2, 3} {
		for _, amount := range []uint32{0, 1, 31} {
			for _, carry := range []uint32{0, flagC} {
				for controls := uint32(0); controls < 32; controls++ {
					// All load/store, byte/word, pre/post, direction and writeback forms.
					instruction := uint32(0xe6010002) | shift<<5 | amount<<7 | controls<<20
					for _, rd := range []uint32{0, 1, cpu.RegisterPC} {
						instruction := instruction | rd<<12
						for _, budget := range []uint64{1, 2} {
							t.Run(fmt.Sprintf("%08x/carry=%08x/budget=%d", instruction, carry, budget), func(t *testing.T) {
								precise, jit := armApplicationOraclePair(t, false)
								for _, backend := range []*Backend{precise, jit} {
									check(t, backend.WriteMemory(0x1000, armApplicationCode(instruction, 0xe1200070)))
									check(t, backend.WriteMemory(0x2000, armApplicationCode(0x1008, 0x1008, 0x1008)))
									check(t, backend.WriteRegister(cpu.RegisterR0, 0xdeadbeef))
									check(t, backend.WriteRegister(cpu.RegisterR1, 0x2004))
									check(t, backend.WriteRegister(cpu.RegisterR2, 4))
									check(t, backend.WriteRegister(cpu.RegisterCPSR, carry|flagV|uint32(processorModeSystem)))
									check(t, backend.WriteRegister(cpu.RegisterPC, 0x1000))
								}
								block := jit.armJITBlockAt(0x1000)
								if block == nil || block.arm[0].op != armMicroSingleTransfer {
									t.Fatal("register-offset instruction did not enter ARM micro-op tier")
								}
								assertARMApplicationOracle(t, precise, jit, 0x1000, budget)
							})
						}
					}
				}
			}
		}
	}
}

func TestARMApplicationSuccessorsAndBudgetCutsMatchPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		for budget := uint64(1); budget <= 274; budget++ {
			t.Run(fmt.Sprintf("trace=%t/budget=%d", traced, budget), func(t *testing.T) {
				precise, jit := armApplicationOraclePair(t, traced)
				for _, backend := range []*Backend{precise, jit} {
					check(t, backend.WriteMemory(0x1000, armApplicationCode(
						0xe2800001, // add r0,r0,#1
						0xe2511001, // subs r1,r1,#1
						0x1afffffc, // bne 0x1000
						0xe1200070,
					)))
					check(t, backend.WriteRegister(cpu.RegisterR1, 90))
				}
				assertARMApplicationOracle(t, precise, jit, 0x1000, budget)
			})
		}
	}
}

func TestARMApplicationSuccessorCacheInvalidates(t *testing.T) {
	precise, jit := armApplicationOraclePair(t, false)
	for _, backend := range []*Backend{precise, jit} {
		check(t, backend.WriteMemory(0x1000, armApplicationCode(
			0xe2800001, 0xea000000, 0xe1200070, // add; b 0x100c; bkpt
			0xe2511001, 0x1afffffa, 0xe1200070, // subs; bne 0x1000; bkpt
		)))
		check(t, backend.WriteRegister(cpu.RegisterR1, 10))
	}
	assertARMApplicationOracle(t, precise, jit, 0x1000, 8)
	first, second := jit.armJITBlockAt(0x1000), jit.armJITBlockAt(0x100c)
	linked := func(from, to *jitBlock) bool {
		return from != nil && from.successorGen == jit.jitGen && (from.successors[0] == to || from.successors[1] == to)
	}
	if !linked(first, second) || !linked(second, first) {
		t.Fatal("test did not warm both successor edges")
	}
	for _, backend := range []*Backend{precise, jit} {
		check(t, backend.WriteMemory(0x1000, armApplicationCode(0xe2800002)))
	}
	// Enter the still-live predecessor whose cached target was rewritten.
	assertARMApplicationOracle(t, precise, jit, 0x100c, 4)
	if got := armOracleRegisterValue(t, jit, cpu.RegisterR0); got != 4 {
		t.Fatalf("linked target retained stale ADD: r0=%d", got)
	}
}

func TestARMApplicationTrapsDisableLoopAcceleration(t *testing.T) {
	program := countedLoopTestPrograms()[1] // ARM SUBS/BNE, independently decoded by New().
	for _, trapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("traps=%t", trapped), func(t *testing.T) {
			precise := configureApplicationCountedLoop(t, New, program, 1000)
			jit := configureApplicationCountedLoop(t, newLoopAccelerationBackend, program, 1000)
			if trapped {
				for _, backend := range []*Backend{precise, jit} {
					check(t, backend.SetExecutionTraps([]cpu.ExecutionTrap{{Address: 0x1004, Mode: cpu.ModeARM}}))
				}
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeARM, runBatchInstructions)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeARM, runBatchInstructions)
			assertLoopBackendParity(t, precise, jit, want, got)
			statistics := jit.ExecutionStatistics()
			if trapped && (statistics.AcceleratedLoopIterations != 0 || statistics.AcceleratedLoopInstructions != 0) {
				t.Fatalf("trap-configured application accelerated: %+v", statistics)
			}
			if !trapped && statistics.AcceleratedLoopIterations == 0 {
				t.Fatal("positive control did not exercise the ARM counted-loop accelerator")
			}
		})
	}
}

func TestARMApplicationSelfModifyPartialStoreFaultMatchesPrecise(t *testing.T) {
	for _, traced := range []bool{false, true} {
		for budget := uint64(1); budget <= 3; budget++ {
			t.Run(fmt.Sprintf("trace=%t/budget=%d", traced, budget), func(t *testing.T) {
				precise, jit := New(), NewJIT()
				for _, backend := range []*Backend{precise, jit} {
					t.Cleanup(func() { _ = backend.Close() })
					check(t, backend.Map(0x1000, 8, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					check(t, backend.WriteMemory(0x1000, armApplicationCode(0xe8a20003, 0xe3a03001)))
					check(t, backend.WriteRegister(cpu.RegisterR0, 0xe3a03002))
					check(t, backend.WriteRegister(cpu.RegisterR1, 0xdeadbeef))
					check(t, backend.WriteRegister(cpu.RegisterR2, 0x1004))
					if traced {
						backend.pcHits = make(map[uint32]uint64)
						backend.pcHistory = make([]uint32, 32)
					}
				}
				assertARMApplicationOracle(t, precise, jit, 0x1000, budget)
				if got := armOracleRegisterValue(t, jit, cpu.RegisterR2); got != 0x1004 {
					t.Fatalf("partial STM fault committed writeback: %#x", got)
				}
			})
		}
	}
}
