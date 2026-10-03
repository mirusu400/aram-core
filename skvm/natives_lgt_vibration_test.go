package skvm

import (
	"context"
	"testing"
	"time"
)

func TestLGTVibrationUsesDeviceState(t *testing.T) {
	vm := mmppVM(t, NativePolicyLGT)
	level := invokeTestNative(t, vm, lgtVibrationClass, "getLevelNum", "()I", 0)
	if got, err := level.Int(); err != nil || got != 100 {
		t.Fatalf("level count = %d, %v", got, err)
	}
	for _, sample := range []struct {
		level    int32
		strength uint8
	}{{1, 1}, {3, 3}, {100, 100}} {
		invokeTestNative(t, vm, lgtVibrationClass, "start", "(II)V", 0, IntValue(sample.level), IntValue(250))
		strength, until := vm.services.Device.Vibration()
		if strength != sample.strength || until != 250*time.Millisecond {
			t.Fatalf("level %d vibration = %d until %s", sample.level, strength, until)
		}
	}
	invokeTestNative(t, vm, lgtVibrationClass, "stop", "()V", 0)
	strength, until := vm.services.Device.Vibration()
	if strength != 0 || until != 0 {
		t.Fatalf("stopped vibration = %d until %s", strength, until)
	}
	if _, _, err := vm.natives[nativeKey{lgtVibrationClass, "start", "(II)V"}](context.Background(), vm, 0, []Value{IntValue(101), IntValue(1)}); err == nil {
		t.Fatal("invalid level accepted")
	}
	for _, policy := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME} {
		other := mmppVM(t, policy)
		if other.SupportsNativeReference(lgtVibrationClass, "start", "(II)V") {
			t.Fatalf("policy %d exposed LGT vibration", policy)
		}
	}
}
