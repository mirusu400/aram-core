//go:build darwin && arm64 && cgo

package interpreter

// darwin/arm64 (Apple Silicon) host bindings for the native Thumb JIT. macOS,
// unlike iOS, allows JIT, but Apple Silicon enforces W^X in hardware: a page
// cannot be writable and executable at once. Executable memory is mapped with
// MAP_JIT and each thread toggles between write and execute with
// pthread_jit_write_protect_np; freshly written code is published with
// sys_icache_invalidate. Those are libSystem calls, so this one host file uses
// cgo (the default on macOS); the windows/android/linux paths stay cgo-free.
// Without cgo (CGO_ENABLED=0) this file drops out and darwin falls back to the
// precise interpreter (native_stub.go).
//
// The AArch64 machine-code emitter (native_aarch64emit.go) and the whole
// translator/Run loop (native_jit.go) are shared with linux/android arm64; only
// this memory/call glue is macOS-specific. All guest-address arithmetic is done
// in C so no Go uintptr->unsafe.Pointer cast is needed (keeping `go vet` clean).
//
// VERIFIED on real Apple Silicon: cpu/conformance's native differential (corpus,
// every condition/flag state, all shifts/ALU, self-loop retirement, and 4000
// random programs) passes bit-for-bit against the interpreter on an M-series Mac.
// Because Apple Silicon has genuinely incoherent I-/D-caches (unlike qemu),
// self-modifying JIT code executing correctly here proves the W^X toggle and
// sys_icache_invalidate are right on real hardware.

/*
#include <pthread.h>
#include <libkern/OSCacheControl.h>
#include <sys/mman.h>
#include <string.h>
#include <stdint.h>

// jit_alloc maps a MAP_JIT executable region; returns 0 on failure.
static uintptr_t jit_alloc(size_t size) {
	void *p = mmap(NULL, size, PROT_READ | PROT_WRITE | PROT_EXEC,
	               MAP_PRIVATE | MAP_ANON | MAP_JIT, -1, 0);
	if (p == MAP_FAILED) {
		return 0;
	}
	return (uintptr_t)p;
}

// jit_write publishes n bytes at base+off: toggle the calling thread's JIT
// region to writable, copy, toggle back to executable, then invalidate the
// i-cache for the written range. Doing the whole sequence in one cgo call keeps
// it on a single OS thread (the write-protect state is per-thread).
static void jit_write(uintptr_t base, size_t off, const void *src, size_t n) {
	void *dst = (void *)(base + off);
	pthread_jit_write_protect_np(0); // writable, not executable
	memcpy(dst, src, n);
	pthread_jit_write_protect_np(1); // executable, not writable
	sys_icache_invalidate(dst, n);
}

// jit_call invokes a translated block: entry(regs, remain) -> status.
static uintptr_t jit_call(uintptr_t entry, uint32_t *regs, uint32_t *remain) {
	uintptr_t (*fn)(uint32_t *, uint32_t *) =
	    (uintptr_t(*)(uint32_t *, uint32_t *))entry;
	return fn(regs, remain);
}

static void jit_free(uintptr_t base, size_t size) {
	munmap((void *)base, size);
}
*/
import "C"

import (
	"sync/atomic"
	"unsafe"
)

// NewNativeJIT returns a backend that runs common ARM and Thumb instructions
// through native AArch64 code, falling back to portable translated blocks for
// unsupported instructions. If the executable arena cannot be mapped it
// degrades to the plain interpreter (nativeBlocks stays nil).
func NewNativeJIT() *Backend {
	b := NewWithMemoryLimit(DefaultMemoryLimit)
	if arena := newCodeArena(nativeArenaSize); arena != nil {
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
	return &arm64emitter{
		tlb: b.tlbBase(), interruptLines: b.interruptLinesBase(),
		activeCount: uintptr(unsafe.Pointer(&b.nativeActiveCount)),
		bailAddress: uintptr(unsafe.Pointer(&b.nativeBailAddress)),
	}
}

func newCodeArena(size uintptr) *codeArena {
	base := uintptr(C.jit_alloc(C.size_t(size)))
	if base == 0 {
		return nil
	}
	a := &codeArena{base: base, size: size}
	a.release = func() { C.jit_free(C.uintptr_t(base), C.size_t(size)) }
	return a
}

// arenaAppend copies a finished block into the MAP_JIT arena (toggling W^X and
// flushing the i-cache for the new range in C) and returns its host entry
// address, or 0 if the arena is full.
func (b *Backend) arenaAppend(code []byte) uintptr {
	a := b.nativeArena
	n := uintptr(len(code))
	off := (a.off + 15) &^ 15 // 16-byte align each block entry
	if !a.reserve(off, n) {
		return 0
	}
	C.jit_write(C.uintptr_t(a.base), C.size_t(off), unsafe.Pointer(&code[0]), C.size_t(n))
	a.off = off + n
	b.executionStatistics.TranslatedHostBytes += uint64(n)
	return a.base + off
}

// callNativeBlock invokes a translated block, passing &regs[0] and &nativeRemain
// through a C trampoline (cgo makes the Go->native->Go transition safe).
func callNativeBlock(entry uintptr, regs, remain *uint32) uintptr {
	return uintptr(C.jit_call(C.uintptr_t(entry),
		(*C.uint32_t)(unsafe.Pointer(regs)), (*C.uint32_t)(unsafe.Pointer(remain))))
}
