package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

var thumbScalarStoreOpcodes = []uint16{0x5088, 0x5288, 0x5488, 0x6008, 0x7008, 0x8008, 0x9000}

func resetThumbScalarStoreOracle(t *testing.T, precise, jit *Backend, raw uint16, address uint32, alignment, warm, traced bool) {
	t.Helper()
	for _, b := range []*Backend{precise, jit} {
		check(t, b.WriteMemory(0x1000, thumbTestCode(raw, 0x3301, 0xbe00)))
		for index := 1; index < len(b.regions); index++ {
			for offset := range b.regions[index].data {
				b.regions[index].data[offset] = byte(offset*17 + index*31)
			}
		}
		for register := uint32(0); register < cpu.RegisterPC; register++ {
			check(t, b.WriteRegister(register, 0x55000000+register*0x101))
		}
		check(t, b.WriteRegister(cpu.RegisterR0, 0x89abcdef))
		check(t, b.WriteRegister(cpu.RegisterR1, address))
		check(t, b.WriteRegister(cpu.RegisterR2, 0))
		check(t, b.WriteRegister(cpu.RegisterSP, address))
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
		b.clearDataCaches()
		if warm {
			for index := range b.regions {
				b.cacheData(&b.regions[index], b.regions[index].address, cpu.PermissionWrite)
			}
		}
	}
	block := jit.jitBlockAt(0x1000)
	if block == nil || len(block.thumb) != 3 || block.extraLoop != nil || block.countedLoop != nil {
		t.Fatal("store oracle did not form a complete ordinary application block")
	}
}

func assertThumbScalarStoreParity(t *testing.T, precise, jit *Backend, budget uint64) {
	t.Helper()
	want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
	var got cpu.Result
	if jit.nativeBlocks == nil {
		got = jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
	} else {
		// Exercise portable execution while a native translation is resident.
		// Backend.Run normally selects native execution in this configuration.
		jit.mode = cpu.ModeThumb
		jit.setModeFlag()
		jit.regs[cpu.RegisterPC] = 0x1000
		retired, reason, err := jit.runThumbJIT(budget)
		got = cpu.Result{Reason: cpu.StopBudget, Instructions: retired, PC: jit.regs[cpu.RegisterPC], Err: err}
		if reason != nil {
			got.Reason = *reason
		} else if err != nil {
			got.Reason = cpu.StopFault
		}
	}
	if got.Reason != want.Reason || got.Instructions != want.Instructions || got.PC != want.PC || !sameError(got.Err, want.Err) {
		t.Fatalf("budget %d: got=%+v want=%+v", budget, got, want)
	}
	for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
		actual := immediateMemoryRegisterValue(t, jit, register)
		expected := immediateMemoryRegisterValue(t, precise, register)
		if actual != expected {
			t.Fatalf("budget %d register %d: got=%08x want=%08x", budget, register, actual, expected)
		}
	}
	for index := range precise.regions {
		if !bytes.Equal(precise.regions[index].data, jit.regions[index].data) {
			t.Fatalf("budget %d memory at %08x differs", budget, precise.regions[index].address)
		}
	}
	if !reflect.DeepEqual(precise.pcHits, jit.pcHits) || precise.pcHistoryNext != jit.pcHistoryNext || !reflect.DeepEqual(precise.pcHistory, jit.pcHistory) {
		t.Fatal("instruction hits/history differ")
	}
}

// Independently fetch and execute the architectural instructions using New().
// Budget=3 must reach the generated full-application executor; budgets 1 and 2
// preserve the guarded partial path. Cache misses and crossings must fall back
// before any write, including a crossing whose first bytes commit before fault.
func TestThumbScalarStoreApplicationMatchesPrecise(t *testing.T) {
	for _, permission := range []cpu.Permissions{cpu.PermissionRead, cpu.PermissionWrite, cpu.PermissionRead | cpu.PermissionWrite, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute} {
		for _, nextPermission := range []cpu.Permissions{0, cpu.PermissionRead, cpu.PermissionRead | cpu.PermissionWrite} {
			precise, jit := New(), NewJIT()
			for _, b := range []*Backend{precise, jit} {
				t.Cleanup(func() { _ = b.Close() })
				check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, b.Map(0x3000, 16, permission))
				if nextPermission != 0 {
					check(t, b.Map(0x3010, 16, nextPermission))
				}
			}
			for _, raw := range thumbScalarStoreOpcodes {
				for _, address := range []uint32{0x2fff, 0x3000, 0x3001, 0x300d, 0x300f, 0x3010, 0x301f, 0x3020} {
					for _, alignment := range []bool{false, true} {
						for _, warm := range []bool{false, true} {
							for _, traced := range []bool{false, true} {
								t.Run(fmt.Sprintf("%04x/address=%x/perm=%d/next=%d/align=%t/warm=%t/trace=%t", raw, address, permission, nextPermission, alignment, warm, traced), func(t *testing.T) {
									for budget := uint64(1); budget <= 3; budget++ {
										resetThumbScalarStoreOracle(t, precise, jit, raw, address, alignment, warm, traced)
										generation := jit.jitGen
										assertThumbScalarStoreParity(t, precise, jit, budget)
										if jit.jitGen != generation {
											t.Fatal("non-code store changed portable code generation")
										}
									}
								})
							}
						}
					}
				}
			}
		}
	}
}

func TestThumbScalarStoreTopAddressApplicationMatchesPrecise(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.Map(0xfffffff0, 16, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	}
	for _, raw := range thumbScalarStoreOpcodes {
		for _, address := range []uint32{0xffffffef, 0xfffffff0, 0xfffffff1, 0xfffffffc, 0xfffffffd, 0xfffffffe, 0xffffffff, 0} {
			for _, alignment := range []bool{false, true} {
				for _, warm := range []bool{false, true} {
					for budget := uint64(1); budget <= 3; budget++ {
						resetThumbScalarStoreOracle(t, precise, jit, raw, address, alignment, warm, false)
						assertThumbScalarStoreParity(t, precise, jit, budget)
					}
				}
			}
		}
	}
}

func TestThumbScalarStoreApplicationRetainsFallbacks(t *testing.T) {
	for _, raw := range thumbScalarStoreOpcodes {
		for _, fallback := range []string{"physical", "tlb", "native", "mapping"} {
			t.Run(fmt.Sprintf("%04x/%s", raw, fallback), func(t *testing.T) {
				precise, jit := New(), NewJIT()
				for _, b := range []*Backend{precise, jit} {
					t.Cleanup(func() { _ = b.Close() })
					check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					check(t, b.Map(0x3000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				}
				resetThumbScalarStoreOracle(t, precise, jit, raw, 0x3000, false, true, false)
				width := 4
				if raw == 0x5288 || raw == 0x8008 {
					width = 2
				} else if raw == 0x5488 || raw == 0x7008 {
					width = 1
				}
				switch fallback {
				case "physical":
					jit.physicalAccess = true
				case "tlb":
					jit.tlb = make([]tlbEntry, nativeTLBEntries*2)
					jit.tlbClear()
					// A missing native bitmap conservatively forbids every
					// write TLB entry. Supply the empty bitmap used by native JIT.
					jit.nativeCodePages = make([]uint64, nativeCodePageWords)
				case "native":
					jit.nativeBlocks = make(map[uint32]*nativeBlock)
					jit.nativeBlockPages = make(blockPageIndex)
					jit.nativeCodeLo, jit.nativeCodeHi = 0x3000, 0x3004
					jit.cacheNativeBlock(0x3000, &nativeBlock{start: 0x3000, end: 0x3004, mode: cpu.ModeThumb})
				case "mapping":
					for _, b := range []*Backend{precise, jit} {
						check(t, b.Map(0x4000, 16, cpu.PermissionRead))
						if _, _, _, hit := b.privateDataHit(0x3000, width, cpu.PermissionWrite); hit {
							t.Fatal("mapping change retained the private cache")
						}
					}
				}
				assertThumbScalarStoreParity(t, precise, jit, 3)
				switch fallback {
				case "physical":
					if !bytes.Equal(jit.writeScratch[:width], []byte{0xef, 0xcd, 0xab, 0x89}[:width]) {
						t.Fatal("physical fallback did not encode the scalar write")
					}
				case "tlb":
					if !jit.tlbHit(0x3000, cpu.PermissionWrite) {
						t.Fatal("store bypassed native TLB bookkeeping")
					}
				case "native":
					if _, present := jit.nativeBlocks[0x3000]; present || jit.nativeGen == 0 {
						t.Fatal("store failed to invalidate a resident native translation")
					}
				}
			})
		}
	}
}
