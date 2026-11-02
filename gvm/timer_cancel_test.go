package gvm_test

import (
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type vibrationSink struct {
	requests []int16
	canceled int
}

func (s *vibrationSink) RequestGVMVibration(seconds int16) error {
	s.requests = append(s.requests, seconds)
	return nil
}

func (s *vibrationSink) CancelGVMVibration() error {
	s.canceled++
	return nil
}

// GVM2X opcode94 arms only its vibration timer (ID 0x1003) and opcode95 kills
// it; neither touches the guest timer slots.
func TestVibrationOpcodesLeaveTimerSlotsAlone(t *testing.T) {
	timer, vibration := new(timerSink), new(vibrationSink)
	code := []byte{0x06, 0xff, 0xff, 0x94, 0x95, 0xff}
	vm, err := gvm.NewWithAddressSpaceAndServices(code, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{Timer: timer, Vibration: vibration})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || !vm.Halted() || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v halted=%v stack=%x", err, vm.Halted(), vm.Stack())
	}
	if !reflect.DeepEqual(vibration.requests, []int16{-1}) || vibration.canceled != 1 {
		t.Fatalf("vibration = %+v", vibration)
	}
	if len(timer.requests) != 0 || len(timer.cancels) != 0 {
		t.Fatalf("timer touched: %+v", timer)
	}
}

func TestVibrationOpcodesWithoutSinkOnlyPop(t *testing.T) {
	vm, err := gvm.NewWithAddressSpaceAndServices([]byte{0x05, 3, 0x94, 0x95, 0xff}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || !vm.Halted() || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v halted=%v stack=%x", err, vm.Halted(), vm.Stack())
	}
}
