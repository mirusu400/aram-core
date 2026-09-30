package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestAddressUnsignedByteLoad(t *testing.T) {
	code := []byte{0x05, 0, 0x05, 1, 0x85, 0xff}
	vm, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{RAM: []byte{0x7f, 0x80}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if got := vm.Stack(); len(got) != 1 || got[0] != 0x80 {
		t.Fatalf("unsigned address byte=%x", got)
	}
}

func TestAddressUnsignedByteRejectsInvalidElement(t *testing.T) {
	code := []byte{0x05, 0, 0x05, 1, 0x85}
	vm, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{RAM: []byte{0xff}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, gvm.ErrInvalidElement) {
		t.Fatalf("address bounds: %v", err)
	}
}
