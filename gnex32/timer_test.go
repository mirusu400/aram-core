package gnex32

import (
	"errors"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32TimerRoundsMillisecondsToFirst60HzFrame(t *testing.T) {
	runtime := newGNEX32TimerTestRuntime(t, []byte{0x86, 0})
	defer runtime.Close()
	if _, err := runtime.service(0x95, []uint32{17, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if !runtime.timers[0].active {
		t.Fatal("17 ms timer fired on the first 60 Hz frame")
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if runtime.timers[0].active {
		t.Fatal("17 ms one-shot timer did not fire on the second frame")
	}
	if runtime.tick != 2 || runtime.AudioTime() != 2*(time.Second/60) {
		t.Fatalf("timer timeline = tick %d time %s", runtime.tick, runtime.AudioTime())
	}

	if _, err := runtime.service(0x95, []uint32{16, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if runtime.timers[0].active {
		t.Fatal("16 ms timer did not fire on the first available frame")
	}
}

func TestGNEX32TimersUseSlotOrderAndRepeatCatchUp(t *testing.T) {
	runtime := newGNEX32TimerTestRuntime(t, []byte{0x86, 0})
	defer runtime.Close()
	for service := uint16(0x95); service <= 0x97; service++ {
		if _, err := runtime.service(service, []uint32{1, 0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if value, err := runtime.vm.memory.ReadWord(0, 0); err != nil || value != 2 {
		t.Fatalf("last same-frame timer swData = %d, err=%v; want slot 2", value, err)
	}
	for slot, timer := range runtime.timers {
		if timer.active {
			t.Fatalf("one-shot timer %d remained active", slot)
		}
	}

	runtime.tick = 10
	runtime.timers[0] = runtimeTimer{due: 1_000, period: 17, repeat: true, active: true}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	timer := runtime.timers[0]
	if !timer.active || timer.due <= runtime.tick*1000 || timer.due != 11_200 {
		t.Fatalf("repeat catch-up = %+v at tick %d", timer, runtime.tick)
	}
}

func TestGNEX32TimerConfigurationAndCallbackFailureBoundaries(t *testing.T) {
	runtime := newGNEX32TimerTestRuntime(t, []byte{0x86, 0})
	defer runtime.Close()
	runtime.timers[0] = runtimeTimer{due: 99, period: 7, repeat: true, active: true}
	want := runtime.timers[0]
	for _, args := range [][]uint32{{60_001, 0}, {10, 2}} {
		if _, err := runtime.service(0x95, args); err == nil {
			t.Fatalf("SetTimer accepted %v", args)
		}
		if runtime.timers[0] != want {
			t.Fatalf("invalid SetTimer(%v) changed timer to %+v", args, runtime.timers[0])
		}
	}

	failing := newGNEX32TimerTestRuntime(t, []byte{0xfe, 0})
	defer failing.Close()
	for slot := range failing.timers {
		failing.timers[slot] = runtimeTimer{due: 1, period: 1, active: true}
	}
	if err := failing.StepFrame(1); err == nil {
		t.Fatal("unsupported timer callback did not fail")
	} else {
		var unsupported *UnsupportedOpcodeError
		if !errors.As(err, &unsupported) {
			t.Fatalf("timer callback error = %v", err)
		}
	}
	if failing.tick != 1 || failing.timers[0].active ||
		!failing.timers[1].active || !failing.timers[2].active {
		t.Fatalf("failure commit boundary = tick %d timers %+v", failing.tick, failing.timers)
	}
	if failing.AudioTime() != 0 {
		t.Fatalf("failed timer frame advanced shared clock to %s", failing.AudioTime())
	}
}

func newGNEX32TimerTestRuntime(t *testing.T, callback []byte) *Runtime {
	t.Helper()
	code := append([]byte{0x86, 0}, callback...)
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: code, Symbols: symbols,
	}
	image.CodePointers[2] = 162
	runtime, err := NewRuntime(image, Identity{MIN: "0111234567", UserID: "98B4970F"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}
	return runtime
}
