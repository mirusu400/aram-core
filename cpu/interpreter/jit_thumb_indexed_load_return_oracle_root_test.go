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

// Assemble independently from the classifier. These are generated synthetic
// operands, not copied guest code or a description of any title's array API.
func indexedLoadReturnWordsRoot(temporary, index, base, destination, shift, offset, popMask uint16) []uint16 {
	return []uint16{
		shift<<6 | index<<3 | temporary,
		0x1800 | base<<6 | temporary<<3 | temporary,
		0x6800 | offset<<6 | temporary<<3 | destination,
		0xbd00 | popMask,
	}
}

func newIndexedLoadReturnPairRoot(t *testing.T, words []uint16, flags, stackLowBits uint32, thumbReturn bool) (*Backend, *Backend, *jitBlock) {
	t.Helper()
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0, 0x400, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, b.Map(0x3000, 0x100, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
		for offset := 0; offset < len(b.regions[0].data); offset += 4 {
			binary.LittleEndian.PutUint32(b.regions[0].data[offset:], 0x12340000+uint32(offset)*17)
		}
		returnBit := uint32(0)
		if thumbReturn {
			returnBit = 1
		}
		for offset := 0; offset < len(b.regions[2].data); offset += 4 {
			binary.LittleEndian.PutUint32(b.regions[2].data[offset:], uint32(0x1800+offset*4)|returnBit)
		}
		for register := uint32(0); register < cpu.RegisterPC; register++ {
			check(t, b.WriteRegister(register, 4*(register+1)))
		}
		check(t, b.WriteRegister(cpu.RegisterSP, 0x3040|stackLowBits))
		check(t, b.WriteRegister(cpu.RegisterPC, 0x1000))
		check(t, b.WriteRegister(cpu.RegisterCPSR, flags|uint32(processorModeSystem)|cpu.StatusThumb))
		b.clearDataCaches()
		b.cacheData(&b.regions[0], 0, cpu.PermissionRead)
		b.cacheData(&b.regions[2], 0x3000, cpu.PermissionRead)
	}
	block := jit.jitBlockAt(0x1000)
	if block == nil || block.indexedLoadReturn == nil || len(block.thumb) != 4 || block.extraLoop != nil {
		t.Fatalf("synthetic program did not classify as an ordinary indexed-four block: %+v", block)
	}
	return precise, jit, block
}

func assertIndexedLoadReturnStateRoot(t *testing.T, precise, jit *Backend) {
	t.Helper()
	want, err := precise.SaveContext()
	check(t, err)
	got, err := jit.SaveContext()
	check(t, err)
	if !bytes.Equal(want, got) {
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if precise.regs[register] != jit.regs[register] {
				t.Errorf("register %d got=%08x want=%08x", register, jit.regs[register], precise.regs[register])
			}
		}
		t.Fatal("indexed-four differs from independent precise context")
	}
	for index := range precise.regions {
		if !bytes.Equal(precise.regions[index].data, jit.regions[index].data) {
			t.Fatalf("indexed-four changed memory region %d", index)
		}
	}
}

func TestThumbIndexedLoadReturnDirectAllRegisterAliasesRoot(t *testing.T) {
	for temporary := uint16(0); temporary < 8; temporary++ {
		for index := uint16(0); index < 8; index++ {
			for base := uint16(0); base < 8; base++ {
				for destination := uint16(0); destination < 8; destination++ {
					t.Run(fmt.Sprintf("tmp%d/index%d/base%d/dst%d", temporary, index, base, destination), func(t *testing.T) {
						words := indexedLoadReturnWordsRoot(temporary, index, base, destination, 2, 2, 0x55)
						precise, jit, block := newIndexedLoadReturnPairRoot(t, words, flagN|flagC|flagV, 0, (temporary^index^base^destination)&1 != 0)
						want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
						if want.Err != nil || want.Instructions != 4 || want.Reason != cpu.StopBudget {
							t.Fatalf("independent precise program failed: %+v", want)
						}
						if collectThumbMicro {
							jit.recordThumbBlock(block) // The application runner owns this boundary.
						}
						if !jit.tryThumbApplicationIndexedLoadReturn(block) {
							t.Fatal("warm complete block did not enter indexed-four executor")
						}
						assertIndexedLoadReturnStateRoot(t, precise, jit)
						if collectThumbMicro {
							expected := &cpu.ThumbMicroStatistics{Blocks: map[uint32]uint64{0x1000: 1}, BlockCalls: 1}
							for _, class := range []thumbInstructionClass{thumbShiftImmediate, thumbAddSubtract, thumbImmediateTransfer, thumbPop} {
								expected.Classes[class]++
							}
							if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, expected) {
								t.Fatalf("original four-class/block diagnostic contract differs: got=%+v want=%+v", got, expected)
							}
						}
					})
				}
			}
		}
	}
}

func TestThumbIndexedLoadReturnApplicationBudgetFlagsAndStackBitsRoot(t *testing.T) {
	for _, shift := range []uint16{0, 1, 2, 7, 16, 31} {
		// Larger shifts need a known zero source to remain inside the small
		// synthetic data mapping. Zero is an ordinary operand, not a fast-path rule.
		for bits := uint32(0); bits < 16; bits++ {
			for budget := uint64(1); budget <= 4; budget++ {
				t.Run(fmt.Sprintf("shift%d/flags%x/budget%d", shift, bits, budget), func(t *testing.T) {
					words := indexedLoadReturnWordsRoot(2, 6, 3, 1, shift, 31, 0x45)
					precise, jit, _ := newIndexedLoadReturnPairRoot(t, words, bits<<28, bits&3, bits&1 != 0)
					if shift >= 7 {
						check(t, precise.WriteRegister(cpu.RegisterR6, 0))
						check(t, jit.WriteRegister(cpu.RegisterR6, 0))
					}
					want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
					got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
					if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
						t.Fatalf("partial/full application result got=%+v want=%+v", got, want)
					}
					assertIndexedLoadReturnStateRoot(t, precise, jit)
				})
			}
		}
	}
}

// After valid classification, poison only the host application switch tags.
// The guest instructions and diagnostic classes stay unchanged. The ordinary
// generated switch cannot implement this program, so success proves that the
// actual full-block caller entered the new executor, not just a direct test API.
func TestThumbIndexedLoadReturnApplicationMustUsePositivePathRoot(t *testing.T) {
	for _, thumbReturn := range []bool{false, true} {
		t.Run(fmt.Sprint(thumbReturn), func(t *testing.T) {
			words := indexedLoadReturnWordsRoot(2, 6, 3, 1, 2, 2, 0x45)
			precise, jit, block := newIndexedLoadReturnPairRoot(t, words, flagN|flagV, 0, thumbReturn)
			for index := 1; index < len(block.thumb); index++ {
				block.thumb[index].appOp = thumbInstructionClass(255)
			}
			want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
			got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
			if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
				t.Fatalf("required positive-path caller result got=%+v want=%+v", got, want)
			}
			assertIndexedLoadReturnStateRoot(t, precise, jit)
			if collectThumbMicro {
				expected := &cpu.ThumbMicroStatistics{Blocks: map[uint32]uint64{0x1000: 1}, BlockCalls: 1}
				for _, class := range []thumbInstructionClass{thumbShiftImmediate, thumbAddSubtract, thumbImmediateTransfer, thumbPop} {
					expected.Classes[class]++
				}
				if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, expected) {
					t.Fatalf("positive caller must count one block and exactly four original operations: got=%+v want=%+v", got, expected)
				}
			}
		})
	}
}

func TestThumbIndexedLoadReturnDeclinesWithoutSideEffectsRoot(t *testing.T) {
	for _, name := range []string{"data-miss", "stack-miss", "data-permission", "stack-permission", "unaligned-load", "unaligned-stack-A", "physical", "tlb", "trace", "arm-mode", "no-metadata"} {
		t.Run(name, func(t *testing.T) {
			words := indexedLoadReturnWordsRoot(2, 6, 3, 1, 2, 2, 0x45)
			_, jit, block := newIndexedLoadReturnPairRoot(t, words, flagV, 0, true)
			dataSlot := privateDataCacheIndex(0, cpu.PermissionRead)
			stackSlot := privateDataCacheIndex(0x3040, cpu.PermissionRead)
			switch name {
			case "data-miss":
				jit.privateDataCache[dataSlot] = dataRegionCache{}
			case "stack-miss":
				jit.privateDataCache[stackSlot] = dataRegionCache{}
			case "data-permission":
				jit.privateDataCache[dataSlot].perms &^= cpu.PermissionRead
			case "stack-permission":
				jit.privateDataCache[stackSlot].perms &^= cpu.PermissionRead
			case "unaligned-load":
				jit.regs[cpu.RegisterR3]++
			case "unaligned-stack-A":
				jit.regs[cpu.RegisterSP]++
				jit.cp15.control |= 2
			case "physical":
				jit.physicalAccess = true
			case "tlb":
				jit.tlb = make([]tlbEntry, 1)
			case "trace":
				jit.pcHits = make(map[uint32]uint64)
			case "arm-mode":
				jit.mode = cpu.ModeARM
				jit.setModeFlag()
			case "no-metadata":
				block.indexedLoadReturn = nil
			}
			jit.flags = pendingFlags{dirty: true, mask: flagC, bits: flagC}
			regs, flags, mode, cache := jit.regs, jit.flags, jit.mode, jit.privateDataCache
			stats := jit.ExecutionStatistics()
			memory := make([][]byte, len(jit.regions))
			for index := range jit.regions {
				memory[index] = append([]byte(nil), jit.regions[index].data...)
			}
			if jit.tryThumbApplicationIndexedLoadReturn(block) {
				t.Fatal("ineligible indexed-four attempt succeeded")
			}
			if regs != jit.regs || flags != jit.flags || mode != jit.mode ||
				!reflect.DeepEqual(cache, jit.privateDataCache) || !reflect.DeepEqual(stats, jit.ExecutionStatistics()) {
				t.Fatal("declined indexed-four attempt changed registers, pending flags, mode, cache or counters")
			}
			for index := range jit.regions {
				if !bytes.Equal(memory[index], jit.regions[index].data) {
					t.Fatalf("declined indexed-four attempt changed memory region %d", index)
				}
			}
		})
	}
}
