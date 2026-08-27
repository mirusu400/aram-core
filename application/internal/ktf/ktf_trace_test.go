package ktf

import (
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFProductTraceDefaultsToCounters(t *testing.T) {
	t.Setenv("ARAM_KTF_TRACE", "")
	backend := interpreter.New()
	defer backend.Close()
	runtime, err := NewRuntime(backend, ktfloader.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.TraceMode() != KTFTraceCounters {
		t.Fatalf("default KTF trace mode = %s, want counters", runtime.TraceMode())
	}
	for range 3 {
		runtime.TraceHostCall("synthetic.call")
		runtime.tracef("detail:%d", 7)
	}
	if runtime.HostCallCount != 3 || runtime.LastHostCall != "synthetic.call" {
		t.Fatalf(
			"counter trace calls=%d last=%q",
			runtime.HostCallCount,
			runtime.LastHostCall,
		)
	}
	if len(runtime.HostTrace) != 0 {
		t.Fatalf("counter mode retained detailed strings: %v", runtime.HostTrace)
	}
	snapshot := runtime.HostTraceSnapshot(10)
	if snapshot.Total != 3 || snapshot.Omitted != 3 || len(snapshot.Entries) != 0 {
		t.Fatalf("counter snapshot = %+v", snapshot)
	}
}

func TestKTFTraceOffAndCountersAllocateNothing(t *testing.T) {
	for _, mode := range []KTFTraceMode{KTFTraceOff, KTFTraceCounters} {
		runtime := &Runtime{traceMode: mode}
		allocations := testing.AllocsPerRun(1000, func() {
			runtime.TraceHostCall("synthetic.call")
			runtime.tracef("detail:%d", 7)
		})
		if allocations != 0 {
			t.Fatalf("%s trace = %g allocs, want 0", mode, allocations)
		}
		if mode == KTFTraceOff && runtime.HostCallCount != 0 {
			t.Fatalf("off trace counted %d calls", runtime.HostCallCount)
		}
	}
}

func TestKTFSampledTraceUsesNumericFixedRing(t *testing.T) {
	runtime := &Runtime{}
	if err := runtime.SetTraceMode(KTFTraceSampled); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= HostTraceSampleInterval*2+1; index++ {
		runtime.TraceHostCall("synthetic.call")
	}
	if len(runtime.HostTrace) != 0 || runtime.traceSampleNext != 3 {
		t.Fatalf(
			"sampled storage strings=%d numeric=%d",
			len(runtime.HostTrace),
			runtime.traceSampleNext,
		)
	}
	snapshot := runtime.HostTraceSnapshot(10)
	if len(snapshot.Entries) != 3 || snapshot.Total != HostTraceSampleInterval*2+1 {
		t.Fatalf("sampled snapshot = %+v", snapshot)
	}
}

func TestKTFHostTraceRetainsNewestWithinLimit(t *testing.T) {
	runtime := &Runtime{traceMode: KTFTraceFull}
	total := ktfHostTraceLimit + ktfHostTraceKeep + 7
	for index := range total {
		runtime.trace(fmt.Sprintf("entry-%d", index))
	}
	if len(runtime.HostTrace) > ktfHostTraceLimit {
		t.Fatalf(
			"retained %d entries, limit %d",
			len(runtime.HostTrace),
			ktfHostTraceLimit,
		)
	}
	if got := runtime.HostTraceDropped + len(runtime.HostTrace); got != total {
		t.Fatalf("dropped+retained = %d, want %d", got, total)
	}
	newest := runtime.HostTrace[len(runtime.HostTrace)-1]
	if want := fmt.Sprintf("entry-%d", total-1); newest != want {
		t.Fatalf("newest entry = %q, want %q", newest, want)
	}
	oldest := runtime.HostTrace[0]
	if want := fmt.Sprintf("entry-%d", runtime.HostTraceDropped); oldest != want {
		t.Fatalf("oldest retained entry = %q, want %q", oldest, want)
	}
}

func TestKTFHostTraceBelowLimitDropsNothing(t *testing.T) {
	runtime := &Runtime{traceMode: KTFTraceFull}
	for index := range ktfHostTraceLimit {
		runtime.tracef("entry-%d", index)
	}
	if runtime.HostTraceDropped != 0 {
		t.Fatalf("dropped = %d, want 0", runtime.HostTraceDropped)
	}
	if len(runtime.HostTrace) != ktfHostTraceLimit {
		t.Fatalf(
			"retained %d entries, want %d",
			len(runtime.HostTrace),
			ktfHostTraceLimit,
		)
	}
}

func TestKTFHostTraceSamplesHighFrequencyGraphicsCalls(t *testing.T) {
	runtime := &Runtime{traceMode: KTFTraceFull}
	entry := "java.method.org/kwis/msp/lcdui/Graphics.setRGBPixels(IIII[III)V"
	total := HostTraceSampleInterval + 1
	for range total {
		runtime.TraceHostCall(entry)
	}
	if runtime.HostCallCount != uint64(total) {
		t.Fatalf("host calls = %d, want %d", runtime.HostCallCount, total)
	}
	if len(runtime.HostTrace) != 2 {
		t.Fatalf("retained %d entries, want 2", len(runtime.HostTrace))
	}
	if got := runtime.HostTraceDropped + len(runtime.HostTrace); got != total {
		t.Fatalf("dropped+retained = %d, want %d", got, total)
	}
	for index, got := range runtime.HostTrace {
		if got != entry {
			t.Fatalf("retained entry %d = %q, want %q", index, got, entry)
		}
	}
}

func TestKTFHostTraceDoesNotSampleOrdinaryCalls(t *testing.T) {
	runtime := &Runtime{traceMode: KTFTraceFull}
	entry := "wipic.2.21"
	for range 3 {
		runtime.TraceHostCall(entry)
	}
	if runtime.HostCallCount != 3 {
		t.Fatalf("host calls = %d, want 3", runtime.HostCallCount)
	}
	if len(runtime.HostTrace) != 3 || runtime.HostTraceDropped != 0 {
		t.Fatalf(
			"retained %d, dropped %d; want retained 3, dropped 0",
			len(runtime.HostTrace),
			runtime.HostTraceDropped,
		)
	}
}

// TestDebugLogSnapshotCountsDroppedEntries pins the reported totals for a log
// that already rolled over, so a snapshot still distinguishes an idle session
// from one whose retention window filled.
func TestDebugLogSnapshotCountsDroppedEntries(t *testing.T) {
	snapshot := guest.NewDebugLogSnapshot([]string{"a", "b", "c"}, 10, 2)
	if snapshot.Total != 13 {
		t.Fatalf("total = %d, want 13", snapshot.Total)
	}
	if snapshot.Omitted != 11 {
		t.Fatalf("omitted = %d, want 11", snapshot.Omitted)
	}
	if len(snapshot.Entries) != 2 ||
		snapshot.Entries[0] != "b" ||
		snapshot.Entries[1] != "c" {
		t.Fatalf("entries = %v, want [b c]", snapshot.Entries)
	}
}
