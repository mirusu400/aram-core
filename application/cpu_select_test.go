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

// TestFastestBackendResolvesAndNeverFails pins the contract that makes
// FastestBackend safe as a stored default: it resolves on every build, it never
// returns ErrBackendUnavailable, and what it picks is a backend this build
// actually registers. On a target with no fast core compiled in it must degrade
// to the precise interpreter rather than failing to open a title.
func TestFastestBackendResolvesAndNeverFails(t *testing.T) {
	factory, err := ResolveCPUBackend(FastestBackend)
	if err != nil {
		t.Fatalf("resolve %q: %v", FastestBackend, err)
	}
	backend := factory()
	if backend == nil {
		t.Fatalf("resolve %q: nil backend", FastestBackend)
	}
	defer backend.Close()

	resolved := ResolvedFastestBackend()
	if resolved != PreciseBackend {
		if _, err := ResolveCPUBackend(resolved); err != nil {
			t.Fatalf("ResolvedFastestBackend reported %q, which does not resolve: %v", resolved, err)
		}
	}
	// The reported name and the returned backend must describe the same core.
	want, err := ResolveCPUBackend(resolved)
	if err != nil {
		t.Fatal(err)
	}
	reference := want()
	defer reference.Close()
	if got, expect := backend.Identity().Name, reference.Identity().Name; got != expect {
		t.Fatalf("fastest backend identity = %q, but ResolvedFastestBackend named %q (%q)",
			got, resolved, expect)
	}
}

// TestFastestBackendPrefersTheFasterCore checks the preference order actually
// prefers a registered fast core over the interpreter. Which core that is
// depends on the build, so this asserts the property rather than a name.
func TestFastestBackendPrefersTheFasterCore(t *testing.T) {
	available := map[string]bool{}
	for _, name := range CPUBackendNames() {
		available[name] = true
	}
	resolved := ResolvedFastestBackend()
	for _, preferred := range fastestBackendOrder {
		if available[preferred] {
			if resolved != preferred {
				t.Fatalf("fastest resolved to %q while %q is registered", resolved, preferred)
			}
			return
		}
	}
	if resolved != PreciseBackend {
		t.Fatalf("no fast core registered but fastest resolved to %q", resolved)
	}
}
