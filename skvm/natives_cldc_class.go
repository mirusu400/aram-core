package skvm

import (
	"context"
	"fmt"
	"strings"
)

func (vm *VM) classObjectName(reference uint32) (string, error) {
	object, ok := vm.Object(reference)
	if !ok || object.Class != "java/lang/Class" {
		return "", fmt.Errorf("invalid Class receiver")
	}
	name, ok := object.Native.(string)
	if !ok || name == "" {
		return "", fmt.Errorf("invalid Class state")
	}
	return name, nil
}

func (vm *VM) classIsInterface(name string) bool {
	if runtime := vm.classes[name]; runtime != nil {
		return runtime.class.AccessFlags&AccessInterface != 0
	}
	return isHostInterface(name)
}

func (vm *VM) installCLDCClassNatives() {
	vm.RegisterNative("java/lang/Class", "getName", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		name, err := vm.classObjectName(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(strings.ReplaceAll(name, "/", "."))), true, nil
	})
	vm.RegisterNative("java/lang/Class", "isArray", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		name, err := vm.classObjectName(receiver)
		return boolValue(strings.HasPrefix(name, "[")), err == nil, err
	})
	vm.RegisterNative("java/lang/Class", "isInterface", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		name, err := vm.classObjectName(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return boolValue(vm.classIsInterface(name)), true, nil
	})
	vm.RegisterNative("java/lang/Class", "isInstance", "(Ljava/lang/Object;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		name, err := vm.classObjectName(receiver)
		if err != nil {
			return Value{}, false, err
		}
		object, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return boolValue(vm.IsInstance(object, name)), true, nil
	})
	vm.RegisterNative("java/lang/Class", "isAssignableFrom", "(Ljava/lang/Class;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		target, err := vm.classObjectName(receiver)
		if err != nil {
			return Value{}, false, err
		}
		other, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if other == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "null Class")
		}
		actual, err := vm.classObjectName(other)
		if err != nil {
			return Value{}, false, err
		}
		return boolValue(vm.classAssignable(actual, target)), true, nil
	})
	vm.RegisterNative("java/lang/Class", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		name, err := vm.classObjectName(receiver)
		if err != nil {
			return Value{}, false, err
		}
		prefix := "class "
		if vm.classIsInterface(name) {
			prefix = "interface "
		}
		return ReferenceValue(vm.NewString(prefix + strings.ReplaceAll(name, "/", "."))), true, nil
	})
	vm.RegisterNative("java/lang/Class", "newInstance", "()Ljava/lang/Object;", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		name, err := vm.classObjectName(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if strings.HasPrefix(name, "[") || vm.classIsInterface(name) {
			return Value{}, false, vm.newThrowable("java/lang/InstantiationException", name)
		}
		reference, err := vm.allocateObject(name)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/lang/InstantiationException", name)
		}
		if native, ok := vm.natives[nativeKey{name, "<init>", "()V"}]; ok {
			if _, _, err = native(ctx, vm, reference, nil); err != nil {
				return Value{}, false, err
			}
		} else if runtime := vm.classes[name]; runtime != nil {
			constructor, ok := runtime.class.Method("<init>", "()V")
			if !ok || constructor.AccessFlags&AccessPublic == 0 {
				return Value{}, false, vm.newThrowable("java/lang/InstantiationException", name)
			}
			budget := vm.remainingBudget()
			if _, _, err = vm.execute(ctx, runtime.class, constructor, reference, nil, &budget); err != nil {
				return Value{}, false, err
			}
		}
		return ReferenceValue(reference), true, nil
	})
}
