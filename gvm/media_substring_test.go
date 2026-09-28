package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaSubstringAndDecimalConversion(t *testing.T) {
	program := []byte{
		0x05, 0, 0x05, 1, 0x05, 6, 0x05, 2, 0x7e,
		0x05, 0, 0x83, 0xff,
	}
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media: []gvm.MediaResource{{}, {Data: []byte("level=42\x00")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(8); err != nil || len(vm.Stack()) != 1 || vm.Stack()[0] != 42 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}
