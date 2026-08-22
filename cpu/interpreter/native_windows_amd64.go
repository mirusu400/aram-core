//go:build windows && amd64

package interpreter

// windows/amd64 host bindings for the native Thumb JIT: executable-memory
// management (VirtualAlloc + W^X protection flips), the block invocation path
// (syscall.SyscallN, which safely transitions Go->native->Go), and the emitter
// constructor. The translator, Run loop, and Thumb decoder are host-independent
// (native_jit.go); the x86-64 machine-code emitter is native_emit_windows_amd64.go.

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc          = kernel32.NewProc("VirtualAlloc")
	procVirtualProtect        = kernel32.NewProc("VirtualProtect")
	procVirtualFree           = kernel32.NewProc("VirtualFree")
	procFlushInstructionCache = kernel32.NewProc("FlushInstructionCache")
	procGetCurrentProcess     = kernel32.NewProc("GetCurrentProcess")
	procWriteProcessMemory    = kernel32.NewProc("WriteProcessMemory")
)

const (
	memCommit            = 0x1000
	memReserve           = 0x2000
	memRelease           = 0x8000
	pageExecuteReadWrite = 0x40
)

// NewNativeJIT returns a backend that runs Thumb through the native machine-code
// recompiler, falling back to the interpreter for untranslated instructions and
// for ARM. If the executable arena cannot be allocated it degrades to the plain
// interpreter (nativeBlocks stays nil). It is architecturally a third CPU
// backend behind the same identity/context as the interpreter; cpu/conformance
// confirms it reproduces the interpreter exactly.
func NewNativeJIT() *Backend {
	b := NewWithMemoryLimit(DefaultMemoryLimit)
	if arena := newCodeArena(nativeArenaSize); arena != nil {
		b.currentProcess, _, _ = procGetCurrentProcess.Call()
		b.nativeArena = arena
		b.nativeBlocks = make(map[uint32]*nativeBlock)
		b.nativeCodeLo, b.nativeCodeHi = ^uint32(0), 0
		b.tlb = newNativeTLB()
		b.nativeCache = make([]nativeCacheEntry, nativeCacheSize)
		b.nativeCodePages = make([]uint64, nativeCodePageWords)
	}
	return b
}

func (b *Backend) newEmitter() emitter { return &x64emitter{tlb: b.tlbBase()} }

// newCodeArena reserves the executable arena. It is mapped read-write-execute
// once instead of being flipped W^X around every block: the original per-block
// VirtualProtect pair plus a whole-arena FlushInstructionCache cost four kernel
// transitions per translated block, and a real title translates blocks by the
// hundred thousand (short runs of Thumb, re-translated after every
// self-modifying write), which made translation - not execution - the frame's
// dominant cost. x86 instruction and data caches are coherent, so no flush is
// required for freshly written code either; protectRW/protectRX stay nil and
// the portable arenaAppend simply skips them.
func newCodeArena(size uintptr) *codeArena {
	base, _, _ := procVirtualAlloc.Call(0, size, memCommit|memReserve, pageExecuteReadWrite)
	if base == 0 {
		return nil
	}
	a := &codeArena{base: base, size: size}
	a.release = func() {
		procVirtualFree.Call(base, 0, memRelease)
	}
	return a
}

// arenaAppend copies a finished block into the arena and returns its host entry
// address, or 0 if the arena is full. The copy goes through WriteProcessMemory
// so no Go pointer is ever derived from the VirtualAlloc'd base address (the
// destination is passed to the OS as a plain integer address, keeping the write
// free of uintptr->unsafe.Pointer casts).
func (b *Backend) arenaAppend(code []byte) uintptr {
	a := b.nativeArena
	n := uintptr(len(code))
	off := (a.off + 15) &^ 15 // 16-byte align each block entry
	if off+n > a.size {
		return 0
	}
	procWriteProcessMemory.Call(b.currentProcess, a.base+off,
		uintptr(unsafe.Pointer(&code[0])), n, 0)
	a.off = off + n
	return a.base + off
}

// callNativeBlock invokes a translated block, passing &regs[0] in RCX and
// &nativeRemain in RDX, and returning the status the block leaves in EAX.
// Implemented as a Go-assembly trampoline in native_trampoline_amd64.s; see
// that file for why a direct CALL replaced the original syscall.SyscallN.
func callNativeBlock(entry uintptr, regs, remain *uint32) uintptr
