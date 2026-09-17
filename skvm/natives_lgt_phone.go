package skvm

import "context"

const lgtPhoneClass = "mmpp/phone/Phone"

func (vm *VM) installLGTPhoneNatives() {
	vm.RegisterHostClass(lgtPhoneClass, "java/lang/Object")
	vm.RegisterNative(
		lgtPhoneClass,
		"getProperty",
		"(Ljava/lang/String;)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if name != "MIN" {
				return ReferenceValue(0), true, nil
			}
			value := vm.services.Device.Config().PhoneNumber
			if value == "" {
				// Deterministic, non-subscriber handset identity for offline titles.
				value = "01000000000"
			}
			return ReferenceValue(vm.NewString(value)), true, nil
		},
	)
}
