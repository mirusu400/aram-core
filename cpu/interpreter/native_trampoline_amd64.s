//go:build windows && amd64

// x86-64 Go-assembly trampoline for the native Thumb JIT, the counterpart of
// native_trampoline_arm64.s.
//
// This replaces the syscall.SyscallN call the block dispatch originally used.
// SyscallN is the safe way to leave Go for opaque foreign code, but it costs a
// full syscall-transition (entersyscall/exitsyscall plus the stack switch) on
// every invocation. Real Thumb code ends a translated block every few
// instructions - a hi-register op, BL, or a block transfer is not translated -
// so a real title makes hundreds of thousands of block calls per frame, and
// that transition, not the emitted code, dominated the frame.
//
// A direct CALL is sound here because a translated block is a leaf: it never
// calls back into Go, never grows the stack (it pushes nothing but the return
// address), and touches only registers that are volatile under both the Windows
// x64 ABI and Go's internal ABI - RAX, RCX, RDX, R8, R9, R10, R11. In
// particular it never touches R14 (Go's g pointer) or R15. A block also runs
// for at most one dispatch batch (runBatchInstructions guest instructions), so
// the window in which this goroutine cannot reach a GC safe point is bounded
// and short.

#include "textflag.h"

// func callNativeBlock(entry uintptr, regs, remain *uint32) uintptr
// Calls entry(regs, remain) with regs in RCX and remain in RDX (the Windows x64
// ABI's first two integer arguments, which the emitted prologue moves into its
// own base registers), and returns the status the block leaves in EAX.
TEXT ·callNativeBlock(SB), NOSPLIT, $0-32
	MOVQ	entry+0(FP), AX
	MOVQ	regs+8(FP), CX
	MOVQ	remain+16(FP), DX
	CALL	AX
	MOVQ	AX, ret+24(FP)
	RET
