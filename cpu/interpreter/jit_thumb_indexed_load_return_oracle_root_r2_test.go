package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

type indexedLoadReturnEdgeRootR2 struct {
	index, base, sum, loadAddress uint32
	shift, offset, popMask        uint16
	stackBase, stackSize, sp      uint32
	returnTarget, loadValue       uint32
	mapData                       bool
	dataSize                      uint32
	dataPermission                cpu.Permissions
}

func indexedLoadReturnDefaultEdgeRootR2() indexedLoadReturnEdgeRootR2 {
	return indexedLoadReturnEdgeRootR2{
		index: 0x1000, base: 0x1000, sum: 0x2000, loadAddress: 0x2000,
		popMask: 1, stackBase: 0x7000, stackSize: 0x100, sp: 0x7040,
		returnTarget: 0x1803, loadValue: 0x89abcdef,
		mapData: true, dataSize: 4, dataPermission: cpu.PermissionRead | cpu.PermissionWrite,
	}
}

func newIndexedLoadReturnEdgePairRootR2(t *testing.T, c indexedLoadReturnEdgeRootR2, flags uint32) (*Backend, *Backend, *jitBlock) {
	t.Helper()
	// Independent synthetic assembly with fixed, distinct register roles:
	// tmp=r2, index=r6, base=r3, load destination=r1.
	words := []uint16{
		c.shift<<6 | 6<<3 | 2,
		0x1800 | 3<<6 | 2<<3 | 2,
		0x6800 | c.offset<<6 | 2<<3 | 1,
		0xbd00 | c.popMask,
	}
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		if c.mapData {
			check(t, b.Map(c.loadAddress, c.dataSize, c.dataPermission))
		}
		if c.stackSize != 0 {
			check(t, b.Map(c.stackBase, c.stackSize, cpu.PermissionRead|cpu.PermissionWrite))
		}
		check(t, b.WriteMemory(0x1000, thumbTestCode(words...)))
		for regionIndex := range b.regions {
			mapped := &b.regions[regionIndex]
			if c.mapData && mapped.address == c.loadAddress {
				var value [4]byte
				binary.LittleEndian.PutUint32(value[:], c.loadValue)
				copy(mapped.data, value[:])
			}
			if c.stackSize != 0 && mapped.address == c.stackBase {
				for offset := range mapped.data {
					mapped.data[offset] = byte(0x80 + offset)
				}
				for offset := 0; offset+4 <= len(mapped.data); offset += 4 {
					binary.LittleEndian.PutUint32(mapped.data[offset:offset+4], 0x76540000+uint32(offset)*17)
				}
				pcAddress := uint64(c.sp&^3) + uint64(bits.OnesCount16(c.popMask))*4
				if pcAddress >= uint64(mapped.address) && pcAddress+4 <= uint64(mapped.address)+uint64(len(mapped.data)) {
					offset := int(pcAddress - uint64(mapped.address))
					binary.LittleEndian.PutUint32(mapped.data[offset:offset+4], c.returnTarget)
				}
			}
		}
		for register := uint32(0); register < cpu.RegisterPC; register++ {
			check(t, b.WriteRegister(register, 0x55550000+register*0x101))
		}
		check(t, b.WriteRegister(cpu.RegisterR6, c.index))
		check(t, b.WriteRegister(cpu.RegisterR3, c.base))
		check(t, b.WriteRegister(cpu.RegisterSP, c.sp))
		check(t, b.WriteRegister(cpu.RegisterPC, 0x1000))
		check(t, b.WriteRegister(cpu.RegisterCPSR, flags|uint32(processorModeSystem)|cpu.StatusThumb))
		b.clearDataCaches()
		for regionIndex := range b.regions {
			mapped := &b.regions[regionIndex]
			if mapped.address != 0x1000 {
				b.cacheData(mapped, mapped.address, cpu.PermissionRead)
			}
		}
	}
	block := jit.jitBlockAt(0x1000)
	if block == nil || block.indexedLoadReturn == nil || block.extraLoop != nil || len(block.thumb) != 4 {
		t.Fatalf("edge fixture did not classify as indexed-four: %+v", block)
	}
	return precise, jit, block
}

func assertIndexedLoadReturnEdgeStateRootR2(t *testing.T, precise, jit *Backend) {
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
		t.Fatal("indexed-four edge context differs from precise New()")
	}
	if len(precise.regions) != len(jit.regions) {
		t.Fatal("mapping count differs")
	}
	for index := range precise.regions {
		if !bytes.Equal(precise.regions[index].data, jit.regions[index].data) {
			t.Fatalf("indexed-four edge memory differs in region %d", index)
		}
	}
}

func TestThumbIndexedLoadReturnArithmeticAndAddressEdgesRootR2(t *testing.T) {
	// Literal uint32 sums and literal NZCV expectations are independent from
	// addWithCarry and the classifier/executor under test.
	vectors := []struct {
		name              string
		index, base, sum  uint32
		shift, offset     uint16
		loadAddress, nzcv uint32
	}{
		{"positive", 4, 8, 12, 0, 0, 12, 0},
		{"zero", 0, 0, 0, 0, 0, 0, flagZ},
		{"carry-zero", 0xffffffff, 1, 0, 0, 0, 0, flagZ | flagC},
		{"negative", 0x80000000, 0, 0x80000000, 0, 0, 0x80000000, flagN},
		{"overflow-negative", 0x7fffffff, 1, 0x80000000, 0, 0, 0x80000000, flagN | flagV},
		{"carry-overflow-zero", 0x80000000, 0x80000000, 0, 0, 0, 0, flagZ | flagC | flagV},
		{"carry-negative", 0xfffffffc, 0xfffffffc, 0xfffffff8, 0, 0, 0xfffffff8, flagN | flagC},
		{"load-address-wrap", 0xfffffff0, 8, 0xfffffff8, 0, 4, 8, flagN},
		{"nonzero-large-shift", 0x80000001, 4, 0x80000004, 31, 0, 0x80000004, flagN},
		{"shift-carry-discarded", 0x80000002, 4, 8, 1, 0, 8, 0},
		{"shift-then-overflow", 0x40000000, 0x80000000, 0, 1, 0, 0, flagZ | flagC | flagV},
	}
	for _, vector := range vectors {
		for initial := uint32(0); initial < 16; initial++ {
			for returnBits := uint32(0); returnBits < 4; returnBits++ {
				for _, direct := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/initial%x/return%d/direct%t", vector.name, initial, returnBits, direct), func(t *testing.T) {
						c := indexedLoadReturnDefaultEdgeRootR2()
						c.index, c.base, c.sum = vector.index, vector.base, vector.sum
						c.shift, c.offset, c.loadAddress = vector.shift, vector.offset, vector.loadAddress
						c.returnTarget = 0x1800 | returnBits
						precise, jit, block := newIndexedLoadReturnEdgePairRootR2(t, c, initial<<28)
						want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
						if want.Err != nil || want.Instructions != 4 || want.Reason != cpu.StopBudget {
							t.Fatalf("independent arithmetic fixture failed: %+v", want)
						}
						if direct {
							if !jit.tryThumbApplicationIndexedLoadReturn(block) {
								t.Fatal("warm arithmetic edge must enter indexed-four helper")
							}
						} else {
							got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
							if got.Reason != want.Reason || got.Instructions != want.Instructions || got.PC != want.PC || !sameError(got.Err, want.Err) {
								t.Fatalf("arithmetic caller got=%+v want=%+v", got, want)
							}
						}
						const nzcv = flagN | flagZ | flagC | flagV
						if !jit.flags.dirty || jit.flags.mask&nzcv != nzcv || jit.flags.bits&nzcv != vector.nzcv {
							t.Fatalf("literal ADD pending flags got=%+v want=%08x", jit.flags, vector.nzcv)
						}
						if jit.regs[cpu.RegisterR2] != vector.sum || jit.regs[cpu.RegisterR1] != c.loadValue {
							t.Fatalf("literal ADD/load got r2=%08x r1=%08x want sum=%08x load=%08x", jit.regs[cpu.RegisterR2], jit.regs[cpu.RegisterR1], vector.sum, c.loadValue)
						}
						assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
					})
				}
			}
		}
	}
}

func TestThumbIndexedLoadReturnTopStackWrapAndReturnBitsRootR2(t *testing.T) {
	for low := uint32(0); low < 4; low++ {
		for returnBits := uint32(0); returnBits < 4; returnBits++ {
			t.Run(fmt.Sprintf("spbits%d/return%d", low, returnBits), func(t *testing.T) {
				c := indexedLoadReturnDefaultEdgeRootR2()
				c.stackBase, c.stackSize, c.sp = 0xffffffdc, 36, 0xffffffdc|low
				c.popMask, c.returnTarget = 0xff, 0x1800|returnBits
				precise, jit, block := newIndexedLoadReturnEdgePairRootR2(t, c, flagN|flagV)
				want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
				if want.Err != nil || want.Instructions != 4 {
					t.Fatalf("precise top stack failed: %+v", want)
				}
				if !jit.tryThumbApplicationIndexedLoadReturn(block) {
					t.Fatal("warm exact-top stack must enter indexed-four helper")
				}
				if jit.regs[cpu.RegisterSP] != low {
					t.Fatalf("SP wrap lost original low bits: got=%08x want=%08x", jit.regs[cpu.RegisterSP], low)
				}
				assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
			})
		}
	}
}

func TestThumbIndexedLoadReturnLoadStackAliasRootR2(t *testing.T) {
	for _, pcSlot := range []bool{false, true} {
		for returnBits := uint32(0); returnBits < 4; returnBits++ {
			t.Run(fmt.Sprintf("pcslot%t/return%d", pcSlot, returnBits), func(t *testing.T) {
				c := indexedLoadReturnDefaultEdgeRootR2()
				c.index, c.base, c.sum, c.loadAddress = 0x7000, 0x40, 0x7040, 0x7040
				if pcSlot {
					c.base, c.sum, c.loadAddress = 0x44, 0x7044, 0x7044
				}
				c.mapData, c.returnTarget, c.sp = false, 0x1800|returnBits, 0x7041
				precise, jit, block := newIndexedLoadReturnEdgePairRootR2(t, c, flagC|flagV)
				want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
				if want.Err != nil || want.Instructions != 4 {
					t.Fatalf("precise stack alias failed: %+v", want)
				}
				if !jit.tryThumbApplicationIndexedLoadReturn(block) {
					t.Fatal("read-only load/stack alias must use the warm helper")
				}
				expected := uint32(0x76540000 + 0x40*17)
				if pcSlot {
					expected = c.returnTarget
				}
				if jit.regs[cpu.RegisterR1] != expected {
					t.Fatalf("aliased load value got=%08x want=%08x", jit.regs[cpu.RegisterR1], expected)
				}
				assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
			})
		}
	}
}

func TestThumbIndexedLoadReturnFaultPriorityAndPartialPopRootR2(t *testing.T) {
	for _, name := range []string{"load-unmapped-before-stack", "load-permission-before-stack", "load-cross-end-before-stack", "pop-first-word-before-pc-fault", "pop-two-registers-before-pc-fault", "pop-three-registers-before-fourth-fault"} {
		for budget := uint64(1); budget <= 4; budget++ {
			t.Run(fmt.Sprintf("%s/budget%d", name, budget), func(t *testing.T) {
				c := indexedLoadReturnDefaultEdgeRootR2()
				c.index, c.base, c.sum, c.loadAddress = 0x80000000, 0x80004000, 0x4000, 0x4000
				c.sp, c.stackBase = 0x7000, 0x7000
				retired, pc, faultAddress := uint64(2), uint32(0x1006), uint32(0x4000)
				permissionFault := false
				switch name {
				case "load-unmapped-before-stack":
					c.mapData, c.stackSize = false, 0
				case "load-permission-before-stack":
					c.dataPermission, c.stackSize, permissionFault = cpu.PermissionWrite, 0, true
				case "load-cross-end-before-stack":
					c.dataSize, c.stackSize, faultAddress = 2, 0, 0x4002
				case "pop-first-word-before-pc-fault":
					c.stackSize = 4
					retired, pc, faultAddress = 3, 0x1008, 0x7004
				case "pop-two-registers-before-pc-fault":
					c.popMask, c.stackSize = 3, 8
					retired, pc, faultAddress = 3, 0x1008, 0x7008
				case "pop-three-registers-before-fourth-fault":
					c.popMask, c.stackSize = 0xff, 12
					retired, pc, faultAddress = 3, 0x1008, 0x700c
				}
				precise, jit, block := newIndexedLoadReturnEdgePairRootR2(t, c, flagN|flagZ)
				regs, pending, cache, stats := jit.regs, jit.flags, jit.privateDataCache, jit.ExecutionStatistics()
				if jit.tryThumbApplicationIndexedLoadReturn(block) {
					t.Fatal("faulting fixture must decline before any architectural effect")
				}
				if regs != jit.regs || pending != jit.flags || !reflect.DeepEqual(cache, jit.privateDataCache) || !reflect.DeepEqual(stats, jit.ExecutionStatistics()) {
					t.Fatal("fault preflight changed registers, pending flags, cache or counters")
				}
				want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
				got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
				if want.Reason != got.Reason || want.Instructions != got.Instructions || want.PC != got.PC || !sameError(want.Err, got.Err) {
					t.Fatalf("faulting fallback got=%+v want=%+v", got, want)
				}
				if budget > retired {
					expectedError := cpu.ErrInvalidAddress
					if permissionFault {
						expectedError = cpu.ErrPermissionDenied
					}
					if got.Reason != cpu.StopFault || got.Instructions != retired || got.PC != pc ||
						!errors.Is(got.Err, expectedError) || !strings.Contains(fmt.Sprint(got.Err), fmt.Sprintf("0x%08x", faultAddress)) {
						t.Fatalf("literal fault precedence/retirement got=%+v want retired=%d pc=%08x fault=%08x", got, retired, pc, faultAddress)
					}
					if gotSP := jit.regs[cpu.RegisterSP]; gotSP != c.sp {
						t.Fatalf("fault committed POP writeback: got=%08x want=%08x", gotSP, c.sp)
					}
					if retired == 3 && jit.regs[cpu.RegisterR0] != 0x76540000 {
						t.Fatal("partial POP lost its already-completed first register load")
					}
				}
				assertIndexedLoadReturnEdgeStateRootR2(t, precise, jit)
			})
		}
	}
}

func TestThumbIndexedLoadReturnClassifierRejectsVariantsRootR2(t *testing.T) {
	_, jit, original := newIndexedLoadReturnEdgePairRootR2(t, indexedLoadReturnDefaultEdgeRootR2(), 0)
	if classifyThumbIndexedLoadReturn(nil) != nil {
		t.Fatal("nil block accepted")
	}
	mutations := []struct {
		name string
		edit func(*jitBlock)
	}{
		{"lsr", func(b *jitBlock) { b.thumb[0].raw |= 0x0800 }},
		{"asr", func(b *jitBlock) { b.thumb[0].raw |= 0x1000 }},
		{"sub", func(b *jitBlock) { b.thumb[1].raw |= 0x0200 }},
		{"add-immediate", func(b *jitBlock) { b.thumb[1].raw |= 0x0400 }},
		{"ldrb", func(b *jitBlock) { b.thumb[2].raw |= 0x1000; b.thumb[2].appOp = thumbAppImmediateTransferBase + 3 }},
		{"str", func(b *jitBlock) { b.thumb[2].raw &^= 0x0800; b.thumb[2].appOp = thumbAppImmediateTransferBase }},
		{"pop-no-pc", func(b *jitBlock) { b.thumb[3].raw &^= 0x0100 }},
		{"shift-flags-not-dead", func(b *jitBlock) { b.thumb[0].appOp = thumbShiftImmediate }},
		{"add-flags-dead", func(b *jitBlock) { b.thumb[1].appOp += thumbDeadFlagsOffset }},
		{"add-destination", func(b *jitBlock) { b.thumb[1].raw = b.thumb[1].raw&^7 | 5 }},
		{"add-left", func(b *jitBlock) { b.thumb[1].raw = b.thumb[1].raw&^(7<<3) | 5<<3 }},
		{"load-base", func(b *jitBlock) { b.thumb[2].raw = b.thumb[2].raw&^(7<<3) | 5<<3 }},
		{"length", func(b *jitBlock) { b.thumb = b.thumb[:3] }},
		{"non-dense-pc", func(b *jitBlock) { b.thumb[2].pc += 2 }},
		{"span", func(b *jitBlock) { b.end += 2 }},
		{"arm-ops", func(b *jitBlock) { b.arm = []jitInstr{{pc: b.start}} }},
		{"extra-loop", func(b *jitBlock) { b.extraLoop = &jitExtraLoop{} }},
		{"counted-loop", func(b *jitBlock) { b.countedLoop = &jitCountedLoop{} }},
		{"compare-tail", func(b *jitBlock) { b.compareBranchTail = &jitThumbCompareBranchTail{} }},
		{"top-exclusive-end", func(b *jitBlock) {
			b.start = 0xfffffff8
			b.end = 0
			for i := range b.thumb {
				b.thumb[i].pc = b.start + uint32(i)*2
			}
		}},
		{"wrapped-span", func(b *jitBlock) {
			b.start = 0xfffffffa
			b.end = 2
			for i := range b.thumb {
				b.thumb[i].pc = b.start + uint32(i)*2
			}
		}},
	}
	for index := 0; index < 4; index++ {
		mutations = append(mutations,
			struct {
				name string
				edit func(*jitBlock)
			}{fmt.Sprintf("class-%d", index), func(b *jitBlock) { b.thumb[index].op = thumbUnsupported }},
			struct {
				name string
				edit func(*jitBlock)
			}{fmt.Sprintf("app-op-%d", index), func(b *jitBlock) { b.thumb[index].appOp = thumbUnsupported }},
		)
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			clone := *original
			clone.thumb = append([]thumbMicroInstr(nil), original.thumb...)
			clone.indexedLoadReturn = nil
			mutation.edit(&clone)
			before := append([]thumbMicroInstr(nil), clone.thumb...)
			start, end := clone.start, clone.end
			if got := classifyThumbIndexedLoadReturn(&clone); got != nil {
				t.Fatalf("unqualified variant classified: %+v", got)
			}
			if start != clone.start || end != clone.end || !reflect.DeepEqual(before, clone.thumb) {
				t.Fatal("rejected classifier input was mutated")
			}
		})
	}
	if jit.jitBlocks[0x1000] != original || original.indexedLoadReturn == nil {
		t.Fatal("negative matrix changed the original classified translation")
	}
}
