package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaCompareSignedResult(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        uint16
	}{
		{"abc\x00", "abd\x00", 0xffff},
		{"abc\x00", "abc\x00", 0},
		{"abd\x00", "abc\x00", 1},
		{"ab\x00", "abc\x00", 0xffff},
	} {
		vm, err := gvm.NewWithAddressSpaceAndServices(
			[]byte{0x05, 0, 0x05, 1, 0x80}, 0, gvm.AddressSpace{},
			&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte(tc.left)}, {Data: []byte(tc.right)}}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(3); !errors.Is(err, gvm.ErrBudget) || len(vm.Stack()) != 1 || vm.Stack()[0] != tc.want {
			t.Fatalf("compare %q/%q: run=%v stack=%v want=%04x", tc.left, tc.right, err, vm.Stack(), tc.want)
		}
	}
}
