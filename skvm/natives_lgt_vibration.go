package skvm

import (
	"context"
	"time"
)

const lgtVibrationClass = "mmpp/media/Vibration"

func (vm *VM) installLGTVibrationNatives() {
	vm.RegisterHostClass(lgtVibrationClass, "java/lang/Object")
	vm.RegisterNative(lgtVibrationClass, "getLevelNum", "()I", func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
		return IntValue(1), true, nil
	})
	vm.RegisterNative(lgtVibrationClass, "start", "(II)V", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		level, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		millis, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if level < 0 || level > 1 || millis < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid vibration level or duration")
		}
		intensity := uint8(0)
		if level == 1 {
			intensity = 100
		}
		return Value{}, false, vm.services.Device.Vibrate(intensity, time.Duration(millis)*time.Millisecond, vm.services.Clock.Monotonic())
	})
	vm.RegisterNative(lgtVibrationClass, "stop", "()V", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, vm.services.Device.Vibrate(0, 0, vm.services.Clock.Monotonic())
	})
}
