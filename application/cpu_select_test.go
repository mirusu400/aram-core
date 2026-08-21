package application

import (
	"errors"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestResolveCPUBackendDefaultsToPrecise(t *testing.T) {
	for _, name := range []string{"", PreciseBackend, "portable"} {
		factory, err := ResolveCPUBackend(name)
		if err != nil {
			t.Fatalf("resolve %q: %v", name, err)
		}
		backend := factory()
		if backend == nil {
			t.Fatalf("resolve %q: nil backend", name)
		}
		if got := backend.Identity().Name; got != interpreter.BackendName {
			t.Fatalf("resolve %q: backend = %q, want interpreter", name, got)
		}
		_ = backend.Close()
	}
}

func TestResolveCPUBackendUnknownIsUnavailable(t *testing.T) {
	if _, err := ResolveCPUBackend("no-such-core"); !errors.Is(err, machinecore.ErrBackendUnavailable) {
		t.Fatalf("resolve unknown: err = %v, want ErrBackendUnavailable", err)
	}
}

func TestRegisterCPUBackendIsSelectable(t *testing.T) {
	const name = "conformance-stub"
	registered := false
	RegisterCPUBackend(name, func() cpu.Backend {
		registered = true
		return interpreter.New()
	})
	factory, err := ResolveCPUBackend(name)
	if err != nil {
		t.Fatalf("resolve registered: %v", err)
	}
	backend := factory()
	defer backend.Close()
	if !registered {
		t.Fatal("registered factory was not invoked")
	}
}

// TestFactoryFallsBackWhenBackendAbsent proves the product stays runnable when a
// requested backend is not compiled in: an unknown ARAM_CPU value must yield the
// precise interpreter rather than a nil backend, matching selectedCPUFactory.
func TestFactoryFallsBackWhenBackendAbsent(t *testing.T) {
	t.Setenv("ARAM_CPU", "native-recompiler-not-in-this-build")
	factory := NewFactory()
	if factory.NewCPU == nil {
		t.Fatal("NewCPU is nil after fallback")
	}
	backend := factory.NewCPU()
	defer backend.Close()
	if got := backend.Identity().Name; got != interpreter.BackendName {
		t.Fatalf("fallback backend = %q, want interpreter", got)
	}
}
