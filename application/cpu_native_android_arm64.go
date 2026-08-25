//go:build android && arm64

package application

import (
	"os"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// This build-tagged file registers the native machine-code CPU backend on
// android/arm64 ??but only when ARAM_NATIVE_ARM64=1 is set. The whole arm64 path
// (codegen, BLR trampoline, mmap W^X, i-cache flush, self-loop linking) passes
// the conformance differential bit-for-bit on emulated aarch64 (qemu), but the
// one thing emulation cannot exercise ??real-hardware I-cache/D-cache incoherence
// ??is still unproven. Gating registration keeps "native" out of the settings
// dropdown by default until an on-device conformance run confirms the i-cache
// flush; flip the env var to opt in for that validation.
func init() {
	if os.Getenv("ARAM_NATIVE_ARM64") == "1" {
		RegisterCPUBackend("native", newNativeCPU)
	}
}

// newNativeCPU is the hand-written Thumb->AArch64 dynamic recompiler
// (interpreter.NewNativeJIT).
func newNativeCPU() cpu.Backend { return interpreter.NewNativeJIT() }
