package gvm_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestIndexedCopyThroughIndexSymbols(t *testing.T) {
	initial := [][]byte{
		make([]byte, 6), make([]byte, 2), make([]byte, 6), make([]byte, 2),
	}
	binary.LittleEndian.PutUint16(initial[0][2:4], 0x1111)
	binary.LittleEndian.PutUint16(initial[1], 1)
	binary.LittleEndian.PutUint16(initial[2][4:6], 0xbeef)
	binary.LittleEndian.PutUint16(initial[3], 2)
	regions := make([]gvm.SymbolRegion, len(initial))
	for index := range initial {
		regions[index] = gvm.SymbolRegion{Length: uint32(len(initial[index])), Initial: initial[index]}
	}
	vm, err := gvm.NewWithSymbols([]byte{0x29, 0, 1, 2, 3, 0xff}, 0, regions)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	destination, _ := vm.Symbol(0)
	if got := binary.LittleEndian.Uint16(destination[2:4]); got != 0xbeef || vm.PC() != 5 || len(vm.Stack()) != 0 {
		t.Fatalf("copy value=%04x pc=%d stack=%x", got, vm.PC(), vm.Stack())
	}
	if err := vm.Run(1); err != nil || !vm.Halted() {
		t.Fatal(err)
	}
}

func TestIndexedCopyThroughIndexSymbolsFaultsAtomically(t *testing.T) {
	for _, indexWord := range []uint16{0xffff, 3} {
		initial := [][]byte{make([]byte, 6), make([]byte, 2), make([]byte, 6), make([]byte, 2)}
		binary.LittleEndian.PutUint16(initial[0][2:4], 0x1234)
		binary.LittleEndian.PutUint16(initial[1], 1)
		binary.LittleEndian.PutUint16(initial[2], 0xabcd)
		binary.LittleEndian.PutUint16(initial[3], indexWord)
		regions := make([]gvm.SymbolRegion, len(initial))
		for index := range initial {
			regions[index] = gvm.SymbolRegion{Length: uint32(len(initial[index])), Initial: initial[index]}
		}
		vm, err := gvm.NewWithSymbols([]byte{0x29, 0, 1, 2, 3}, 0, regions)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := vm.Symbol(0)
		before = append([]byte(nil), before...)
		err = vm.Step()
		destination, _ := vm.Symbol(0)
		if !errors.Is(err, gvm.ErrInvalidElement) || !reflect.DeepEqual(destination, before) || vm.PC() != 1 {
			t.Fatalf("index=%04x err=%v destination=%x", indexWord, err, destination)
		}
	}
	vm, err := gvm.NewWithSymbols([]byte{0x29, 0, 1}, 0, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}, {Length: 2, Initial: make([]byte, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, gvm.ErrTruncated) {
		t.Fatalf("truncated err=%v", err)
	}
}
