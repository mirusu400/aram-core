package cheat

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPagedUnknownScanCovers32MiB(t *testing.T) {
	memory := newTestMemory(32 << 20)
	engine, err := New(memory, Options{Regions: testOptions(32 << 20).Regions})
	check(t, err)
	summary, err := engine.StartScan(ScanRequest{Type: TypeUint32, Comparison: CompareUnknown})
	check(t, err)
	if summary.Total != 8_388_608 {
		t.Fatalf("count = %d", summary.Total)
	}
	page, err := engine.ScanPage(summary.Total-3, 3)
	check(t, err)
	if len(page.Matches) != 3 || page.Matches[2].Address != testMemoryBase+(32<<20)-4 {
		t.Fatalf("last page = %+v", page)
	}
	// A live view must not change the previous-value baseline.
	check(t, engine.Write(testMemoryBase, U32(42), nil))
	page, err = engine.ScanPage(0, 1)
	check(t, err)
	if page.Matches[0].Value != U32(42) {
		t.Fatalf("live value = %+v", page)
	}
	summary, err = engine.RefineScan(NextScanRequest{Comparison: CompareIncreased})
	check(t, err)
	if summary.Total != 1 {
		t.Fatalf("refined count = %d", summary.Total)
	}
	if _, err := engine.ScanPage(0, MaxScanPageSize+1); err == nil {
		t.Fatal("unbounded page accepted")
	}
	if _, err := engine.ScanPage(-1, 1); err == nil {
		t.Fatal("negative offset accepted")
	}
}

func TestScanAlignmentBoundariesAndLegacyLimit(t *testing.T) {
	memory := newTestMemory(15)
	options := testOptions(15)
	options.Regions[0].Start++
	options.Regions[0].Size--
	options.MaxResults = 1
	engine, err := New(memory, options)
	check(t, err)
	if _, err := engine.Scan(ScanRequest{Type: TypeUint32, Comparison: CompareUnknown}); !errors.Is(err, ErrTooManyResults) {
		t.Fatalf("legacy limit: %v", err)
	}
	if _, err := engine.ScanSummary(); !errors.Is(err, ErrScanNotStarted) {
		t.Fatalf("failed scan changed session: %v", err)
	}
	summary, err := engine.StartScan(ScanRequest{Type: TypeUint32, Comparison: CompareUnknown})
	check(t, err)
	if summary.Total != 2 {
		t.Fatalf("aligned count = %d", summary.Total)
	}
	page, err := engine.ScanPage(0, 8)
	check(t, err)
	if page.Matches[0].Address != testMemoryBase+4 || page.Matches[1].Address != testMemoryBase+8 {
		t.Fatalf("addresses: %+v", page)
	}
	if _, err := engine.StartScan(ScanRequest{Type: TypeUint32, Regions: []string{"missing"}}); err == nil {
		t.Fatal("unknown region accepted")
	}
	after, err := engine.ScanSummary()
	check(t, err)
	if after != summary {
		t.Fatal("failed scan discarded baseline")
	}
	if _, err := engine.Read(testMemoryBase+12, TypeUint32); !errors.Is(err, ErrAddressOutsideRegions) {
		t.Fatalf("boundary read: %v", err)
	}
	options.MaxScanBytes = 13
	limited, err := New(memory, options)
	check(t, err)
	if _, err := limited.StartScan(ScanRequest{Type: TypeUint8}); !errors.Is(err, ErrScanLimitExceeded) {
		t.Fatalf("byte cap: %v", err)
	}
}

func TestPagedNumericRefinements(t *testing.T) {
	for _, values := range [][3]Value{{I32(-5), I32(-2), I32(-9)}, {F32(-1.5), F32(2.5), F32(-3.5)}, {F64(-1.5), F64(2.5), F64(-3.5)}} {
		for _, comparison := range []Comparison{CompareIncreased, CompareDecreased, CompareChanged, CompareUnchanged} {
			for _, endian := range []Endian{EndianLittle, EndianBig} {
				memory := newTestMemory(3 * values[0].Type.Size())
				options := testOptions(uint32(len(memory.data)))
				options.ByteOrder = endian
				engine, err := New(memory, options)
				check(t, err)
				for i := 0; i < 3; i++ {
					check(t, engine.Write(testMemoryBase+uint32(i*values[0].Type.Size()), values[0], nil))
				}
				_, err = engine.StartScan(ScanRequest{Type: values[0].Type})
				check(t, err)
				check(t, engine.Write(testMemoryBase, values[1], nil))
				check(t, engine.Write(testMemoryBase+uint32(values[0].Type.Size()), values[2], nil))
				summary, err := engine.RefineScan(NextScanRequest{Comparison: comparison})
				check(t, err)
				want := 1
				if comparison == CompareChanged {
					want = 2
				}
				if summary.Total != want {
					t.Fatalf("%v/%v/%v: %d", values[0].Type, endian, comparison, summary.Total)
				}
			}
		}
	}
}

func TestMachineInvalidatesScanAtLifecycleBoundaries(t *testing.T) {
	memory := newTestMemory(4)
	target := &mutatingMachine{memory: memory}
	machine, err := Wrap(target, memory, testOptions(4))
	check(t, err)
	for _, action := range []func() error{
		func() error { return machine.Reset(context.Background()) },
		func() error { return machine.LoadState(bytes.NewReader(nil)) },
		machine.Close,
	} {
		_, err := machine.Cheats().StartScan(ScanRequest{Type: TypeUint8})
		check(t, err)
		check(t, action())
		if _, err := machine.Cheats().ScanSummary(); !errors.Is(err, ErrScanNotStarted) {
			t.Fatalf("scan survives lifecycle: %v", err)
		}
	}
}

func BenchmarkUnknown32MiB(b *testing.B) {
	memory := newTestMemory(32 << 20)
	engine, err := New(memory, Options{Regions: testOptions(32 << 20).Regions})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.StartScan(ScanRequest{Type: TypeUint32}); err != nil {
			b.Fatal(err)
		}
	}
}
