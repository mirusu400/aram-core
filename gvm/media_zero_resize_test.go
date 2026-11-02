package gvm_test

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaZeroResizeClearsRequestedExtentAndPops(t *testing.T) {
	sink := new(copiedMedia)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 0, 0x05, 3, 0x7b, 0x05, 0, 0x90, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{
			Media:     []gvm.MediaResource{{Data: []byte{1}}},
			MediaLoad: sink,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(6); err != nil || len(vm.Stack()) != 0 || !bytes.Equal(sink.data, []byte{0, 0, 0}) {
		t.Fatalf("run=%v stack=%x media=%x", err, vm.Stack(), sink.data)
	}
}

func TestMediaZeroResizeFailureStillPops(t *testing.T) {
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x05, 2, 0x7b, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte{1}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}
