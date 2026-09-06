//go:build android && arm64

package application

import (
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// This build-tagged file registers the native machine-code CPU backend on
// android/arm64. The whole arm64 path (codegen, BLR trampoline, mmap W^X,
// i-cache flush, self-loop linking) passes the conformance differential
// bit-for-bit both on emulated aarch64 (qemu) and, as of 2026-09-06, on a
// physical device (Pixel 6a, Tensor) — the one thing emulation could not
// exercise, real-hardware I-cache/D-cache incoherence, is now confirmed
// correct. Registration used to be gated behind ARAM_NATIVE_ARM64=1 pending
// that on-device run; it registers unconditionally now, matching the
// windows/amd64 native backend.
func init() {
	RegisterCPUBackend("native", newNativeCPU)
}

// newNativeCPU is the hand-written Thumb->AArch64 dynamic recompiler
// (interpreter.NewNativeJIT).
func newNativeCPU() cpu.Backend { return interpreter.NewNativeJIT() }
