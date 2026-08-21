//go:build darwin && arm64 && cgo

package application

import (
	"os"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// This build-tagged file registers the native machine-code CPU backend on
// darwin/arm64 (Apple Silicon) ??but only when ARAM_NATIVE_ARM64=1 is set. macOS
// allows JIT (unlike iOS), and the AArch64 codegen + self-loop linking are
// conformance-verified under qemu, but the macOS memory glue (MAP_JIT +
// pthread_jit_write_protect_np + sys_icache_invalidate in
// cpu/interpreter/native_darwin_arm64.go) has not been exercised on real Apple
// Silicon here. Gating registration keeps "native" out of the settings dropdown
// by default until it is validated on-device; flip the env var to opt in. The
// backend requires cgo (the default on macOS); with CGO_ENABLED=0 this file and
// the darwin native path drop out and selection falls back to the interpreter.
func init() {
	if os.Getenv("ARAM_NATIVE_ARM64") == "1" {
		RegisterCPUBackend("native", newNativeCPU)
	}
}

// newNativeCPU is the hand-written Thumb->AArch64 dynamic recompiler
// (interpreter.NewNativeJIT).
func newNativeCPU() cpu.Backend { return interpreter.NewNativeJIT() }
