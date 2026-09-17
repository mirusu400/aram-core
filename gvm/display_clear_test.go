package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type displayClearSink struct {
	calls int
	err   error
}

func (s *displayClearSink) ClearGVMDisplay() error {
	s.calls++
	return s.err
}

func TestDisplayClearService(t *testing.T) {
	providerErr := errors.New("clear failed")
	for _, test := range []struct {
		name string
		sink *displayClearSink
		err  error
	}{
		{name: "success", sink: new(displayClearSink)},
		{name: "unavailable", err: gvm.ErrDisplayClearUnavailable},
		{name: "provider-error", sink: &displayClearSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sink gvm.DisplayClearSink
			if test.sink != nil {
				sink = test.sink
			}
			v, err := gvm.NewWithAddressSpaceAndServices(
				[]byte{0x05, 7, 0x55, 0xff},
				0,
				gvm.AddressSpace{},
				&gvm.ServiceConfig{DisplayClear: sink},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Step(); err != nil {
				t.Fatal(err)
			}
			beforeStack := v.Stack()
			err = v.Step()
			if test.name == "success" {
				if err != nil || test.sink.calls != 1 || v.PC() != 3 || !reflect.DeepEqual(v.Stack(), beforeStack) {
					t.Fatalf("success: err=%v calls=%d pc=%d stack=%v", err, test.sink.calls, v.PC(), v.Stack())
				}
				if err := v.Step(); err != nil || !v.Halted() {
					t.Fatalf("halt: %v", err)
				}
				return
			}
			if !errors.Is(err, test.err) || v.PC() != 3 || !reflect.DeepEqual(v.Stack(), beforeStack) {
				t.Fatalf("failure: err=%v pc=%d stack=%v", err, v.PC(), v.Stack())
			}
			if test.sink != nil && test.sink.calls != 1 {
				t.Fatalf("calls=%d", test.sink.calls)
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("display clear failure was not sticky")
			}
		})
	}
}

func TestDisplayClearRequiresExplicitServiceConstructor(t *testing.T) {
	constructors := map[string]func() (*gvm.VM, error){
		"new":     func() (*gvm.VM, error) { return gvm.New([]byte{0x55}), nil },
		"new-at":  func() (*gvm.VM, error) { return gvm.NewAt([]byte{0x55}, 0) },
		"symbols": func() (*gvm.VM, error) { return gvm.NewWithSymbols([]byte{0x55}, 0, nil) },
		"address": func() (*gvm.VM, error) { return gvm.NewWithAddressSpace([]byte{0x55}, 0, gvm.AddressSpace{}) },
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			v, err := construct()
			if err != nil {
				t.Fatal(err)
			}
			err = v.Step()
			var unsupported *gvm.UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 0x55 || unsupported.Offset != 0 {
				t.Fatalf("legacy constructor accepted display clear: %v", err)
			}
		})
	}
}
