package application

import (
	"fmt"
	"os"
	"sync"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// PreciseBackend is the portable interpreter: ARAM's accuracy oracle and the
// default CPU on every target. It is pure-Go and always available, so it is the
// fallback whenever a selected backend is absent from this build.
const PreciseBackend = "precise"

var (
	cpuMu       sync.RWMutex
	cpuBackends = map[string]CPUFactory{
		PreciseBackend: newPreciseCPU,
		"portable":     newPreciseCPU,
	}
)

func newPreciseCPU() cpu.Backend { return interpreter.New() }

// RegisterCPUBackend makes a CPU backend selectable by name. A native or cgo
// recompiler (Unicorn, dynarmic, …) registers itself here from a build-tagged
// file so the pure-Go core never imports it; on a target where that file is not
// compiled, nothing registers the name and selection falls back to the precise
// interpreter. Any backend registered here MUST reproduce the interpreter's
// architectural state exactly — validate it with cpu/conformance before making
// it selectable in a shipped build. Registration is process-global and intended
// for init-time use.
func RegisterCPUBackend(name string, factory CPUFactory) {
	if name == "" || factory == nil {
		return
	}
	cpuMu.Lock()
	defer cpuMu.Unlock()
	cpuBackends[name] = factory
}

// ResolveCPUBackend returns the factory registered for name. An empty name
// resolves to the precise core; an unknown or unregistered name (e.g. a native
// backend absent from this build) returns ErrBackendUnavailable so the caller
// can decide whether to fall back.
func ResolveCPUBackend(name string) (CPUFactory, error) {
	if name == "" {
		name = PreciseBackend
	}
	cpuMu.RLock()
	factory, ok := cpuBackends[name]
	cpuMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: CPU backend %q", machinecore.ErrBackendUnavailable, name)
	}
	return factory, nil
}

// selectedCPUFactory resolves the ARAM_CPU environment override, falling back
// to the precise interpreter when it is unset or names a backend this build
// does not provide. This keeps the product runnable on every target while
// letting desktop opt into a faster registered core.
func selectedCPUFactory() CPUFactory {
	factory, err := ResolveCPUBackend(os.Getenv("ARAM_CPU"))
	if err != nil {
		return newPreciseCPU
	}
	return factory
}
