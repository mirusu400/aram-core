//go:build (windows && amd64) || ((android || linux) && arm64)

package conformance

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func newNative() cpu.Backend { return interpreter.NewNativeJIT() }

// TestNativeMatchesInterpreterOracle runs the shared Tier-1 corpus on the native
// machine-code recompiler and requires it to reproduce the interpreter oracle's
// architectural state exactly, program by program. Anything the native backend
// does not translate falls back to the interpreter, so equality must be total.
func TestNativeMatchesInterpreterOracle(t *testing.T) {
	for _, p := range Corpus {
		oracle, err := Execute(interp, p)
		if err != nil {
			t.Fatalf("%s: oracle: %v", p.Name, err)
		}
		native, err := Execute(newNative, p)
		if err != nil {
			t.Fatalf("%s: native: %v", p.Name, err)
		}
		if d := Diff(oracle, native); d != "" {
			t.Fatalf("%s: native JIT diverged from interpreter: %s", p.Name, d)
		}
	}
}
