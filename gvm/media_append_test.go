package gvm

import (
	"bytes"
	"errors"
	"testing"
)

func TestMediaAppendString(t *testing.T) {
	vm, err := NewWithAddressSpaceAndServices(
		[]byte{0x05, 0, 0x05, 1, 0x7f, 0xff}, 0, AddressSpace{},
		&ServiceConfig{Media: []MediaResource{{Data: []byte("ab\x00")}, {Data: []byte("cd\x00")}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || !vm.Halted() || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v halted=%v stack=%x", err, vm.Halted(), vm.Stack())
	}
	got := vm.services.media[0]
	if !bytes.Equal(got, []byte("abcd\x00")) {
		t.Fatalf("destination=%q", got)
	}
}

func TestMediaAppendStringRejectsUnterminatedSource(t *testing.T) {
	vm, err := NewWithAddressSpaceAndServices(
		[]byte{0x05, 0, 0x05, 1, 0x7f}, 0, AddressSpace{},
		&ServiceConfig{Media: []MediaResource{{Data: []byte("ab\x00")}, {Data: []byte("cd")}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); !errors.Is(err, ErrInvalidMediaResource) {
		t.Fatalf("run=%v", err)
	}
	got := vm.services.media[0]
	if !bytes.Equal(got, []byte("ab\x00")) {
		t.Fatalf("destination changed: %q", got)
	}
}
