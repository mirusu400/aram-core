package skvm

import (
	"context"
	"testing"
	"time"
)

func TestLGTVibrationUsesDeviceState(t *testing.T) {
	vm := mmppVM(t, NativePolicyLGT)
	level := invokeTestNative(t, vm, lgtVibrationClass, "getLevelNum", "()I", 0)
	if got, err := level.Int(); err != nil || got != 1 {
		t.Fatalf("level count = %d, %v", got, err)
	}
	invokeTestNative(t, vm, lgtVibrationClass, "start", "(II)V", 0, IntValue(1), IntValue(250))
	strength, until := vm.services.Device.Vibration()
	if strength != 100 || until != 250*time.Millisecond {
		t.Fatalf("vibration = %d until %s", strength, until)
	}
	invokeTestNative(t, vm, lgtVibrationClass, "stop", "()V", 0)
	strength, until = vm.services.Device.Vibration()
	if strength != 0 || until != 0 {
		t.Fatalf("stopped vibration = %d until %s", strength, until)
	}
	if _, _, err := vm.natives[nativeKey{lgtVibrationClass, "start", "(II)V"}](context.Background(), vm, 0, []Value{IntValue(2), IntValue(1)}); err == nil {
		t.Fatal("invalid level accepted")
	}
	for _, policy := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME} {
		other := mmppVM(t, policy)
		if other.SupportsNativeReference(lgtVibrationClass, "start", "(II)V") {
			t.Fatalf("policy %d exposed LGT vibration", policy)
		}
	}
}
