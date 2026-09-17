package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type displayPresentSink struct {
	calls int
	err   error
}

func (s *displayPresentSink) PresentGVMDisplay() error {
	s.calls++
	return s.err
}

func TestDisplayPresentService(t *testing.T) {
	sink := new(displayPresentSink)
	v, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 7, 0x78, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{DisplayPresent: sink},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(2); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	if sink.calls != 1 || v.PC() != 3 || len(v.Stack()) != 1 || v.Stack()[0] != 7 {
		t.Fatalf("calls=%d pc=%d stack=%v", sink.calls, v.PC(), v.Stack())
	}
}

func TestDisplayPresentFailureBoundaries(t *testing.T) {
	providerErr := errors.New("present failed")
	for _, test := range []struct {
		name string
		sink gvm.DisplayPresentSink
		err  error
	}{
		{name: "unavailable", err: gvm.ErrDisplayPresentUnavailable},
		{name: "provider", sink: &displayPresentSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(
				[]byte{0x78}, 0, gvm.AddressSpace{},
				&gvm.ServiceConfig{DisplayPresent: test.sink},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = v.Step()
			if !errors.Is(err, test.err) || v.PC() != 1 {
				t.Fatalf("err=%v pc=%d", err, v.PC())
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("presentation failure was not sticky")
			}
		})
	}
}

func TestDisplayPresentRequiresExplicitServiceConstructor(t *testing.T) {
	v := gvm.New([]byte{0x78})
	err := v.Step()
	var unsupported *gvm.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) || unsupported.Opcode != 0x78 || unsupported.Offset != 0 {
		t.Fatalf("legacy constructor accepted presentation: %v", err)
	}
}
