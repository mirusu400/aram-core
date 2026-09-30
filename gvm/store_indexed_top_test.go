package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestStoreIndexedTopPreservesStackAndFetchesNextOpcode(t *testing.T) {
	code := []byte{0x06, 0x80, 0x01, 0x4a, 0x00, 0x01, 0xff}
	vm, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 4, Initial: make([]byte, 4)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	region, err := vm.Symbol(0)
	if err != nil || binary.LittleEndian.Uint16(region[2:]) != 0x8001 || vm.PC() != 6 {
		t.Fatalf("indexed store: region=%x pc=%d err=%v", region, vm.PC(), err)
	}
	if got := vm.Stack(); len(got) != 1 || got[0] != 0x8001 {
		t.Fatalf("stack after indexed store: %x", got)
	}
	if err := vm.Step(); err != nil || !vm.Halted() {
		t.Fatalf("next opcode: %v halted=%t", err, vm.Halted())
	}
}

func TestStoreIndexedTopRejectsOutOfRangeElement(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0x05, 7, 0x4a, 0, 1}, 0,
		[]gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, gvm.ErrInvalidElement) || vm.PC() != 3 {
		t.Fatalf("invalid indexed store: pc=%d err=%v", vm.PC(), err)
	}
}
