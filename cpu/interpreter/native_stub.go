//go:build !(windows && amd64) && !((android || linux) && arm64) && !(darwin && arm64 && cgo)

package interpreter

import "github.com/mirusu400/aram-core/cpu"

// This stub keeps the interpreter package building on hosts without a native
// JIT emitter (iOS, desktop Linux/macOS, wasm, ...). Backend.Run references
// runThumbNative unconditionally, so the symbol must exist everywhere; but no
// NewNativeJIT is defined on these targets and nothing sets b.nativeBlocks, so
// the method is never reached. There is deliberately no NewNativeJIT here: the
// application layer only registers the "native" backend from build-tagged files
// present on windows/amd64 and android/arm64, so on every other target the name
// is simply absent and selection falls back to the precise interpreter.

func (b *Backend) runThumbNative(uint64) (uint64, *cpu.StopReason, error) {
	panic("interpreter: native JIT backend not built for this platform")
}
