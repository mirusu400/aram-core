//go:build windows && amd64

package interpreter

// x86-64 machine-code emitter for the native Thumb JIT. It implements the
// host-independent emitter interface (native_jit.go): the high-level methods at
// the bottom translate one Thumb instruction each, built from the byte-level
// primitives above them. Register convention is fixed:
//
//	R11        = ctx base (&regs[0]); set once by prologue at block entry
//	EAX        = working value / result (N,Z read from it in the flag helpers)
//	ECX, EDX   = scratch (EDX builds CPSR in the flag helpers)
//	R8D, R9D   = scratch (carry capture, CPSR load, condition evaluation)
//
// Guest register i lives at [R11 + 4*i]; regs[16] is CPSR (offset 64) holding
// eager N(31)/Z(30)/C(29)/V(28) plus the T bit. All operands are 32-bit unless
// noted. Encodings are validated end-to-end by the conformance differential
// against the interpreter oracle.

import "github.com/mirusu400/aram-core/cpu"

type x64emitter struct {
	buf []byte
}

func (a *x64emitter) b(bytes ...byte) { a.buf = append(a.buf, bytes...) }

func (a *x64emitter) imm32(v uint32) {
	a.buf = append(a.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (a *x64emitter) code() []byte { return a.buf }

// disp is the [R11+disp8] displacement of guest register gi (gi <= 16 -> <=64).
func disp(gi uint32) byte { return byte(4 * gi) }

const cpsrDisp = byte(4 * cpu.RegisterCPSR) // 64
const pcDisp = byte(4 * cpu.RegisterPC)     // 60

// --- setup / control -------------------------------------------------------

// prologue puts the two argument pointers in dedicated base registers: R11 =
// &regs[0] (RCX), R10 = &nativeRemain (RDX). Both are volatile under the Windows
// x64 ABI, so the leaf block may keep them without saving.
func (a *x64emitter) prologue() { a.b(0x49, 0x89, 0xCB, 0x49, 0x89, 0xD2) } // mov r11,rcx ; mov r10,rdx

func (a *x64emitter) mark() int           { return len(a.buf) }
func (a *x64emitter) appendCode(c []byte) { a.buf = append(a.buf, c...) }

func (a *x64emitter) loadEAXremain()  { a.b(0x41, 0x8B, 0x02) } // mov eax,[r10]
func (a *x64emitter) storeEAXremain() { a.b(0x41, 0x89, 0x02) } // mov [r10],eax
func (a *x64emitter) movMemPC(v uint32) {
	a.b(0x41, 0xC7, 0x43, pcDisp) // REX.B for r11 base
	a.imm32(v)
}                           // mov dword [r11+PC], imm32
func (a *x64emitter) ret1() { a.b(0xC3) } // ret

// gate: eax = remain - count; if it borrowed (remain < count) exit with
// nativeStatusBudget (PC = startPC), else commit remain -= count and fall
// through into the body. The exit block is a fixed 13 bytes, so JAE skips it
// without a fix-up.
func (a *x64emitter) gate(count int, startPC uint32) {
	a.loadEAXremain()
	a.subEAXimm(uint32(count)) // sets CF when remain < count
	a.b(0x73, 14)              // jae body_ok (skip the 14-byte exit)
	a.movMemPC(startPC)        // 8 bytes
	a.b(0xB8)
	a.imm32(nativeStatusBudget) // mov eax, 2  (5 bytes)
	a.ret1()                    // 1 byte
	a.storeEAXremain()          // body_ok: remain -= count
}

func (a *x64emitter) selfLoopUncond(gateOff int) {
	pos := a.mark()
	a.b(0xE9)
	a.imm32(uint32(int32(gateOff - (pos + 5)))) // jmp gate (backward)
}

func (a *x64emitter) selfLoopCond(cond uint8, gateOff int, nextPC uint32) {
	a.emitCondition(cond) // taken flag (0/1) in ECX
	a.testECXECX()
	pos := a.mark()
	a.b(0x0F, 0x85)
	a.imm32(uint32(int32(gateOff - (pos + 6)))) // jnz gate (backward, taken -> loop)
	// not taken: exit NORM at nextPC
	a.movMemPC(nextPC)
	a.b(0x31, 0xC0) // xor eax, eax (nativeStatusNorm)
	a.ret1()
}

func (a *x64emitter) exitBranch(pc uint32) {
	a.movMemPC(pc)
	a.b(0x31, 0xC0) // xor eax, eax
	a.ret1()
}

func (a *x64emitter) exitCondBranch(cond uint8, takenPC, nextPC uint32) {
	a.emitCondition(cond)
	a.movEAXimm(nextPC)
	a.movEDXimm(takenPC)
	a.testECXECX()
	a.cmovnzEAXEDX() // eax = cond ? taken : next
	a.storeEAXtoPC()
	a.b(0x31, 0xC0) // xor eax, eax
	a.ret1()
}

func (a *x64emitter) exitBkpt(nextPC uint32) {
	a.movMemPC(nextPC)
	a.b(0xB8)
	a.imm32(nativeStatusBKPT) // mov eax, 1
	a.ret1()
}

// --- guest register moves --------------------------------------------------

func (a *x64emitter) loadEAX(gi uint32)  { a.b(0x41, 0x8B, 0x43, disp(gi)) } // mov eax,[r11+d]
func (a *x64emitter) loadECX(gi uint32)  { a.b(0x41, 0x8B, 0x4B, disp(gi)) } // mov ecx,[r11+d]
func (a *x64emitter) storeEAX(gi uint32) { a.b(0x41, 0x89, 0x43, disp(gi)) } // mov [r11+d],eax
func (a *x64emitter) storeEAXtoPC()      { a.b(0x41, 0x89, 0x43, pcDisp) }   // mov [r11+60],eax

func (a *x64emitter) movEAXimm(v uint32) { a.b(0xB8); a.imm32(v) } // mov eax, imm32
func (a *x64emitter) movEDXimm(v uint32) { a.b(0xBA); a.imm32(v) } // mov edx, imm32

// --- arithmetic / logic ----------------------------------------------------

func (a *x64emitter) addEAXimm(v uint32) { a.b(0x05); a.imm32(v) } // add eax, imm32
func (a *x64emitter) subEAXimm(v uint32) { a.b(0x2D); a.imm32(v) } // sub eax, imm32
func (a *x64emitter) addEAXECX()         { a.b(0x01, 0xC8) }       // add eax, ecx
func (a *x64emitter) subEAXECX()         { a.b(0x29, 0xC8) }       // sub eax, ecx
func (a *x64emitter) andEAXECX()         { a.b(0x21, 0xC8) }       // and eax, ecx
func (a *x64emitter) xorEAXEAX()         { a.b(0x31, 0xC0) }       // xor eax, eax
func (a *x64emitter) notEAX()            { a.b(0xF7, 0xD0) }       // not eax
func (a *x64emitter) notECX()            { a.b(0xF7, 0xD1) }       // not ecx

func (a *x64emitter) andEAXmem(gi uint32)  { a.b(0x41, 0x23, 0x43, disp(gi)) }       // and eax,[r11+d]
func (a *x64emitter) xorEAXmem(gi uint32)  { a.b(0x41, 0x33, 0x43, disp(gi)) }       // xor eax,[r11+d]
func (a *x64emitter) orEAXmem(gi uint32)   { a.b(0x41, 0x0B, 0x43, disp(gi)) }       // or  eax,[r11+d]
func (a *x64emitter) addEAXmem(gi uint32)  { a.b(0x41, 0x03, 0x43, disp(gi)) }       // add eax,[r11+d]
func (a *x64emitter) subEAXmem(gi uint32)  { a.b(0x41, 0x2B, 0x43, disp(gi)) }       // sub eax,[r11+d]
func (a *x64emitter) imulEAXmem(gi uint32) { a.b(0x41, 0x0F, 0xAF, 0x43, disp(gi)) } // imul eax,[r11+d]

func (a *x64emitter) shlEAXimm(k uint8) { a.b(0xC1, 0xE0, k) } // shl eax, k
func (a *x64emitter) shrEAXimm(k uint8) { a.b(0xC1, 0xE8, k) } // shr eax, k
func (a *x64emitter) sarEAXimm(k uint8) { a.b(0xC1, 0xF8, k) } // sar eax, k

func (a *x64emitter) movR8DEAX()        { a.b(0x41, 0x89, 0xC0) }       // mov r8d, eax
func (a *x64emitter) shrR8Dimm(k uint8) { a.b(0x41, 0xC1, 0xE8, k) }    // shr r8d, k
func (a *x64emitter) andR8Dimm1()       { a.b(0x41, 0x83, 0xE0, 0x01) } // and r8d, 1

func (a *x64emitter) testECXECX()   { a.b(0x85, 0xC9) }       // test ecx, ecx
func (a *x64emitter) cmovnzEAXEDX() { a.b(0x0F, 0x45, 0xC2) } // cmovnz eax, edx

// --- flag commit helpers ---------------------------------------------------
//
// Each rebuilds the N/Z/C/V nibble of CPSR (regs[16]) from EAX and, for the
// arithmetic form, the freshly-set host CF/OF. They preserve every other CPSR
// bit (notably T at bit 5) by masking only the flags they own. commitNZCV must
// be emitted immediately after the defining arithmetic (its seto/setc read the
// host flags before anything clobbers them).

// commitNZ: set N,Z from EAX; preserve C,V. Result must be in EAX.
func (a *x64emitter) commitNZ() {
	a.b(0x41, 0x8B, 0x53, cpsrDisp)         // mov edx,[r11+64]
	a.b(0x81, 0xE2, 0xFF, 0xFF, 0xFF, 0x3F) // and edx, 0x3FFFFFFF (clear N,Z)
	a.b(0x89, 0xC1)                         // mov ecx, eax
	a.b(0x81, 0xE1, 0x00, 0x00, 0x00, 0x80) // and ecx, 0x80000000 (N)
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x85, 0xC0)                         // test eax, eax
	a.b(0x0F, 0x94, 0xC1)                   // setz cl
	a.b(0x0F, 0xB6, 0xC9)                   // movzx ecx, cl
	a.b(0xC1, 0xE1, 0x1E)                   // shl ecx, 30 (Z)
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x41, 0x89, 0x53, cpsrDisp)         // mov [r11+64], edx
}

// commitNZC: set N,Z from EAX and C from R8D (0/1); preserve V.
func (a *x64emitter) commitNZC() {
	a.b(0x41, 0x8B, 0x53, cpsrDisp)         // mov edx,[r11+64]
	a.b(0x81, 0xE2, 0xFF, 0xFF, 0xFF, 0x1F) // and edx, 0x1FFFFFFF (clear N,Z,C)
	a.b(0x89, 0xC1)                         // mov ecx, eax
	a.b(0x81, 0xE1, 0x00, 0x00, 0x00, 0x80) // and ecx, 0x80000000 (N)
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x85, 0xC0)                         // test eax, eax
	a.b(0x0F, 0x94, 0xC1)                   // setz cl
	a.b(0x0F, 0xB6, 0xC9)                   // movzx ecx, cl
	a.b(0xC1, 0xE1, 0x1E)                   // shl ecx, 30 (Z)
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x44, 0x89, 0xC1)                   // mov ecx, r8d (C)
	a.b(0xC1, 0xE1, 0x1D)                   // shl ecx, 29
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x41, 0x89, 0x53, cpsrDisp)         // mov [r11+64], edx
}

// commitNZCV: set N,Z from EAX and C,V from host flags. sub selects ARM carry =
// !CF (subtract) vs CF (add); V = OF either way. Emit right after the op.
func (a *x64emitter) commitNZCV(sub bool) {
	a.b(0x41, 0x0F, 0x90, 0xC0) // seto r8b (V)
	if sub {
		a.b(0x41, 0x0F, 0x93, 0xC1) // setnc r9b (ARM C = !CF)
	} else {
		a.b(0x41, 0x0F, 0x92, 0xC1) // setc r9b (ARM C = CF)
	}
	a.b(0x41, 0x8B, 0x53, cpsrDisp)         // mov edx,[r11+64]
	a.b(0x81, 0xE2, 0xFF, 0xFF, 0xFF, 0x0F) // and edx, 0x0FFFFFFF (clear N,Z,C,V)
	a.b(0x89, 0xC1)                         // mov ecx, eax
	a.b(0x81, 0xE1, 0x00, 0x00, 0x00, 0x80) // and ecx, 0x80000000 (N)
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x85, 0xC0)                         // test eax, eax
	a.b(0x0F, 0x94, 0xC1)                   // setz cl
	a.b(0x0F, 0xB6, 0xC9)                   // movzx ecx, cl
	a.b(0xC1, 0xE1, 0x1E)                   // shl ecx, 30 (Z)
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x41, 0x0F, 0xB6, 0xC9)             // movzx ecx, r9b (C)
	a.b(0xC1, 0xE1, 0x1D)                   // shl ecx, 29
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x41, 0x0F, 0xB6, 0xC8)             // movzx ecx, r8b (V)
	a.b(0xC1, 0xE1, 0x1C)                   // shl ecx, 28
	a.b(0x09, 0xCA)                         // or edx, ecx
	a.b(0x41, 0x89, 0x53, cpsrDisp)         // mov [r11+64], edx
}

// --- condition evaluation --------------------------------------------------
//
// emitCondition leaves the branch-taken flag (0/1) in ECX for an ARM condition
// code (0x0..0xd), reading CPSR once into R9D and using R8D as scratch. It
// mirrors conditionPassed exactly.

func (a *x64emitter) loadR9DfromCPSR() { a.b(0x45, 0x8B, 0x4B, cpsrDisp) } // mov r9d,[r11+64]

// bitToECX: ECX = (CPSR >> bit) & 1, from R9D.
func (a *x64emitter) bitToECX(bit uint8) {
	a.b(0x44, 0x89, 0xC9) // mov ecx, r9d
	a.b(0xC1, 0xE9, bit)  // shr ecx, bit
	a.b(0x83, 0xE1, 0x01) // and ecx, 1
}

// bitToR8D: R8D = (CPSR >> bit) & 1, from R9D.
func (a *x64emitter) bitToR8D(bit uint8) {
	a.b(0x45, 0x89, 0xC8)       // mov r8d, r9d
	a.b(0x41, 0xC1, 0xE8, bit)  // shr r8d, bit
	a.b(0x41, 0x83, 0xE0, 0x01) // and r8d, 1
}

func (a *x64emitter) xorECX1()   { a.b(0x83, 0xF1, 0x01) }       // xor ecx, 1
func (a *x64emitter) xorR8D1()   { a.b(0x41, 0x83, 0xF0, 0x01) } // xor r8d, 1
func (a *x64emitter) andECXR8D() { a.b(0x44, 0x21, 0xC1) }       // and ecx, r8d
func (a *x64emitter) orECXR8D()  { a.b(0x44, 0x09, 0xC1) }       // or ecx, r8d
func (a *x64emitter) xorECXR8D() { a.b(0x44, 0x31, 0xC1) }       // xor ecx, r8d

// Flag bit positions in CPSR.
const (
	bitN = 31
	bitZ = 30
	bitC = 29
	bitV = 28
)

func (a *x64emitter) emitCondition(condition uint8) {
	a.loadR9DfromCPSR()
	switch condition {
	case 0x0: // EQ: Z
		a.bitToECX(bitZ)
	case 0x1: // NE: !Z
		a.bitToECX(bitZ)
		a.xorECX1()
	case 0x2: // CS: C
		a.bitToECX(bitC)
	case 0x3: // CC: !C
		a.bitToECX(bitC)
		a.xorECX1()
	case 0x4: // MI: N
		a.bitToECX(bitN)
	case 0x5: // PL: !N
		a.bitToECX(bitN)
		a.xorECX1()
	case 0x6: // VS: V
		a.bitToECX(bitV)
	case 0x7: // VC: !V
		a.bitToECX(bitV)
		a.xorECX1()
	case 0x8: // HI: C && !Z
		a.bitToECX(bitC)
		a.bitToR8D(bitZ)
		a.xorR8D1()
		a.andECXR8D()
	case 0x9: // LS: !C || Z
		a.bitToECX(bitC)
		a.xorECX1()
		a.bitToR8D(bitZ)
		a.orECXR8D()
	case 0xa: // GE: N == V
		a.bitToECX(bitN)
		a.bitToR8D(bitV)
		a.xorECXR8D()
		a.xorECX1()
	case 0xb: // LT: N != V
		a.bitToECX(bitN)
		a.bitToR8D(bitV)
		a.xorECXR8D()
	case 0xc: // GT: !Z && (N == V)
		a.bitToECX(bitN)
		a.bitToR8D(bitV)
		a.xorECXR8D()
		a.xorECX1() // ECX = !(N^V)
		a.bitToR8D(bitZ)
		a.xorR8D1() // R8D = !Z
		a.andECXR8D()
	default: // 0xd LE: Z || (N != V)
		a.bitToECX(bitN)
		a.bitToR8D(bitV)
		a.xorECXR8D() // ECX = N^V
		a.bitToR8D(bitZ)
		a.orECXR8D()
	}
}

// --- emitter interface: one Thumb instruction each -------------------------

func (a *x64emitter) moveImm(rd, imm uint32) {
	a.movEAXimm(imm)
	a.storeEAX(rd)
	a.commitNZ()
}

func (a *x64emitter) addImm(rd, imm uint32) {
	a.loadEAX(rd)
	a.addEAXimm(imm)
	a.commitNZCV(false)
	a.storeEAX(rd)
}

func (a *x64emitter) subImm(rd, imm uint32) {
	a.loadEAX(rd)
	a.subEAXimm(imm)
	a.commitNZCV(true)
	a.storeEAX(rd)
}

func (a *x64emitter) cmpImm(rd, imm uint32) {
	a.loadEAX(rd)
	a.subEAXimm(imm)
	a.commitNZCV(true)
}

func (a *x64emitter) addSub(rd, rs, rn uint32, immediate, subtract bool) {
	a.loadEAX(rs)
	if immediate {
		if subtract {
			a.subEAXimm(rn)
		} else {
			a.addEAXimm(rn)
		}
	} else {
		a.loadECX(rn)
		if subtract {
			a.subEAXECX()
		} else {
			a.addEAXECX()
		}
	}
	a.commitNZCV(subtract)
	a.storeEAX(rd)
}

// shiftImm emits LSL/LSR/ASR by a compile-time immediate; carry goes into R8D
// for commitNZC (which preserves V). Mirrors the interpreter's corner cases
// (imm 0 encoding a shift of 32 for LSR/ASR, LSL #0 = MOV keeping C).
func (a *x64emitter) shiftImm(rd, rs, op, shift uint32) {
	switch op {
	case 0: // LSL
		if shift == 0 { // result = value, carry = old C -> MOV + setNZ
			a.loadEAX(rs)
			a.storeEAX(rd)
			a.commitNZ()
			return
		}
		a.loadEAX(rs)
		a.movR8DEAX()
		a.shrR8Dimm(uint8(32 - shift)) // carry = bit(32-shift)
		a.andR8Dimm1()
		a.shlEAXimm(uint8(shift))
		a.storeEAX(rd)
		a.commitNZC()
	case 1: // LSR (imm 0 -> shift of 32)
		if shift == 0 {
			a.loadEAX(rs)
			a.movR8DEAX()
			a.shrR8Dimm(31) // carry = bit31
			a.andR8Dimm1()
			a.xorEAXEAX() // result = 0
			a.storeEAX(rd)
			a.commitNZC()
			return
		}
		a.loadEAX(rs)
		a.movR8DEAX()
		a.shrR8Dimm(uint8(shift - 1)) // carry = bit(shift-1)
		a.andR8Dimm1()
		a.shrEAXimm(uint8(shift))
		a.storeEAX(rd)
		a.commitNZC()
	default: // op == 2, ASR (imm 0 -> shift of 32)
		if shift == 0 {
			a.loadEAX(rs)
			a.sarEAXimm(31) // result = 0 or 0xFFFFFFFF (sign)
			a.movR8DEAX()
			a.andR8Dimm1() // carry = bit31 (== bit0 of the sign-extended result)
			a.storeEAX(rd)
			a.commitNZC()
			return
		}
		a.loadEAX(rs)
		a.movR8DEAX()
		a.shrR8Dimm(uint8(shift - 1)) // carry = bit(shift-1)
		a.andR8Dimm1()
		a.sarEAXimm(uint8(shift))
		a.storeEAX(rd)
		a.commitNZC()
	}
}

// alu emits the register data-processing ops, bailing (false, no bytes) on the
// register-shift and carry-in sub-ops (LSL/LSR/ASR/ROR by register, ADC, SBC)
// the interpreter handles.
func (a *x64emitter) alu(op, rd, rs uint32) bool {
	switch op {
	case 0x0: // AND
		a.loadEAX(rd)
		a.andEAXmem(rs)
		a.storeEAX(rd)
		a.commitNZ()
	case 0x1: // EOR
		a.loadEAX(rd)
		a.xorEAXmem(rs)
		a.storeEAX(rd)
		a.commitNZ()
	case 0x8: // TST (no writeback)
		a.loadEAX(rd)
		a.andEAXmem(rs)
		a.commitNZ()
	case 0x9: // NEG: 0 - Rs -> setNZCV (sub)
		a.xorEAXEAX()
		a.subEAXmem(rs)
		a.commitNZCV(true)
		a.storeEAX(rd)
	case 0xa: // CMP: Rd - Rs -> setNZCV (sub), no writeback
		a.loadEAX(rd)
		a.subEAXmem(rs)
		a.commitNZCV(true)
	case 0xb: // CMN: Rd + Rs -> setNZCV (add), no writeback
		a.loadEAX(rd)
		a.addEAXmem(rs)
		a.commitNZCV(false)
	case 0xc: // ORR
		a.loadEAX(rd)
		a.orEAXmem(rs)
		a.storeEAX(rd)
		a.commitNZ()
	case 0xd: // MUL
		a.loadEAX(rd)
		a.imulEAXmem(rs)
		a.storeEAX(rd)
		a.commitNZ()
	case 0xe: // BIC: Rd & ~Rs
		a.loadEAX(rd)
		a.loadECX(rs)
		a.notECX()
		a.andEAXECX()
		a.storeEAX(rd)
		a.commitNZ()
	case 0xf: // MVN: ~Rs
		a.loadEAX(rs)
		a.notEAX()
		a.storeEAX(rd)
		a.commitNZ()
	default:
		return false // 0x2/0x3/0x4 register shifts, 0x5 ADC, 0x6 SBC, 0x7 ROR
	}
	return true
}

func (a *x64emitter) adjustStack(sub bool, offset uint32) {
	a.loadEAX(cpu.RegisterSP)
	if sub {
		a.subEAXimm(offset)
	} else {
		a.addEAXimm(offset)
	}
	a.storeEAX(cpu.RegisterSP)
}

func (a *x64emitter) addSPImm(rd, offset uint32) {
	a.loadEAX(cpu.RegisterSP)
	a.addEAXimm(offset)
	a.storeEAX(rd)
}

func (a *x64emitter) setRegConst(rd, value uint32) {
	a.movEAXimm(value)
	a.storeEAX(rd)
}
