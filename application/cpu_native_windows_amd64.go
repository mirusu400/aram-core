//go:build windows && amd64

package application

import (
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// This build-tagged file registers the native machine-code CPU backend on
// windows/amd64. On any target without a native emitter the file is absent, the
// "native" name is never registered, and selection falls back to the precise
// interpreter (ResolveCPUBackend returns ErrBackendUnavailable). The native
// backend is validated bit-for-bit against the interpreter oracle by
// cpu/conformance before it is offered here.
func init() { RegisterCPUBackend("native", newNativeCPU) }

// newNativeCPU uses emitted x86-64 ARM/application Thumb blocks and the faster
// compact Go Thumb tier for whole-system firmware on Windows.
func newNativeCPU() cpu.Backend { return interpreter.NewHybridJIT() }
