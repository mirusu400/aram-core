//go:build windows && amd64

package interpreter

// x86-64 machine-code emitter for the native Thumb JIT. It implements the
// host-independent emitter interface (native_jit.go): the high-level methods at
// the bottom translate one Thumb instruction each, built from the byte-level
// primitives above them. Register convention is fixed:
//
//	R11        = ctx base (&regs[0]); set once by prologue at block entry
//	RDI        = &interruptLines in whole-system blocks (preserved)
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
	buf            []byte
	tlb            uintptr // host address of the backend's software TLB (native_tlb.go)
	interruptLines uintptr // address of Backend.interruptLines for system polls
	activeCount    uintptr // address of Backend.nativeActiveCount
	bailAddress    uintptr // address of Backend.nativeBailAddress
}

func (a *x64emitter) b(bytes ...byte) { a.buf = append(a.buf, bytes...) }

func (a *x64emitter) imm32(v uint32) {
	a.buf = append(a.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (a *x64emitter) imm64(v uint64) {
	a.buf = append(a.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

func (a *x64emitter) code() []byte { return a.buf }

// disp is the [R11+disp8] displacement of guest register gi (gi <= 16 -> <=64).
func disp(gi uint32) byte { return byte(4 * gi) }

const cpsrDisp = byte(4 * cpu.RegisterCPSR) // 64
const pcDisp = byte(4 * cpu.RegisterPC)     // 60

// --- setup / control -------------------------------------------------------

// prologue puts the two argument pointers in dedicated base registers: R11 =
// &regs[0] (RCX), R10 = &nativeRemain (RDX). A whole-system block additionally
// preserves RDI and keeps &interruptLines there, avoiding an imm64 load in every
// guest instruction's poll.
func (a *x64emitter) prologue() {
	a.b(0x49, 0x89, 0xCB, 0x49, 0x89, 0xD2) // mov r11,rcx ; mov r10,rdx
	if a.interruptLines != 0 {
		a.b(0x57, 0x48, 0xBF) // push rdi ; mov rdi,imm64
		a.imm64(uint64(a.interruptLines))
	}
}

func (a *x64emitter) mark() int           { return len(a.buf) }
func (a *x64emitter) appendCode(c []byte) { a.buf = append(a.buf, c...) }

func (a *x64emitter) loadEAXremain()  { a.b(0x41, 0x8B, 0x02) } // mov eax,[r10]
func (a *x64emitter) storeEAXremain() { a.b(0x41, 0x89, 0x02) } // mov [r10],eax
func (a *x64emitter) movMemPC(v uint32) {
	a.b(0x41, 0xC7, 0x43, pcDisp) // REX.B for r11 base
	a.imm32(v)
} // mov dword [r11+PC], imm32
func (a *x64emitter) ret1() {
	if a.interruptLines != 0 {
		a.b(0x5F) // pop rdi
	}
	a.b(0xC3)
}

// gate: eax = remain - count; if it borrowed (remain < count) exit with
// nativeStatusBudget (PC = startPC), else commit remain -= count and fall
// through into the body. The exit size differs by one byte in system blocks
// because they restore RDI before returning.
func (a *x64emitter) gate(count int, startPC uint32) {
	a.loadEAXremain()
	a.subEAXimm(uint32(count)) // sets CF when remain < count
	exitSize := byte(14)
	if a.interruptLines != 0 {
		exitSize++
	}
	a.b(0x73, exitSize) // jae body_ok
	a.movMemPC(startPC) // 8 bytes
	a.b(0xB8)
	a.imm32(nativeStatusBudget) // mov eax, 2  (5 bytes)
	a.ret1()                    // 1 byte
	a.storeEAXremain()          // body_ok: remain -= count
	if a.activeCount != 0 {
		a.b(0x48, 0xB8) // mov rax,activeCount
		a.imm64(uint64(a.activeCount))
		a.b(0xC7, 0x00) // mov dword [rax],count
		a.imm32(uint32(count))
	}
}

// interruptPoll exits at this architectural boundary when either input is
// asserted and its CPSR mask is clear. x86 aligned 32-bit loads are atomic;
// SetInterruptLine publishes the same word with Go atomics.
func (a *x64emitter) interruptPoll(pc uint32, retired int) {
	if a.interruptLines == 0 {
		return
	}
	// RDI was loaded once by the system prologue; aligned dword load is atomic.
	a.b(0x8B, 0x0F) // mov ecx,[rdi]
	// Both lines are normally low. Collapse that overwhelmingly common case
	// before paying for two individual line tests and any CPSR reads.
	a.testECXECX()
	noLines := a.mark()
	a.b(0x74, 0) // jz continue

	// FIQ has priority. A masked FIQ falls through to the IRQ test.
	a.b(0xF6, 0xC1, 0x02) // test cl,2
	noFIQ := a.mark()
	a.b(0x74, 0) // jz irq_test
	a.loadEAX(cpu.RegisterCPSR)
	a.b(0xA9)
	a.imm32(statusFIQDisable) // test eax, F
	serviceFIQ := a.mark()
	a.b(0x74, 0) // jz service

	irqTest := a.mark()
	a.buf[noFIQ+1] = byte(irqTest - (noFIQ + 2))
	a.b(0xF6, 0xC1, 0x01) // test cl,1
	noIRQ := a.mark()
	a.b(0x74, 0) // jz continue
	a.loadEAX(cpu.RegisterCPSR)
	a.b(0xA9)
	a.imm32(statusIRQDisable) // test eax, I
	maskedIRQ := a.mark()
	a.b(0x75, 0) // jnz continue

	service := a.mark()
	a.buf[serviceFIQ+1] = byte(service - (serviceFIQ + 2))
	a.movMemPC(pc)
	a.b(0xB8)
	a.imm32(nativeInterruptStatus(retired))
	a.ret1()

	continuation := a.mark()
	a.buf[noLines+1] = byte(continuation - (noLines + 2))
	a.buf[noIRQ+1] = byte(continuation - (noIRQ + 2))
	a.buf[maskedIRQ+1] = byte(continuation - (maskedIRQ + 2))
}

func (a *x64emitter) conditionStart(condition uint8) int {
	if condition >= 0xe {
		return -1
	}
	a.emitCondition(condition)
	a.testECXECX()
	site := a.mark()
	a.b(0x0F, 0x84) // jz skip_instruction
	a.imm32(0)
	return site
}

func (a *x64emitter) conditionEnd(site int) {
	if site < 0 {
		return
	}
	displacement := int32(a.mark() - (site + 6))
	for index := 0; index < 4; index++ {
		a.buf[site+2+index] = byte(uint32(displacement) >> (8 * index))
	}
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

// exitLinked follows a stable pointer slot to another block's budget gate.
// A zero slot means the target is untranslated or was invalidated, in which
// case the normal Go dispatcher exit remains the correctness fallback.
func (a *x64emitter) exitLinked(slot uintptr, pc uint32) {
	a.b(0x48, 0xB8)
	a.imm64(uint64(slot))       // mov rax,slot
	a.b(0x48, 0x8B, 0x00)       // mov rax,[rax]
	a.b(0x48, 0x85, 0xC0)       // test rax,rax
	a.b(0x74, 0x02, 0xFF, 0xE0) // jz fallback ; jmp rax
	a.exitBranch(pc)
}

func (a *x64emitter) exitCondLinked(
	cond uint8, takenSlot uintptr, takenPC uint32, nextSlot uintptr, nextPC uint32,
) {
	a.emitCondition(cond)
	a.testECXECX()
	site := a.mark()
	a.b(0x0F, 0x84)
	a.imm32(0) // jz not_taken
	a.exitLinked(takenSlot, takenPC)
	displacement := int32(a.mark() - (site + 6))
	for index := 0; index < 4; index++ {
		a.buf[site+2+index] = byte(uint32(displacement) >> (8 * index))
	}
	a.exitLinked(nextSlot, nextPC)
}

// exitBranchLink is the BL terminator: both the link value and the target are
// constants fixed when the block was translated, so it is two immediate stores.
func (a *x64emitter) exitBranchLink(link, target uint32) {
	a.movEAXimm(link)
	a.storeEAX(cpu.RegisterLR)
	a.movMemPC(target)
	a.b(0x31, 0xC0) // xor eax, eax (nativeStatusNorm)
	a.ret1()
}

func (a *x64emitter) exitBranchLinkLinked(link uint32, slot uintptr, target uint32) {
	a.movEAXimm(link)
	a.storeEAX(cpu.RegisterLR)
	a.exitLinked(slot, target)
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
func (a *x64emitter) movECXimm(v uint32) { a.b(0xB9); a.imm32(v) } // mov ecx, imm32
func (a *x64emitter) movR8Dimm(v uint32) { a.b(0x41, 0xB8); a.imm32(v) }

// --- arithmetic / logic ----------------------------------------------------

func (a *x64emitter) addEAXimm(v uint32) { a.b(0x05); a.imm32(v) } // add eax, imm32
func (a *x64emitter) subEAXimm(v uint32) { a.b(0x2D); a.imm32(v) } // sub eax, imm32
func (a *x64emitter) addEAXECX()         { a.b(0x01, 0xC8) }       // add eax, ecx
func (a *x64emitter) subEAXECX()         { a.b(0x29, 0xC8) }       // sub eax, ecx
func (a *x64emitter) andEAXECX()         { a.b(0x21, 0xC8) }       // and eax, ecx
func (a *x64emitter) orEAXECX()          { a.b(0x09, 0xC8) }       // or eax, ecx
func (a *x64emitter) xorEAXECX()         { a.b(0x31, 0xC8) }       // xor eax, ecx
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

// --- inline memory through the software TLB --------------------------------
//
// memory translates one Thumb single load/store into a probe of the software
// TLB (native_tlb.go) plus a direct host access, instead of ending the block and
// letting the interpreter run it. This is what makes the native backend win on
// the guest software blitters that dominate a heavy frame: an access is a
// handful of host instructions rather than a Go call with a region lookup.
//
// Registers (all volatile under the Windows x64 ABI, so the leaf block may
// clobber them): EAX = address then loaded value, ECX = table byte index,
// EDX = guest page number then in-page offset, R9 = half-table base then host
// page pointer, R8D = value to store.
//
// The probe is: tag == guest page, and the access does not cross the page. Both
// failures jump to a bail stub that leaves PC on this instruction and returns
// nativeBailStatus(retired); the Run loop then hands the instruction to the
// interpreter, which installs the page. Region bounds and permissions are not
// checked here because tlbNote only ever installs a page that lies wholly
// inside a region with the matching permission, and never installs a writable
// page that overlaps translated code (so an inline store can never be
// self-modifying).
func (a *x64emitter) memory(m memAccess, pc uint32, retired int) {
	// 1. Effective address into EAX, exactly as the interpreter computes it.
	if m.absolute {
		a.movEAXimm(m.offset)
	} else {
		a.loadEAX(m.base)
		if m.hasIndex {
			if m.subtract {
				a.subEAXmem(m.index)
			} else {
				a.addEAXmem(m.index)
			}
		}
		if m.offset != 0 {
			if m.subtract {
				a.subEAXimm(m.offset)
			} else {
				a.addEAXimm(m.offset)
			}
		}
	}

	misses := a.probeTLB(m.store, uint32(m.size))

	// 2. The access itself. Unaligned is fine on x86-64 and matches the
	// interpreter's deliberately linear unaligned reads.
	if m.store {
		a.b(0x45, 0x8B, 0x43, disp(m.rd)) // mov r8d, [r11+4*rd]
		switch m.size {
		case 4:
			a.b(0x45, 0x89, 0x04, 0x11) // mov [r9+rdx], r8d
		case 2:
			a.b(0x66, 0x45, 0x89, 0x04, 0x11) // mov [r9+rdx], r8w
		default:
			a.b(0x45, 0x88, 0x04, 0x11) // mov [r9+rdx], r8b
		}
	} else {
		switch {
		case m.size == 4:
			a.b(0x41, 0x8B, 0x04, 0x11) // mov eax, [r9+rdx]
		case m.size == 2 && m.signed:
			a.b(0x41, 0x0F, 0xBF, 0x04, 0x11) // movsx eax, word [r9+rdx]
		case m.size == 2:
			a.b(0x41, 0x0F, 0xB7, 0x04, 0x11) // movzx eax, word [r9+rdx]
		case m.signed:
			a.b(0x41, 0x0F, 0xBE, 0x04, 0x11) // movsx eax, byte [r9+rdx]
		default:
			a.b(0x41, 0x0F, 0xB6, 0x04, 0x11) // movzx eax, byte [r9+rdx]
		}
		a.storeEAX(m.rd)
	}
	a.bailStub(pc, retired, misses)
}

// multi translates PUSH/POP/STMIA/LDMIA: one probe covering the whole list,
// then a word per register at a fixed displacement, then the base writeback.
// See multiAccess (native_common.go) for why the instruction is all-or-nothing.
func (a *x64emitter) multi(m multiAccess, pc uint32, retired int) {
	span := uint32(4 * len(m.regs))
	a.loadEAX(m.base)
	if m.preDec {
		a.subEAXimm(span) // PUSH transfers below the base
	}
	misses := a.probeTLB(m.store, span)
	for i, reg := range m.regs {
		offset := byte(4 * i)
		if m.store {
			a.b(0x45, 0x8B, 0x43, disp(reg))    // mov r8d, [r11+4*reg]
			a.b(0x45, 0x89, 0x44, 0x11, offset) // mov [r9+rdx+off], r8d
		} else {
			a.b(0x45, 0x8B, 0x44, 0x11, offset) // mov r8d, [r9+rdx+off]
			a.b(0x45, 0x89, 0x43, disp(reg))    // mov [r11+4*reg], r8d
		}
	}
	if m.writeback {
		// PUSH leaves the base at the bottom of the block it just wrote; the
		// ascending forms leave it one word past the top.
		if !m.preDec {
			a.addEAXimm(span)
		}
		a.storeEAX(m.base)
	}
	a.bailStub(pc, retired, misses)
}

// probeTLB emits the software-TLB probe for an access of span bytes whose guest
// address is already in EAX, leaving the host page pointer in R9 and the
// in-page offset in EDX. It returns the forward-jump sites that must land on
// the bail stub, which bailStub patches.
//
// Registers (all volatile under the Windows x64 ABI): ECX = table byte index,
// EDX = guest page then in-page offset, R9 = half-table base then host page.
// Region bounds and permissions are not checked, because tlbNote only installs
// a page that lies wholly inside a region with the matching permission, and
// never installs a writable page that holds translated code.
func (a *x64emitter) probeTLB(store bool, span uint32) []int {
	a.b(0x89, 0xC1)              // mov ecx, eax
	a.b(0xC1, 0xE9, tlbPageBits) // shr ecx, 12
	a.b(0x89, 0xCA)              // mov edx, ecx      (page number)
	a.b(0x81, 0xE1)              // and ecx, mask
	a.imm32(nativeTLBMask)
	a.b(0xC1, 0xE1, 4) // shl ecx, 4 (tlbEntryBytes)
	table := uint64(a.tlb)
	if store {
		table += tlbWriteOffset // stores probe the write half
	}
	a.b(0x49, 0xB9) // movabs r9, table
	a.imm64(table)
	a.b(0x41, 0x3B, 0x14, 0x09) // cmp edx, [r9+rcx]  (entry.tag)
	misses := []int{a.mark()}
	a.b(0x75, 0)    // jne bail (patched)
	a.b(0x89, 0xC2) // mov edx, eax
	a.b(0x81, 0xE2) // and edx, 0xfff
	a.imm32(tlbPageSize - 1)
	if span > 1 {
		// Keep a straddling access - the only case the interpreter would
		// service byte-wise across regions - on the interpreter.
		a.b(0x81, 0xFA) // cmp edx, 4096-span
		a.imm32(tlbPageSize - span)
		misses = append(misses, a.mark())
		a.b(0x77, 0) // ja bail (patched)
	}
	a.b(0x4D, 0x8B, 0x4C, 0x09, 0x08) // mov r9, [r9+rcx+8] (entry.host)
	return misses
}

// bailStub closes an inline access: jump over the stub on success, then emit a
// stub that leaves PC on this instruction and returns how many instructions the
// block retired before it, so the Run loop can undo the gate's up-front
// subtraction and let the interpreter service the access.
func (a *x64emitter) bailStub(pc uint32, retired int, misses []int) {
	skip := a.mark()
	a.b(0xEB, 0) // jmp done
	bail := a.mark()
	if a.bailAddress != 0 {
		a.b(0x49, 0xB8) // mov r8,bailAddress
		a.imm64(uint64(a.bailAddress))
		a.b(0x41, 0x89, 0x00) // mov [r8],eax
	}
	a.movMemPC(pc)
	a.b(0xB8)
	a.imm32(nativeBailStatus(retired))
	a.ret1()
	done := a.mark()
	for _, site := range misses {
		a.buf[site+1] = byte(bail - (site + 2))
	}
	a.buf[skip+1] = byte(done - (skip + 2))
}

// highRegister translates the ADD/CMP/MOV high-register forms. ADD and MOV set
// no flags at all; CMP sets N/Z/C/V from the subtraction exactly as the
// low-register CMP does. Reading R15 yields pcValue, so a PC operand becomes an
// immediate. Writing R15 (and BX/BLX) is a branch, which this does not
// translate - the block ends there and the interpreter takes it.
func (a *x64emitter) highRegister(op, rd, rs, pcValue uint32) bool {
	if op == 3 || (rd == cpu.RegisterPC && op != 1) {
		return false
	}
	loadEAXOperand := func(gi uint32) {
		if gi == cpu.RegisterPC {
			a.movEAXimm(pcValue)
		} else {
			a.loadEAX(gi)
		}
	}
	switch op {
	case 0: // ADD rd, rs (no flags)
		loadEAXOperand(rd)
		if rs == cpu.RegisterPC {
			a.addEAXimm(pcValue)
		} else {
			a.addEAXmem(rs)
		}
		a.storeEAX(rd)
	case 1: // CMP rd, rs -> setNZCV(sub), no writeback
		loadEAXOperand(rd)
		if rs == cpu.RegisterPC {
			a.subEAXimm(pcValue)
		} else {
			a.subEAXmem(rs)
		}
		a.commitNZCV(true)
	default: // 2: MOV rd, rs (no flags)
		loadEAXOperand(rs)
		a.storeEAX(rd)
	}
	return true
}

func (a *x64emitter) armDataProcessing(op nativeARMDataOp) bool {
	if op.opcode >= 5 && op.opcode <= 7 {
		return false
	}
	loadEAX := func(gi uint32) {
		if gi == cpu.RegisterPC {
			a.movEAXimm(op.pcValue)
		} else {
			a.loadEAX(gi)
		}
	}
	loadECX := func(gi uint32) {
		if gi == cpu.RegisterPC {
			a.movECXimm(op.pcValue)
		} else {
			a.loadECX(gi)
		}
	}
	loadOperandECX := func() {
		if op.operandReg {
			loadECX(op.operand)
		} else {
			a.movECXimm(op.operand)
		}
	}
	loadOperandEAX := func() {
		if op.operandReg {
			loadEAX(op.operand)
		} else {
			a.movEAXimm(op.operand)
		}
	}

	writes := op.opcode < 8 || op.opcode >= 12
	arithmetic := false
	subtract := false
	switch op.opcode {
	case 0: // AND
		loadEAX(op.rn)
		loadOperandECX()
		a.andEAXECX()
	case 1: // EOR
		loadEAX(op.rn)
		loadOperandECX()
		a.xorEAXECX()
	case 2, 10: // SUB / CMP
		loadEAX(op.rn)
		loadOperandECX()
		a.subEAXECX()
		arithmetic, subtract = true, true
	case 3: // RSB
		loadOperandEAX()
		loadECX(op.rn)
		a.subEAXECX()
		arithmetic, subtract = true, true
	case 4, 11: // ADD / CMN
		loadEAX(op.rn)
		loadOperandECX()
		a.addEAXECX()
		arithmetic = true
	case 8: // TST
		loadEAX(op.rn)
		loadOperandECX()
		a.andEAXECX()
	case 9: // TEQ
		loadEAX(op.rn)
		loadOperandECX()
		a.xorEAXECX()
	case 12: // ORR
		loadEAX(op.rn)
		loadOperandECX()
		a.orEAXECX()
	case 13: // MOV
		loadOperandEAX()
	case 14: // BIC
		loadEAX(op.rn)
		loadOperandECX()
		a.notECX()
		a.andEAXECX()
	case 15: // MVN
		loadOperandEAX()
		a.notEAX()
	default:
		return false
	}
	if op.setFlags {
		if arithmetic {
			a.commitNZCV(subtract)
		} else if op.carry >= 0 {
			a.movR8Dimm(uint32(op.carry))
			a.commitNZC()
		} else {
			a.commitNZ()
		}
	}
	if writes {
		a.storeEAX(op.rd)
	}
	return true
}
