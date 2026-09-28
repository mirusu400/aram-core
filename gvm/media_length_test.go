package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaStringLengthReplacesIndex(t *testing.T) {
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 0, 0x7c, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte("Score: 0\x00extra")}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil || len(vm.Stack()) != 1 || vm.Stack()[0] != 8 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}
