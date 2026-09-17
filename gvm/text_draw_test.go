package gvm_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type textDrawCall struct {
	resource []byte
	x, y     int16
	style    gvm.TextDrawStyle
}

type textDrawSink struct {
	calls []textDrawCall
	err   error
}

func (s *textDrawSink) DrawGVMText(resource []byte, x, y int16, style gvm.TextDrawStyle) error {
	s.calls = append(s.calls, textDrawCall{append([]byte(nil), resource...), x, y, style})
	if len(resource) != 0 {
		resource[0] ^= 0xff
	}
	return s.err
}

func TestTextDrawForwardsResourceCoordinatesAndStyle(t *testing.T) {
	resource := []byte{0xc4, 0xbf, 0xb9, 0xc2, 0}
	var code []byte
	for _, value := range []uint16{2, 3, 1, 1} {
		code = icPush(code, value)
	}
	code = append(code, 0x66)
	code = icPush(code, 41)
	code = icPush(code, 0xff9d)
	code = icPush(code, 0)
	code = append(code, 0x6a)
	sink := new(textDrawSink)
	v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media: []gvm.MediaResource{{Data: resource}}, TextDraw: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(9); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	wantStyle := gvm.TextDrawStyle{Mode: 2, Primary: 3, Alignment: 1}
	if len(sink.calls) != 1 || sink.calls[0].x != 41 || sink.calls[0].y != -99 || sink.calls[0].style != wantStyle || !bytes.Equal(sink.calls[0].resource, resource) || len(v.Stack()) != 0 {
		t.Fatalf("calls=%+v stack=%x", sink.calls, v.Stack())
	}
}

func TestTextDrawFailurePreservesStack(t *testing.T) {
	providerErr := errors.New("text failed")
	for _, test := range []struct {
		name  string
		index uint16
		sink  gvm.TextDrawSink
		err   error
	}{
		{name: "negative-index", index: 0xffff, sink: new(textDrawSink), err: gvm.ErrInvalidMediaIndex},
		{name: "large-index", index: 1, sink: new(textDrawSink), err: gvm.ErrInvalidMediaIndex},
		{name: "unavailable", index: 0, err: gvm.ErrTextDrawUnavailable},
		{name: "provider", index: 0, sink: &textDrawSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			code := icPush(nil, 10)
			code = icPush(code, 20)
			code = icPush(code, test.index)
			code = append(code, 0x6a)
			v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
				Media: []gvm.MediaResource{{Data: []byte("text\x00")}}, TextDraw: test.sink,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Run(3); !errors.Is(err, gvm.ErrBudget) {
				t.Fatal(err)
			}
			before := v.Stack()
			err = v.Step()
			if !errors.Is(err, test.err) || !reflect.DeepEqual(v.Stack(), before) || v.Step() != err {
				t.Fatalf("err=%v stack=%x", err, v.Stack())
			}
		})
	}
}
