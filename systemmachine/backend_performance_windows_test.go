//go:build windows && amd64

package systemmachine

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// TestPrivateSCHW830CPUBackendParityAndThroughput compares every interpreter
// tier from one whole-phone snapshot. It stays opt-in because the firmware and
// snapshot are private reference artifacts, and logs retired guest MIPS so
// system-firmware performance work has a repeatable measurement.
func TestPrivateSCHW830CPUBackendParityAndThroughput(t *testing.T) {
	prefix := os.Getenv("ARAM_SCHW830_PERF_SNAPSHOT_PREFIX")
	if prefix == "" {
		t.Skip("ARAM_SCHW830_PERF_SNAPSHOT_PREFIX is not configured")
	}
	budget := uint64(50_000_000)
	if text := os.Getenv("ARAM_SCHW830_PERF_BUDGET"); text != "" {
		parsed, err := strconv.ParseUint(text, 0, 64)
		if err != nil || parsed == 0 {
			t.Fatalf("invalid ARAM_SCHW830_PERF_BUDGET %q", text)
		}
		budget = parsed
	}
	set := openSamsungSCHReferenceSet(t, schw830ReferenceDirectory(t))
	tiers := []struct {
		name string
		new  func() cpu.Backend
	}{
		{name: "precise", new: func() cpu.Backend { return interpreter.New() }},
		{name: "go-jit", new: func() cpu.Backend { return interpreter.NewJIT() }},
		{name: "native-jit", new: func() cpu.Backend { return interpreter.NewNativeJIT() }},
	}
	type observation struct {
		result cpu.Result
		frame  string
	}
	var want observation
	for index, tier := range tiers {
		t.Run(tier.name, func(t *testing.T) {
			machine, err := New(set, Options{Backend: tier.new()})
			if err != nil {
				t.Fatal(err)
			}
			defer machine.Close()
			loadSystemMachineSnapshot(t, machine, prefix)
			started := time.Now()
			result := machine.Run(context.Background(), budget)
			elapsed := time.Since(started)
			if result.Reason != cpu.StopBudget || result.Err != nil || result.Instructions != budget {
				t.Fatalf("backend run = %+v", result)
			}
			got := observation{result: result, frame: machine.FrameSHA256()}
			t.Logf("backend=%s elapsed=%s throughput=%.2f MIPS pc=0x%08x frame=%s",
				machine.Identity().CPU.Name, elapsed,
				float64(result.Instructions)/elapsed.Seconds()/1_000_000,
				result.PC, got.frame)
			if index == 0 {
				want = got
				return
			}
			if got.result != want.result || got.frame != want.frame {
				t.Fatalf("observable state differs from precise: got=%+v want=%+v", got, want)
			}
		})
	}
}
