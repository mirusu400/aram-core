package cheat

import (
	"errors"
	"testing"
)

type failingScanMemory struct {
	*testMemory
	fail bool
}

func (memory *failingScanMemory) ReadMemory(address uint32, destination []byte) error {
	if memory.fail {
		return errors.New("synthetic unavailable memory")
	}
	return memory.testMemory.ReadMemory(address, destination)
}

func TestEmptyScanRefinementDoesNotReadMemory(t *testing.T) {
	memory := &failingScanMemory{testMemory: newTestMemory(16)}
	engine, err := New(memory, testOptions(16))
	check(t, err)
	target := U32(99)
	matches, err := engine.Scan(ScanRequest{Type: TypeUint32, Comparison: CompareEqual, Value: &target})
	check(t, err)
	if len(matches) != 0 {
		t.Fatal("expected zero matches")
	}
	memory.fail = true
	matches, err = engine.NextScan(NextScanRequest{Comparison: CompareChanged})
	check(t, err)
	if len(matches) != 0 {
		t.Fatal("empty scan gained candidates")
	}
}

type regionFailingMemory struct {
	*testMemory
	failFrom uint32
}

func (memory *regionFailingMemory) ReadMemory(address uint32, destination []byte) error {
	if memory.failFrom != 0 && address >= memory.failFrom {
		return errors.New("synthetic unavailable region")
	}
	return memory.testMemory.ReadMemory(address, destination)
}

// Like the legacy per-candidate scan, refinement only revisits regions that
// still hold candidates, and an emptied region's snapshot is released.
func TestRefinementSkipsRegionsWithoutCandidates(t *testing.T) {
	memory := &regionFailingMemory{testMemory: newTestMemory(32)}
	options := testOptions(16)
	options.Regions = append(options.Regions, Region{Name: "spare", Start: testMemoryBase + 16, Size: 16, Writable: true, Scannable: true})
	engine, err := New(memory, options)
	check(t, err)
	check(t, engine.Write(testMemoryBase+4, U32(77), nil))
	target := U32(77)
	summary, err := engine.StartScan(ScanRequest{Type: TypeUint32, Comparison: CompareEqual, Value: &target})
	check(t, err)
	if summary.Total != 1 || len(engine.scan.regions) != 1 {
		t.Fatalf("first scan kept %d regions for %+v", len(engine.scan.regions), summary)
	}
	memory.failFrom = testMemoryBase + 16
	summary, err = engine.RefineScan(NextScanRequest{Comparison: CompareUnchanged})
	check(t, err)
	if summary.Total != 1 {
		t.Fatalf("refined count = %d", summary.Total)
	}
	matches, err := engine.NextScan(NextScanRequest{Comparison: CompareEqual, Value: &target})
	check(t, err)
	if len(matches) != 1 || matches[0].Address != testMemoryBase+4 {
		t.Fatalf("legacy refinement = %+v", matches)
	}
	memory.failFrom = 0
	summary, err = engine.RefineScan(NextScanRequest{Comparison: CompareChanged})
	check(t, err)
	if summary.Total != 0 || len(engine.scan.regions) != 0 {
		t.Fatalf("emptied scan kept %d regions for %+v", len(engine.scan.regions), summary)
	}
}

func TestFailedScanReadPreservesComparisonBaseline(t *testing.T) {
	memory := &failingScanMemory{testMemory: newTestMemory(16)}
	engine, err := New(memory, testOptions(16))
	check(t, err)
	_, err = engine.StartScan(ScanRequest{Type: TypeUint32})
	check(t, err)
	check(t, engine.Write(testMemoryBase, U32(7), nil))
	memory.fail = true
	if _, err := engine.RefineScan(NextScanRequest{Comparison: CompareChanged}); err == nil {
		t.Fatal("read failure hidden")
	}
	memory.fail = false
	summary, err := engine.RefineScan(NextScanRequest{Comparison: CompareChanged})
	check(t, err)
	if summary.Total != 1 {
		t.Fatalf("failed read advanced baseline: %+v", summary)
	}
}
