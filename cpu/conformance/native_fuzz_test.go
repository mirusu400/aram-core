//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package conformance

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// This file exhaustively exercises the hand-encoded x86-64 emitter of the native
// Thumb JIT against the interpreter oracle: every ARM condition code across
// every N/Z/C/V state, every shift-immediate form, every translated ALU op, and
// randomized straight-line programs that also cross the native->interpreter bail
// boundary (ADC/SBC/register shifts fall back). Correctness is defined solely as
// bit-for-bit equality with the interpreter, so these need no expected values.

func code(words ...uint16) []byte {
	out := make([]byte, 0, len(words)*2)
	for _, w := range words {
		out = append(out, byte(w), byte(w>>8))
	}
	return out
}

// Thumb encoders (low registers 0..7 unless noted).
func movImm(rd, imm uint16) uint16      { return 0x2000 | rd<<8 | imm&0xff }
func cmpImm(rd, imm uint16) uint16      { return 0x2800 | rd<<8 | imm&0xff }
func addImm(rd, imm uint16) uint16      { return 0x3000 | rd<<8 | imm&0xff }
func subImm(rd, imm uint16) uint16      { return 0x3800 | rd<<8 | imm&0xff }
func addReg(rd, rs, rn uint16) uint16   { return 0x1800 | rn<<6 | rs<<3 | rd }
func subReg(rd, rs, rn uint16) uint16   { return 0x1a00 | rn<<6 | rs<<3 | rd }
func addImm3(rd, rs, imm uint16) uint16 { return 0x1c00 | imm<<6 | rs<<3 | rd }
func subImm3(rd, rs, imm uint16) uint16 { return 0x1e00 | imm<<6 | rs<<3 | rd }
func lslImm(rd, rs, sh uint16) uint16   { return 0x0000 | sh<<6 | rs<<3 | rd }
func lsrImm(rd, rs, sh uint16) uint16   { return 0x0800 | sh<<6 | rs<<3 | rd }
func asrImm(rd, rs, sh uint16) uint16   { return 0x1000 | sh<<6 | rs<<3 | rd }
func alu(op, rd, rs uint16) uint16      { return 0x4000 | op<<6 | rs<<3 | rd }
func adjSP(sub bool, imm uint16) uint16 {
	if sub {
		return 0xb080 | imm&0x7f
	}
	return 0xb000 | imm&0x7f
}
func addSP(rd, imm uint16) uint16 { return 0xa800 | rd<<8 | imm&0xff }
func addPC(rd, imm uint16) uint16 { return 0xa000 | rd<<8 | imm&0xff }
func bcc(cond, imm uint16) uint16 { return 0xd000 | cond<<8 | imm&0xff }

const bkpt = 0xbe00

func mustAgree(t *testing.T, name string, p Program) {
	t.Helper()
	oracle, err := Execute(interp, p)
	if err != nil {
		t.Fatalf("%s: oracle: %v", name, err)
	}
	native, err := Execute(newNative, p)
	if err != nil {
		t.Fatalf("%s: native: %v", name, err)
	}
	if d := Diff(oracle, native); d != "" {
		t.Fatalf("%s: native diverged: %s\n  code=% x regs=%v", name, d, p.Code, p.Regs)
	}
}

// TestNativeConditionCodes covers every conditional-branch code (0x0..0xd) under
// every N/Z/C/V combination: the CPSR is seeded directly and the branch skips
// one instruction, so the taken/not-taken decision and the CMOV selection are
// checked for all 16 flag states.
func TestNativeConditionCodes(t *testing.T) {
	for cond := uint16(0); cond <= 0xd; cond++ {
		for nzcv := uint16(0); nzcv < 16; nzcv++ {
			cpsr := uint32(nzcv)<<28 | cpu.StatusThumb
			p := Program{
				Name: fmt.Sprintf("cond%x/nzcv%x", cond, nzcv),
				Mode: cpu.ModeThumb,
				Regs: map[uint32]uint32{cpu.RegisterCPSR: cpsr},
				Code: code(
					bcc(cond, 0),    // taken -> skip the next instruction
					movImm(0, 0xaa), // executed only when not taken
					movImm(1, 0xbb),
					bkpt,
				),
			}
			mustAgree(t, p.Name, p)
		}
	}
}

// TestNativeShiftImmediate covers LSL/LSR/ASR by every amount 0..31 over a set
// of bit patterns, validating result and the carry-out corner cases (amount 0
// encoding a shift of 32 for LSR/ASR, carry preservation for LSL #0).
func TestNativeShiftImmediate(t *testing.T) {
	values := []uint32{0, 1, 0x80000000, 0xffffffff, 0xc0000000, 0x00010000, 0x0000ffff, 0xa5a5a5a5}
	ops := []struct {
		name string
		enc  func(rd, rs, sh uint16) uint16
	}{
		{"lsl", lslImm}, {"lsr", lsrImm}, {"asr", asrImm},
	}
	for _, op := range ops {
		for sh := uint16(0); sh < 32; sh++ {
			for _, v := range values {
				p := Program{
					Name: fmt.Sprintf("%s#%d/%08x", op.name, sh, v),
					Mode: cpu.ModeThumb,
					// Seed carry both ways via CPSR so LSL #0 (carry = old C) is
					// exercised in both states.
					Regs: map[uint32]uint32{cpu.RegisterR1: v, cpu.RegisterCPSR: cpu.StatusThumb | flagCbit},
					Code: code(op.enc(0, 1, sh), bkpt),
				}
				mustAgree(t, p.Name, p)
				p.Regs[cpu.RegisterCPSR] = cpu.StatusThumb
				mustAgree(t, p.Name+"/noC", p)
			}
		}
	}
}

const flagCbit = uint32(1) << 29

// TestNativeALUOps covers every thumbALU sub-op (including the ones the native
// backend bails on: ADC/SBC/register shifts/ROR) over varied operands, so both
// the translated ops and the fall-back boundary match the interpreter.
func TestNativeALUOps(t *testing.T) {
	operands := [][2]uint32{
		{0, 0}, {1, 0}, {0, 1}, {0xffffffff, 1}, {0x80000000, 0x80000000},
		{0x7fffffff, 1}, {0xdeadbeef, 0x0000000f}, {5, 40}, {40, 5}, {0xffffffff, 0xffffffff},
	}
	for op := uint16(0); op < 16; op++ {
		for _, o := range operands {
			for _, c := range []uint32{0, flagCbit} {
				p := Program{
					Name: fmt.Sprintf("alu%x/%08x,%08x/C%d", op, o[0], o[1], c>>29),
					Mode: cpu.ModeThumb,
					Regs: map[uint32]uint32{
						cpu.RegisterR0: o[0], cpu.RegisterR1: o[1],
						cpu.RegisterCPSR: cpu.StatusThumb | c,
					},
					Code: code(alu(op, 0, 1), bkpt),
				}
				mustAgree(t, p.Name, p)
			}
		}
	}
}

// TestNativeImmediateAndAddSub covers MOV/CMP/ADD/SUB immediate and the
// register/imm3 add-subtract forms across edge immediates and operands.
func TestNativeImmediateAndAddSub(t *testing.T) {
	regvals := []uint32{0, 1, 0xff, 0x100, 0x7fffffff, 0x80000000, 0xffffffff}
	imms := []uint16{0, 1, 2, 0x7f, 0xff}
	for _, v := range regvals {
		base := map[uint32]uint32{cpu.RegisterR0: v, cpu.RegisterR1: v, cpu.RegisterR2: 7}
		for _, imm := range imms {
			for _, enc := range []struct {
				n string
				w uint16
			}{
				{"mov", movImm(0, imm)}, {"cmp", cmpImm(0, imm)},
				{"add", addImm(0, imm)}, {"sub", subImm(0, imm)},
			} {
				p := Program{Name: fmt.Sprintf("%s/%08x/#%d", enc.n, v, imm), Mode: cpu.ModeThumb,
					Regs: cloneRegs(base), Code: code(enc.w, bkpt)}
				mustAgree(t, p.Name, p)
			}
		}
		for imm3 := uint16(0); imm3 < 8; imm3++ {
			for _, enc := range []struct {
				n string
				w uint16
			}{
				{"addI3", addImm3(0, 1, imm3)}, {"subI3", subImm3(0, 1, imm3)},
				{"addR", addReg(0, 1, 2)}, {"subR", subReg(0, 1, 2)},
			} {
				p := Program{Name: fmt.Sprintf("%s/%08x/#%d", enc.n, v, imm3), Mode: cpu.ModeThumb,
					Regs: cloneRegs(base), Code: code(enc.w, bkpt)}
				mustAgree(t, p.Name, p)
			}
		}
	}
}

// TestNativeStackAndPCRelative covers SP adjust and ADD Rd, SP/PC, #imm.
func TestNativeStackAndPCRelative(t *testing.T) {
	for imm := uint16(0); imm < 0x80; imm += 7 {
		for _, sub := range []bool{false, true} {
			p := Program{Name: fmt.Sprintf("adjsp/%v/#%d", sub, imm), Mode: cpu.ModeThumb,
				Code: code(adjSP(sub, imm), bkpt)}
			mustAgree(t, p.Name, p)
		}
	}
	for imm := uint16(0); imm < 0x100; imm += 13 {
		mustAgree(t, fmt.Sprintf("addsp/#%d", imm), Program{Mode: cpu.ModeThumb,
			Code: code(addSP(0, imm), bkpt)})
		mustAgree(t, fmt.Sprintf("addpc/#%d", imm), Program{Mode: cpu.ModeThumb,
			Code: code(addPC(0, imm), bkpt)})
	}
}

// TestNativeLoopRetires runs a countdown loop (backward conditional branch) and
// requires identical final state AND identical retired-instruction counts,
// checking block re-dispatch and exact instruction accounting.
func TestNativeLoopRetires(t *testing.T) {
	for _, start := range []uint16{1, 5, 20, 100} {
		p := Program{
			Name: fmt.Sprintf("countdown/%d", start),
			Mode: cpu.ModeThumb,
			Code: code(
				movImm(0, start), // r0 = start
				subImm(0, 1),     // loop: subs r0, #1
				bcc(0x1, 0x1fd),  // bne loop (offset -6 -> back to subs)
				bkpt,
			),
			Budget: 100000,
		}
		mustAgree(t, p.Name, p)
	}
}

// TestNativeRandomStraightLine fuzzes randomized straight-line programs built
// only from the arithmetic/logic/shift/move classes (plus BKPT), with random
// initial registers. It stresses flag computation and the native->interpreter
// bail path (a generated ADC/SBC/register-shift ends a native block mid-stream).
func TestNativeRandomStraightLine(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5eed1234))
	gens := []func() uint16{
		func() uint16 { return movImm(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return cmpImm(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return addImm(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return subImm(r3(rng), uint16(rng.Intn(256))) },
		func() uint16 { return addReg(r3(rng), r3(rng), r3(rng)) },
		func() uint16 { return subReg(r3(rng), r3(rng), r3(rng)) },
		func() uint16 { return addImm3(r3(rng), r3(rng), uint16(rng.Intn(8))) },
		func() uint16 { return subImm3(r3(rng), r3(rng), uint16(rng.Intn(8))) },
		func() uint16 { return lslImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return lsrImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return asrImm(r3(rng), r3(rng), uint16(rng.Intn(32))) },
		func() uint16 { return alu(uint16(rng.Intn(16)), r3(rng), r3(rng)) },
		func() uint16 { return adjSP(rng.Intn(2) == 0, uint16(rng.Intn(0x80))) },
	}
	for iter := 0; iter < 4000; iter++ {
		n := 1 + rng.Intn(12)
		words := make([]uint16, 0, n+1)
		for i := 0; i < n; i++ {
			words = append(words, gens[rng.Intn(len(gens))]())
		}
		words = append(words, bkpt)
		regs := map[uint32]uint32{cpu.RegisterCPSR: cpu.StatusThumb}
		if rng.Intn(2) == 0 {
			regs[cpu.RegisterCPSR] |= flagCbit
		}
		for r := uint32(0); r < 8; r++ {
			regs[r] = rng.Uint32()
		}
		mustAgree(t, fmt.Sprintf("rand/%d", iter), Program{
			Name: fmt.Sprintf("rand/%d", iter), Mode: cpu.ModeThumb, Regs: regs, Code: code(words...),
		})
	}
}

func r3(rng *rand.Rand) uint16 { return uint16(rng.Intn(8)) }

func cloneRegs(m map[uint32]uint32) map[uint32]uint32 {
	out := make(map[uint32]uint32, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
