//go:build darwin && arm64 && cgo

package application

import (
	"os"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// This build-tagged file registers the native machine-code CPU backend on
// darwin/arm64 (Apple Silicon). macOS allows JIT (unlike iOS). The whole path ??
// codegen, self-loop linking, and the macOS memory glue (MAP_JIT +
// pthread_jit_write_protect_np + sys_icache_invalidate) ??is conformance-verified
// bit-for-bit on real Apple Silicon (cpu/interpreter/native_darwin_arm64.go). It
// is still gated behind ARAM_NATIVE_ARM64=1 as an opt-in while it is new; the
// gate can be dropped once it has run against the game corpus. The backend
// requires cgo (the default on macOS); with CGO_ENABLED=0 this file and the
// darwin native path drop out and selection falls back to the interpreter.
func init() {
	if os.Getenv("ARAM_NATIVE_ARM64") == "1" {
		RegisterCPUBackend("native", newNativeCPU)
	}
}

// newNativeCPU is the hand-written Thumb->AArch64 dynamic recompiler
// (interpreter.NewNativeJIT).
func newNativeCPU() cpu.Backend { return interpreter.NewNativeJIT() }
