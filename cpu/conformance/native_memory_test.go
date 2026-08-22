//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package conformance

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Memory differential for the native JIT's inline software-TLB path. The native
// emitters translate single loads and stores into a TLB probe plus a direct host
// access instead of calling back into Go, so every addressing form, width, sign
// extension, alignment, page boundary and fault has to reproduce the interpreter
// oracle exactly - including the retired-instruction count, which frame pacing
// depends on and which the bail path has to reconstruct.
//
// The harness maps three adjacent one-page regions (code RWX at 0x1000, data RW
// at 0x2000, stack RW at 0x3000), so these cases naturally cover an access that
// starts in one region and runs into the next, an access that leaves mapped
// memory entirely, and stores into the executable page the code itself runs
// from.

// Thumb single-transfer encoders (low registers only, as the encodings allow).
func ldrImm(rd, rb, imm5 uint16) uint16  { return 0x6800 | imm5<<6 | rb<<3 | rd }
func strImm(rd, rb, imm5 uint16) uint16  { return 0x6000 | imm5<<6 | rb<<3 | rd }
func ldrbImm(rd, rb, imm5 uint16) uint16 { return 0x7800 | imm5<<6 | rb<<3 | rd }
func strbImm(rd, rb, imm5 uint16) uint16 { return 0x7000 | imm5<<6 | rb<<3 | rd }
func ldrhImm(rd, rb, imm5 uint16) uint16 { return 0x8800 | imm5<<6 | rb<<3 | rd }
func strhImm(rd, rb, imm5 uint16) uint16 { return 0x8000 | imm5<<6 | rb<<3 | rd }
func ldrSP(rd, imm8 uint16) uint16       { return 0x9800 | rd<<8 | imm8&0xff }
func strSP(rd, imm8 uint16) uint16       { return 0x9000 | rd<<8 | imm8&0xff }
func ldrPC(rd, imm8 uint16) uint16       { return 0x4800 | rd<<8 | imm8&0xff }

// regTransfer encodes the register-offset family: op 0 STR, 1 STRH, 2 STRB,
// 3 LDRSB, 4 LDR, 5 LDRH, 6 LDRB, 7 LDRSH.
func regTransfer(op, rd, rb, ro uint16) uint16 { return 0x5000 | op<<9 | ro<<6 | rb<<3 | rd }

// memBases are the addresses these tests point a base register at: region
// starts, unaligned offsets, the last bytes of a region (so a wide access runs
// into the neighbouring region), and unmapped addresses.
var memBases = []uint32{
	DataBase, DataBase + 1, DataBase + 2, DataBase + 3, DataBase + 4,
	DataBase + DataSize - 4, DataBase + DataSize - 3, DataBase + DataSize - 2,
	DataBase + DataSize - 1,
	CodeBase, CodeBase + 0x10, CodeBase + CodeSize - 2,
	StackBase, StackBase + 0x40, StackTop - 4, StackTop - 1, StackTop,
	0, 0x900, 0x9000, 0xfffffffc,
}

// memPattern seeds the scratch region so loads see distinguishable bytes.
func memPattern() map[uint32][]byte {
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(0x80 + i*3)
	}
	return map[uint32][]byte{DataBase: data}
}

// memProgram emits the given transfer sequence THREE times before the BKPT.
// That repetition is what makes these cases test the inline path at all: the
// software TLB starts cold, so the first execution of a memory instruction
// always misses and bails to the interpreter (which installs the page). Only a
// later execution runs the emitted probe-and-access code. A single copy would
// silently test nothing but the bail path.
func memProgram(name string, base uint32, index uint32, words ...uint16) Program {
	body := make([]uint16, 0, 3*len(words)+1)
	for range 3 {
		body = append(body, words...)
	}
	return Program{
		Name: name,
		Mode: cpu.ModeThumb,
		Regs: map[uint32]uint32{
			cpu.RegisterR0: 0xdeadbeef,
			cpu.RegisterR1: base,
			cpu.RegisterR2: index,
			cpu.RegisterR3: 0x11223344,
		},
		Data:     memPattern(),
		Code:     code(append(body, bkpt)...),
		CaptureN: DataSize,
	}
}

// TestNativeSingleTransferForms walks every single load/store encoding the
// native emitters translate, over every interesting base address. Each case
// stores and then loads back, so both directions are compared, and a faulting
// address is compared as a fault (same reason, PC, error text and retired count).
func TestNativeSingleTransferForms(t *testing.T) {
	forms := []struct {
		name  string
		words []uint16
	}{
		{"str/ldr#0", []uint16{strImm(0, 1, 0), ldrImm(3, 1, 0)}},
		{"str/ldr#4", []uint16{strImm(0, 1, 1), ldrImm(3, 1, 1)}},
		{"str/ldr#124", []uint16{strImm(0, 1, 31), ldrImm(3, 1, 31)}},
		{"strb/ldrb#0", []uint16{strbImm(0, 1, 0), ldrbImm(3, 1, 0)}},
		{"strb/ldrb#31", []uint16{strbImm(0, 1, 31), ldrbImm(3, 1, 31)}},
		{"strh/ldrh#0", []uint16{strhImm(0, 1, 0), ldrhImm(3, 1, 0)}},
		{"strh/ldrh#62", []uint16{strhImm(0, 1, 31), ldrhImm(3, 1, 31)}},
		{"reg str/ldr", []uint16{regTransfer(0, 0, 1, 2), regTransfer(4, 3, 1, 2)}},
		{"reg strh/ldrh", []uint16{regTransfer(1, 0, 1, 2), regTransfer(5, 3, 1, 2)}},
		{"reg strb/ldrb", []uint16{regTransfer(2, 0, 1, 2), regTransfer(6, 3, 1, 2)}},
		{"reg ldrsb", []uint16{regTransfer(2, 0, 1, 2), regTransfer(3, 3, 1, 2)}},
		{"reg ldrsh", []uint16{regTransfer(1, 0, 1, 2), regTransfer(7, 3, 1, 2)}},
		{"ldrb-only", []uint16{ldrbImm(3, 1, 0)}},
		{"ldrh-only", []uint16{ldrhImm(3, 1, 0)}},
		{"ldr-only", []uint16{ldrImm(3, 1, 0)}},
	}
	for _, base := range memBases {
		for _, index := range []uint32{0, 1, 2, 3, 8} {
			for _, f := range forms {
				name := fmt.Sprintf("%s/base=%08x/ro=%d", f.name, base, index)
				mustAgree(t, name, memProgram(name, base, index, f.words...))
			}
		}
	}
}

// TestNativeStackAndLiteralTransfers covers the SP-relative form (whose base is
// a high register) and the PC-relative literal load (whose address is a compile-
// time constant, so the emitters fold it), including SP values that make the
// access straddle or leave the stack region.
func TestNativeStackAndLiteralTransfers(t *testing.T) {
	stacks := []uint32{
		StackBase, StackBase + 4, StackBase + 2, StackTop - 4, StackTop - 2,
		StackTop, DataBase, 0x9000,
	}
	for _, sp := range stacks {
		for _, imm := range []uint16{0, 1, 3, 255} {
			name := fmt.Sprintf("sp-transfer/sp=%08x/#%d", sp, imm)
			p := memProgram(name, DataBase, 0, strSP(0, imm), ldrSP(3, imm))
			p.Regs[cpu.RegisterSP] = sp
			mustAgree(t, name, p)
		}
	}
	for _, imm := range []uint16{0, 1, 2, 200, 255} {
		name := fmt.Sprintf("literal-load/#%d", imm)
		mustAgree(t, name, memProgram(name, DataBase, 0, ldrPC(3, imm)))
	}
}

// TestNativeMemoryLoopRetires runs a copy loop between two regions with a
// backward branch, so the block re-dispatches, the software TLB holds a source
// page and a destination page at once, and both the copied bytes and the exact
// retired-instruction count must match the interpreter.
func TestNativeMemoryLoopRetires(t *testing.T) {
	for _, n := range []uint16{1, 2, 7, 64} {
		for _, budget := range []uint64{4, 16, 1024, 100000} {
			name := fmt.Sprintf("copyloop/n=%d/budget=%d", n, budget)
			p := Program{
				Name: name,
				Mode: cpu.ModeThumb,
				Regs: map[uint32]uint32{
					cpu.RegisterR0: DataBase,
					cpu.RegisterR1: StackBase,
					cpu.RegisterR3: uint32(n),
				},
				Data: memPattern(),
				Code: code(
					ldrbImm(2, 0, 0), // loop: ldrb r2,[r0]
					strbImm(2, 1, 0), //       strb r2,[r1]
					addImm(0, 1),     //       adds r0,#1
					addImm(1, 1),     //       adds r1,#1
					subImm(3, 1),     //       subs r3,#1
					bcc(0x1, 0x1f9),  //       bne loop
					bkpt,
				),
				Budget:   budget,
				CaptureN: DataSize,
			}
			mustAgree(t, name, p)
		}
	}
}

// TestNativeRandomMemoryPrograms fuzzes programs that mix the transfer forms
// with arithmetic, pointing base registers at random addresses in and around the
// mapped regions. It stresses the interaction the directed cases cannot
// enumerate: TLB fills and misses interleaved with block re-translation, bails
// in the middle of a block (which must give back exactly the unretired part of
// the budget), and faults raised from inside a translated block.
func TestNativeRandomMemoryPrograms(t *testing.T) {
	rng := rand.New(rand.NewSource(0x10ADED))
	addrs := []uint32{
		DataBase, DataBase + 1, DataBase + 0xff0, DataBase + DataSize - 1,
		StackBase, StackTop - 1, CodeBase, CodeBase + CodeSize - 1, 0, 0x9000,
	}
	gens := []func() uint16{
		func() uint16 { return ldrImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return strImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return ldrbImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return strbImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return ldrhImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return strhImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return regTransfer(uint16(rng.Intn(8)), r3(rng), r3(rng), r3(rng)) },
		func() uint16 { return ldrSP(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return strSP(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return ldrPC(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return addImm(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return subImm(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return alu(uint16(rng.Intn(16)), r3(rng), r3(rng)) },
		func() uint16 { return lslImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
	}
	for iter := 0; iter < 4000; iter++ {
		n := 1 + rng.Intn(14)
		words := make([]uint16, 0, n+1)
		for i := 0; i < n; i++ {
			words = append(words, gens[rng.Intn(len(gens))]())
		}
		words = append(words, bkpt)
		regs := map[uint32]uint32{cpu.RegisterCPSR: cpu.StatusThumb}
		for r := uint32(0); r < 8; r++ {
			// Mostly plausible pointers, sometimes an arbitrary word, so both
			// the hit path and the fault path are exercised.
			if rng.Intn(4) == 0 {
				regs[r] = rng.Uint32()
			} else {
				regs[r] = addrs[rng.Intn(len(addrs))] + uint32(rng.Intn(8))
			}
		}
		name := fmt.Sprintf("randmem/%d", iter)
		mustAgree(t, name, Program{
			Name: name, Mode: cpu.ModeThumb, Regs: regs, Data: memPattern(),
			Code: code(words...), Budget: 4096, CaptureN: DataSize,
		})
	}
}

// TestNativeCrossPageAfterWarmTLB is the case the plain form sweep cannot
// reach. An access that straddles two pages must never be serviced inline: a
// software TLB entry describes exactly one page, and the interpreter services a
// straddling access byte-wise across whatever regions it spans. The sweep above
// misses this because a straddling access never installs a TLB entry, so it
// bails for a second reason and the page-crossing check is never the thing
// under test. Here an in-page access warms the page first, so the crossing
// check is the only guard left.
//
// The decisive base is the LAST mapped page (the stack), whose next page is
// unmapped: the interpreter must fault, and an inline access would instead read
// or write the bytes that happen to follow the region's backing array and not
// fault at all. Straddling into the next MAPPED region is checked too, but on
// its own it is not a reliable detector - Go may place the two regions' backing
// arrays adjacently, in which case reading past one lands exactly on the other.
func TestNativeCrossPageAfterWarmTLB(t *testing.T) {
	for _, edge := range []struct {
		name       string
		warm, last uint32
	}{
		{"into-mapped", DataBase, DataBase + DataSize},
		{"into-unmapped", StackBase, StackTop},
	} {
		for _, off := range []uint32{4, 3, 2, 1} {
			for _, form := range []struct {
				name       string
				load, save uint16
			}{
				{"word", ldrImm(4, 2, 0), strImm(0, 2, 0)},
				{"half", ldrhImm(4, 2, 0), strhImm(0, 2, 0)},
				{"byte", ldrbImm(4, 2, 0), strbImm(0, 2, 0)},
			} {
				name := fmt.Sprintf("cross/%s/%s/-%d", edge.name, form.name, off)
				mustAgree(t, name, Program{
					Name: name,
					Mode: cpu.ModeThumb,
					Regs: map[uint32]uint32{
						cpu.RegisterR0: 0xdeadbeef,
						cpu.RegisterR1: edge.warm,
						cpu.RegisterR2: edge.last - off,
						cpu.RegisterR5: StackBase,
					},
					Data: map[uint32][]byte{
						DataBase:  {0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88},
						StackBase: {0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18},
					},
					Code: code(
						ldrImm(3, 1, 0), // warm this page's read entry
						strImm(0, 1, 0), // warm this page's write entry
						ldrImm(3, 1, 0), // now served inline
						strImm(0, 1, 0),
						form.load,       // the straddling access, on a warm page
						form.save,       //
						ldrImm(6, 5, 0), // read back what the store left behind
						bkpt,
					),
					CaptureN: DataSize,
				})
			}
		}
	}
}
