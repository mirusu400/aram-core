package gvm_test

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaResizeAndSignedByteWrite(t *testing.T) {
	sink := new(copiedMedia)
	program := []byte{
		0x05, 0, 0x05, 3, 0x7a,
		0x05, 0, 0x05, 2, 0x05, 0xb9, 0x82,
		0x05, 0, 0x90, 0xff,
	}
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media: []gvm.MediaResource{{}}, MediaLoad: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(10); err != nil || !bytes.Equal(sink.data, []byte{0, 0, 0xb9, 0}) ||
		len(vm.Stack()) != 1 || vm.Stack()[0] != 1 {
		t.Fatalf("run=%v media=%x stack=%x", err, sink.data, vm.Stack())
	}
}
