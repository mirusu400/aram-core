package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestIndexedSymbolCopy(t *testing.T) {
	destination := []byte{1, 2, 3, 4}
	source := []byte{5, 6, 0x34, 0x12}
	v, err := gvm.NewWithSymbols(
		[]byte{0x2f, 0, 0, 1, 1, 0xff},
		0,
		[]gvm.SymbolRegion{
			{Initial: destination, Length: uint32(len(destination))},
			{Initial: source, Length: uint32(len(source))},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Step(); err != nil || v.PC() != 5 || len(v.Stack()) != 0 {
		t.Fatalf("copy: err=%v pc=%d stack=%v", err, v.PC(), v.Stack())
	}
	got, _ := v.Symbol(0)
	if binary.LittleEndian.Uint16(got[:2]) != 0x1234 || binary.LittleEndian.Uint16(got[2:]) != 0x0403 {
		t.Fatalf("destination=%x", got)
	}
	if err := v.Step(); err != nil || !v.Halted() {
		t.Fatalf("halt: %v", err)
	}
}

func TestIndexedSymbolCopyOverlappingAlias(t *testing.T) {
	program := []byte{0x2f, 0, 0, 1, 0, 0xff, 0x34, 0x12}
	destinationOffset, sourceOffset := uint32(6), uint32(5)
	v, err := gvm.NewWithSymbols(program, 0, []gvm.SymbolRegion{
		{ProgramOffset: &destinationOffset, Length: 2},
		{ProgramOffset: &sourceOffset, Length: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	got := v.Program()
	if got[4] != 0 || got[5] != 0xff || got[6] != 0xff || got[7] != 0x34 {
		t.Fatalf("overlap=%x", got[4:8])
	}
}

func TestIndexedSymbolCopyFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		program []byte
		symbols []gvm.SymbolRegion
		want    error
	}{
		{name: "truncated", program: []byte{0x2f, 0, 0, 0}, want: gvm.ErrTruncated},
		{name: "destination-symbol", program: []byte{0x2f, 1, 0, 0, 0}, symbols: []gvm.SymbolRegion{{Initial: []byte{0, 0}, Length: 2}}, want: gvm.ErrInvalidSymbol},
		{name: "destination-element", program: []byte{0x2f, 0, 1, 0, 0}, symbols: []gvm.SymbolRegion{{Initial: []byte{0, 0}, Length: 2}}, want: gvm.ErrInvalidElement},
		{name: "source-symbol", program: []byte{0x2f, 0, 0, 1, 0}, symbols: []gvm.SymbolRegion{{Initial: []byte{0, 0}, Length: 2}}, want: gvm.ErrInvalidSymbol},
		{name: "source-element", program: []byte{0x2f, 0, 0, 1, 1}, symbols: []gvm.SymbolRegion{{Initial: []byte{0, 0}, Length: 2}, {Initial: []byte{0, 0}, Length: 2}}, want: gvm.ErrInvalidElement},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithSymbols(test.program, 0, test.symbols)
			if err != nil {
				t.Fatal(err)
			}
			err = v.Step()
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v", err)
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("copy failure was not sticky")
			}
		})
	}
}
