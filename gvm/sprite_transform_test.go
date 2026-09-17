package gvm_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type spriteTransformCall struct {
	resource []byte
	x, y     int16
	mirror   bool
}

type spriteTransformSink struct {
	calls []spriteTransformCall
	err   error
}

func (s *spriteTransformSink) DrawGVMTransformedSprite(resource []byte, x, y int16, mirror bool) error {
	s.calls = append(s.calls, spriteTransformCall{append([]byte(nil), resource...), x, y, mirror})
	if len(resource) != 0 {
		resource[0] ^= 0xff
	}
	return s.err
}

func spriteTransformVM(t *testing.T, code, resource []byte, sink gvm.SpriteTransformSink) *gvm.VM {
	t.Helper()
	v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media: []gvm.MediaResource{{Data: resource}}, SpriteTransform: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSpriteTransformServicePreservesOrderAndRawNonzeroFlag(t *testing.T) {
	resource := []byte{8, 3, 1, 0, 0, 1, 2, 3}
	code := icPush(nil, 41)
	code = icPush(code, 0xff9d)
	code = icPush(code, 0)
	code = icPush(code, 0xffff)
	code = append(code, 0x70)
	sink := new(spriteTransformSink)
	v := spriteTransformVM(t, code, resource, sink)
	if err := v.Run(5); err != gvm.ErrBudget || len(sink.calls) != 1 || sink.calls[0].x != 41 || sink.calls[0].y != -99 || !sink.calls[0].mirror || !bytes.Equal(sink.calls[0].resource, resource) || len(v.Stack()) != 0 {
		t.Fatalf("err=%v calls=%+v stack=%x", err, sink.calls, v.Stack())
	}
}

func TestSpriteTransformFailureBoundaries(t *testing.T) {
	providerErr := errors.New("transform failed")
	for _, test := range []struct {
		name  string
		index uint16
		sink  gvm.SpriteTransformSink
		err   error
	}{
		{name: "negative-index", index: 0xffff, sink: new(spriteTransformSink), err: gvm.ErrInvalidMediaIndex},
		{name: "large-index", index: 1, sink: new(spriteTransformSink), err: gvm.ErrInvalidMediaIndex},
		{name: "unavailable", index: 0, err: gvm.ErrSpriteTransformUnavailable},
		{name: "provider", index: 0, sink: &spriteTransformSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			code := icPush(nil, 10)
			code = icPush(code, 20)
			code = icPush(code, test.index)
			code = icPush(code, 1)
			code = append(code, 0x70)
			v := spriteTransformVM(t, code, []byte{2}, test.sink)
			if err := v.Run(4); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			before := v.Stack()
			err := v.Step()
			if !errors.Is(err, test.err) || !reflect.DeepEqual(v.Stack(), before) || v.Step() != err {
				t.Fatalf("err=%v stack=%x", err, v.Stack())
			}
		})
	}
}
