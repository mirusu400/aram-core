package gvm_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestIndexedImmediateStore(t *testing.T) {
	regions := []gvm.SymbolRegion{
		{Length: 6, Initial: make([]byte, 6)},
		{Length: 2, Initial: []byte{1, 0}},
	}
	vm, err := gvm.NewWithSymbols([]byte{0x2c, 0, 1, 0xfe, 0xff}, 0, regions)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	destination, _ := vm.Symbol(0)
	if got := int16(binary.LittleEndian.Uint16(destination[2:4])); got != -2 || vm.PC() != 4 {
		t.Fatalf("stored value = %d pc=%d, want -2 pc=4", got, vm.PC())
	}
}

func TestIndexedImmediateStoreFailureIsAtomic(t *testing.T) {
	regions := []gvm.SymbolRegion{
		{Length: 4, Initial: []byte{1, 0, 2, 0}},
		{Length: 2, Initial: []byte{2, 0}},
	}
	vm, err := gvm.NewWithSymbols([]byte{0x2c, 0, 1, 7}, 0, regions)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := vm.Symbol(0)
	before = append([]byte(nil), before...)
	err = vm.Step()
	destination, _ := vm.Symbol(0)
	if !errors.Is(err, gvm.ErrInvalidElement) || !reflect.DeepEqual(destination, before) || vm.PC() != 1 {
		t.Fatalf("err=%v destination=%x pc=%d", err, destination, vm.PC())
	}
}
