package gvm_test

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestFormatSignedWordIntoDynamicMediaString(t *testing.T) {
	sink := new(copiedMedia)
	program := []byte{0x05, 0, 0x05, 1, 0x05, 0xf9, 0x8a, 0x05, 0, 0x90, 0xff}
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media: []gvm.MediaResource{{}, {Data: []byte("Score: %d\x00")}}, MediaLoad: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(7); err != nil || !bytes.Equal(sink.data, []byte("Score: -7\x00")) || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v formatted=%q stack=%x", err, sink.data, vm.Stack())
	}
}

func TestFormatTwoSignedWordsWithWidth(t *testing.T) {
	sink := new(copiedMedia)
	program := []byte{
		0x05, 0, 0x05, 1, 0x05, 90, 0x05, 0x90, 0x8b,
		0x05, 0, 0x90, 0xff,
	}
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media: []gvm.MediaResource{{}, {Data: []byte("ATT.%4d-%d\x00")}}, MediaLoad: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(8); err != nil || !bytes.Equal(sink.data, []byte("ATT.  90--112\x00")) || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v formatted=%q stack=%x", err, sink.data, vm.Stack())
	}
}
