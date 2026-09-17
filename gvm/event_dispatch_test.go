package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestBeginSymbolDispatch(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0xff, 0x04, 0x00, 0xff}, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); err != nil {
		t.Fatal(err)
	}
	started, err := vm.BeginSymbolDispatch(0, 0x1234, 1)
	if err != nil || !started {
		t.Fatalf("BeginSymbolDispatch = %v, %v", started, err)
	}
	if err := vm.Run(2); err != nil {
		t.Fatal(err)
	}
	if got := vm.Stack(); len(got) != 1 || got[0] != 0x1234 {
		t.Fatalf("stack = %v, want [4660]", got)
	}
}

func TestBeginSymbolDispatchZeroEntryPublishesOnly(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0xff}, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); err != nil {
		t.Fatal(err)
	}
	started, err := vm.BeginSymbolDispatch(0, 7, 0)
	if err != nil || started {
		t.Fatalf("BeginSymbolDispatch = %v, %v", started, err)
	}
	symbol, err := vm.Symbol(0)
	if err != nil || binary.LittleEndian.Uint16(symbol) != 7 {
		t.Fatalf("symbol = %v, %v", symbol, err)
	}
	if !vm.Halted() {
		t.Fatal("zero entry resumed VM")
	}
}

func TestBeginSymbolDispatchValidationIsAtomic(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0xff}, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{1, 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); err != nil {
		t.Fatal(err)
	}
	before, _ := vm.Symbol(0)
	if _, err := vm.BeginSymbolDispatch(0, 9, 2); !errors.Is(err, gvm.ErrInvalidTarget) {
		t.Fatalf("invalid entry error = %v", err)
	}
	after, _ := vm.Symbol(0)
	if string(after) != string(before) {
		t.Fatalf("symbol changed on invalid entry: %v -> %v", before, after)
	}
	if _, err := vm.BeginSymbolDispatch(1, 9, 0); !errors.Is(err, gvm.ErrInvalidSymbol) {
		t.Fatalf("invalid symbol error = %v", err)
	}
}
