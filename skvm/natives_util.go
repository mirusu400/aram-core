package skvm

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (vm *VM) installVectorNatives() {
	initialize := func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &vectorState{})
	}
	vm.RegisterNative("java/util/Vector", "<init>", "()V", initialize)
	vm.RegisterNative("java/util/Vector", "<init>", "(II)V", initialize)
	vm.RegisterNative("java/util/Stack", "<init>", "()V", initialize)
	vm.RegisterNative(
		"java/util/Vector",
		"addElement",
		"(Ljava/lang/Object;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state.values = append(state.values, value)
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"contains",
		"(Ljava/lang/Object;)Z",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			for _, current := range state.values {
				if vm.referencesEqual(current, value) {
					return IntValue(1), true, nil
				}
			}
			return IntValue(0), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"removeElement",
		"(Ljava/lang/Object;)Z",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			for index, current := range state.values {
				if vm.referencesEqual(current, value) {
					copy(state.values[index:], state.values[index+1:])
					state.values = state.values[:len(state.values)-1]
					return IntValue(1), true, nil
				}
			}
			return IntValue(0), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"removeElementAt",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			index, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if index < 0 || int(index) >= len(state.values) {
				return Value{}, false, vm.newThrowable(
					"java/lang/ArrayIndexOutOfBoundsException",
					"",
				)
			}
			copy(state.values[index:], state.values[index+1:])
			state.values = state.values[:len(state.values)-1]
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"setElementAt",
		"(Ljava/lang/Object;I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			index, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			if index < 0 || int(index) >= len(state.values) {
				return Value{}, false, vm.newThrowable(
					"java/lang/ArrayIndexOutOfBoundsException",
					"",
				)
			}
			state.values[index] = value
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"size",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(state.values))), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"isEmpty",
		"()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return boolValue(len(state.values) == 0), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"elementAt",
		"(I)Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			index, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if index < 0 || int(index) >= len(state.values) {
				return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
			}
			return ReferenceValue(state.values[index]), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Vector",
		"removeAllElements",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			state.values = nil
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/util/Stack",
		"push",
		"(Ljava/lang/Object;)Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state.values = append(state.values, value)
			return ReferenceValue(value), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Stack",
		"pop",
		"()Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if len(state.values) == 0 {
				return Value{}, false, vm.newThrowable("java/util/NoSuchElementException", "")
			}
			index := len(state.values) - 1
			value := state.values[index]
			state.values = state.values[:index]
			return ReferenceValue(value), true, nil
		},
	)
}

func (vm *VM) installHashtableNatives() {
	vm.RegisterNative("java/util/Hashtable", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &hashtableState{
			values: make(map[string]uint32),
			keys:   make(map[string]uint32),
		})
	})
	vm.RegisterNative(
		"java/util/Hashtable",
		"put",
		"(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.hashtable(receiver)
			if err != nil {
				return Value{}, false, err
			}
			key, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value, err := referenceArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			identity := vm.objectKey(key)
			previous := state.values[identity]
			state.values[identity] = value
			state.keys[identity] = key
			return ReferenceValue(previous), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Hashtable",
		"get",
		"(Ljava/lang/Object;)Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.hashtable(receiver)
			if err != nil {
				return Value{}, false, err
			}
			key, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(state.values[vm.objectKey(key)]), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Hashtable",
		"remove",
		"(Ljava/lang/Object;)Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.hashtable(receiver)
			if err != nil {
				return Value{}, false, err
			}
			key, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			identity := vm.objectKey(key)
			previous := state.values[identity]
			delete(state.values, identity)
			delete(state.keys, identity)
			return ReferenceValue(previous), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Hashtable",
		"clear",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.hashtable(receiver)
			if err != nil {
				return Value{}, false, err
			}
			clear(state.values)
			clear(state.keys)
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/util/Hashtable",
		"keys",
		"()Ljava/util/Enumeration;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.hashtable(receiver)
			if err != nil {
				return Value{}, false, err
			}
			identities := make([]string, 0, len(state.keys))
			for identity := range state.keys {
				identities = append(identities, identity)
			}
			sort.Strings(identities)
			values := make([]uint32, 0, len(identities))
			for _, identity := range identities {
				values = append(values, state.keys[identity])
			}
			return ReferenceValue(vm.NewObject(
				"java/util/Enumeration",
				&vectorState{values: values},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Enumeration",
		"hasMoreElements",
		"()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return boolValue(len(state.values) != 0), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Enumeration",
		"nextElement",
		"()Ljava/lang/Object;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if len(state.values) == 0 {
				return Value{}, false, vm.newThrowable(
					"java/util/NoSuchElementException",
					"",
				)
			}
			value := state.values[0]
			state.values = state.values[1:]
			return ReferenceValue(value), true, nil
		},
	)
}

func (vm *VM) installExceptionNatives() {
	for _, class := range []string{
		"java/lang/Throwable",
		"java/lang/Exception",
		"java/lang/RuntimeException",
		"java/lang/NullPointerException",
		"java/lang/IllegalArgumentException",
		"java/lang/ArrayIndexOutOfBoundsException",
		"java/lang/NumberFormatException",
		"java/util/NoSuchElementException",
		"java/io/IOException",
	} {
		vm.RegisterNative(class, "<init>", "()V", nativeVoid)
		vm.RegisterNative(class, "<init>", "(Ljava/lang/String;)V", func(
			_ context.Context,
			vm *VM,
			receiver uint32,
			args []Value,
		) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, value)
		})
	}
	vm.RegisterNative("java/lang/Throwable", "printStackTrace", "()V", nativeVoid)
	vm.RegisterNative(
		"java/lang/Throwable",
		"toString",
		"()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Throwable reference")
			}
			value := strings.ReplaceAll(object.Class, "/", ".")
			if message, ok := object.Native.(string); ok && message != "" {
				value += ": " + message
			}
			return ReferenceValue(vm.NewString(value)), true, nil
		},
	)
}

func (vm *VM) installTimeNatives() {
	vm.RegisterNative(
		"java/util/Date",
		"<init>",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.setNative(
				receiver,
				&dateState{millis: vm.services.Clock.WallMillis()},
			)
		},
	)
	vm.RegisterNative(
		"java/util/Date",
		"<init>",
		"(J)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			millis, err := args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &dateState{millis: millis})
		},
	)
	vm.RegisterNative(
		"java/util/Date",
		"getTime",
		"()J",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.date(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return LongValue(state.millis), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Date",
		"setTime",
		"(J)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.date(receiver)
			if err != nil {
				return Value{}, false, err
			}
			state.millis, err = args[0].Long()
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"java/util/Calendar",
		"getInstance",
		"()Ljava/util/Calendar;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(
				vm.NewObject(
					"java/util/Calendar",
					&dateState{millis: vm.services.Clock.WallMillis()},
				),
			), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Calendar",
		"getInstance",
		"(Ljava/util/TimeZone;)Ljava/util/Calendar;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			if _, err := referenceArgument(args, 0); err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewObject(
				"java/util/Calendar",
				&dateState{millis: vm.services.Clock.WallMillis()},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Calendar",
		"setTimeZone",
		"(Ljava/util/TimeZone;)V",
		func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			_, err := referenceArgument(args, 0)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"java/util/TimeZone",
		"getTimeZone",
		"(Ljava/lang/String;)Ljava/util/TimeZone;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			identifier, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewObject(
				"java/util/TimeZone",
				identifier,
			)), true, nil
		},
	)
	vm.RegisterNative(
		"java/util/Calendar",
		"setTime",
		"(Ljava/util/Date;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.date(receiver)
			if err != nil {
				return Value{}, false, err
			}
			dateReference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			date, err := vm.date(dateReference)
			if err != nil {
				return Value{}, false, err
			}
			state.millis = date.millis
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/util/Calendar",
		"get",
		"(I)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.date(receiver)
			if err != nil {
				return Value{}, false, err
			}
			field, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value := time.UnixMilli(state.millis).UTC()
			var result int
			switch field {
			case 1:
				result = value.Year()
			case 2:
				result = int(value.Month()) - 1
			case 5:
				result = value.Day()
			case 11:
				result = value.Hour()
			case 12:
				result = value.Minute()
			case 13:
				result = value.Second()
			case 14:
				result = value.Nanosecond() / int(time.Millisecond)
			}
			return IntValue(int32(result)), true, nil
		},
	)
}

func (vm *VM) installTimerNatives() {
	vm.RegisterNative("java/util/Timer", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &timerObjectState{})
	})
	vm.RegisterNative("java/util/Timer", "cancel", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		state, err := vm.timerObject(receiver)
		if err != nil {
			return Value{}, false, err
		}
		for _, id := range state.timers {
			if err := vm.services.Timers.Cancel(id, vm.serviceOwner); err != nil {
				return Value{}, false, err
			}
		}
		return Value{}, false, nil
	})
	vm.RegisterNative(
		"java/util/Timer",
		"schedule",
		"(Ljava/util/TimerTask;JJ)V",
		nativeTimerSchedule,
	)
	vm.RegisterNative(
		"java/util/Timer",
		"scheduleAtFixedRate",
		"(Ljava/util/TimerTask;JJ)V",
		nativeTimerSchedule,
	)
	vm.RegisterNative("java/util/TimerTask", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &timerTaskState{})
	})
	vm.RegisterNative(
		"java/util/TimerTask",
		"cancel",
		"()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.timerTask(receiver)
			if err != nil {
				return Value{}, false, err
			}
			wasActive := false
			if state.timer != 0 {
				timer, getErr := vm.services.Timers.Get(state.timer, vm.serviceOwner)
				if getErr != nil {
					return Value{}, false, getErr
				}
				wasActive = timer.Active
				if err := vm.services.Timers.Cancel(
					state.timer,
					vm.serviceOwner,
				); err != nil {
					return Value{}, false, err
				}
			}
			state.cancelled = true
			if wasActive {
				return IntValue(1), true, nil
			}
			return IntValue(0), true, nil
		},
	)
}

func nativeTimerSchedule(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	timer, err := vm.timerObject(receiver)
	if err != nil {
		return Value{}, false, err
	}
	taskReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	task, err := vm.timerTask(taskReference)
	if err != nil {
		return Value{}, false, err
	}
	delay, err := args[1].Long()
	if err != nil {
		return Value{}, false, err
	}
	period, err := args[2].Long()
	if err != nil {
		return Value{}, false, err
	}
	maxMillis := int64(math.MaxInt64 / int64(time.Millisecond))
	if task.cancelled || task.timer != 0 || delay < 0 || period < 0 ||
		delay > maxMillis || period > maxMillis {
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"invalid timer schedule",
		)
	}
	id, err := vm.services.Timers.Define(
		vm.serviceOwner,
		fmt.Sprintf("skvm.timer.%08x", taskReference),
	)
	if err != nil {
		return Value{}, false, err
	}
	deadline := vm.services.Clock.Monotonic() + time.Duration(delay)*time.Millisecond
	if deadline < vm.services.Clock.Monotonic() {
		_ = vm.services.Timers.Destroy(id, vm.serviceOwner, vm.services.Events)
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"timer deadline overflow",
		)
	}
	if err := vm.services.Timers.Set(
		id,
		vm.serviceOwner,
		deadline,
		time.Duration(period)*time.Millisecond,
		int64(taskReference),
	); err != nil {
		_ = vm.services.Timers.Destroy(id, vm.serviceOwner, vm.services.Events)
		return Value{}, false, err
	}
	task.timer = id
	timer.timers = append(timer.timers, id)
	return Value{}, false, nil
}

func (vm *VM) vector(reference uint32) (*vectorState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Vector reference")
	}
	state, ok := object.Native.(*vectorState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a Vector", reference)
	}
	return state, nil
}

func (vm *VM) date(reference uint32) (*dateState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Date reference")
	}
	state, ok := object.Native.(*dateState)
	if !ok {
		return nil, fmt.Errorf("object %d has no date state", reference)
	}
	return state, nil
}

func (vm *VM) timerObject(reference uint32) (*timerObjectState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Timer reference")
	}
	state, ok := object.Native.(*timerObjectState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a Timer", reference)
	}
	return state, nil
}

func (vm *VM) timerTask(reference uint32) (*timerTaskState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid TimerTask reference")
	}
	state, ok := object.Native.(*timerTaskState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a TimerTask", reference)
	}
	return state, nil
}

func (vm *VM) hashtable(reference uint32) (*hashtableState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Hashtable reference")
	}
	state, ok := object.Native.(*hashtableState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a Hashtable", reference)
	}
	return state, nil
}

func (vm *VM) objectKey(reference uint32) string {
	if value, err := vm.String(reference); err == nil {
		return "string:" + value
	}
	return fmt.Sprintf("ref:%d", reference)
}

func (vm *VM) referencesEqual(left, right uint32) bool {
	if left == right {
		return true
	}
	if left == 0 || right == 0 {
		return false
	}
	leftString, leftErr := vm.String(left)
	rightString, rightErr := vm.String(right)
	return leftErr == nil && rightErr == nil && leftString == rightString
}

func (vm *VM) charArrayArgument(
	args []Value,
	index int,
	offset int32,
	length int32,
) (string, error) {
	reference, err := referenceArgument(args, index)
	if err != nil {
		return "", err
	}
	object, ok := vm.Object(reference)
	if !ok || object.Array == nil || object.Array.Descriptor != "[C" {
		return "", fmt.Errorf("object %d is not char[]", reference)
	}
	if length < 0 {
		length = int32(len(object.Array.Elements)) - offset
	}
	if offset < 0 || length < 0 ||
		int64(offset)+int64(length) > int64(len(object.Array.Elements)) {
		return "", vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	units := make([]uint16, length)
	for index := range units {
		value, intErr := object.Array.Elements[int(offset)+index].Int()
		if intErr != nil {
			return "", intErr
		}
		units[index] = uint16(value)
	}
	return string(utf16.Decode(units)), nil
}

func (vm *VM) newCharArray(units []uint16) uint32 {
	elements := make([]Value, len(units))
	for index, unit := range units {
		elements[index] = IntValue(int32(unit))
	}
	return vm.newArray("[C", elements)
}

func (vm *VM) objectString(reference uint32) string {
	if reference == 0 {
		return "null"
	}
	object, ok := vm.Object(reference)
	if !ok {
		return "null"
	}
	if value, ok := object.Native.(string); ok {
		return value
	}
	if value, ok := object.Native.(*integerState); ok {
		return strconv.FormatInt(int64(value.value), 10)
	}
	return fmt.Sprintf("%s@%x", strings.ReplaceAll(object.Class, "/", "."), reference)
}

func boolValue(value bool) Value {
	if value {
		return IntValue(1)
	}
	return IntValue(0)
}

func (vm *VM) textEncoding(name string) (shared.TextEncoding, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case "euc-kr", "euckr", "ksc5601", "ks-c-5601-1987", "ms949", "cp949":
		return shared.EncodingEUCKR, nil
	case "utf-8", "utf8":
		return shared.EncodingUTF8, nil
	case "utf-16", "utf-16be", "utf16", "utf16be":
		return shared.EncodingUTF16BE, nil
	case "utf-16le", "utf16le":
		return shared.EncodingUTF16LE, nil
	default:
		return "", vm.newThrowable(
			"java/io/UnsupportedEncodingException",
			name,
		)
	}
}
