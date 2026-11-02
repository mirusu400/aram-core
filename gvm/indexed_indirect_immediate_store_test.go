package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestIndexedIndirectImmediateStore(t *testing.T) {
	vm, err := gvm.NewWithSymbols(
		[]byte{0x27, 0, 1, 1, 0x80, 0xff}, 0,
		[]gvm.SymbolRegion{
			{Length: 6, Initial: []byte{1, 0, 2, 0, 3, 0}},
			{Length: 4, Initial: []byte{0, 0, 2, 0}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); err != nil {
		t.Fatal(err)
	}
	got, err := vm.Symbol(0)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(got[4:]) != 0xff80 || binary.LittleEndian.Uint16(got[:2]) != 1 {
		t.Fatalf("destination = %x", got)
	}
	if len(vm.Stack()) != 0 || vm.PC() != 6 {
		t.Fatalf("stack=%v pc=%d", vm.Stack(), vm.PC())
	}
}

func TestIndexedIndirectImmediateStoreRejectsInvalidOperands(t *testing.T) {
	for name, tc := range map[string]struct {
		code    []byte
		symbols []gvm.SymbolRegion
		want    error
	}{
		"truncated":          {[]byte{0x27, 0, 1, 0}, nil, gvm.ErrTruncated},
		"index symbol":       {[]byte{0x27, 0, 2, 0, 1}, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}, {Length: 2, Initial: make([]byte, 2)}}, gvm.ErrInvalidSymbol},
		"index shape":        {[]byte{0x27, 0, 1, 0, 1}, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}, {Length: 1, Initial: make([]byte, 1)}}, gvm.ErrInvalidSymbolRegion},
		"index element":      {[]byte{0x27, 0, 1, 1, 1}, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}, {Length: 2, Initial: make([]byte, 2)}}, gvm.ErrInvalidElement},
		"destination symbol": {[]byte{0x27, 2, 1, 0, 1}, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}, {Length: 2, Initial: make([]byte, 2)}}, gvm.ErrInvalidSymbol},
		"negative element":   {[]byte{0x27, 0, 1, 0, 1}, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}, {Length: 2, Initial: []byte{0xff, 0xff}}}, gvm.ErrInvalidElement},
	} {
		t.Run(name, func(t *testing.T) {
			vm, err := gvm.NewWithSymbols(tc.code, 0, tc.symbols)
			if err != nil {
				t.Fatal(err)
			}
			if err := vm.Step(); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestIndexedIndirectImmediateStoreKeepsNativeUpperStackGuard(t *testing.T) {
	code := make([]byte, 0, 2*65+5)
	for range 65 {
		code = append(code, 0x05, 1)
	}
	code = append(code, 0x27, 0, 0, 0, 1)
	vm, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(66); !errors.Is(err, gvm.ErrStackOverflow) {
		t.Fatalf("error = %v", err)
	}
	got, err := vm.Symbol(0)
	if err != nil || binary.LittleEndian.Uint16(got) != 0 || len(vm.Stack()) != 65 {
		t.Fatalf("symbol=%x stack=%d err=%v", got, len(vm.Stack()), err)
	}
}
