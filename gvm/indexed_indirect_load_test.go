package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestIndexedIndirectLoadFromFixedIndexElement(t *testing.T) {
	destination := []byte{0, 0}
	source := []byte{0x11, 0, 0x22, 0, 0x33, 0}
	indices := []byte{0, 0, 2, 0}
	vm, err := gvm.NewWithSymbols(
		[]byte{0x32, 0, 1, 2, 1, 0xff}, 0,
		[]gvm.SymbolRegion{
			{Length: 2, Initial: destination},
			{Length: 6, Initial: source},
			{Length: 4, Initial: indices},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); err != nil {
		t.Fatal(err)
	}
	got, err := vm.Symbol(0)
	if err != nil || binary.LittleEndian.Uint16(got) != 0x33 {
		t.Fatalf("destination = %x, err=%v", got, err)
	}
}

func TestIndexedIndirectLoadRejectsInvalidSelectors(t *testing.T) {
	for name, tc := range map[string]struct {
		code    []byte
		symbols []gvm.SymbolRegion
		want    error
	}{
		"truncated":         {[]byte{0x32, 0, 1, 2}, nil, gvm.ErrTruncated},
		"symbol":            {[]byte{0x32, 0, 1, 3, 0}, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0, 0}}, {Length: 2, Initial: []byte{0, 0}}}, gvm.ErrInvalidSymbol},
		"fixed element":     {[]byte{0x32, 0, 1, 2, 1}, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0, 0}}, {Length: 2, Initial: []byte{0, 0}}, {Length: 2, Initial: []byte{0, 0}}}, gvm.ErrInvalidElement},
		"negative indirect": {[]byte{0x32, 0, 1, 2, 0}, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0, 0}}, {Length: 2, Initial: []byte{0, 0}}, {Length: 2, Initial: []byte{0xff, 0xff}}}, gvm.ErrInvalidElement},
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
