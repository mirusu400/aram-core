package skvm

import (
	"context"
	"fmt"
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
		"wait",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			if vm.runningThread != 0 {
				return Value{}, false, &threadYield{delay: time.Nanosecond}
			}
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Object",
		"wait",
		"(J)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			milliseconds, err := args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
			if milliseconds < 0 ||
				milliseconds > int64((^uint64(0)>>1)/uint64(time.Millisecond)) {
				return Value{}, false, vm.newThrowable(
					"java/lang/IllegalArgumentException",
					"invalid wait duration",
				)
			}
			if vm.runningThread != 0 {
				delay := time.Duration(milliseconds) * time.Millisecond
				if delay == 0 {
					delay = time.Nanosecond
				}
				return Value{}, false, &threadYield{delay: delay}
			}
			return Value{}, false, nil
		},
	)
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
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"setCharAt",
		"(IC)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			index, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			character, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			units := utf16.Encode([]rune(state.value))
			if index < 0 || int(index) >= len(units) {
				return Value{}, false, vm.newThrowable(
					"java/lang/StringIndexOutOfBoundsException",
					"",
				)
			}
			units[index] = uint16(character)
			state.value = string(utf16.Decode(units))
			return Value{}, false, nil
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
			data, err := vm.services.Text.Encode(value, shared.EncodingEUCKR)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewByteArray(data)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"getBytes",
		"(Ljava/lang/String;)[B",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			encoding, err := vm.textEncoding(name)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.services.Text.Encode(value, encoding)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewByteArray(data)), true, nil
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
	vm.RegisterNative(
		"java/lang/String",
		"valueOf",
		"([CII)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
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
			return ReferenceValue(vm.NewString(value)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"getChars",
		"(II[CI)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			begin, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			end, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			destinationReference, err := referenceArgument(args, 2)
			if err != nil {
				return Value{}, false, err
			}
			destinationOffset, err := intArgument(args, 3)
			if err != nil {
				return Value{}, false, err
			}
			destination, ok := vm.Object(destinationReference)
			units := utf16.Encode([]rune(value))
			if !ok || destination.Array == nil ||
				destination.Array.Descriptor != "[C" ||
				begin < 0 || end < begin || int(end) > len(units) ||
				destinationOffset < 0 ||
				int64(destinationOffset)+int64(end-begin) >
					int64(len(destination.Array.Elements)) {
				return Value{}, false, vm.newThrowable(
					"java/lang/IndexOutOfBoundsException",
					"",
				)
			}
			for index, unit := range units[begin:end] {
				destination.Array.Elements[int(destinationOffset)+index] =
					IntValue(int32(unit))
			}
			return Value{}, false, nil
		},
	)
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
	vm.RegisterNative(
		"java/lang/Integer",
		"valueOf",
		"(Ljava/lang/String;)Ljava/lang/Integer;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			parsed, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return Value{}, false, vm.newThrowable(
					"java/lang/NumberFormatException",
					err.Error(),
				)
			}
			return ReferenceValue(vm.NewObject(
				"java/lang/Integer",
				&integerState{value: int32(parsed)},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Byte",
		"parseByte",
		"(Ljava/lang/String;)B",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			parsed, err := strconv.ParseInt(value, 10, 8)
			if err != nil {
				return Value{}, false, vm.newThrowable(
					"java/lang/NumberFormatException",
					err.Error(),
				)
			}
			return IntValue(int32(int8(parsed))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Byte",
		"toString",
		"(B)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewString(
				strconv.FormatInt(int64(int8(value)), 10),
			)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Long",
		"parseLong",
		"(Ljava/lang/String;)J",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return Value{}, false, vm.newThrowable(
					"java/lang/NumberFormatException",
					err.Error(),
				)
			}
			return LongValue(parsed), true, nil
		},
	)
}

func (vm *VM) installRuntimeNatives() {
	runtime := vm.NewObject("java/lang/Runtime", nil)
	vm.RegisterStaticField(
		"java/lang/Runtime",
		"__aramSingleton",
		"Ljava/lang/Runtime;",
		ReferenceValue(runtime),
	)
	vm.RegisterNative(
		"java/lang/Runtime",
		"getRuntime",
		"()Ljava/lang/Runtime;",
		func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(runtime), true, nil
		},
	)
	vm.RegisterNative("java/lang/Runtime", "gc", "()V", func(
		_ context.Context,
		vm *VM,
		_ uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.collectGarbage()
	})
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

func (vm *VM) installRandomNatives() {
	seed := func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value := int64(0)
		if len(args) != 0 {
			var err error
			value, err = args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
		}
		stream := fmt.Sprintf("skvm.java.random.%08x", receiver)
		if err := vm.services.Random.SetJavaSeed(stream, value); err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, &randomState{stream: stream})
	}
	vm.RegisterNative("java/util/Random", "<init>", "()V", seed)
	vm.RegisterNative("java/util/Random", "<init>", "(J)V", seed)
	vm.RegisterNative("java/util/Random", "setSeed", "(J)V", seed)
	vm.RegisterNative(
		"java/util/Random",
		"nextInt",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Random receiver")
			}
			state, ok := object.Native.(*randomState)
			if !ok {
				state = &randomState{
					stream: fmt.Sprintf("skvm.java.random.%08x", receiver),
				}
				if err := vm.services.Random.SetJavaSeed(state.stream, 0); err != nil {
					return Value{}, false, err
				}
				object.Native = state
			}
			value, err := vm.services.Random.JavaInt(state.stream)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(value), true, nil
		},
	)
}
