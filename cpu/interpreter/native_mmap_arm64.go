//go:build (android || linux) && arm64

package interpreter

// arm64 Linux/Android host bindings for the native Thumb JIT: executable-memory
// management (mmap + mprotect W^X), i-cache maintenance, and block invocation.
// Android is Linux, so the same mmap/mprotect syscalls and AArch64 assembly
// serve both; linux/arm64 (servers, SBCs, Asahi) gets the native path too and
// makes the arm64 codegen executable-testable under qemu/Docker.
// The translator, Run loop, and Thumb decoder are host-independent
// (native_jit.go); the AArch64 machine-code emitter (native_aarch64emit.go) is
// pure Go and unit-tested on the amd64 dev host.
//
// VERIFIED under emulation, real-hardware i-cache still unproven: the whole arm64
// path ??this glue (the Go->native BLR trampoline, mmap W^X arena, and DC/IC
// cache-flush loop), the emitter, and the self-loop block linking ??executes
// correctly on emulated aarch64: cpu/conformance's native differential (corpus,
// every condition/flag state, all shifts/ALU, self-loop retirement, and 4000
// random programs) passes bit-for-bit against the interpreter under qemu (Docker
// linux/arm64). The one thing emulation cannot exercise is I-cache/D-cache
// incoherence on real silicon (qemu auto-invalidates its translation cache), so
// the flushICache correctness on a physical device is still unproven. Because of
// that residual risk the android "native" backend is registered only when
// ARAM_NATIVE_ARM64=1 is set (see the application layer), pending an on-device
// conformance run.

import (
	"syscall"
	"unsafe"
)

// callNativeBlock invokes a translated block: entry is called as a leaf AAPCS64
// function with &regs[0] in X0 and &nativeRemain in X1, returning the block
// status in X0. Implemented in native_trampoline_android_arm64.s.
func callNativeBlock(entry uintptr, regs, remain *uint32) uintptr

// flushICache performs AArch64 instruction-cache maintenance over [start,end):
// clean D-cache to PoU, then invalidate I-cache, with the required barriers.
// Required after writing code because arm64 I-cache/D-cache are not coherent.
// Implemented in native_trampoline_android_arm64.s.
func flushICache(start, end uintptr)

// NewNativeJIT returns a backend that runs Thumb through the native AArch64
// recompiler, falling back to the interpreter for untranslated instructions and
// for ARM. If the executable arena cannot be mapped it degrades to the plain
// interpreter (nativeBlocks stays nil).
func NewNativeJIT() *Backend {
	b := NewWithMemoryLimit(DefaultMemoryLimit)
	if arena := newCodeArena(nativeArenaSize); arena != nil {
		b.nativeArena = arena
		b.nativeBlocks = make(map[uint32]*nativeBlock)
		b.nativeCodeLo, b.nativeCodeHi = ^uint32(0), 0
		b.tlb = newNativeTLB()
		b.nativeCache = make([]nativeCacheEntry, nativeCacheSize)
		b.nativeCodePages = make([]uint64, nativeCodePageWords)
	}
	return b
}

func (b *Backend) newEmitter() emitter { return &arm64emitter{tlb: b.tlbBase()} }

func newCodeArena(size uintptr) *codeArena {
	mem, err := syscall.Mmap(-1, 0, int(size),
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil || len(mem) == 0 {
		return nil
	}
	base := uintptr(unsafe.Pointer(&mem[0]))
	a := &codeArena{base: base, size: size, mem: mem}
	a.protectRW = func() { syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_WRITE) }
	a.protectRX = func() { syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC) }
	a.release = func() { syscall.Munmap(mem) }
	return a
}

// arenaAppend copies a finished block into the arena (flipping W^X around the
// write and flushing the i-cache for the new range) and returns its host entry
// address, or 0 if the arena is full.
func (b *Backend) arenaAppend(code []byte) uintptr {
	a := b.nativeArena
	n := uintptr(len(code))
	off := (a.off + 15) &^ 15 // 16-byte align each block entry
	if off+n > a.size {
		return 0
	}
	a.protectRW()
	copy(a.mem[off:off+n], code) // a.mem is the mmap'd slice; no uintptr->pointer cast
	a.protectRX()
	flushICache(a.base+off, a.base+off+n)
	a.off = off + n
	return a.base + off
}
