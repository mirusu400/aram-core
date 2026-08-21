//go:build android || linux

// AArch64 assembly for the native Thumb JIT (arm64 Linux/Android host).
//
// VERIFIED under emulation (qemu linux/arm64, Docker): the trampoline and
// cache-flush loop execute correctly ??cpu/conformance's native differential
// passes bit-for-bit against the interpreter on emulated aarch64. Only
// real-hardware I-cache incoherence (which qemu does not model) is still
// unproven, so the android backend stays behind ARAM_NATIVE_ARM64=1 pending an
// on-device run. It follows the standard Go-asm and AArch64 conventions (framed
// non-leaf saves/restores LR automatically; the block is a leaf AAPCS64
// function; the cache-flush loop is the canonical DC CVAU / IC IVAU sequence).

#include "textflag.h"

// func callNativeBlock(entry uintptr, regs, remain *uint32) uintptr
// Calls entry(regs, remain) with regs in X0 and remain in X1 (AAPCS64 args 0,1),
// returns its X0 result. The $16 frame is non-leaf, so the assembler
// saves/restores LR around the BL.
TEXT ·callNativeBlock(SB), NOSPLIT, $16-32
	MOVD	entry+0(FP), R2
	MOVD	regs+8(FP), R0
	MOVD	remain+16(FP), R1
	BL	(R2)
	MOVD	R0, ret+24(FP)
	RET

// func flushICache(start, end uintptr)
// Cleans the data cache to the point of unification and invalidates the
// instruction cache over [start,end), using the line sizes from CTR_EL0, then
// issues the required barriers. Must run after writing code (arm64 D/I caches
// are not coherent).
TEXT ·flushICache(SB), NOSPLIT, $0-16
	MOVD	start+0(FP), R0
	MOVD	end+8(FP), R1
	WORD	$0xD53B0023	// mrs x3, ctr_el0

	// Data cache: line bytes = 4 << ((ctr>>16)&0xF).
	LSR	$16, R3, R4
	AND	$0xF, R4, R4
	MOVD	$4, R5
	LSL	R4, R5, R5	// R5 = dcache line bytes
	SUB	$1, R5, R7	// R7 = line - 1
	BIC	R7, R0, R6	// R6 = start &^ (line-1)  (aligned down)
dloop:
	WORD	$0xD50B7B26	// dc cvau, x6
	ADD	R5, R6, R6
	CMP	R1, R6
	BLO	dloop
	WORD	$0xD5033B9F	// dsb ish

	// Instruction cache: line bytes = 4 << (ctr&0xF).
	AND	$0xF, R3, R4
	MOVD	$4, R5
	LSL	R4, R5, R5
	SUB	$1, R5, R7
	BIC	R7, R0, R6
iloop:
	WORD	$0xD50B7526	// ic ivau, x6
	ADD	R5, R6, R6
	CMP	R1, R6
	BLO	iloop
	WORD	$0xD5033B9F	// dsb ish
	WORD	$0xD5033FDF	// isb
	RET
