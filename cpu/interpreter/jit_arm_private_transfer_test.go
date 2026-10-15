package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/bits"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Assemble synthetic ARM instructions independently of the JIT decoder.
func armPrivateTransferInstruction(pre, up, writeback, load bool, base uint32, mask uint16) uint32 {
	word := uint32(0xe8000000) | base<<16 | uint32(mask)
	for _, flag := range []struct {
		set bool
		bit uint
	}{{pre, 24}, {up, 23}, {writeback, 21}, {load, 20}} {
		if flag.set {
			word |= 1 << flag.bit
		}
	}
	return word
}

func armPrivateTransferStart(base uint32, count int, pre, up, alignment bool) uint32 {
	address := base
	if up {
		if pre {
			address += 4
		}
	} else {
		address -= uint32(count * 4)
		if !pre {
			address += 4
		}
	}
	if !alignment {
		address &^= 3
	}
	return address
}

func armPrivateTransferPair(t *testing.T) (*Backend, *Backend) {
	t.Helper()
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, 0x200, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.Map(0x3000, 0x200, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	}
	return precise, jit
}

func prepareARMPrivateTransfer(t *testing.T, b *Backend, instruction, base uint32, alignment bool) {
	t.Helper()
	seed := make([]byte, 0x200)
	for i := range seed {
		seed[i] = byte(i*29 + 17)
	}
	check(t, b.WriteMemory(0x3000, seed))
	check(t, b.WriteMemory(0x1000, armApplicationCode(instruction, 0xe1200070)))
	check(t, b.WriteMemory(0x1100, armApplicationCode(0xe1200070)))
	for register := uint32(0); register < cpu.RegisterPC; register++ {
		check(t, b.WriteRegister(register, 0x89ab0000+register*0x101))
	}
	check(t, b.WriteRegister(instruction>>16&15, base))
	check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
	b.cp15.control &^= 2
	if alignment {
		b.cp15.control |= 2
	}
	b.clearDataCaches()
}

func warmARMPrivateTransfer(t *testing.T, b *Backend, address uint32, load bool) {
	t.Helper()
	permission := cpu.PermissionWrite
	if load {
		permission = cpu.PermissionRead
	}
	mapped, _, err := b.findRegion(address, permission)
	check(t, err)
	b.cacheData(mapped, address, permission)
}

func assertARMPrivateTransferState(t *testing.T, precise, jit *Backend, want, got cpu.Result) {
	t.Helper()
	if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
		t.Fatalf("result got=%+v want=%+v", got, want)
	}
	wantContext, err := precise.SaveContext()
	check(t, err)
	gotContext, err := jit.SaveContext()
	check(t, err)
	if !bytes.Equal(wantContext, gotContext) {
		t.Fatal("complete architectural context differs from scalar New()")
	}
	if len(precise.regions) != len(jit.regions) {
		t.Fatal("mapping count differs")
	}
	for i := range precise.regions {
		if !bytes.Equal(precise.regions[i].data, jit.regions[i].data) {
			t.Fatalf("memory region %d differs", i)
		}
	}
	if !reflect.DeepEqual(precise.PCHistory(), jit.PCHistory()) || !reflect.DeepEqual(precise.pcHits, jit.pcHits) ||
		!reflect.DeepEqual(precise.PCRegisterCaptures(), jit.PCRegisterCaptures()) {
		t.Fatal("execution observations differ")
	}
}

func runARMPrivateTransferOracle(t *testing.T, precise, jit *Backend, pc uint32, mode cpu.Mode, budget uint64) (cpu.Result, cpu.Result) {
	t.Helper()
	want := precise.Run(context.Background(), pc, mode, budget)
	got := jit.Run(context.Background(), pc, mode, budget)
	assertARMPrivateTransferState(t, precise, jit, want, got)
	return want, got
}

func TestARMPrivateTransferRegistersAddressModesAndAlignment(t *testing.T) {
	precise, jit := armPrivateTransferPair(t)
	for controls := uint32(0); controls < 16; controls++ {
		pre, up, writeback, load := controls&1 != 0, controls&2 != 0, controls&4 != 0, controls&8 != 0
		for rn := uint32(0); rn < cpu.RegisterPC; rn++ {
			for _, mask := range []uint16{1, 3, 0x003c, 0x5555, 0x7fff, 1 << rn, 1<<rn | 1<<14} {
				instruction := armPrivateTransferInstruction(pre, up, writeback, load, rn, mask)
				count := bits.OnesCount16(mask)
				for _, alignment := range []bool{false, true} {
					for lane := uint32(0); lane < 4; lane++ {
						base := uint32(0x3080) + lane
						address := armPrivateTransferStart(base, count, pre, up, alignment)
						for _, b := range []*Backend{precise, jit} {
							prepareARMPrivateTransfer(t, b, instruction, base, alignment)
						}
						block := jit.armJITBlockAt(0x1000)
						if block == nil || block.arm[0].op != armMicroBlockTransfer || block.arm[0].exec != nil {
							t.Fatal("ordinary instruction did not enter the ARM micro-op executor")
						}
						warmARMPrivateTransfer(t, jit, address, load)
						_, _, hit := jit.armPrivateBlockTransferSpan(address, count, load)
						if hit != (address&3 == 0) {
							t.Fatalf("controls=%d rn=%d mask=%04x alignment=%t lane=%d hit=%t", controls, rn, mask, alignment, lane, hit)
						}
						runARMPrivateTransferOracle(t, precise, jit, 0x1000, cpu.ModeARM, 1)
					}
				}
			}
		}
	}
}

func TestARMPrivateTransferFullListPCAndOriginalStoreBase(t *testing.T) {
	for _, load := range []bool{false, true} {
		for controls := uint32(0); controls < 8; controls++ {
			pre, up, writeback := controls&1 != 0, controls&2 != 0, controls&4 != 0
			for _, target := range []uint32{0x1100, 0x1101, 0x1102, 0x1103} {
				for _, mask := range []uint16{0x8001, 0xa001, 0xffff} {
					precise, jit := armPrivateTransferPair(t)
					const rn, base = uint32(13), uint32(0x3081)
					instruction := armPrivateTransferInstruction(pre, up, writeback, load, rn, mask)
					count := bits.OnesCount16(mask)
					address := armPrivateTransferStart(base, count, pre, up, false)
					for _, b := range []*Backend{precise, jit} {
						prepareARMPrivateTransfer(t, b, instruction, base, false)
						if load {
							check(t, b.WriteMemory(address+uint32(count-1)*4, armApplicationCode(target)))
						}
						if target&1 != 0 {
							check(t, b.WriteMemory(target&^1, thumbTestCode(0xbe00)))
						}
					}
					if block := jit.armJITBlockAt(0x1000); block == nil || block.arm[0].op != armMicroBlockTransfer {
						t.Fatal("PC/full-list transfer was not a micro-op")
					}
					warmARMPrivateTransfer(t, jit, address, load)
					if _, _, hit := jit.armPrivateBlockTransferSpan(address, count, load); !hit {
						t.Fatal("full-list/PC positive proof was vacuous")
					}
					_, got := runARMPrivateTransferOracle(t, precise, jit, 0x1000, cpu.ModeARM, 1)
					if load {
						mode, pc := cpu.ModeARM, target&^3
						if target&1 != 0 {
							mode, pc = cpu.ModeThumb, target&^1
						}
						if got.PC != pc || jit.mode != mode {
							t.Fatalf("deferred PC branch got PC=%x mode=%v", got.PC, jit.mode)
						}
						runARMPrivateTransferOracle(t, precise, jit, pc, mode, 1)
					} else {
						var word [4]byte
						check(t, jit.ReadMemory(address+uint32(count-1)*4, word[:]))
						if binary.LittleEndian.Uint32(word[:]) != 0x100c {
							t.Fatal("STM stored the host PC instead of architectural PC+12")
						}
						if mask&(1<<rn) != 0 {
							rank := bits.OnesCount16(mask & ((1 << rn) - 1))
							check(t, jit.ReadMemory(address+uint32(rank)*4, word[:]))
							if binary.LittleEndian.Uint32(word[:]) != base {
								t.Fatal("STM base in a non-first list position lost its original low bits")
							}
						}
					}
				}
			}
		}
	}
}

func TestARMPrivateTransferColdFaultSplitAndSpareBacking(t *testing.T) {
	for _, load := range []bool{false, true} {
		for _, scenario := range []string{"cold", "second-unmapped", "second-read-only", "split-writable", "spare-backing"} {
			t.Run(fmt.Sprintf("load=%t/%s", load, scenario), func(t *testing.T) {
				precise, jit := armPrivateTransferPair(t)
				instruction := armPrivateTransferInstruction(false, true, true, load, 2, 3)
				base := uint32(0x5000)
				for _, b := range []*Backend{precise, jit} {
					prepareARMPrivateTransfer(t, b, instruction, base, false)
					switch scenario {
					case "cold":
						check(t, b.Map(base, 8, cpu.PermissionRead|cpu.PermissionWrite))
					case "second-unmapped":
						check(t, b.Map(base, 4, cpu.PermissionRead|cpu.PermissionWrite))
					case "second-read-only":
						check(t, b.Map(base, 4, cpu.PermissionRead|cpu.PermissionWrite))
						check(t, b.Map(base+4, 4, cpu.PermissionRead))
					case "split-writable":
						check(t, b.Map(base, 4, cpu.PermissionRead|cpu.PermissionWrite))
						check(t, b.Map(base+4, 4, cpu.PermissionRead|cpu.PermissionWrite))
					case "spare-backing":
						check(t, b.Map(base, 7, cpu.PermissionRead|cpu.PermissionWrite))
						mapped, _, err := b.findRegion(base, cpu.PermissionRead)
						check(t, err)
						mapped.data = make([]byte, 7, 64)
					}
					check(t, b.WriteMemory(base, []byte{0x44, 0x33, 0x22, 0x11}))
				}
				if scenario != "cold" {
					warmARMPrivateTransfer(t, jit, base, load)
				}
				if _, _, hit := jit.armPrivateBlockTransferSpan(base, 2, load); hit {
					t.Fatal("incomplete, split or cold span was accepted")
				}
				_, got := runARMPrivateTransferOracle(t, precise, jit, 0x1000, cpu.ModeARM, 1)
				fault := scenario == "second-unmapped" || scenario == "spare-backing" || scenario == "second-read-only" && !load
				if fault {
					if got.Err == nil || got.Instructions != 0 || jit.regs[2] != base {
						t.Fatalf("partial transfer fault retired or wrote back: %+v", got)
					}
					if load && jit.regs[0] != 0x11223344 {
						t.Fatal("LDM fault lost its completed first register")
					}
				} else if got.Err != nil || got.Instructions != 1 {
					t.Fatalf("scalar fallback failed: %+v", got)
				}
				if scenario == "cold" {
					if _, _, hit := jit.armPrivateBlockTransferSpan(base, 2, load); !hit {
						t.Fatal("successful scalar transfer did not warm the next private span")
					}
				}
			})
		}
	}
}

func TestARMPrivateTransferTopAddressAndWrappingFallback(t *testing.T) {
	for _, load := range []bool{false, true} {
		for _, count := range []int{1, 2, 16} {
			precise, jit := armPrivateTransferPair(t)
			mask := uint16(3)
			if count == 1 {
				mask = 1
			} else if count == 16 {
				mask = 0xffff
			}
			base := uint32(0xfffffffc)
			if count == 16 {
				base = 0xffffffc0
			}
			instruction := armPrivateTransferInstruction(false, true, true, load, 2, mask)
			for _, b := range []*Backend{precise, jit} {
				prepareARMPrivateTransfer(t, b, instruction, base, false)
				check(t, b.Map(0xffffffc0, 64, cpu.PermissionRead|cpu.PermissionWrite))
				check(t, b.Map(0, 8, cpu.PermissionRead|cpu.PermissionWrite))
				if load && count == 16 {
					check(t, b.WriteMemory(0xfffffffc, armApplicationCode(0x1101)))
					check(t, b.WriteMemory(0x1100, thumbTestCode(0xbe00)))
				}
			}
			warmARMPrivateTransfer(t, jit, base, load)
			_, _, hit := jit.armPrivateBlockTransferSpan(base, count, load)
			if hit != (count != 2) {
				t.Fatalf("count=%d top-of-address-space proof=%t", count, hit)
			}
			runARMPrivateTransferOracle(t, precise, jit, 0x1000, cpu.ModeARM, 1)
		}
	}
}

func TestARMPrivateTransferGuardDeclinesAreAtomic(t *testing.T) {
	for _, guard := range []string{"physical", "bus", "tlb", "history", "hits", "capture", "trap", "jit-code", "native-thumb", "native-arm", "zero", "large", "unaligned", "wrap"} {
		t.Run(guard, func(t *testing.T) {
			_, b := armPrivateTransferPair(t)
			prepareARMPrivateTransfer(t, b, armPrivateTransferInstruction(false, true, true, false, 2, 3), 0x3080, false)
			address, count := uint32(0x3080), 2
			warmARMPrivateTransfer(t, b, address, false)
			switch guard {
			case "physical":
				b.physicalAccess = true
			case "bus":
				b.systemBus = &testSystemBus{memory: make(map[uint32]byte)}
			case "tlb":
				b.tlb = []tlbEntry{{}}
			case "history":
				check(t, b.SetPCHistoryLimit(4))
			case "hits":
				b.pcHits = make(map[uint32]uint64)
			case "capture":
				check(t, b.SetPCRegisterCapture(0x1000, 1))
			case "trap":
				check(t, b.SetExecutionTraps([]cpu.ExecutionTrap{{Address: 0x1000, Mode: cpu.ModeARM}}))
			case "jit-code":
				check(t, b.WriteMemory(0x3000, armApplicationCode(0xe1200070)))
				if b.armJITBlockAt(0x3000) == nil {
					t.Fatal("other-page ARM code was not translated")
				}
				warmARMPrivateTransfer(t, b, address, false)
			case "native-thumb":
				b.nativeBlocks = make(map[uint32]*nativeBlock)
				b.nativeCodePages = nil // No bitmap must conservatively decline.
			case "native-arm":
				b.nativeARMBlocks = make(map[uint32]*nativeBlock)
				b.nativeCodePages = nil
			case "zero":
				count = 0
			case "large":
				count = 17
			case "unaligned":
				address++
			case "wrap":
				address = 0xfffffffc
			}
			b.setNZCV(0, true, true)
			registers, flags, stats := b.regs, b.flags, b.ExecutionStatistics()
			memory := make([][]byte, len(b.regions))
			for i := range b.regions {
				memory[i] = append([]byte(nil), b.regions[i].data...)
			}
			if _, _, hit := b.armPrivateBlockTransferSpan(address, count, false); hit {
				t.Fatal("guard did not decline")
			}
			if b.regs != registers || b.flags != flags || !reflect.DeepEqual(b.ExecutionStatistics(), stats) {
				t.Fatal("declined proof changed registers, raw/pending flags or statistics")
			}
			for i := range b.regions {
				if !bytes.Equal(memory[i], b.regions[i].data) {
					t.Fatal("declined proof changed memory")
				}
			}
			b.systemBus, b.nativeBlocks, b.nativeARMBlocks = nil, nil, nil
		})
	}
}

func TestARMPrivateTransferRawCaptureWithoutIntermediateSave(t *testing.T) {
	for _, load := range []bool{false, true} {
		for flags := uint32(0); flags < 16; flags++ {
			precise, jit := armPrivateTransferPair(t)
			instruction := armPrivateTransferInstruction(false, true, true, load, 2, 3)
			for _, b := range []*Backend{precise, jit} {
				prepareARMPrivateTransfer(t, b, instruction, 0x3080, false)
				check(t, b.WriteRegister(cpu.RegisterCPSR, flags<<28|uint32(processorModeSystem)))
				b.setNZ(0) // Pending partial flags must survive an unconditional transfer.
			}
			warmARMPrivateTransfer(t, jit, 0x3080, load)
			if _, _, hit := jit.armPrivateBlockTransferSpan(0x3080, 2, load); !hit {
				t.Fatal("untraced positive proof was vacuous")
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
			if want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) || precise.flags != jit.flags || precise.regs[cpu.RegisterCPSR] != jit.regs[cpu.RegisterCPSR] {
				t.Fatal("transfer changed raw or pending CPSR before observation")
			}
			// Neither ReadRegister(CPSR) nor SaveContext may normalize this state.
			for _, b := range []*Backend{precise, jit} {
				check(t, b.SetPCRegisterCapture(0x1004, 1))
			}
			want = precise.Run(context.Background(), 0x1004, cpu.ModeARM, 1)
			got = jit.Run(context.Background(), 0x1004, cpu.ModeARM, 1)
			if len(jit.PCRegisterCaptures()) != 1 {
				t.Fatal("capture comparison was vacuous")
			}
			assertARMPrivateTransferState(t, precise, jit, want, got)
		}
	}
}
