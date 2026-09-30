package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestRAMStoreTopLeavesValueOnStack(t *testing.T) {
	code := []byte{0x05, 1, 0x06, 0x80, 0x01, 0x50, 0xff}
	vm, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{RAM: make([]byte, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if got := vm.Stack(); len(got) != 1 || got[0] != 0x8001 {
		t.Fatalf("RAM store stack=%x", got)
	}
	value, err := vm.ReadWord(1)
	if err != nil || value != 0x8001 {
		t.Fatalf("RAM store value=%x err=%v", value, err)
	}
}

func TestRAMStoreTopRejectsOutOfRangeAddress(t *testing.T) {
	vm, err := gvm.NewWithAddressSpace([]byte{0x05, 2, 0x05, 7, 0x50}, 0,
		gvm.AddressSpace{RAM: make([]byte, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, gvm.ErrInvalidAddress) {
		t.Fatalf("RAM store bounds: %v", err)
	}
}
