package application

import (
	"reflect"
	"testing"
)

// stepGVMTimer advances one frame and returns the slots that fire in it.
func stepGVMTimer(timer *gvmTimerBoundary) []uint8 {
	timer.advanceFrame()
	var due []uint8
	for slot := range uint8(len(timer.slots)) {
		if timer.takeDue(slot) {
			due = append(due, slot)
		}
	}
	return due
}

func TestGVMOperationalTimerModesAndDelay(t *testing.T) {
	timer := new(gvmTimerBoundary)
	if err := timer.RequestGVMTimerMode(2, 200, 1); err != nil {
		t.Fatal(err)
	}
	for frame := 1; frame < 12; frame++ {
		if due := stepGVMTimer(timer); due != nil {
			t.Fatalf("frame %d fired %v early", frame, due)
		}
	}
	if due := stepGVMTimer(timer); !reflect.DeepEqual(due, []uint8{2}) {
		t.Fatalf("frame 12: %v", due)
	}
	if err := timer.RequestGVMTimerMode(1, 100, 0); err != nil {
		t.Fatal(err)
	}
	for frame := 13; frame < 18; frame++ {
		if due := stepGVMTimer(timer); due != nil {
			t.Fatalf("frame %d fired %v early", frame, due)
		}
	}
	if due := stepGVMTimer(timer); !reflect.DeepEqual(due, []uint8{1}) {
		t.Fatalf("frame 18: %v", due)
	}
	for frame := 19; frame < 24; frame++ {
		if due := stepGVMTimer(timer); due != nil {
			t.Fatalf("frame %d fired %v early", frame, due)
		}
	}
	if due := stepGVMTimer(timer); !reflect.DeepEqual(due, []uint8{2}) {
		t.Fatalf("frame 24: %v", due)
	}
	if err := timer.CancelGVMTimerMode(2); err != nil {
		t.Fatal(err)
	}
	for frame := 25; frame <= 36; frame++ {
		if due := stepGVMTimer(timer); due != nil {
			t.Fatalf("frame %d fired cancelled %v", frame, due)
		}
	}
}

// Separate WM_TIMER messages reach the guest one after another; a frame in
// which two slots are due runs both, in slot order.
func TestGVMTimerSlotsDueTogetherAllFire(t *testing.T) {
	timer := new(gvmTimerBoundary)
	for slot := range uint8(3) {
		if err := timer.RequestGVMTimerMode(slot, 50, 1); err != nil {
			t.Fatal(err)
		}
	}
	for frame := 1; frame < 3; frame++ {
		if due := stepGVMTimer(timer); due != nil {
			t.Fatalf("frame %d fired %v early", frame, due)
		}
	}
	if due := stepGVMTimer(timer); !reflect.DeepEqual(due, []uint8{0, 1, 2}) {
		t.Fatalf("frame 3: %v", due)
	}
}
