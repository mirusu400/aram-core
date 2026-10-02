package skvm

import (
	"context"
	"errors"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (vm *VM) registerVoidBrowserNative(class, name string) {
	vm.RegisterNative(class, name, "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		target, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		_, err = vm.services.Device.Request(vm.serviceOwner, shared.RequestBrowser, target, nil, vm.services.Clock.Monotonic())
		if err != nil {
			// A void handset call cannot report that a browser request was refused.
			if errors.Is(err, shared.ErrInvalidArgument) || errors.Is(err, shared.ErrLimitExceeded) {
				return Value{}, false, nil
			}
			return Value{}, false, err
		}
		return Value{}, false, nil
	})
}
