package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type rectangleDrawCall struct{ x1, y1, x2, y2 int16 }

type rectangleDrawSink struct {
	calls []rectangleDrawCall
	err   error
}

func (s *rectangleDrawSink) DrawGVMRectangle(x1, y1, x2, y2 int16) error {
	s.calls = append(s.calls, rectangleDrawCall{x1, y1, x2, y2})
	return s.err
}

func TestRectangleDrawServicePreservesSignedStackOrder(t *testing.T) {
	sink := new(rectangleDrawSink)
	var code []byte
	for _, value := range []uint16{0xffd8, 0xff9d, 40, 0xffe9} {
		code = icPush(code, value)
	}
	code = append(code, 0x62)
	v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{RectangleDraw: sink})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(5); err != gvm.ErrBudget || !reflect.DeepEqual(sink.calls, []rectangleDrawCall{{-40, -99, 40, -23}}) || len(v.Stack()) != 0 {
		t.Fatalf("err=%v calls=%v stack=%x", err, sink.calls, v.Stack())
	}
}

func TestRectangleDrawFailureBoundaries(t *testing.T) {
	providerErr := errors.New("rectangle draw failed")
	for _, test := range []struct {
		name string
		code []byte
		sink gvm.RectangleDrawSink
		err  error
	}{
		{name: "underflow", code: []byte{0x05, 1, 0x62}, sink: new(rectangleDrawSink), err: gvm.ErrStackUnderflow},
		{name: "unavailable", code: []byte{0x05, 1, 0x05, 2, 0x05, 3, 0x05, 4, 0x62}, err: gvm.ErrRectangleDrawUnavailable},
		{name: "provider", code: []byte{0x05, 1, 0x05, 2, 0x05, 3, 0x05, 4, 0x62}, sink: &rectangleDrawSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(test.code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{RectangleDraw: test.sink})
			if err != nil {
				t.Fatal(err)
			}
			for v.PC() < len(test.code)-1 {
				if err := v.Step(); err != nil {
					t.Fatal(err)
				}
			}
			before := v.Stack()
			err = v.Step()
			if !errors.Is(err, test.err) || !reflect.DeepEqual(v.Stack(), before) || v.Step() != err {
				t.Fatalf("err=%v stack=%x", err, v.Stack())
			}
		})
	}
}
