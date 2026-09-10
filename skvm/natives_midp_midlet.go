package skvm

import (
	"context"
	"errors"
	"net/url"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	midletLifecycleField = "\x00aram-midlet-lifecycle"
	midletResumeField    = "\x00aram-midlet-resume-requested"
)

func (vm *VM) installMIDletLifecycleExtras() {
	vm.RegisterNative("javax/microedition/midlet/MIDlet", "notifyDestroyed", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if ok {
			object.Fields[midletLifecycleField] = IntValue(2)
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/midlet/MIDlet", "notifyPaused", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if ok {
			object.Fields[midletLifecycleField] = IntValue(1)
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/midlet/MIDlet", "resumeRequest", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if ok {
			object.Fields[midletResumeField] = IntValue(1)
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/midlet/MIDlet", "platformRequest", "(Ljava/lang/String;)Z", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		target, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		parsed, parseErr := url.Parse(target)
		if parseErr != nil || strings.TrimSpace(parsed.Scheme) == "" {
			return Value{}, false, vm.newThrowable("javax/microedition/io/ConnectionNotFoundException", "unsupported platform URL")
		}
		_, err = vm.services.Device.Request(vm.serviceOwner, shared.RequestBrowser, target, nil, vm.services.Clock.Monotonic())
		if err != nil {
			if errors.Is(err, shared.ErrInvalidArgument) || errors.Is(err, shared.ErrLimitExceeded) {
				return Value{}, false, vm.newThrowable("javax/microedition/io/ConnectionNotFoundException", err.Error())
			}
			return Value{}, false, err
		}
		return IntValue(0), true, nil
	})
	vm.RegisterNative("javax/microedition/midlet/MIDlet", "checkPermission", "(Ljava/lang/String;)I", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		permission, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if strings.TrimSpace(permission) == "" {
			return IntValue(-1), true, nil
		}
		return IntValue(1), true, nil
	})
	vm.RegisterNative("javax/microedition/midlet/MIDletStateChangeException", "<init>", "()V", nativeVoid)
	vm.RegisterNative("javax/microedition/midlet/MIDletStateChangeException", "<init>", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		message, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, message)
	})
}
