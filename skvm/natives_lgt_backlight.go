package skvm

import (
	"context"
	"time"
)

// The archived MMPP BackLight contract defines milliseconds and zero as an
// indefinite request. Color capabilities are not modeled by this adapter.
func (vm *VM) installLGTBacklightNatives() {
	vm.RegisterNative("mmpp/media/BackLight", "on", "(I)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			milliseconds, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.services.Device.SetBacklight(true,
				time.Duration(milliseconds)*time.Millisecond, vm.services.Clock.Monotonic())
		})
	vm.RegisterNative("mmpp/media/BackLight", "off", "()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.services.Device.SetBacklight(false, 0, vm.services.Clock.Monotonic())
		})
}
