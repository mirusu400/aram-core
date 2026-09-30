package application

import "testing"

func TestGVMOperationalTimerModesAndDelay(t *testing.T) {
	timer := new(gvmTimerBoundary)
	if err := timer.RequestGVMTimerMode(2, 200, 1); err != nil {
		t.Fatal(err)
	}
	for frame := 1; frame < 12; frame++ {
		if mode, due := timer.advanceFrame(); due {
			t.Fatalf("frame %d fired mode %d early", frame, mode)
		}
	}
	if mode, due := timer.advanceFrame(); !due || mode != 2 {
		t.Fatalf("frame 12: mode=%d due=%v", mode, due)
	}
	if err := timer.RequestGVMTimerMode(1, 100, 0); err != nil {
		t.Fatal(err)
	}
	for frame := 13; frame < 18; frame++ {
		if mode, due := timer.advanceFrame(); due {
			t.Fatalf("frame %d fired mode %d early", frame, mode)
		}
	}
	if mode, due := timer.advanceFrame(); !due || mode != 1 {
		t.Fatalf("frame 18: mode=%d due=%v", mode, due)
	}
	for frame := 19; frame < 24; frame++ {
		if mode, due := timer.advanceFrame(); due {
			t.Fatalf("frame %d fired mode %d early", frame, mode)
		}
	}
	if mode, due := timer.advanceFrame(); !due || mode != 2 {
		t.Fatalf("frame 24: mode=%d due=%v", mode, due)
	}
	if err := timer.CancelGVMTimerMode(2); err != nil {
		t.Fatal(err)
	}
	for frame := 25; frame <= 36; frame++ {
		if mode, due := timer.advanceFrame(); due {
			t.Fatalf("frame %d fired cancelled mode %d", frame, mode)
		}
	}
}
