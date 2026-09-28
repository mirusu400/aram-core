package gvm_test

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestStringCopyRejectsOutOfRangeDestinationAndPopsOperands(t *testing.T) {
	// GVM2X's 0x7d handler calls its destination bounds helper first.
	program := []byte{0x06, 0x01, 0x2c, 0x06, 0x00, 0x01, 0x7d, 0xff}
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !vm.Halted() || len(vm.Stack()) != 0 {
		t.Fatalf("halted=%v stack=%x", vm.Halted(), vm.Stack())
	}
}

type copiedMedia struct{ data []byte }

func (s *copiedMedia) LoadGVMMedia(_ uint16, data []byte) error {
	s.data = bytes.Clone(data)
	return nil
}

func TestStringCopyGrowsDynamicMediaDestination(t *testing.T) {
	program := []byte{0x05, 0, 0x05, 1, 0x7d, 0x05, 0, 0x90, 0xff}
	sink := new(copiedMedia)
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media: []gvm.MediaResource{{}, {Data: []byte("Hi\x00ignored")}}, MediaLoad: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(6); err != nil || !bytes.Equal(sink.data, []byte("Hi\x00")) {
		t.Fatalf("run=%v copied=%q", err, sink.data)
	}
}
