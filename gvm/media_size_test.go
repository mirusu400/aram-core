package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaSizeReplacesIndex(t *testing.T) {
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x79, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Media: []gvm.MediaResource{{}, {Data: []byte{1, 2, 3, 0}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil || len(vm.Stack()) != 1 || vm.Stack()[0] != 4 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}

func TestMediaSizeRejectsInvalidIndex(t *testing.T) {
	for _, raw := range []byte{0xff, 1} {
		vm, err := gvm.NewWithAddressSpaceAndServices(
			[]byte{0x05, raw, 0x79}, 0, gvm.AddressSpace{},
			&gvm.ServiceConfig{Media: []gvm.MediaResource{{}}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(2); !errors.Is(err, gvm.ErrInvalidMediaIndex) {
			t.Fatalf("index %#x error = %v", raw, err)
		}
	}
}

func TestMediaSizeLegacyConstructorIsUnsupported(t *testing.T) {
	vm := gvm.New([]byte{0x05, 0, 0x79})
	var unsupported *gvm.UnsupportedOpcodeError
	if err := vm.Run(2); !errors.As(err, &unsupported) || unsupported.Opcode != 0x79 {
		t.Fatalf("error = %v", err)
	}
}
