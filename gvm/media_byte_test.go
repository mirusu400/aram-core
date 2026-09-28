package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaByteReadSignExtendsAndPopsOffset(t *testing.T) {
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 0, 0x05, 1, 0x81, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte{1, 0xfe}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || len(vm.Stack()) != 1 || vm.Stack()[0] != 0xfffe {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}
