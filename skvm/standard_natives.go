package skvm

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

type vectorState struct {
	values []uint32
}

type integerState struct {
	value int32
}

type dateState struct {
	millis int64
}

type hashtableState struct {
	values map[string]uint32
	keys   map[string]uint32
}

type timerObjectState struct {
	timers []shared.ServiceID
}

type timerTaskState struct {
	timer     shared.ServiceID
	cancelled bool
}

func (vm *VM) installExtendedCoreNatives() {
	vm.installObjectNatives()
	vm.installStringNatives()
	vm.installNumberNatives()
	vm.installRuntimeNatives()
	vm.installVectorNatives()
	vm.installHashtableNatives()
	vm.installExceptionNatives()
	vm.installKWISNatives()
	vm.installExtendedStringBufferNatives()
	vm.installTimeNatives()
	vm.installTimerNatives()
}

func (vm *VM) installObjectNatives() {
	for _, method := range []struct {
		name       string
		descriptor string
	}{
		{"notify", "()V"},
		{"notifyAll", "()V"},
		{"wait", "()V"},
		{"wait", "(J)V"},
	} {
		vm.RegisterNative(
			"java/lang/Object",
			method.name,
			method.descriptor,
			nativeVoid,
		)
	}
	vm.RegisterNative(
		"java/lang/Object",
		"equals",
		"(Ljava/lang/Object;)Z",
		func(_ context.Context, _ *VM, receiver uint32, args []Value) (Value, bool, error) {
			other, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return boolValue(receiver == other), true, nil
		},
	)
}

func (vm *VM) installExtendedStringBufferNatives() {
	vm.RegisterNative("java/lang/StringBuffer", "<init>", "(I)V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &stringBufferState{})
	})
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"<init>",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &stringBufferState{value: value})
		},
	)
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"insert",
		"(ILjava/lang/String;)Ljava/lang/StringBuffer;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			offset, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value, err := vm.stringArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			units := utf16.Encode([]rune(state.value))
			if offset < 0 || int(offset) > len(units) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			inserted := utf16.Encode([]rune(value))
			combined := make([]uint16, 0, len(units)+len(inserted))
			combined = append(combined, units[:offset]...)
			combined = append(combined, inserted...)
			combined = append(combined, units[offset:]...)
			state.value = string(utf16.Decode(combined))
			return ReferenceValue(receiver), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"setLength",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			length, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if length < 0 {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			units := utf16.Encode([]rune(state.value))
			if int(length) < len(units) {
				units = units[:length]
			} else {
				units = append(units, make([]uint16, int(length)-len(units))...)
			}
			state.value = string(utf16.Decode(units))
			return Value{}, false, nil
		},
	)
	for _, method := range []struct {
		descriptor string
		format     func(Value, *VM) (string, error)
	}{
		{
			"(C)Ljava/lang/StringBuffer;",
			func(value Value, _ *VM) (string, error) {
				integer, err := value.Int()
				return string(rune(integer)), err
			},
		},
		{
			"(J)Ljava/lang/StringBuffer;",
			func(value Value, _ *VM) (string, error) {
				long, err := value.Long()
				return strconv.FormatInt(long, 10), err
			},
		},
		{
			"(Ljava/lang/Object;)Ljava/lang/StringBuffer;",
			func(value Value, vm *VM) (string, error) {
				reference, err := value.Reference()
				if err != nil {
					return "", err
				}
				return vm.objectString(reference), nil
			},
		},
	} {
		spec := method
		vm.RegisterNative(
			"java/lang/StringBuffer",
			"append",
			spec.descriptor,
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				state, err := vm.stringBuffer(receiver)
				if err != nil {
					return Value{}, false, err
				}
				value, err := spec.format(args[0], vm)
				if err != nil {
					return Value{}, false, err
				}
				state.value += value
				return ReferenceValue(receiver), true, nil
			},
		)
	}
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"length",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(utf16.Encode([]rune(state.value))))), true, nil
		},
	)
}

func (vm *VM) installStringNatives() {
	for _, constructor := range []struct {
		descriptor string
		native     NativeFunc
	}{
		{
			"(Ljava/lang/String;)V",
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				value, err := vm.stringArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.setNative(receiver, value)
			},
		},
		{
			"(Ljava/lang/StringBuffer;)V",
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				reference, err := referenceArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				buffer, err := vm.stringBuffer(reference)
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.setNative(receiver, buffer.value)
			},
		},
		{
			"([C)V",
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				value, err := vm.charArrayArgument(args, 0, 0, -1)
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.setNative(receiver, value)
			},
		},
		{
			"([CII)V",
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				offset, err := intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
				length, err := intArgument(args, 2)
				if err != nil {
					return Value{}, false, err
				}
				value, err := vm.charArrayArgument(args, 0, offset, length)
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.setNative(receiver, value)
			},
		},
		{
			"([BLjava/lang/String;)V",
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				reference, err := referenceArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				data, err := vm.ByteArray(reference)
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.setNative(receiver, string(data))
			},
		},
	} {
		vm.RegisterNative(
			"java/lang/String",
			"<init>",
			constructor.descriptor,
			constructor.native,
		)
	}

	stringResult := func(
		descriptor string,
		transform func(string, []Value, *VM) (string, error),
	) {
		vm.RegisterNative(
			"java/lang/String",
			strings.Split(descriptor, "\x00")[0],
			strings.Split(descriptor, "\x00")[1],
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				value, err := vm.String(receiver)
				if err != nil {
					return Value{}, false, err
				}
				result, err := transform(value, args, vm)
				if err != nil {
					return Value{}, false, err
				}
				return ReferenceValue(vm.NewString(result)), true, nil
			},
		)
	}
	stringResult("trim\x00()Ljava/lang/String;", func(value string, _ []Value, _ *VM) (string, error) {
		return strings.TrimSpace(value), nil
	})
	stringResult("toLowerCase\x00()Ljava/lang/String;", func(value string, _ []Value, _ *VM) (string, error) {
		return strings.ToLower(value), nil
	})
	stringResult("toUpperCase\x00()Ljava/lang/String;", func(value string, _ []Value, _ *VM) (string, error) {
		return strings.ToUpper(value), nil
	})
	stringResult("concat\x00(Ljava/lang/String;)Ljava/lang/String;", func(
		value string,
		args []Value,
		vm *VM,
	) (string, error) {
		suffix, err := vm.stringArgument(args, 0)
		return value + suffix, err
	})
	stringResult("substring\x00(I)Ljava/lang/String;", func(
		value string,
		args []Value,
		_ *VM,
	) (string, error) {
		start, err := intArgument(args, 0)
		if err != nil {
			return "", err
		}
		units := utf16.Encode([]rune(value))
		if start < 0 || int(start) > len(units) {
			return "", fmt.Errorf("substring index is out of range")
		}
		return string(utf16.Decode(units[start:])), nil
	})
	stringResult("replace\x00(CC)Ljava/lang/String;", func(
		value string,
		args []Value,
		_ *VM,
	) (string, error) {
		oldValue, err := intArgument(args, 0)
		if err != nil {
			return "", err
		}
		newValue, err := intArgument(args, 1)
		if err != nil {
			return "", err
		}
		return strings.ReplaceAll(value, string(rune(oldValue)), string(rune(newValue))), nil
	})

	vm.RegisterNative(
		"java/lang/String",
		"equals",
		"(Ljava/lang/Object;)Z",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			left, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			rightReference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			right, err := vm.String(rightReference)
			if err != nil {
				return IntValue(0), true, nil
			}
			return boolValue(left == right), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"compareTo",
		"(Ljava/lang/String;)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			left, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			right, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			comparison := strings.Compare(left, right)
			return IntValue(int32(comparison)), true, nil
		},
	)
	for _, method := range []struct {
		name string
		last bool
	}{
		{"startsWith", false},
		{"endsWith", true},
	} {
		name, last := method.name, method.last
		vm.RegisterNative(
			"java/lang/String",
			name,
			"(Ljava/lang/String;)Z",
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				value, err := vm.String(receiver)
				if err != nil {
					return Value{}, false, err
				}
				part, err := vm.stringArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				matches := strings.HasPrefix(value, part)
				if last {
					matches = strings.HasSuffix(value, part)
				}
				return boolValue(matches), true, nil
			},
		)
	}
	vm.RegisterNative(
		"java/lang/String",
		"hashCode",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			var hash int32
			for _, unit := range utf16.Encode([]rune(value)) {
				hash = 31*hash + int32(unit)
			}
			return IntValue(hash), true, nil
		},
	)
	for _, method := range []struct {
		descriptor   string
		stringNeedle bool
		hasStart     bool
	}{
		{"(I)I", false, false},
		{"(II)I", false, true},
		{"(Ljava/lang/String;)I", true, false},
		{"(Ljava/lang/String;I)I", true, true},
	} {
		spec := method
		vm.RegisterNative(
			"java/lang/String",
			"indexOf",
			spec.descriptor,
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				value, err := vm.String(receiver)
				if err != nil {
					return Value{}, false, err
				}
				needle := ""
				if spec.stringNeedle {
					needle, err = vm.stringArgument(args, 0)
				} else {
					character, intErr := intArgument(args, 0)
					err = intErr
					needle = string(rune(character))
				}
				if err != nil {
					return Value{}, false, err
				}
				start := int32(0)
				if spec.hasStart {
					start, err = intArgument(args, 1)
					if err != nil {
						return Value{}, false, err
					}
				}
				units := utf16.Encode([]rune(value))
				if start < 0 {
					start = 0
				}
				if int(start) > len(units) {
					return IntValue(-1), true, nil
				}
				index := strings.Index(string(utf16.Decode(units[start:])), needle)
				if index < 0 {
					return IntValue(-1), true, nil
				}
				prefixUnits := utf16.Encode([]rune(string(utf16.Decode(units[start:]))[:index]))
				return IntValue(start + int32(len(prefixUnits))), true, nil
			},
		)
	}
	vm.RegisterNative(
		"java/lang/String",
		"getBytes",
		"()[B",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewByteArray([]byte(value))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"getBytes",
		"(Ljava/lang/String;)[B",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewByteArray([]byte(value))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"toCharArray",
		"()[C",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.newCharArray(utf16.Encode([]rune(value)))), true, nil
		},
	)
	for _, method := range []struct {
		descriptor string
		native     NativeFunc
	}{
		{
			"(I)Ljava/lang/String;",
			func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
				value, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				return ReferenceValue(vm.NewString(strconv.FormatInt(int64(value), 10))), true, nil
			},
		},
		{
			"(C)Ljava/lang/String;",
			func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
				value, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				return ReferenceValue(vm.NewString(string(rune(value)))), true, nil
			},
		},
		{
			"(Ljava/lang/Object;)Ljava/lang/String;",
			func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
				reference, err := referenceArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				return ReferenceValue(vm.NewString(vm.objectString(reference))), true, nil
			},
		},
	} {
		vm.RegisterNative("java/lang/String", "valueOf", method.descriptor, method.native)
	}
}

func (vm *VM) installNumberNatives() {
	parseInt := func(radixArgument bool) NativeFunc {
		return func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			radix := int32(10)
			if radixArgument {
				radix, err = intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
			}
			parsed, err := strconv.ParseInt(value, int(radix), 32)
			if err != nil {
				return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", err.Error())
			}
			return IntValue(int32(parsed)), true, nil
		}
	}
	vm.RegisterNative("java/lang/Integer", "parseInt", "(Ljava/lang/String;)I", parseInt(false))
	vm.RegisterNative("java/lang/Integer", "parseInt", "(Ljava/lang/String;I)I", parseInt(true))
	vm.RegisterNative(
		"java/lang/Integer",
		"<init>",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &integerState{value: value})
		},
	)
	vm.RegisterNative(
		"java/lang/Integer",
		"intValue",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Integer")
			}
			state, ok := object.Native.(*integerState)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Integer state")
			}
			return IntValue(state.value), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Integer",
		"toString",
		"(I)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewString(strconv.FormatInt(int64(value), 10))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Integer",
		"toHexString",
		"(I)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewString(strconv.FormatUint(uint64(uint32(value)), 16))), true, nil
		},
	)
}

func (vm *VM) installRuntimeNatives() {
	runtime := vm.NewObject("java/lang/Runtime", nil)
	vm.RegisterNative(
		"java/lang/Runtime",
		"getRuntime",
		"()Ljava/lang/Runtime;",
		func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(runtime), true, nil
		},
	)
	vm.RegisterNative("java/lang/Runtime", "gc", "()V", nativeVoid)
	for _, method := range []string{"freeMemory", "totalMemory"} {
		value := int64(32 << 20)
		if method == "totalMemory" {
			value = 64 << 20
		}
		returned := value
		vm.RegisterNative(
			"java/lang/Runtime",
			method,
			"()J",
			func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
				return LongValue(returned), true, nil
			},
		)
	}
}

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
}

func (vm *VM) installExceptionNatives() {
	for _, class := range []string{
		"java/lang/Throwable",
		"java/lang/Exception",
		"java/lang/RuntimeException",
		"java/lang/NullPointerException",
		"java/lang/IllegalArgumentException",
		"java/lang/ArrayIndexOutOfBoundsException",
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
}

func (vm *VM) installKWISNatives() {
	vm.RegisterNative(
		"org/kwis/msp/handset/BackLight",
		"alwaysOn",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.services.Device.SetBacklight(
				true,
				0,
				vm.services.Clock.Monotonic(),
			)
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/File",
		"<init>",
		"(Ljava/lang/String;I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.newXFile(args)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, state)
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/File",
		"write",
		"([B)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.ByteArray(reference)
			if err != nil {
				return Value{}, false, err
			}
			state.data = append(state.data, data...)
			state.offset = len(state.data)
			if err := vm.persistXFile(state); err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(data))), true, nil
		},
	)
	vm.RegisterNative("org/kwis/msp/io/File", "close", "()V", nativeVoid)
	vm.RegisterNative(
		"org/kwis/msp/io/File",
		"sizeOf",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(state.data))), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/File",
		"read",
		"([B)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			destination, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			object, ok := vm.Object(destination)
			if !ok || object.Array == nil {
				return Value{}, false, fmt.Errorf("File.read destination is not array")
			}
			if state.offset >= len(state.data) {
				return IntValue(-1), true, nil
			}
			count := min(len(object.Array.Elements), len(state.data)-state.offset)
			for index := range count {
				object.Array.Elements[index] =
					IntValue(int32(int8(state.data[state.offset+index])))
			}
			state.offset += count
			return IntValue(int32(count)), true, nil
		},
	)
	vm.RegisterNative("org/kwis/msp/lcdui/Jlet", "<init>", "()V", nativeVoid)
	vm.RegisterNative("org/kwis/msp/lcdui/Jlet", "notifyDestroyed", "()V", nativeVoid)
	vm.RegisterNative("org/kwis/msp/lcdui/Card", "<init>", "()V", nativeVoid)
	display := vm.NewObject("org/kwis/msp/lcdui/Display", nil)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Display",
		"getDefaultDisplay",
		"()Lorg/kwis/msp/lcdui/Display;",
		func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(display), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Display",
		"pushCard",
		"(Lorg/kwis/msp/lcdui/Card;)V",
		nativeVoid,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Card",
		"repaint",
		"(IIII)V",
		nativeVoid,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Card",
		"getWidth",
		"()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenWidth)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Card",
		"getHeight",
		"()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenHeight)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Font",
		"getDefaultFont",
		"()Lorg/kwis/msp/lcdui/Font;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(vm.NewObject(
				"org/kwis/msp/lcdui/Font",
				&fontState{font: vm.defaultFont},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/FileSystem",
		"isFile",
		"(Ljava/lang/String;)Z",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			_, err = vm.services.Storage.Stat(shared.NamespacePrivate, name)
			return boolValue(err == nil), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/FileSystem",
		"remove",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.services.Storage.Delete(
				shared.NamespacePrivate,
				name,
			)
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Font",
		"getFont",
		"(III)Lorg/kwis/msp/lcdui/Font;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			return vm.newFontObject("org/kwis/msp/lcdui/Font", args)
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Font",
		"stringWidth",
		"(Ljava/lang/String;)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			font, err := vm.font(receiver)
			if err != nil {
				return Value{}, false, err
			}
			width, err := vm.services.Text.Measure(vm.serviceOwner, font.font, value)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(width), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Image",
		"createImage",
		"(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
			data, _ := vm.resource(name)
			reference, err := vm.newImage(data)
			if err != nil {
				return Value{}, false, err
			}
			object, _ := vm.Object(reference)
			object.Class = "org/kwis/msp/lcdui/Image"
			return ReferenceValue(reference), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Image",
		"createImage",
		"(II)Lorg/kwis/msp/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			width, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			height, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			state, err := vm.newImageState(int(max(1, width)), int(max(1, height)))
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(
				vm.NewObject("org/kwis/msp/lcdui/Image", state),
			), true, nil
		},
	)
	for _, method := range []struct {
		name  string
		value func(*imageState) int32
	}{
		{"getWidth", func(state *imageState) int32 { return int32(state.width) }},
		{"getHeight", func(state *imageState) int32 { return int32(state.height) }},
	} {
		spec := method
		vm.RegisterNative(
			"org/kwis/msp/lcdui/Image",
			spec.name,
			"()I",
			func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
				state, err := vm.image(receiver)
				if err != nil {
					return Value{}, false, err
				}
				return IntValue(spec.value(state)), true, nil
			},
		)
	}
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Image",
		"getGraphics",
		"()Lorg/kwis/msp/lcdui/Graphics;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.image(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewObject(
				"org/kwis/msp/lcdui/Graphics",
				&graphicsState{
					width: state.width, height: state.height,
					surface: state.surface, font: vm.defaultFont,
					color: 0xff000000,
				},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"setColor",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			color, err := intArgument(args, 0)
			state.color = 0xff000000 | uint32(color)&0xffffff
			return Value{}, false, err
		},
	)
	for _, method := range []struct {
		name       string
		descriptor string
		native     NativeFunc
	}{
		{"fillRect", "(IIII)V", nativeFillRect},
		{"drawRect", "(IIII)V", nativeDrawRect},
		{"drawLine", "(IIII)V", nativeDrawLine},
	} {
		vm.RegisterNative(
			"org/kwis/msp/lcdui/Graphics",
			method.name,
			method.descriptor,
			method.native,
		)
	}
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
