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

const (
	defaultThroughputSlices      = 48
	defaultThroughputSliceBudget = uint64(25_000_000)
)

func throughputSliceCount(t *testing.T) int {
	t.Helper()
	text := os.Getenv("ARAM_THROUGHPUT_SLICES")
	if text == "" {
		return defaultThroughputSlices
	}
	parsed, err := strconv.Atoi(text)
	if err != nil || parsed <= 0 {
		t.Fatalf("invalid ARAM_THROUGHPUT_SLICES %q", text)
	}
	return parsed
}

func throughputSliceBudget(t *testing.T) uint64 {
	t.Helper()
	text := os.Getenv("ARAM_THROUGHPUT_SLICE_BUDGET")
	if text == "" {
		return defaultThroughputSliceBudget
	}
	parsed, err := strconv.ParseUint(text, 0, 64)
	if err != nil || parsed == 0 {
		t.Fatalf("invalid ARAM_THROUGHPUT_SLICE_BUDGET %q", text)
	}
	return parsed
}

// TestPrivateSCHW830ColdBootThroughput measures the whole-phone machine from
// reset, which is the run a person actually waits through. The existing
// parity/throughput test needs a saved snapshot; this one needs only the
// firmware, so it can be used while changing the boot path itself.
//
// It also samples the guest instruction set at every slice boundary. The
// translated-block backends are Thumb-only, so knowing how much of a run is
// ARM is what says whether a Thumb optimization can matter at all.
func TestPrivateSCHW830ColdBootThroughput(t *testing.T) {
	if os.Getenv("ARAM_THROUGHPUT") == "" {
		t.Skip("ARAM_THROUGHPUT is not configured")
	}
	set := openSamsungSCHReferenceSet(t, schw830ReferenceDirectory(t))
	backend := interpreter.NewJIT()
	machine, err := New(set, Options{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()

	slices := throughputSliceCount(t)
	perSlice := throughputSliceBudget(t)
	var (
		total       uint64
		armSlices   int
		thumbSlices int
	)
	overall := time.Now()
	for slice := range slices {
		started := time.Now()
		result := machine.Run(context.Background(), perSlice)
		elapsed := time.Since(started)
		total += result.Instructions
		status, statusErr := backend.ReadRegister(cpu.RegisterCPSR)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		mode := "ARM"
		if status&cpu.StatusThumb != 0 {
			mode = "Thumb"
			thumbSlices++
		} else {
			armSlices++
		}
		if slice%8 == 0 || slice == slices-1 {
			t.Logf("slice %2d  cumulative=%4dM  %6.2f MIPS  mode=%-5s pc=0x%08x",
				slice, total/1_000_000,
				float64(result.Instructions)/elapsed.Seconds()/1e6, mode, result.PC)
		}
		if result.Err != nil {
			t.Fatalf("slice %d faulted: %v", slice, result.Err)
		}
		if result.Reason != cpu.StopBudget || result.Instructions != perSlice {
			t.Fatalf("slice %d = %+v", slice, result)
		}
	}
	seconds := time.Since(overall).Seconds()
	t.Logf("THROUGHPUT retired=%dM elapsed=%.1fs -> %.2f MIPS (slice mode: ARM=%d Thumb=%d)",
		total/1_000_000, seconds, float64(total)/seconds/1e6, armSlices, thumbSlices)
}
