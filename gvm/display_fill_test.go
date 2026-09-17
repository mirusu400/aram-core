package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type displayFillSink struct {
	selectors []int16
	err       error
}

func (s *displayFillSink) FillGVMDisplay(selector int16) error {
	s.selectors = append(s.selectors, selector)
	return s.err
}

func TestDisplayFillService(t *testing.T) {
	for _, test := range []struct {
		value uint16
		want  int16
	}{{value: 26, want: 26}, {value: 183, want: 1}, {value: 0xffff, want: -1}, {value: 0x8000, want: -8}} {
		sink := new(displayFillSink)
		code := icPush(nil, test.value)
		code = append(code, 0x57)
		v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{DisplayFill: sink})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Run(2); err != gvm.ErrBudget || !reflect.DeepEqual(sink.selectors, []int16{test.want}) || len(v.Stack()) != 0 {
			t.Fatalf("value=%x err=%v selectors=%v stack=%x", test.value, err, sink.selectors, v.Stack())
		}
	}
}

func TestDisplayFillFailureBoundaries(t *testing.T) {
	providerErr := errors.New("fill failed")
	for _, test := range []struct {
		name string
		code []byte
		sink gvm.DisplayFillSink
		err  error
	}{
		{name: "underflow", code: []byte{0x57}, sink: new(displayFillSink), err: gvm.ErrStackUnderflow},
		{name: "unavailable", code: []byte{0x05, 1, 0x57}, err: gvm.ErrDisplayFillUnavailable},
		{name: "provider", code: []byte{0x05, 1, 0x57}, sink: &displayFillSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(test.code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{DisplayFill: test.sink})
			if err != nil {
				t.Fatal(err)
			}
			if len(test.code) > 1 {
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
