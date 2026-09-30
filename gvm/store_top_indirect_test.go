package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestStoreTopIndirect(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0x05, 42, 0x08, 0, 1, 0xff}, 0, []gvm.SymbolRegion{
		{Initial: []byte{0, 0, 0, 0}, Length: 4},
		{Initial: []byte{1, 0}, Length: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil {
		t.Fatal(err)
	}
	value, _ := vm.Symbol(0)
	if got := binary.LittleEndian.Uint16(value[2:]); got != 42 || len(vm.Stack()) != 0 {
		t.Fatalf("stored=%d stack=%v", got, vm.Stack())
	}
}

func TestStoreTopIndirectRejectsNegativeIndex(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0x05, 42, 0x08, 0, 1}, 0, []gvm.SymbolRegion{
		{Initial: []byte{0, 0}, Length: 2},
		{Initial: []byte{0xff, 0xff}, Length: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); !errors.Is(err, gvm.ErrInvalidElement) {
		t.Fatalf("Run = %v", err)
	}
	if got := vm.Stack(); len(got) != 1 || got[0] != 42 {
		t.Fatalf("stack = %v", got)
	}
}

func TestStoreTopIndexedIndirect(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0x05, 42, 0x07, 0, 1, 1, 0xff}, 0, []gvm.SymbolRegion{
		{Initial: []byte{0, 0, 0, 0}, Length: 4},
		{Initial: []byte{0, 0, 1, 0}, Length: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil {
		t.Fatal(err)
	}
	value, _ := vm.Symbol(0)
	if got := binary.LittleEndian.Uint16(value[2:]); got != 42 || len(vm.Stack()) != 0 {
		t.Fatalf("stored=%d stack=%v", got, vm.Stack())
	}
}
