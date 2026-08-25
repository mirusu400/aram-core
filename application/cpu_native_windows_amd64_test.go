//go:build windows && amd64

package application

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu/conformance"
)

// TestNativeBackendResolvesAndMatchesOracle proves the shipped selection path
// for the native core: resolve the registered "native" name (as ARAM_CPU or the
// settings dropdown would), run the conformance corpus on it, and require
// bit-for-bit agreement with the precise interpreter oracle. This is the
// application-level guarantee behind offering "native" in the UI.
func TestNativeBackendResolvesAndMatchesOracle(t *testing.T) {
	precise, err := ResolveCPUBackend(PreciseBackend)
	if err != nil {
		t.Fatalf("resolve precise: %v", err)
	}
	native, err := ResolveCPUBackend("native")
	if err != nil {
		t.Fatalf("resolve native: %v", err)
	}
	// The registered native backend must report its distinct identity.
	if id := native().Identity().Name; id != "portable-interpreter-native" {
		t.Fatalf("native identity = %q, want portable-interpreter-native", id)
	}
	for _, p := range conformance.Corpus {
		oracle, err := conformance.Execute(precise, p)
		if err != nil {
			t.Fatalf("%s: oracle: %v", p.Name, err)
		}
		got, err := conformance.Execute(native, p)
		if err != nil {
			t.Fatalf("%s: native: %v", p.Name, err)
		}
		if d := conformance.Diff(oracle, got); d != "" {
			t.Fatalf("%s: native backend diverged from oracle: %s", p.Name, d)
		}
	}
}
