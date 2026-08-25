package application

import (
	"fmt"
	"os"
	"sort"
	"sync"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// PreciseBackend is the portable interpreter: ARAM's accuracy oracle and the
// default CPU on every target. It is pure-Go and always available, so it is the
// fallback whenever a selected backend is absent from this build.
const PreciseBackend = "precise"

// FastestBackend selects the fastest CPU core this build actually provides,
// rather than naming one. It resolves to the native machine-code recompiler
// where an emitter is compiled in and registered, then the pure-Go JIT, then
// the precise interpreter - so a single stored setting is portable across
// desktop, mobile, and targets where a JIT is forbidden, instead of failing or
// silently dropping to the slowest core. Every candidate is validated
// bit-for-bit against the interpreter oracle by cpu/conformance.
const FastestBackend = "fastest"

// fastestBackendOrder is the preference order FastestBackend walks. It is a
// list, not a comparison, because "fastest" is a property of the host and the
// measurements behind it (cpu/interpreter's Benchmark* suites), not something
// the code can determine at run time.
var fastestBackendOrder = []string{"native", "jit"}

var (
	cpuMu sync.RWMutex
	// cpuBackends holds the distinct, selectable backends shown to the UI. It
	// starts with only the precise interpreter; "portable" and the empty string
	// are aliases for it handled in ResolveCPUBackend, not separate entries.
	cpuBackends = map[string]CPUFactory{
		PreciseBackend: newPreciseCPU,
		"jit":          newJITCPU,
	}
)

func newPreciseCPU() cpu.Backend { return interpreter.New() }

// newJITCPU is the pure-Go dynamic recompiler (interpreter.NewJIT): a second
// CPU backend that translates and caches Thumb blocks, falling back to the
// interpreter for anything it does not translate. It is validated against the
// interpreter oracle by cpu/conformance.
func newJITCPU() cpu.Backend { return interpreter.NewJIT() }

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
	switch name {
	case "", PreciseBackend, "portable":
		return newPreciseCPU, nil
	case FastestBackend:
		return fastestCPUFactory(), nil
	}
	cpuMu.RLock()
	factory, ok := cpuBackends[name]
	cpuMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: CPU backend %q", machinecore.ErrBackendUnavailable, name)
	}
	return factory, nil
}

// fastestCPUFactory returns the factory for the fastest registered backend,
// falling back to the precise interpreter when this build registers none.
func fastestCPUFactory() CPUFactory {
	cpuMu.RLock()
	defer cpuMu.RUnlock()
	for _, name := range fastestBackendOrder {
		if factory, ok := cpuBackends[name]; ok {
			return factory
		}
	}
	return newPreciseCPU
}

// ResolvedFastestBackend reports which concrete backend FastestBackend selects
// in this build, for diagnostics and for a settings UI that wants to show what
// "fastest" actually means here.
func ResolvedFastestBackend() string {
	cpuMu.RLock()
	defer cpuMu.RUnlock()
	for _, name := range fastestBackendOrder {
		if _, ok := cpuBackends[name]; ok {
			return name
		}
	}
	return PreciseBackend
}

// CPUBackendNames returns the distinct selectable backend names in sorted order,
// for a settings dropdown. The precise interpreter is always present; a
// registered fast/native core appears once it registers itself.
func CPUBackendNames() []string {
	cpuMu.RLock()
	names := make([]string, 0, len(cpuBackends))
	for name := range cpuBackends {
		names = append(names, name)
	}
	cpuMu.RUnlock()
	sort.Strings(names)
	return names
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
