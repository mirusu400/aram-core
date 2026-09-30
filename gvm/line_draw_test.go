package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type lineDrawCall struct{ x1, y1, x2, y2 int16 }
type lineDrawSink struct{ calls []lineDrawCall }

func (s *lineDrawSink) DrawGVMLine(x1, y1, x2, y2 int16) error {
	s.calls = append(s.calls, lineDrawCall{x1, y1, x2, y2})
	return nil
}

func TestLineDrawConsumesFourSignedCoordinates(t *testing.T) {
	sink := new(lineDrawSink)
	code := []byte{0x05, 0xfe, 0x05, 3, 0x05, 4, 0x05, 5, 0x5f, 0xff}
	vm, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{LineDraw: sink})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if len(sink.calls) != 1 || sink.calls[0] != (lineDrawCall{-2, 3, 4, 5}) || len(vm.Stack()) != 0 {
		t.Fatalf("line draw: calls=%v stack=%x", sink.calls, vm.Stack())
	}
}
