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
