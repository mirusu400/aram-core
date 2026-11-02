package gvm_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestDeviceMINCopiesTerminatedIdentityAndPops(t *testing.T) {
	sink := new(copiedMedia)
	identity := gvm.DeviceIdentityProfile{MIN: "0111234567", UserID: "USER"}
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 0, 0x52, 0x05, 0, 0x90, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{
			DeviceIdentity: &identity,
			Media:          []gvm.MediaResource{{}},
			MediaLoad:      sink,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	identity.MIN = "changed"
	if err := vm.Run(5); err != nil || len(vm.Stack()) != 0 || !bytes.Equal(sink.data, []byte("0111234567\x00\x00")) {
		t.Fatalf("run=%v stack=%x media=%q", err, vm.Stack(), sink.data)
	}
}

func TestDeviceMINResizeFailureStillPops(t *testing.T) {
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x52, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{DeviceIdentity: &gvm.DeviceIdentityProfile{MIN: "1"}, Media: []gvm.MediaResource{{}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}

func TestDeviceMINRequiresExplicitService(t *testing.T) {
	legacy := gvm.New([]byte{0x05, 0, 0x52})
	var unsupported *gvm.UnsupportedOpcodeError
	if err := legacy.Run(2); !errors.As(err, &unsupported) || unsupported.Opcode != 0x52 {
		t.Fatalf("legacy error = %v", err)
	}
	vm, err := gvm.NewWithAddressSpaceAndServices([]byte{0x05, 0, 0x52}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); !errors.Is(err, gvm.ErrDeviceIdentityUnavailable) {
		t.Fatalf("service-aware error = %v", err)
	}
}

func TestDeviceIdentityValidation(t *testing.T) {
	for _, identity := range []gvm.DeviceIdentityProfile{
		{MIN: "bad\x00min"},
		{UserID: "bad\x00user"},
		{MIN: strings.Repeat("x", 65535)},
		{UserID: strings.Repeat("x", 65535)},
	} {
		if vm, err := gvm.NewWithAddressSpaceAndServices([]byte{0xff}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{DeviceIdentity: &identity}); vm != nil || !errors.Is(err, gvm.ErrInvalidServiceConfig) {
			t.Fatalf("identity accepted: vm=%v err=%v", vm, err)
		}
	}
}
