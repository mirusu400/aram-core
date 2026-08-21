package interpreter

// AArch64 machine-code emitter for the native Thumb JIT (Android/arm64 host).
//
// This file has no build constraint on purpose: it is pure Go that only appends
// instruction words to a byte slice, so it compiles and its encodings are
// unit-tested on the amd64 development host (native_emit_arm64_test.go) even
// though the code only *executes* on arm64. Every instruction word here was
// checked against a real assembler (clang --target=aarch64). The parts that
// cannot be exercised off-device ??calling the emitted block, executable memory,
// and i-cache maintenance ??are in the build-tagged native_android_arm64.go and
// its assembly, and are documented there as requiring on-device validation.
//
// Register convention in an emitted block (a leaf AAPCS64 function; X0 = &regs[0]
// on entry, status in W0 on return):
//
//	X9        = ctx base (&regs[0]); set once by prologue
//	W0        = working value / result (N,Z read from it in the flag helpers)
//	W1, W2    = scratch (flag/CPSR assembly)
//	W3        = shift carry (0/1) passed to commitNZC
//
// Guest register i is at [X9, #4*i] (LDR/STR word offset i); regs[16] is CPSR
// with eager N(31)/Z(30)/C(29)/V(28) plus the T bit. AArch64 PSTATE.NZCV has the
// same semantics as ARM32 CPSR flags (including C = !borrow for subtract), so
// arithmetic flags come straight from the host via MRS NZCV with no fix-ups;
// logic/shift flags are assembled by hand to preserve C/V exactly as the
// interpreter does.

import "github.com/mirusu400/aram-core/cpu"

const (
	a64Base = 9  // X9 holds &regs[0]
	a64WZR  = 31 // the zero register
)

// AND-immediate templates (opcode | encoded bitmask, minus Rn/Rd), captured from
// clang: `and wd, wn, #mask`. Reused by ORing in (Rn<<5)|Rd.
const (
	a64MaskClearNZ   = 0x12007400 // & 0x3FFFFFFF (clear N,Z)
	a64MaskClearNZC  = 0x12007000 // & 0x1FFFFFFF (clear N,Z,C)
	a64MaskClearNZCV = 0x12006C00 // & 0x0FFFFFFF (clear N,Z,C,V)
	a64MaskN         = 0x12010000 // & 0x80000000 (isolate N)
	a64MaskTop4      = 0x12040C00 // & 0xF0000000 (isolate N,Z,C,V nibble)
	a64Mask1         = 0x12000000 // & 0x00000001 (isolate bit 0)
)

type arm64emitter struct {
	buf []byte
}

var _ emitter = (*arm64emitter)(nil)

func (e *arm64emitter) w(word uint32) {
	e.buf = append(e.buf, byte(word), byte(word>>8), byte(word>>16), byte(word>>24))
}

func (e *arm64emitter) code() []byte { return e.buf }

// --- primitive encoders (all validated against clang) ----------------------

func (e *arm64emitter) ldrW(rt, gi uint32) { e.w(0xB9400000 | (gi << 10) | (a64Base << 5) | rt) } // ldr wt,[x9,#4*gi]
func (e *arm64emitter) strW(rt, gi uint32) { e.w(0xB9000000 | (gi << 10) | (a64Base << 5) | rt) } // str wt,[x9,#4*gi]

func (e *arm64emitter) movz(rd, imm16 uint32)   { e.w(0x52800000 | (imm16 << 5) | rd) } // movz wd,#imm16
func (e *arm64emitter) movk16(rd, imm16 uint32) { e.w(0x72A00000 | (imm16 << 5) | rd) } // movk wd,#imm16,lsl#16

// loadConst materialises a full 32-bit constant into wd (1 or 2 instructions).
func (e *arm64emitter) loadConst(rd, v uint32) {
	e.movz(rd, v&0xffff)
	if (v>>16)&0xffff != 0 {
		e.movk16(rd, (v>>16)&0xffff)
	}
}

func (e *arm64emitter) addImm12(rd, rn, imm uint32)  { e.w(0x11000000 | (imm << 10) | (rn << 5) | rd) } // add  wd,wn,#imm
func (e *arm64emitter) subImm12(rd, rn, imm uint32)  { e.w(0x51000000 | (imm << 10) | (rn << 5) | rd) } // sub  wd,wn,#imm
func (e *arm64emitter) addsImm12(rd, rn, imm uint32) { e.w(0x31000000 | (imm << 10) | (rn << 5) | rd) } // adds wd,wn,#imm
func (e *arm64emitter) subsImm12(rd, rn, imm uint32) { e.w(0x71000000 | (imm << 10) | (rn << 5) | rd) } // subs wd,wn,#imm

func (e *arm64emitter) addsReg(rd, rn, rm uint32) { e.w(0x2B000000 | (rm << 16) | (rn << 5) | rd) } // adds wd,wn,wm
func (e *arm64emitter) subsReg(rd, rn, rm uint32) { e.w(0x6B000000 | (rm << 16) | (rn << 5) | rd) } // subs wd,wn,wm
func (e *arm64emitter) andReg(rd, rn, rm uint32)  { e.w(0x0A000000 | (rm << 16) | (rn << 5) | rd) } // and  wd,wn,wm
func (e *arm64emitter) orrReg(rd, rn, rm uint32)  { e.w(0x2A000000 | (rm << 16) | (rn << 5) | rd) } // orr  wd,wn,wm
func (e *arm64emitter) eorReg(rd, rn, rm uint32)  { e.w(0x4A000000 | (rm << 16) | (rn << 5) | rd) } // eor  wd,wn,wm
func (e *arm64emitter) bicReg(rd, rn, rm uint32)  { e.w(0x0A200000 | (rm << 16) | (rn << 5) | rd) } // bic  wd,wn,wm
func (e *arm64emitter) mvn(rd, rm uint32)         { e.w(0x2A2003E0 | (rm << 16) | rd) }             // mvn  wd,wm
func (e *arm64emitter) mul(rd, rn, rm uint32)     { e.w(0x1B007C00 | (rm << 16) | (rn << 5) | rd) } // mul  wd,wn,wm

func (e *arm64emitter) lslI(rd, rn, sh uint32) { // lsl wd,wn,#sh (1..31)
	immr := (32 - sh) & 31
	imms := 31 - sh
	e.w(0x53000000 | (immr << 16) | (imms << 10) | (rn << 5) | rd)
}
func (e *arm64emitter) lsrI(rd, rn, sh uint32) {
	e.w(0x53000000 | (sh << 16) | (31 << 10) | (rn << 5) | rd)
} // lsr wd,wn,#sh
func (e *arm64emitter) asrI(rd, rn, sh uint32) {
	e.w(0x13000000 | (sh << 16) | (31 << 10) | (rn << 5) | rd)
} // asr wd,wn,#sh

func (e *arm64emitter) csetEQ(rd uint32) { e.w(0x1A9F17E0 | rd) } // cset wd,eq
func (e *arm64emitter) csel(rd, rn, rm uint32, c uint8) {
	e.w(0x1A800000 | (rm << 16) | (uint32(c) << 12) | (rn << 5) | rd)
}                                                       // csel wd,wn,wm,cond
func (e *arm64emitter) cmp0(rn uint32)                  { e.w(0x71000000 | (rn << 5) | a64WZR) } // subs wzr,wn,#0
func (e *arm64emitter) mrsNZCV(rt uint32)               { e.w(0xD53B4200 | rt) }                 // mrs xt,nzcv
func (e *arm64emitter) msrNZCV(rt uint32)               { e.w(0xD51B4200 | rt) }                 // msr nzcv,xt
func (e *arm64emitter) andMask(rd, rn, template uint32) { e.w(template | (rn << 5) | rd) }       // and wd,wn,#mask
func (e *arm64emitter) ret()                            { e.w(0xD65F03C0) }                      // ret

// --- flag commit helpers (mirror the interpreter's flag semantics) ---------

// commitNZ sets N,Z from W0; preserves C,V.
func (e *arm64emitter) commitNZ() {
	e.ldrW(1, cpu.RegisterCPSR)
	e.andMask(1, 1, a64MaskClearNZ)
	e.andMask(2, 0, a64MaskN) // W2 = W0 & 0x80000000 (N)
	e.orrReg(1, 1, 2)
	e.cmp0(0)
	e.csetEQ(2) // W2 = (W0 == 0)
	e.lslI(2, 2, 30)
	e.orrReg(1, 1, 2)
	e.strW(1, cpu.RegisterCPSR)
}

// commitNZC sets N,Z from W0 and C from W3 (0/1); preserves V.
func (e *arm64emitter) commitNZC() {
	e.ldrW(1, cpu.RegisterCPSR)
	e.andMask(1, 1, a64MaskClearNZC)
	e.andMask(2, 0, a64MaskN)
	e.orrReg(1, 1, 2)
	e.cmp0(0)
	e.csetEQ(2)
	e.lslI(2, 2, 30)
	e.orrReg(1, 1, 2)
	e.lslI(2, 3, 29) // C from W3
	e.orrReg(1, 1, 2)
	e.strW(1, cpu.RegisterCPSR)
}

// commitNZCV sets N,Z,C,V from the host PSTATE (which matches ARM32 exactly).
// Must be emitted immediately after the defining ADDS/SUBS (MRS reads the flags
// before anything clobbers them).
func (e *arm64emitter) commitNZCV(bool) {
	e.mrsNZCV(1) // X1[31:28] = NZCV
	e.ldrW(2, cpu.RegisterCPSR)
	e.andMask(2, 2, a64MaskClearNZCV)
	e.andMask(1, 1, a64MaskTop4)
	e.orrReg(2, 2, 1)
	e.strW(2, cpu.RegisterCPSR)
}

// --- emitter interface: one Thumb instruction each -------------------------

// prologue puts the argument pointers in base registers: X9 = &regs[0] (X0),
// X10 = &nativeRemain (X1). Both are caller-saved temporaries; the leaf block
// keeps them without saving.
func (e *arm64emitter) prologue() { e.w(0xAA0003E9); e.w(0xAA0103EA) } // mov x9,x0 ; mov x10,x1

func (e *arm64emitter) mark() int           { return len(e.buf) }
func (e *arm64emitter) appendCode(c []byte) { e.buf = append(e.buf, c...) }

func (e *arm64emitter) ldrRemain(rt uint32) { e.w(0xB9400140 | rt) } // ldr wt,[x10]
func (e *arm64emitter) strRemain(rt uint32) { e.w(0xB9000140 | rt) } // str wt,[x10]

// bUncond emits B to a byte displacement relative to the current instruction.
func (e *arm64emitter) bUncond(rel int) { e.w(0x14000000 | (uint32(rel/4) & 0x3FFFFFF)) }

// bCond emits B.<cond> to a byte displacement relative to the current instruction.
func (e *arm64emitter) bCond(cond uint8, rel int) {
	e.w(0x54000000 | ((uint32(rel/4) & 0x7FFFF) << 5) | uint32(cond))
}

// patchBCond rewrites the B.cond word at byte offset pos to branch to target.
func (e *arm64emitter) patchBCond(pos, target int, cond uint8) {
	word := uint32(0x54000000) | ((uint32((target-pos)/4) & 0x7FFFF) << 5) | uint32(cond)
	e.buf[pos] = byte(word)
	e.buf[pos+1] = byte(word >> 8)
	e.buf[pos+2] = byte(word >> 16)
	e.buf[pos+3] = byte(word >> 24)
}

const a64CondHS = 2 // unsigned >= (carry set): remain >= count

func (e *arm64emitter) moveImm(rd, imm uint32) {
	e.loadConst(0, imm)
	e.strW(0, rd)
	e.commitNZ()
}

func (e *arm64emitter) addImm(rd, imm uint32) {
	e.ldrW(0, rd)
	e.addsImm12(0, 0, imm)
	e.commitNZCV(false)
	e.strW(0, rd)
}

func (e *arm64emitter) subImm(rd, imm uint32) {
	e.ldrW(0, rd)
	e.subsImm12(0, 0, imm)
	e.commitNZCV(true)
	e.strW(0, rd)
}

func (e *arm64emitter) cmpImm(rd, imm uint32) {
	e.ldrW(0, rd)
	e.subsImm12(0, 0, imm)
	e.commitNZCV(true)
}

func (e *arm64emitter) addSub(rd, rs, rn uint32, immediate, subtract bool) {
	e.ldrW(0, rs)
	if immediate {
		if subtract {
			e.subsImm12(0, 0, rn)
		} else {
			e.addsImm12(0, 0, rn)
		}
	} else {
		e.ldrW(1, rn)
		if subtract {
			e.subsReg(0, 0, 1)
		} else {
			e.addsReg(0, 0, 1)
		}
	}
	e.commitNZCV(subtract)
	e.strW(0, rd)
}

// shiftImm emits LSL/LSR/ASR by a compile-time immediate; carry goes into W3 for
// commitNZC (which preserves V). Mirrors the interpreter's corner cases (imm 0
// encoding a shift of 32 for LSR/ASR, LSL #0 = MOV keeping C).
func (e *arm64emitter) shiftImm(rd, rs, op, shift uint32) {
	switch op {
	case 0: // LSL
		if shift == 0 { // result = value, carry = old C -> MOV + setNZ
			e.ldrW(0, rs)
			e.strW(0, rd)
			e.commitNZ()
			return
		}
		e.ldrW(0, rs)
		e.lsrI(3, 0, 32-shift) // carry = bit(32-shift)
		e.andMask(3, 3, a64Mask1)
		e.lslI(0, 0, shift)
		e.strW(0, rd)
		e.commitNZC()
	case 1: // LSR (imm 0 -> shift of 32)
		if shift == 0 {
			e.ldrW(0, rs)
			e.lsrI(3, 0, 31) // carry = bit31 (already isolated in bit 0)
			e.movz(0, 0)     // result = 0
			e.strW(0, rd)
			e.commitNZC()
			return
		}
		e.ldrW(0, rs)
		e.lsrI(3, 0, shift-1) // carry = bit(shift-1)
		e.andMask(3, 3, a64Mask1)
		e.lsrI(0, 0, shift)
		e.strW(0, rd)
		e.commitNZC()
	default: // op == 2, ASR (imm 0 -> shift of 32)
		if shift == 0 {
			e.ldrW(0, rs)
			e.asrI(0, 0, 31)          // result = 0 or 0xFFFFFFFF (sign)
			e.andMask(3, 0, a64Mask1) // carry = bit31 (== bit0 of the sign result)
			e.strW(0, rd)
			e.commitNZC()
			return
		}
		e.ldrW(0, rs)
		e.lsrI(3, 0, shift-1) // carry = bit(shift-1) of the original value
		e.andMask(3, 3, a64Mask1)
		e.asrI(0, 0, shift)
		e.strW(0, rd)
		e.commitNZC()
	}
}

// alu emits the register data-processing ops, bailing (false, no bytes) on the
// register-shift and carry-in sub-ops (LSL/LSR/ASR/ROR by register, ADC, SBC).
func (e *arm64emitter) alu(op, rd, rs uint32) bool {
	switch op {
	case 0x0: // AND
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.andReg(0, 0, 1)
		e.strW(0, rd)
		e.commitNZ()
	case 0x1: // EOR
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.eorReg(0, 0, 1)
		e.strW(0, rd)
		e.commitNZ()
	case 0x8: // TST (no writeback)
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.andReg(0, 0, 1)
		e.commitNZ()
	case 0x9: // NEG: 0 - Rs -> setNZCV
		e.ldrW(1, rs)
		e.subsReg(0, a64WZR, 1)
		e.commitNZCV(true)
		e.strW(0, rd)
	case 0xa: // CMP: Rd - Rs -> setNZCV (no writeback)
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.subsReg(0, 0, 1)
		e.commitNZCV(true)
	case 0xb: // CMN: Rd + Rs -> setNZCV (no writeback)
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.addsReg(0, 0, 1)
		e.commitNZCV(false)
	case 0xc: // ORR
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.orrReg(0, 0, 1)
		e.strW(0, rd)
		e.commitNZ()
	case 0xd: // MUL
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.mul(0, 0, 1)
		e.strW(0, rd)
		e.commitNZ()
	case 0xe: // BIC: Rd & ~Rs
		e.ldrW(0, rd)
		e.ldrW(1, rs)
		e.bicReg(0, 0, 1)
		e.strW(0, rd)
		e.commitNZ()
	case 0xf: // MVN: ~Rs
		e.ldrW(1, rs)
		e.mvn(0, 1)
		e.strW(0, rd)
		e.commitNZ()
	default:
		return false // 0x2/0x3/0x4 register shifts, 0x5 ADC, 0x6 SBC, 0x7 ROR
	}
	return true
}

func (e *arm64emitter) adjustStack(sub bool, offset uint32) {
	e.ldrW(0, cpu.RegisterSP)
	if sub {
		e.subImm12(0, 0, offset)
	} else {
		e.addImm12(0, 0, offset)
	}
	e.strW(0, cpu.RegisterSP)
}

func (e *arm64emitter) addSPImm(rd, offset uint32) {
	e.ldrW(0, cpu.RegisterSP)
	e.addImm12(0, 0, offset)
	e.strW(0, rd)
}

func (e *arm64emitter) setRegConst(rd, value uint32) {
	e.loadConst(0, value)
	e.strW(0, rd)
}

// gate: w0 = remain - count; if it borrowed (remain < count) exit with
// nativeStatusBudget (PC = startPC), else commit remain -= count and fall
// through. The exit block has a data-dependent size (loadConst is 1-2 words), so
// the skip branch is backpatched rather than using a fixed displacement.
func (e *arm64emitter) gate(count int, startPC uint32) {
	e.ldrRemain(0)
	e.subsImm12(0, 0, uint32(count)) // sets C=0 (borrow) when remain < count
	skip := e.mark()
	e.bCond(a64CondHS, 0) // placeholder: if remain >= count, skip the exit
	// exit_budget: leave remain unchanged, set PC=startPC, return Budget status.
	e.loadConst(1, startPC)
	e.strW(1, cpu.RegisterPC)
	e.movz(0, nativeStatusBudget)
	e.ret()
	e.patchBCond(skip, e.mark(), a64CondHS)
	// body_ok:
	e.strRemain(0) // commit remain -= count
}

func (e *arm64emitter) selfLoopUncond(gateOff int) {
	e.bUncond(gateOff - e.mark())
}

func (e *arm64emitter) selfLoopCond(cond uint8, gateOff int, nextPC uint32) {
	e.ldrW(1, cpu.RegisterCPSR)
	e.andMask(1, 1, a64MaskTop4)
	e.msrNZCV(1)
	e.bCond(cond, gateOff-e.mark()) // taken -> loop back to the gate
	// not taken: exit NORM at nextPC
	e.loadConst(0, nextPC)
	e.strW(0, cpu.RegisterPC)
	e.movz(0, nativeStatusNorm)
	e.ret()
}

func (e *arm64emitter) exitBranch(pc uint32) {
	e.loadConst(0, pc)
	e.strW(0, cpu.RegisterPC)
	e.movz(0, nativeStatusNorm)
	e.ret()
}

func (e *arm64emitter) exitCondBranch(cond uint8, takenPC, nextPC uint32) {
	e.ldrW(1, cpu.RegisterCPSR)
	e.andMask(1, 1, a64MaskTop4)
	e.msrNZCV(1)
	e.loadConst(2, takenPC)
	e.loadConst(3, nextPC)
	e.csel(0, 2, 3, cond) // W0 = cond ? taken : next
	e.strW(0, cpu.RegisterPC)
	e.movz(0, nativeStatusNorm)
	e.ret()
}

func (e *arm64emitter) exitBkpt(nextPC uint32) {
	e.loadConst(0, nextPC)
	e.strW(0, cpu.RegisterPC)
	e.movz(0, nativeStatusBKPT)
	e.ret()
}
