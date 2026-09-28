package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestPointOpcodeRejectsOffscreenCoordinate(t *testing.T) {
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x05, 0x9e, 0x05, 0, 0x58, 0xff}, 0,
		gvm.AddressSpace{}, &gvm.ServiceConfig{DeviceQuery: &gvm.DeviceQueryProfile{Width: 120, Height: 80}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); err != nil || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
	vm, err = gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x05, 2, 0x05, 0, 0x58}, 0,
		gvm.AddressSpace{}, &gvm.ServiceConfig{DeviceQuery: &gvm.DeviceQueryProfile{Width: 120, Height: 80}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var unsupported *gvm.UnsupportedOpcodeError
	if err := vm.Run(4); !errors.As(err, &unsupported) || unsupported.Opcode != 0x58 {
		t.Fatalf("in-bounds error = %v", err)
	}
}
