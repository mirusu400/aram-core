package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type mappingSelectSink struct {
	calls    int
	selector uint8
	err      error
}

func (s *mappingSelectSink) SelectGVMMapping(selector uint8) error {
	s.calls++
	s.selector = selector
	return s.err
}

func TestMappingSelectionService(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  uint16
		want uint8
	}{
		{name: "negative", raw: 0x8000, want: 0},
		{name: "zero", raw: 0, want: 0},
		{name: "inside", raw: 3, want: 3},
		{name: "upper", raw: 6, want: 6},
		{name: "above", raw: 7, want: 6},
		{name: "maximum", raw: 0x7fff, want: 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := new(mappingSelectSink)
			v, err := gvm.NewWithAddressSpaceAndServices(
				[]byte{0x06, byte(test.raw >> 8), byte(test.raw), 0x59, 0xff},
				0,
				gvm.AddressSpace{},
				&gvm.ServiceConfig{MappingSelect: sink},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Run(2); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			if sink.calls != 1 || sink.selector != test.want || len(v.Stack()) != 0 || v.PC() != 4 {
				t.Fatalf("calls=%d selector=%d stack=%v pc=%d", sink.calls, sink.selector, v.Stack(), v.PC())
			}
		})
	}
}

func TestMappingSelectionFailureIsTransactional(t *testing.T) {
	providerErr := errors.New("mapping failed")
	for _, test := range []struct {
		name string
		sink gvm.MappingSelectSink
		err  error
	}{
		{name: "unavailable", err: gvm.ErrMappingSelectUnavailable},
		{name: "provider", sink: &mappingSelectSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(
				[]byte{0x05, 3, 0x59}, 0, gvm.AddressSpace{},
				&gvm.ServiceConfig{MappingSelect: test.sink},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Step(); err != nil {
				t.Fatal(err)
			}
			before := v.Stack()
			err = v.Step()
			if !errors.Is(err, test.err) || !reflect.DeepEqual(v.Stack(), before) || v.PC() != 3 {
				t.Fatalf("err=%v stack=%v pc=%d", err, v.Stack(), v.PC())
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("mapping failure was not sticky")
			}
		})
	}
}

func TestMappingSelectionRequiresExplicitServiceConstructor(t *testing.T) {
	v := gvm.New([]byte{0x59})
	err := v.Step()
	var unsupported *gvm.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) || unsupported.Opcode != 0x59 || unsupported.Offset != 0 {
		t.Fatalf("legacy constructor accepted mapping selection: %v", err)
	}
}
