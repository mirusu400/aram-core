package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestAddressByteStoreWritesLowByteAndPops(t *testing.T) {
	code := []byte{0x05, 1, 0x05, 2, 0x06, 0x12, 0x80, 0x86, 0xff}
	vm, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{
		RAM:     []byte{0, 0, 0, 0, 0, 0},
		Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Offset: 2, Length: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); err != nil || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
	if got, err := vm.ReadWord(2); err != nil || got != 0x0080 {
		t.Fatalf("word = %#x, err=%v", got, err)
	}
}

func TestAddressByteStoreRejectsInvalidAccessAtomically(t *testing.T) {
	for name, tc := range map[string]struct {
		ref, element int16
		want         error
	}{
		"negative offset": {0, -1, gvm.ErrInvalidElement},
		"past end":        {0, 4, gvm.ErrInvalidElement},
		"invalid ref":     {3, 0, gvm.ErrInvalidAddress},
	} {
		t.Run(name, func(t *testing.T) {
			code := []byte{0x06, byte(uint16(tc.ref) >> 8), byte(tc.ref), 0x06, byte(uint16(tc.element) >> 8), byte(tc.element), 0x05, 1, 0x86}
			vm, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{RAM: []byte{1, 2, 3, 4}})
			if err != nil {
				t.Fatal(err)
			}
			if err := vm.Run(4); !errors.Is(err, tc.want) || len(vm.Stack()) != 3 {
				t.Fatalf("error=%v stack=%x", err, vm.Stack())
			}
		})
	}
}

func TestAddressByteStoreLegacyConstructorIsUnsupported(t *testing.T) {
	vm := gvm.New([]byte{0x05, 0, 0x05, 0, 0x05, 0, 0x86})
	var unsupported *gvm.UnsupportedOpcodeError
	if err := vm.Run(4); !errors.As(err, &unsupported) || unsupported.Opcode != 0x86 {
		t.Fatalf("error = %v", err)
	}
}
