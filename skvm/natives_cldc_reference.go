package skvm

import "context"

const referenceValueField = "\x00aram-reference-value"

func (vm *VM) installCLDCReferenceNatives() {
	vm.RegisterNative(
		"java/lang/ref/WeakReference",
		"<init>",
		"(Ljava/lang/Object;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "invalid WeakReference")
			}
			object.Fields[referenceValueField] = ReferenceValue(reference)
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/lang/ref/Reference",
		"get",
		"()Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "invalid Reference")
			}
			value, ok := object.Fields[referenceValueField]
			if !ok {
				value = ReferenceValue(0)
			}
			return value, true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/ref/Reference",
		"clear",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "invalid Reference")
			}
			object.Fields[referenceValueField] = ReferenceValue(0)
			return Value{}, false, nil
		},
	)
}
