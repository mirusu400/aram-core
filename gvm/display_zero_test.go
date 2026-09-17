package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type zeroDisplay struct{ calls int }

func (d *zeroDisplay) ZeroGVMDisplay() error { d.calls++; return nil }

func TestDisplayZeroService(t *testing.T) {
	display := new(zeroDisplay)
	vm, err := gvm.NewWithAddressSpaceAndServices([]byte{0x56, 0xff}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{DisplayZero: display})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); !errors.Is(err, gvm.ErrBudget) || display.calls != 1 || vm.PC() != 1 || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v calls=%d pc=%d stack=%x", err, display.calls, vm.PC(), vm.Stack())
	}
}
