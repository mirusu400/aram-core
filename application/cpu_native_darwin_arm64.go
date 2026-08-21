//go:build darwin && arm64 && cgo

package application

import (
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// This build-tagged file registers the native machine-code CPU backend on
// darwin/arm64 (Apple Silicon). macOS allows JIT (unlike iOS). The whole path ??
// codegen, self-loop linking, and the macOS memory glue (MAP_JIT +
// pthread_jit_write_protect_np + sys_icache_invalidate) ??is conformance-verified
// bit-for-bit AND ~3.3x faster than the interpreter on real Apple Silicon
// (Apple M4 Pro), so it registers unconditionally like the windows backend
// (android stays gated because it is only qemu-verified, not on a device). The
// backend requires cgo (the default on macOS); with CGO_ENABLED=0 this file and
// the darwin native path drop out and selection falls back to the interpreter.
func init() { RegisterCPUBackend("native", newNativeCPU) }

// newNativeCPU is the hand-written Thumb->AArch64 dynamic recompiler
// (interpreter.NewNativeJIT).
func newNativeCPU() cpu.Backend { return interpreter.NewNativeJIT() }
