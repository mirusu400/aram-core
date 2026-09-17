package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type rectangleFillCall struct{ x1, y1, x2, y2 int16 }

type rectangleFillSink struct {
	calls []rectangleFillCall
	err   error
}

func (s *rectangleFillSink) FillGVMRectangle(x1, y1, x2, y2 int16) error {
	s.calls = append(s.calls, rectangleFillCall{x1, y1, x2, y2})
	return s.err
}

func TestRectangleFillServicePreservesSignedStackOrder(t *testing.T) {
	sink := new(rectangleFillSink)
	var code []byte
	for _, value := range []uint16{0xffff, 2, 100, 0x8000} {
		code = icPush(code, value)
	}
	code = append(code, 0x63)
	v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{RectangleFill: sink})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(5); err != gvm.ErrBudget || !reflect.DeepEqual(sink.calls, []rectangleFillCall{{-1, 2, 100, -32768}}) || len(v.Stack()) != 0 {
		t.Fatalf("err=%v calls=%v stack=%x", err, sink.calls, v.Stack())
	}
}

func TestRectangleFillFailureBoundaries(t *testing.T) {
	providerErr := errors.New("rectangle fill failed")
	for _, test := range []struct {
		name string
		code []byte
		sink gvm.RectangleFillSink
		err  error
	}{
		{name: "underflow", code: []byte{0x05, 1, 0x63}, sink: new(rectangleFillSink), err: gvm.ErrStackUnderflow},
		{name: "unavailable", code: []byte{0x05, 1, 0x05, 2, 0x05, 3, 0x05, 4, 0x63}, err: gvm.ErrRectangleFillUnavailable},
		{name: "provider", code: []byte{0x05, 1, 0x05, 2, 0x05, 3, 0x05, 4, 0x63}, sink: &rectangleFillSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(test.code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{RectangleFill: test.sink})
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
