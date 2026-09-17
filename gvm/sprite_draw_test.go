package gvm_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type spriteDrawCall struct {
	resource []byte
	x        int16
	y        int16
}

type spriteDrawSink struct {
	calls []spriteDrawCall
	err   error
}

func (s *spriteDrawSink) DrawGVMSprite(resource []byte, x, y int16) error {
	s.calls = append(s.calls, spriteDrawCall{resource: append([]byte(nil), resource...), x: x, y: y})
	if len(resource) != 0 {
		resource[0] ^= 0xff
	}
	return s.err
}

func spriteDrawVM(t *testing.T, code, resource []byte, sink gvm.SpriteDrawSink) *gvm.VM {
	t.Helper()
	v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media:      []gvm.MediaResource{{Data: resource}},
		SpriteDraw: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSpriteDrawService(t *testing.T) {
	resource := []byte{2, 3, 4, 5, 6}
	code := icPush(nil, 0xffcc)
	code = icPush(code, 0xffb5)
	code = icPush(code, 0)
	code = append(code, 0x6f, 0xff)
	sink := new(spriteDrawSink)
	v := spriteDrawVM(t, code, resource, sink)
	if err := v.Run(4); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	if len(sink.calls) != 1 || sink.calls[0].x != -52 || sink.calls[0].y != -75 || !bytes.Equal(sink.calls[0].resource, resource) {
		t.Fatalf("calls=%+v", sink.calls)
	}
	if got := v.Stack(); len(got) != 0 {
		t.Fatalf("stack=%x", got)
	}
}

func TestSpriteDrawFailureBoundaries(t *testing.T) {
	providerErr := errors.New("draw failed")
	for _, test := range []struct {
		name  string
		index uint16
		sink  gvm.SpriteDrawSink
		err   error
	}{
		{name: "negative-index", index: 0xffff, sink: new(spriteDrawSink), err: gvm.ErrInvalidMediaIndex},
		{name: "large-index", index: 1, sink: new(spriteDrawSink), err: gvm.ErrInvalidMediaIndex},
		{name: "unavailable", index: 0, err: gvm.ErrSpriteDrawUnavailable},
		{name: "provider", index: 0, sink: &spriteDrawSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			code := icPush(nil, 10)
			code = icPush(code, 20)
			code = icPush(code, test.index)
			code = append(code, 0x6f)
			v := spriteDrawVM(t, code, []byte{2}, test.sink)
			if err := v.Run(3); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			before := v.Stack()
			err := v.Step()
			if !errors.Is(err, test.err) || !reflect.DeepEqual(v.Stack(), before) {
				t.Fatalf("err=%v stack=%x", err, v.Stack())
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("draw failure was not sticky")
			}
		})
	}
}

func TestSpriteDrawRequiresExplicitServiceConstructor(t *testing.T) {
	code := icPush(nil, 1)
	code = icPush(code, 2)
	code = icPush(code, 0)
	code = append(code, 0x6f)
	v := gvm.New(code)
	if err := v.Run(3); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	err := v.Step()
	var unsupported *gvm.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) || unsupported.Opcode != 0x6f {
		t.Fatalf("legacy constructor accepted sprite draw: %v", err)
	}
}
