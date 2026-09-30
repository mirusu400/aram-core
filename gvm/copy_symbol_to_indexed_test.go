package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestCopySymbolToIndexed(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0x2b, 0, 1, 2, 0xff}, 0, []gvm.SymbolRegion{
		{Initial: []byte{3, 0, 4, 0}, Length: 4},
		{Initial: []byte{1, 0}, Length: 2},
		{Initial: []byte{9, 0}, Length: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); err != nil {
		t.Fatal(err)
	}
	got, err := vm.Symbol(0)
	if err != nil || binary.LittleEndian.Uint16(got[:2]) != 3 || binary.LittleEndian.Uint16(got[2:]) != 9 {
		t.Fatalf("destination = %x, %v", got, err)
	}
}

func TestCopySymbolToIndexedRejectsNegativeIndex(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0x2b, 0, 1, 2}, 0, []gvm.SymbolRegion{
		{Initial: []byte{3, 0}, Length: 2},
		{Initial: []byte{0xff, 0xff}, Length: 2},
		{Initial: []byte{9, 0}, Length: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, gvm.ErrInvalidElement) {
		t.Fatalf("Step = %v", err)
	}
	got, _ := vm.Symbol(0)
	if binary.LittleEndian.Uint16(got) != 3 {
		t.Fatalf("destination changed: %x", got)
	}
}
