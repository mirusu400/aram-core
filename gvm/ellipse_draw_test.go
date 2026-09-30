package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type ellipseDrawCall struct{ x, y, radiusX, radiusY int16 }
type ellipseDrawSink struct{ calls []ellipseDrawCall }

func (s *ellipseDrawSink) DrawGVMEllipse(x, y, radiusX, radiusY int16) error {
	s.calls = append(s.calls, ellipseDrawCall{x, y, radiusX, radiusY})
	return nil
}

func TestEllipseDrawConsumesFourSignedWords(t *testing.T) {
	sink := new(ellipseDrawSink)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 3, 0x05, 4, 0x05, 5, 0x05, 6, 0x65, 0xff},
		0, gvm.AddressSpace{}, &gvm.ServiceConfig{EllipseDraw: sink},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if len(sink.calls) != 1 || sink.calls[0] != (ellipseDrawCall{3, 4, 5, 6}) || len(vm.Stack()) != 0 {
		t.Fatalf("calls=%v stack=%v", sink.calls, vm.Stack())
	}
}
