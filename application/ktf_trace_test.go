package application

import (
	"fmt"
	"testing"
)

func TestKTFHostTraceRetainsNewestWithinLimit(t *testing.T) {
	runtime := &ktfRuntime{}
	total := ktfHostTraceLimit + ktfHostTraceKeep + 7
	for index := range total {
		runtime.trace(fmt.Sprintf("entry-%d", index))
	}
	if len(runtime.hostTrace) > ktfHostTraceLimit {
		t.Fatalf(
			"retained %d entries, limit %d",
			len(runtime.hostTrace),
			ktfHostTraceLimit,
		)
	}
	if got := runtime.hostTraceDropped + len(runtime.hostTrace); got != total {
		t.Fatalf("dropped+retained = %d, want %d", got, total)
	}
	newest := runtime.hostTrace[len(runtime.hostTrace)-1]
	if want := fmt.Sprintf("entry-%d", total-1); newest != want {
		t.Fatalf("newest entry = %q, want %q", newest, want)
	}
	oldest := runtime.hostTrace[0]
	if want := fmt.Sprintf("entry-%d", runtime.hostTraceDropped); oldest != want {
		t.Fatalf("oldest retained entry = %q, want %q", oldest, want)
	}
}

func TestKTFHostTraceBelowLimitDropsNothing(t *testing.T) {
	runtime := &ktfRuntime{}
	for index := range ktfHostTraceLimit {
		runtime.tracef("entry-%d", index)
	}
	if runtime.hostTraceDropped != 0 {
		t.Fatalf("dropped = %d, want 0", runtime.hostTraceDropped)
	}
	if len(runtime.hostTrace) != ktfHostTraceLimit {
		t.Fatalf(
			"retained %d entries, want %d",
			len(runtime.hostTrace),
			ktfHostTraceLimit,
		)
	}
}

// TestDebugLogSnapshotCountsDroppedEntries pins the reported totals for a log
// that already rolled over, so a snapshot still distinguishes an idle session
// from one whose retention window filled.
func TestDebugLogSnapshotCountsDroppedEntries(t *testing.T) {
	snapshot := debugLogSnapshot([]string{"a", "b", "c"}, 10, 2)
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
