package gvm_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSymbolStoreAliases(t *testing.T) {
	code := []byte{0x36, 0, 0xff, 0, 0, 0xff}
	a, b := uint32(3), uint32(4)
	regions := []gvm.SymbolRegion{{ProgramOffset: &a, Length: 2}, {ProgramOffset: &b, Length: 2}}
	v, err := gvm.NewWithSymbols(code, 0, regions)
	if err != nil {
		t.Fatal(err)
	}
	code[0] = 0xee
	a = 0
	regions[0].Length = 0
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	if v.PC() != 3 || len(v.Stack()) != 0 {
		t.Fatal("store PC/stack")
	}
	s, err := v.Symbol(0)
	if err != nil || !bytes.Equal(s, []byte{0xff, 0xff}) {
		t.Fatalf("signextension: %x %v", s, err)
	}
	overlap, err := v.Symbol(1)
	if err != nil || !bytes.Equal(overlap, []byte{0xff, 0xff}) {
		t.Fatalf("overlap: %x %v", overlap, err)
	}
	s[0] = 0xee
	overlap[0] = 0xee
	snapshot := v.Program()
	snapshot[3] = 0xee
	if err := v.Step(); err != nil || !v.Halted() || v.PC() != 4 {
		t.Fatalf("aliased future fetch: %v pc %d", err, v.PC())
	}
	if code[3] != 0 {
		t.Fatal("mutated caller program")
	}
}

func TestIndependentSymbolRAM(t *testing.T) {
	initial := []byte{8, 9, 10}
	v, err := gvm.NewWithSymbols([]byte{5, 7, 0x36, 0, 0x7f, 0xff}, 0, []gvm.SymbolRegion{{Length: 3, Initial: initial}, {Length: 3, Initial: initial}})
	if err != nil {
		t.Fatal(err)
	}
	initial[0] = 0xee
	if err := v.Run(3); err != nil {
		t.Fatal(err)
	}
	first, _ := v.Symbol(0)
	second, _ := v.Symbol(1)
	if !bytes.Equal(first, []byte{0x7f, 0, 10}) || !bytes.Equal(second, []byte{8, 9, 10}) {
		t.Fatalf("RAM %x %x", first, second)
	}
	if v.Stack()[0] != 7 || v.Program()[0] != 5 || initial[0] != 0xee {
		t.Fatal("store mutated stack/program/caller")
	}
	if _, err := v.Symbol(2); !errors.Is(err, gvm.ErrInvalidSymbol) {
		t.Fatalf("index: %v", err)
	}
}

func TestSymbolRegionValidation(t *testing.T) {
	start, end, huge := uint32(0), uint32(1), ^uint32(0)
	for _, region := range []gvm.SymbolRegion{
		{ProgramOffset: &start, Length: 2}, {ProgramOffset: &end, Length: 1},
		{ProgramOffset: &huge, Length: 2}, {ProgramOffset: &start, Length: ^uint32(0)},
		{ProgramOffset: &start, Length: 1, Initial: []byte{}},
		{Length: 1}, {Length: 0, Initial: []byte{1}},
	} {
		v, err := gvm.NewWithSymbols([]byte{0xff}, 0, []gvm.SymbolRegion{region})
		if v != nil || !errors.Is(err, gvm.ErrInvalidSymbolRegion) {
			t.Fatalf("region %+v: %v", region, err)
		}
	}
	if v, err := gvm.NewWithSymbols([]byte{0xff}, 1, nil); v != nil || !errors.Is(err, gvm.ErrInvalidTarget) {
		t.Fatalf("entry: %v", err)
	}
	// Empty regions are representable but cannot satisfy a two-byte store.
	if _, err := gvm.NewWithSymbols([]byte{0xff}, 0, []gvm.SymbolRegion{{ProgramOffset: &end}, {}}); err != nil {
		t.Fatal(err)
	}
}

func TestSymbolStoreSafety(t *testing.T) {
	for _, tt := range []struct {
		name    string
		code    []byte
		regions []gvm.SymbolRegion
		want    error
	}{
		{"missing index", []byte{0x36}, nil, gvm.ErrTruncated},
		{"invalid index precedes immediate", []byte{0x36, 0}, nil, gvm.ErrInvalidSymbol},
		{"invalid index", []byte{0x36, 1, 0x80}, []gvm.SymbolRegion{{Length: 2, Initial: []byte{1, 2}}}, gvm.ErrInvalidSymbol},
		{"short region precedes immediate", []byte{0x36, 0}, []gvm.SymbolRegion{{Length: 1, Initial: []byte{1}}}, gvm.ErrInvalidSymbolRegion},
		{"missing immediate", []byte{0x36, 0}, []gvm.SymbolRegion{{Length: 2, Initial: []byte{1, 2}}}, gvm.ErrTruncated},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v, err := gvm.NewWithSymbols(tt.code, 0, tt.regions)
			if err != nil {
				t.Fatal(err)
			}
			before := v.Program()
			err = v.Step()
			var fault *gvm.ExecutionError
			if !errors.Is(err, tt.want) || !errors.As(err, &fault) || fault.Offset != 0 || v.PC() != 1 || v.Halted() {
				t.Fatalf("err %v pc %d", err, v.PC())
			}
			if !bytes.Equal(before, v.Program()) || v.Step() != err {
				t.Fatal("fault changed program or resumed")
			}
			if len(tt.regions) > 0 {
				s, _ := v.Symbol(0)
				if !bytes.Equal(s, tt.regions[0].Initial) {
					t.Fatal("fault changed symbol")
				}
			}
		})
	}
}
