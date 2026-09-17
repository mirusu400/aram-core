package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type colorSelectSink struct {
	selectors []uint8
	err       error
}

func (s *colorSelectSink) SelectGVMColor(selector uint8) error {
	s.selectors = append(s.selectors, selector)
	return s.err
}

func TestColorSelectServiceUsesLowByteModulo182(t *testing.T) {
	for _, test := range []struct {
		value uint16
		want  uint8
	}{{value: 35, want: 35}, {value: 182, want: 0}, {value: 0x0123, want: 35}, {value: 0xffff, want: 73}, {value: 4, want: 4}} {
		sink := new(colorSelectSink)
		code := icPush(nil, test.value)
		code = append(code, 0x5e)
		v, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{ColorSelect: sink})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Run(2); err != gvm.ErrBudget || !reflect.DeepEqual(sink.selectors, []uint8{test.want}) || len(v.Stack()) != 0 {
			t.Fatalf("value=%x err=%v selectors=%v stack=%x", test.value, err, sink.selectors, v.Stack())
		}
	}
}

func TestColorSelectFailureBoundaries(t *testing.T) {
	providerErr := errors.New("color select failed")
	for _, test := range []struct {
		name string
		code []byte
		sink gvm.ColorSelectSink
		err  error
	}{
		{name: "underflow", code: []byte{0x5e}, sink: new(colorSelectSink), err: gvm.ErrStackUnderflow},
		{name: "unavailable", code: []byte{0x05, 1, 0x5e}, err: gvm.ErrColorSelectUnavailable},
		{name: "provider", code: []byte{0x05, 1, 0x5e}, sink: &colorSelectSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(test.code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{ColorSelect: test.sink})
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
