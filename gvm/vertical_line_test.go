package gvm_test

import (
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestVerticalLineUsesSignedCoordinates(t *testing.T) {
	sink := new(rectangleDrawSink)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 2, 0x05, 32, 0x05, 0x9d, 0x60, 0xff}, 0,
		gvm.AddressSpace{}, &gvm.ServiceConfig{RectangleDraw: sink},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); err != nil || !reflect.DeepEqual(sink.calls, []rectangleDrawCall{{-99, 2, -99, 32}}) || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v calls=%v stack=%x", err, sink.calls, vm.Stack())
	}
}

func TestHorizontalLineUsesSignedCoordinates(t *testing.T) {
	sink := new(rectangleDrawSink)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 2, 0x05, 32, 0x05, 0x9d, 0x61, 0xff}, 0,
		gvm.AddressSpace{}, &gvm.ServiceConfig{RectangleDraw: sink},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); err != nil || !reflect.DeepEqual(sink.calls, []rectangleDrawCall{{2, -99, 32, -99}}) || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v calls=%v stack=%x", err, sink.calls, vm.Stack())
	}
}
