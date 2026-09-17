package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSpritePaletteCopy(t *testing.T) {
	tests := []struct {
		typeCode byte
		entries  []byte
		want     []byte
	}{
		{2, []byte{1, 4}, []byte{0x14}},
		{3, []byte{1, 2, 3, 4}, []byte{0x12, 0x34}},
		{5, []byte{9, 4}, []byte{9, 4}},
		{6, []byte{1, 2, 3, 4}, []byte{1, 2, 3, 4}},
	}
	for _, test := range tests {
		ram := make([]byte, 64)
		ram[16] = test.typeCode
		for index, value := range test.entries {
			ram[18+2*index] = value
		}
		vm, err := gvm.NewWithAddressSpaceAndServices(
			[]byte{0x05, 0, 0x05, 8, 0x6e, 0xff}, 0,
			gvm.AddressSpace{RAM: ram}, &gvm.ServiceConfig{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(3); !errors.Is(err, gvm.ErrBudget) {
			t.Fatal(err)
		}
		for index, want := range test.want {
			word, err := vm.ReadWord(uint16(index / 2))
			if err != nil {
				t.Fatal(err)
			}
			got := byte(word >> uint(8*(index%2)))
			if got != want {
				t.Fatalf("type%d byte%d=%02x want=%02x", test.typeCode, index, got, want)
			}
		}
	}
}
