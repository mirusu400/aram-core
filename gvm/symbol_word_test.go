package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSymbolWordReadWriteBetweenDispatches(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0xff}, 0, []gvm.SymbolRegion{
		{Length: 4, Initial: []byte{0x34, 0x12, 9, 9}},
		{Length: 1, Initial: []byte{1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := vm.SymbolWord(0); err != nil || got != 0x1234 {
		t.Fatalf("SymbolWord = %#x, %v", got, err)
	}
	if err := vm.SetSymbolWord(0, 7); !errors.Is(err, gvm.ErrDispatchActive) {
		t.Fatalf("write during dispatch = %v", err)
	}
	if err := vm.Run(1); err != nil {
		t.Fatal(err)
	}
	if err := vm.SetSymbolWord(0, 0xbeef); err != nil {
		t.Fatal(err)
	}
	symbol, _ := vm.Symbol(0)
	if got, _ := vm.SymbolWord(0); got != 0xbeef || symbol[2] != 9 || symbol[3] != 9 {
		t.Fatalf("symbol after write = %x", symbol)
	}
	if err := vm.SetSymbolWord(1, 1); !errors.Is(err, gvm.ErrInvalidSymbolRegion) {
		t.Fatalf("short symbol write = %v", err)
	}
	if _, err := vm.SymbolWord(2); !errors.Is(err, gvm.ErrInvalidSymbol) {
		t.Fatalf("missing symbol read = %v", err)
	}
	if err := vm.SetSymbolWord(2, 1); !errors.Is(err, gvm.ErrInvalidSymbol) {
		t.Fatalf("missing symbol write = %v", err)
	}
}
