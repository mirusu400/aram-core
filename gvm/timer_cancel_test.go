package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type cancelTimer struct{ canceled int }

func (*cancelTimer) RequestGVMTimer(int16, uint16) error { return nil }
func (t *cancelTimer) CancelGVMTimer() error {
	t.canceled++
	return nil
}

func TestCancelTimerOpcode(t *testing.T) {
	timer := new(cancelTimer)
	vm, err := gvm.NewWithAddressSpaceAndServices([]byte{0x95, 0xff}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{Timer: timer})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); err != nil || !vm.Halted() || timer.canceled != 1 || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v halted=%v cancels=%d stack=%x", err, vm.Halted(), timer.canceled, vm.Stack())
	}
}
