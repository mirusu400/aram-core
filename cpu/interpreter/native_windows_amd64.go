//go:build windows && amd64

package interpreter

// windows/amd64 host bindings for the native Thumb JIT: executable-memory
// management (VirtualAlloc + W^X protection flips), the block invocation path
// (syscall.SyscallN, which safely transitions Go->native->Go), and the emitter
// constructor. The translator, Run loop, and Thumb decoder are host-independent
// (native_jit.go); the x86-64 machine-code emitter is native_emit_windows_amd64.go.

import (
	"sync/atomic"
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
	// procArenaCopy is kernel32!RtlMoveMemory, a plain user-mode memmove. The
	// arena write used WriteProcessMemory, which is a kernel transition
	// (NtWriteVirtualMemory) for what is a copy inside our own address space,
	// and cost 7.6% of a real title's frame. RtlMoveMemory has been exported
	// from kernel32 since NT; nil means this build could not resolve it, and
	// arenaAppend keeps using WriteProcessMemory.
	procArenaCopy = findArenaCopy()
)

// findArenaCopy resolves kernel32!RtlMoveMemory once, returning nil rather than
// panicking (LazyProc.Call would) if the export is somehow absent.
func findArenaCopy() *syscall.LazyProc {
	proc := kernel32.NewProc("RtlMoveMemory")
	if proc.Find() != nil {
		return nil
	}
	return proc
}

const (
	memCommit            = 0x1000
	memReserve           = 0x2000
	memRelease           = 0x8000
	pageExecuteReadWrite = 0x40

	// arenaCommitChunk is how much of the reserved arena is backed at a time.
	// The reservation is large enough that a title never has to flush, so it
	// must not be charged to the process up front; committing a megabyte at a
	// time keeps the commit charge proportional to the code actually emitted at
	// the cost of one VirtualAlloc per megabyte.
	arenaCommitChunk = uintptr(1 << 20)
)

// NewNativeJIT returns a backend that runs common ARM and Thumb instructions
// through native machine code, falling back to portable translated blocks for
// unsupported instructions. If the executable arena cannot be allocated it
// degrades to the plain interpreter (nativeBlocks stays nil). It is architecturally a third CPU
// backend behind the same identity/context as the interpreter; cpu/conformance
// confirms it reproduces the interpreter exactly.
func NewNativeJIT() *Backend {
	b := NewWithMemoryLimit(DefaultMemoryLimit)
	if arena := newCodeArena(nativeArenaSize); arena != nil {
		b.currentProcess, _, _ = procGetCurrentProcess.Call()
		b.nativeArena = arena
		b.nativeBlocks = make(map[uint32]*nativeBlock)
		b.nativeBlockPages = make(blockPageIndex)
		b.nativeARMBlocks = make(map[uint32]*nativeBlock)
		b.nativeARMBlockPages = make(blockPageIndex)
		b.nativeLinks = make(map[nativeLinkKey]*atomic.Uintptr)
		b.nativeSlow = make(map[nativeLinkKey]nativeSlowState)
		b.armJITBlocks = make(map[uint32]*jitBlock)
		b.armJITBlockPages = make(blockPageIndex)
		b.armJITCache = make([]jitCacheEntry, jitCacheSize)
		b.jitCodePages = make([]uint64, nativeCodePageWords)
		b.nativeCodeLo, b.nativeCodeHi = ^uint32(0), 0
		b.tlb = newNativeTLB()
		b.nativeCache = new([nativeCacheSize]nativeCacheEntry)
		b.nativeARMCache = new([nativeCacheSize]nativeCacheEntry)
		b.nativeCodePages = make([]uint64, nativeCodePageWords)
	}
	return b
}

func (b *Backend) newEmitter() emitter {
	return &x64emitter{
		tlb: b.tlbBase(), interruptLines: b.interruptLinesBase(),
		activeCount: uintptr(unsafe.Pointer(&b.nativeActiveCount)),
		bailAddress: uintptr(unsafe.Pointer(&b.nativeBailAddress)),
	}
}

// newCodeArena reserves the executable arena. It is mapped read-write-execute
// once instead of being flipped W^X around every block: the original per-block
// VirtualProtect pair plus a whole-arena FlushInstructionCache cost four kernel
// transitions per translated block, and a real title translates blocks by the
// hundred thousand (short runs of Thumb, re-translated after every
// self-modifying write), which made translation - not execution - the frame's
// dominant cost. x86 instruction and data caches are coherent, so no flush is
// required for freshly written code either, so arenaAppend writes into the
// arena with no protection flip at all.
//
// Only the address range is reserved up front; pages are committed as the bump
// allocator reaches them, so a 128 MiB arena does not put 128 MiB on the
// process commit charge.
func newCodeArena(size uintptr) *codeArena {
	base, _, _ := procVirtualAlloc.Call(0, size, memReserve, pageExecuteReadWrite)
	if base == 0 {
		return nil
	}
	a := &codeArena{base: base, size: size}
	committed := uintptr(0)
	a.commit = func(end uintptr) bool {
		if end <= committed {
			return true
		}
		want := min((end+arenaCommitChunk-1)&^(arenaCommitChunk-1), size)
		addr, _, _ := procVirtualAlloc.Call(base+committed, want-committed,
			memCommit, pageExecuteReadWrite)
		if addr == 0 {
			return false
		}
		committed = want
		return true
	}
	a.release = func() {
		procVirtualFree.Call(base, 0, memRelease)
	}
	return a
}

// arenaAppend copies a finished block into the arena and returns its host entry
// address, or 0 if the arena is full. The copy goes through a Windows API so no
// Go pointer is ever derived from the VirtualAlloc'd base address: the
// destination is passed as a plain integer address, keeping the write free of
// uintptr->unsafe.Pointer casts.
func (b *Backend) arenaAppend(code []byte) uintptr {
	a := b.nativeArena
	n := uintptr(len(code))
	off := (a.off + 15) &^ 15 // 16-byte align each block entry
	if !a.reserve(off, n) {
		return 0
	}
	source := uintptr(unsafe.Pointer(&code[0]))
	if procArenaCopy != nil {
		procArenaCopy.Call(a.base+off, source, n)
	} else {
		procWriteProcessMemory.Call(b.currentProcess, a.base+off, source, n, 0)
	}
	a.off = off + n
	b.executionStatistics.TranslatedHostBytes += uint64(n)
	return a.base + off
}

// callNativeBlock invokes a translated block, passing &regs[0] in RCX and
// &nativeRemain in RDX, and returning the status the block leaves in EAX.
// Implemented as a Go-assembly trampoline in native_trampoline_amd64.s; see
// that file for why a direct CALL replaced the original syscall.SyscallN.
func callNativeBlock(entry uintptr, regs, remain *uint32) uintptr
