package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type displayCopyCall struct {
	source      gvm.DisplayBuffer
	destination gvm.DisplayBuffer
}

type displayCopySink struct {
	calls []displayCopyCall
	err   error
}

func (s *displayCopySink) CopyGVMDisplay(source, destination gvm.DisplayBuffer) error {
	s.calls = append(s.calls, displayCopyCall{source: source, destination: destination})
	return s.err
}

func TestDisplayCopyServices(t *testing.T) {
	sink := new(displayCopySink)
	v, err := gvm.NewWithAddressSpaceAndServices([]byte{0x05, 9, 0x76, 0x77, 0xff}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{DisplayCopy: sink})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(3); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	want := []displayCopyCall{
		{source: gvm.DisplayBufferDrawing, destination: gvm.DisplayBufferAuxiliary},
		{source: gvm.DisplayBufferAuxiliary, destination: gvm.DisplayBufferDrawing},
	}
	if !reflect.DeepEqual(sink.calls, want) || !reflect.DeepEqual(v.Stack(), []uint16{9}) {
		t.Fatalf("calls=%+v stack=%x", sink.calls, v.Stack())
	}
}

func TestDisplayCopyFailureBoundaries(t *testing.T) {
	providerErr := errors.New("copy failed")
	for _, test := range []struct {
		name string
		sink gvm.DisplayCopySink
		err  error
	}{
		{name: "unavailable", err: gvm.ErrDisplayCopyUnavailable},
		{name: "provider", sink: &displayCopySink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices([]byte{0x76}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{DisplayCopy: test.sink})
			if err != nil {
				t.Fatal(err)
			}
			err = v.Step()
			if !errors.Is(err, test.err) || v.PC() != 1 || v.Step() != err || v.Run(1) != err {
				t.Fatalf("err=%v pc=%d", err, v.PC())
			}
		})
	}
}

func TestDisplayCopyRequiresExplicitServiceConstructor(t *testing.T) {
	for _, op := range []byte{0x76, 0x77} {
		v := gvm.New([]byte{op})
		err := v.Step()
		var unsupported *gvm.UnsupportedOpcodeError
		if !errors.As(err, &unsupported) || unsupported.Opcode != op {
			t.Fatalf("opcode %x accepted by legacy constructor: %v", op, err)
		}
	}
}
