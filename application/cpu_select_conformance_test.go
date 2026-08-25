package application

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/conformance"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// TestSelectedBackendMatchesOracle exercises the whole two-backend pipeline:
// resolve a backend through the selection registry, run the conformance corpus
// on it, and diff against the precise interpreter oracle. A real fast/native
// core registered under a name is validated by exactly this path, so any
// architectural divergence fails here at the first differing instruction.
//
// The stand-in backend here is the interpreter itself (the only backend this
// pure-Go build provides), so the diff is trivially empty; the value is proving
// the resolve -> Execute -> Diff wiring is sound and ready for a genuine second
// backend to slot into.
func TestSelectedBackendMatchesOracle(t *testing.T) {
	const name = "conformance-alt"
	RegisterCPUBackend(name, func() cpu.Backend { return interpreter.New() })

	precise, err := ResolveCPUBackend(PreciseBackend)
	if err != nil {
		t.Fatalf("resolve precise: %v", err)
	}
	subject, err := ResolveCPUBackend(name)
	if err != nil {
		t.Fatalf("resolve %q: %v", name, err)
	}

	for _, p := range conformance.Corpus {
		oracle, err := conformance.Execute(precise, p)
		if err != nil {
			t.Fatalf("%s: oracle: %v", p.Name, err)
		}
		got, err := conformance.Execute(subject, p)
		if err != nil {
			t.Fatalf("%s: subject: %v", p.Name, err)
		}
		if d := conformance.Diff(oracle, got); d != "" {
			t.Fatalf("%s: selected backend diverged from oracle: %s", p.Name, d)
		}
	}
}
