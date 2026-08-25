package conformance

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func interp() cpu.Backend { return interpreter.New() }

// TestJITMatchesInterpreterOracle runs the corpus on the pure-Go dynamic
// recompiler and requires it to reproduce the interpreter oracle's architectural
// state exactly, program by program.
func TestJITMatchesInterpreterOracle(t *testing.T) {
	newJIT := func() cpu.Backend { return interpreter.NewJIT() }
	for _, p := range Corpus {
		oracle, err := Execute(interp, p)
		if err != nil {
			t.Fatalf("%s: oracle: %v", p.Name, err)
		}
		jit, err := Execute(newJIT, p)
		if err != nil {
			t.Fatalf("%s: jit: %v", p.Name, err)
		}
		if d := Diff(oracle, jit); d != "" {
			t.Fatalf("%s: JIT diverged from interpreter: %s", p.Name, d)
		}
	}
}

// TestCorpusRunsCleanOnInterpreter validates the corpus itself: every program
// must reach its BKPT on the reference interpreter (a bad encoding would fault),
// giving a self-checking Tier-1 gate.
func TestCorpusRunsCleanOnInterpreter(t *testing.T) {
	for _, p := range Corpus {
		snap, err := Execute(interp, p)
		if err != nil {
			t.Fatalf("%s: execute: %v", p.Name, err)
		}
		if snap.Reason != cpu.StopBreakpoint {
			t.Fatalf("%s: reason=%v err=%q (program did not reach BKPT)",
				p.Name, snap.Reason, snap.Err)
		}
	}
}

// TestInterpreterIsDeterministic runs each program twice through fresh
// interpreters and requires identical snapshots — the oracle must be stable
// before it can judge a second backend.
func TestInterpreterIsDeterministic(t *testing.T) {
	for _, p := range Corpus {
		a, err := Execute(interp, p)
		if err != nil {
			t.Fatalf("%s: run a: %v", p.Name, err)
		}
		b, err := Execute(interp, p)
		if err != nil {
			t.Fatalf("%s: run b: %v", p.Name, err)
		}
		if d := Diff(a, b); d != "" {
			t.Fatalf("%s: interpreter not deterministic: %s", p.Name, d)
		}
	}
}

// mutantBackend wraps the interpreter but corrupts one CPSR bit on read,
// standing in for a fast backend with a flag-semantics bug. It proves Diff
// actually catches architectural divergence rather than always passing.
type mutantBackend struct{ cpu.Backend }

func (m mutantBackend) ReadRegister(id uint32) (uint32, error) {
	value, err := m.Backend.ReadRegister(id)
	if err == nil && id == cpu.RegisterCPSR {
		value ^= 1 << 29 // flip C
	}
	return value, err
}

// TestDiffCatchesDivergence ensures the harness fails a backend that disagrees
// with the oracle: the mutant flips carry, so at least one flag-bearing program
// must diverge.
func TestDiffCatchesDivergence(t *testing.T) {
	newMutant := func() cpu.Backend { return mutantBackend{interpreter.New()} }
	caught := 0
	for _, p := range Corpus {
		oracle, err := Execute(interp, p)
		if err != nil {
			t.Fatalf("%s: oracle: %v", p.Name, err)
		}
		subject, err := Execute(newMutant, p)
		if err != nil {
			t.Fatalf("%s: subject: %v", p.Name, err)
		}
		if Diff(oracle, subject) != "" {
			caught++
		}
	}
	if caught == 0 {
		t.Fatal("Diff caught no divergence from a carry-corrupting backend; harness is blind")
	}
}
