package skvm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	stringBufferCapacityField = "\x00aram-buffer-capacity"
	threadPriorityField       = "\x00aram-thread-priority"
	threadNameField           = "\x00aram-thread-name"
)

func (vm *VM) installCLDCCoreExtras() {
	vm.installCLDCObjectExtras()
	vm.installCLDCIntegralWrappers()
	vm.installCLDCStringExtras()
	vm.installCLDCStringBufferExtras()
	vm.installCLDCThreadExtras()
	vm.installCLDCRandomExtras()
}

func (vm *VM) installCLDCObjectExtras() {
	vm.RegisterNative("java/lang/System", "identityHashCode", "(Ljava/lang/Object;)I", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		reference, err := referenceArgument(args, 0)
		return IntValue(int32(reference)), err == nil, err
	})
	exit := func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		if _, err := intArgument(args, 0); err != nil {
			return Value{}, false, err
		}
		return Value{}, false, ErrHalted
	}
	vm.RegisterNative("java/lang/System", "exit", "(I)V", exit)
	vm.RegisterNative("java/lang/Runtime", "exit", "(I)V", exit)
	vm.RegisterNative("java/lang/Object", "hashCode", "()I", func(_ context.Context, _ *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return IntValue(int32(receiver)), true, nil
	})
	vm.RegisterNative("java/lang/Object", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(vm.NewString(vm.objectString(receiver))), true, nil
	})
	vm.RegisterNative("java/lang/Object", "clone", "()Ljava/lang/Object;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid clone receiver")
		}
		if object.Array == nil && !vm.classAssignable(object.Class, "java/lang/Cloneable") {
			return Value{}, false, vm.newThrowable("java/lang/CloneNotSupportedException", object.Class)
		}
		if object.Array == nil && object.Native != nil {
			return Value{}, false, vm.newThrowable("java/lang/CloneNotSupportedException", object.Class)
		}
		clone := vm.NewObject(object.Class, nil)
		copyObject, _ := vm.Object(clone)
		for name, value := range object.Fields {
			copyObject.Fields[name] = value
		}
		if object.Array != nil {
			copyObject.Array = &Array{Descriptor: object.Array.Descriptor, Elements: append([]Value(nil), object.Array.Elements...)}
		}
		return ReferenceValue(clone), true, nil
	})
	vm.RegisterNative("java/lang/Object", "wait", "(JI)V", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		milliseconds, err := args[0].Long()
		if err != nil {
			return Value{}, false, err
		}
		nanoseconds, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if milliseconds < 0 || nanoseconds < 0 || nanoseconds > 999999 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid wait duration")
		}
		delay := timeDurationMillis(milliseconds)
		if nanoseconds != 0 {
			delay++
		}
		if vm.runningThread != 0 {
			return Value{}, false, &threadYield{delay: delay}
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("java/lang/Object", "finalize", "()V", nativeVoid)
}

func timeDurationMillis(milliseconds int64) time.Duration {
	if milliseconds > int64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func (vm *VM) installCLDCIntegralWrappers() {
	vm.RegisterStaticField("java/lang/Byte", "MIN_VALUE", "B", IntValue(math.MinInt8))
	vm.RegisterStaticField("java/lang/Byte", "MAX_VALUE", "B", IntValue(math.MaxInt8))
	vm.RegisterStaticField("java/lang/Integer", "MIN_VALUE", "I", IntValue(math.MinInt32))
	vm.RegisterStaticField("java/lang/Integer", "MAX_VALUE", "I", IntValue(math.MaxInt32))
	vm.RegisterStaticField("java/lang/Long", "MIN_VALUE", "J", LongValue(math.MinInt64))
	vm.RegisterStaticField("java/lang/Long", "MAX_VALUE", "J", LongValue(math.MaxInt64))
	vm.RegisterNative("java/lang/Byte", "<init>", "(B)V", vm.wrapperIntConstructor)
	vm.RegisterNative("java/lang/Byte", "byteValue", "()B", vm.wrapperIntGetter("java/lang/Byte"))
	vm.RegisterNative("java/lang/Byte", "hashCode", "()I", vm.wrapperIntGetter("java/lang/Byte"))
	vm.RegisterNative("java/lang/Byte", "toString", "()Ljava/lang/String;", vm.wrapperIntToString("java/lang/Byte", 8))
	vm.registerWrapperEquals("java/lang/Byte", ValueInt)
	vm.RegisterNative("java/lang/Byte", "parseByte", "(Ljava/lang/String;I)B", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		text, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		radix, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		value, err := strconv.ParseInt(text, int(radix), 8)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", err.Error())
		}
		return IntValue(int32(value)), true, nil
	})
	for _, method := range []struct {
		name, descriptor string
		convert          func(int32) Value
	}{{"byteValue", "()B", func(v int32) Value { return IntValue(int32(int8(v))) }}, {"shortValue", "()S", func(v int32) Value { return IntValue(int32(int16(v))) }}, {"longValue", "()J", func(v int32) Value { return LongValue(int64(v)) }}, {"floatValue", "()F", func(v int32) Value { return FloatValue(float32(v)) }}, {"doubleValue", "()D", func(v int32) Value { return DoubleValue(float64(v)) }}, {"hashCode", "()I", func(v int32) Value { return IntValue(v) }}} {
		method := method
		vm.RegisterNative("java/lang/Integer", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.integer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return method.convert(state.value), true, nil
		})
	}
	vm.RegisterNative("java/lang/Integer", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.integer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(strconv.FormatInt(int64(state.value), 10))), true, nil
	})
	vm.installIntegerRadixStatics()
	vm.RegisterNative("java/lang/Long", "<init>", "(J)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, err := args[0].Long()
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setWrapperValue(receiver, LongValue(value))
	})
	for _, method := range []struct {
		name, descriptor string
		convert          func(int64) Value
	}{{"longValue", "()J", func(v int64) Value { return LongValue(v) }}, {"floatValue", "()F", func(v int64) Value { return FloatValue(float32(v)) }}, {"doubleValue", "()D", func(v int64) Value { return DoubleValue(float64(v)) }}, {"hashCode", "()I", func(v int64) Value { return IntValue(int32(v ^ (v >> 32))) }}} {
		method := method
		vm.RegisterNative("java/lang/Long", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.wrapperValue(receiver, "java/lang/Long", ValueLong)
			if err != nil {
				return Value{}, false, err
			}
			integer, _ := value.Long()
			return method.convert(integer), true, nil
		})
	}
	vm.RegisterNative("java/lang/Long", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := vm.wrapperValue(receiver, "java/lang/Long", ValueLong)
		if err != nil {
			return Value{}, false, err
		}
		integer, _ := value.Long()
		return ReferenceValue(vm.NewString(strconv.FormatInt(integer, 10))), true, nil
	})
	vm.registerWrapperEquals("java/lang/Long", ValueLong)
	parseLong := func(radix bool) NativeFunc {
		return func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			text, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			base := int32(10)
			if radix {
				base, err = intArgument(args, 1)
			}
			value, parseErr := strconv.ParseInt(text, int(base), 64)
			if err != nil || parseErr != nil {
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", parseErr.Error())
			}
			return LongValue(value), true, nil
		}
	}
	vm.RegisterNative("java/lang/Long", "parseLong", "(Ljava/lang/String;I)J", parseLong(true))
	for _, descriptor := range []string{"(J)Ljava/lang/String;", "(JI)Ljava/lang/String;"} {
		descriptor := descriptor
		vm.RegisterNative("java/lang/Long", "toString", descriptor, func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := args[0].Long()
			base := int32(10)
			if descriptor == "(JI)Ljava/lang/String;" && err == nil {
				base, err = intArgument(args, 1)
			}
			if err != nil {
				return Value{}, false, err
			}
			if base < 2 || base > 36 {
				base = 10
			}
			return ReferenceValue(vm.NewString(strconv.FormatInt(value, int(base)))), true, nil
		})
	}
}

func (vm *VM) integer(reference uint32) (*integerState, error) {
	object, ok := vm.Object(reference)
	if !ok || object.Class != "java/lang/Integer" {
		return nil, fmt.Errorf("invalid Integer")
	}
	state, ok := object.Native.(*integerState)
	if !ok {
		return nil, fmt.Errorf("invalid Integer state")
	}
	return state, nil
}

func (vm *VM) installIntegerRadixStatics() {
	for _, method := range []struct {
		name       string
		descriptor string
		base       int32
	}{{"toString", "(II)Ljava/lang/String;", 0}, {"toOctalString", "(I)Ljava/lang/String;", 8}, {"toBinaryString", "(I)Ljava/lang/String;", 2}} {
		method := method
		vm.RegisterNative("java/lang/Integer", method.name, method.descriptor, func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			base := method.base
			if base == 0 {
				base, err = intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
				if base < 2 || base > 36 {
					base = 10
				}
				return ReferenceValue(vm.NewString(strconv.FormatInt(int64(value), int(base)))), true, nil
			}
			return ReferenceValue(vm.NewString(strconv.FormatUint(uint64(uint32(value)), int(base)))), true, nil
		})
	}
	vm.RegisterNative("java/lang/Integer", "valueOf", "(Ljava/lang/String;I)Ljava/lang/Integer;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		text, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		base, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		value, err := strconv.ParseInt(text, int(base), 32)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", err.Error())
		}
		return ReferenceValue(vm.NewObject("java/lang/Integer", &integerState{value: int32(value)})), true, nil
	})
}

func (vm *VM) installCLDCStringExtras() {
	vm.RegisterNative("java/lang/String", "startsWith", "(Ljava/lang/String;I)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		text, err := vm.String(receiver)
		if err != nil {
			return Value{}, false, err
		}
		prefix, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		offset, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		units := utf16.Encode([]rune(text))
		prefixUnits := utf16.Encode([]rune(prefix))
		if offset < 0 || int64(offset)+int64(len(prefixUnits)) > int64(len(units)) {
			return boolValue(false), true, nil
		}
		for index, unit := range prefixUnits {
			if units[int(offset)+index] != unit {
				return boolValue(false), true, nil
			}
		}
		return boolValue(true), true, nil
	})
	for _, descriptor := range []string{"(Z)Ljava/lang/String;", "(J)Ljava/lang/String;", "(F)Ljava/lang/String;", "(D)Ljava/lang/String;"} {
		descriptor := descriptor
		vm.RegisterNative("java/lang/String", "valueOf", descriptor, func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			text, err := vm.primitiveText(args[0], descriptor[1])
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewString(text)), true, nil
		})
	}
	vm.RegisterNative("java/lang/String", "valueOf", "([C)Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		text, err := vm.charArrayArgument(args, 0, 0, -1)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(text)), true, nil
	})
	vm.RegisterNative("java/lang/String", "equalsIgnoreCase", "(Ljava/lang/String;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		left, err := vm.String(receiver)
		if err != nil {
			return Value{}, false, err
		}
		other, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if other == 0 {
			return boolValue(false), true, nil
		}
		right, err := vm.String(other)
		return boolValue(err == nil && strings.EqualFold(left, right)), true, nil
	})
	vm.RegisterNative("java/lang/String", "regionMatches", "(ZILjava/lang/String;II)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		left, err := vm.String(receiver)
		if err != nil {
			return Value{}, false, err
		}
		ignoreCase, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		leftOffset, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		other, err := referenceArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
		rightOffset, err := intArgument(args, 3)
		if err != nil {
			return Value{}, false, err
		}
		length, err := intArgument(args, 4)
		if err != nil {
			return Value{}, false, err
		}
		right, err := vm.String(other)
		if err != nil {
			return boolValue(false), true, nil
		}
		leftUnits, rightUnits := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
		if leftOffset < 0 || rightOffset < 0 || length < 0 || int64(leftOffset)+int64(length) > int64(len(leftUnits)) || int64(rightOffset)+int64(length) > int64(len(rightUnits)) {
			return boolValue(false), true, nil
		}
		leftRegion := string(utf16.Decode(leftUnits[leftOffset : leftOffset+length]))
		rightRegion := string(utf16.Decode(rightUnits[rightOffset : rightOffset+length]))
		if ignoreCase != 0 {
			return boolValue(strings.EqualFold(leftRegion, rightRegion)), true, nil
		}
		return boolValue(leftRegion == rightRegion), true, nil
	})
	vm.RegisterNative("java/lang/String", "<init>", "([BIILjava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		data, err := vm.byteSliceArgument(args)
		if err != nil {
			return Value{}, false, err
		}
		name, err := vm.stringArgument(args, 3)
		if err != nil {
			return Value{}, false, err
		}
		encoding, err := vm.textEncoding(name)
		if err != nil {
			return Value{}, false, err
		}
		text, err := vm.services.Text.Decode(data, encoding)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, text)
	})
	for _, descriptor := range []string{"(I)I", "(II)I"} {
		descriptor := descriptor
		vm.RegisterNative("java/lang/String", "lastIndexOf", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			text, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			character, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			units := utf16.Encode([]rune(text))
			from := int32(len(units) - 1)
			if descriptor == "(II)I" {
				from, err = intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
			}
			if from >= int32(len(units)) {
				from = int32(len(units) - 1)
			}
			for index := from; index >= 0; index-- {
				if units[index] == uint16(character) {
					return IntValue(index), true, nil
				}
			}
			return IntValue(-1), true, nil
		})
	}
	vm.RegisterNative("java/lang/String", "intern", "()Ljava/lang/String;", func(_ context.Context, _ *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(receiver), true, nil
	})
}

func (vm *VM) installCLDCStringBufferExtras() {
	for _, descriptor := range []string{"(Z)Ljava/lang/StringBuffer;", "(F)Ljava/lang/StringBuffer;", "(D)Ljava/lang/StringBuffer;"} {
		descriptor := descriptor
		vm.RegisterNative("java/lang/StringBuffer", "append", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			text, err := vm.primitiveText(args[0], descriptor[1])
			if err != nil {
				return Value{}, false, err
			}
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			state.value += text
			return ReferenceValue(receiver), true, nil
		})
	}
	for _, descriptor := range []string{"([C)Ljava/lang/StringBuffer;", "([CII)Ljava/lang/StringBuffer;"} {
		descriptor := descriptor
		vm.RegisterNative("java/lang/StringBuffer", "append", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			offset, length := int32(0), int32(-1)
			if descriptor == "([CII)Ljava/lang/StringBuffer;" {
				var err error
				offset, err = intArgument(args, 1)
				if err == nil {
					length, err = intArgument(args, 2)
				}
				if err != nil {
					return Value{}, false, err
				}
			}
			text, err := vm.charArrayArgument(args, 0, offset, length)
			if err != nil {
				return Value{}, false, err
			}
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			state.value += text
			return ReferenceValue(receiver), true, nil
		})
	}
	vm.RegisterNative("java/lang/StringBuffer", "delete", "(II)Ljava/lang/StringBuffer;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		start, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		end, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		state, err := vm.stringBuffer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		units := utf16.Encode([]rune(state.value))
		if start < 0 || start > end || int(start) > len(units) {
			return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
		}
		if int(end) > len(units) {
			end = int32(len(units))
		}
		units = append(units[:start], units[end:]...)
		state.value = string(utf16.Decode(units))
		return ReferenceValue(receiver), true, nil
	})
	vm.RegisterNative("java/lang/StringBuffer", "deleteCharAt", "(I)Ljava/lang/StringBuffer;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		state, err := vm.stringBuffer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		units := utf16.Encode([]rune(state.value))
		if index < 0 || int(index) >= len(units) {
			return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
		}
		units = append(units[:index], units[index+1:]...)
		state.value = string(utf16.Decode(units))
		return ReferenceValue(receiver), true, nil
	})
	vm.RegisterNative("java/lang/StringBuffer", "reverse", "()Ljava/lang/StringBuffer;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.stringBuffer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		units := utf16.Encode([]rune(state.value))
		for left, right := 0, len(units)-1; left < right; left, right = left+1, right-1 {
			units[left], units[right] = units[right], units[left]
		}
		// Match Java's surrogate-pair repair after reversing UTF-16 code units.
		for index := 0; index+1 < len(units); index++ {
			if units[index] >= 0xdc00 && units[index] <= 0xdfff && units[index+1] >= 0xd800 && units[index+1] <= 0xdbff {
				units[index], units[index+1] = units[index+1], units[index]
				index++
			}
		}
		state.value = string(utf16.Decode(units))
		return ReferenceValue(receiver), true, nil
	})
	vm.RegisterNative("java/lang/StringBuffer", "capacity", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.stringBuffer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		object, _ := vm.Object(receiver)
		capacity, _ := object.Fields[stringBufferCapacityField].Int()
		minimum := int32(len(utf16.Encode([]rune(state.value))) + 16)
		if capacity < minimum {
			capacity = minimum
		}
		return IntValue(capacity), true, nil
	})
	vm.RegisterNative("java/lang/StringBuffer", "ensureCapacity", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		minimum, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid StringBuffer")
		}
		current, _ := object.Fields[stringBufferCapacityField].Int()
		if minimum > current {
			object.Fields[stringBufferCapacityField] = IntValue(max(minimum, current*2+2))
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("java/lang/StringBuffer", "charAt", "(I)C", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.stringBuffer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		index, err := intArgument(args, 0)
		units := utf16.Encode([]rune(state.value))
		if err != nil || index < 0 || int(index) >= len(units) {
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
		}
		return IntValue(int32(units[index])), true, nil
	})
	vm.RegisterNative("java/lang/StringBuffer", "getChars", "(II[CI)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.stringBuffer(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return vm.copyStringChars(state.value, args)
	})
	for _, descriptor := range []string{"(IZ)Ljava/lang/StringBuffer;", "(IC)Ljava/lang/StringBuffer;", "(II)Ljava/lang/StringBuffer;", "(IJ)Ljava/lang/StringBuffer;", "(IF)Ljava/lang/StringBuffer;", "(ID)Ljava/lang/StringBuffer;"} {
		descriptor := descriptor
		vm.RegisterNative("java/lang/StringBuffer", "insert", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			offset, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			text, err := vm.primitiveText(args[1], descriptor[2])
			if err != nil {
				return Value{}, false, err
			}
			return vm.stringBufferInsert(receiver, offset, text)
		})
	}
	vm.RegisterNative("java/lang/StringBuffer", "insert", "(I[C)Ljava/lang/StringBuffer;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		offset, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		text, err := vm.charArrayArgument(args, 1, 0, -1)
		if err != nil {
			return Value{}, false, err
		}
		return vm.stringBufferInsert(receiver, offset, text)
	})
	vm.RegisterNative("java/lang/StringBuffer", "insert", "(ILjava/lang/Object;)Ljava/lang/StringBuffer;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		offset, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		reference, err := referenceArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		return vm.stringBufferInsert(receiver, offset, vm.objectString(reference))
	})
}

func (vm *VM) copyStringChars(text string, args []Value) (Value, bool, error) {
	start, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	end, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	destination, err := referenceArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	offset, err := intArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	units := utf16.Encode([]rune(text))
	object, ok := vm.Object(destination)
	if !ok || object.Array == nil || object.Array.Descriptor != "[C" || start < 0 || end < start || int(end) > len(units) || offset < 0 || int64(offset)+int64(end-start) > int64(len(object.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	for index, unit := range units[start:end] {
		object.Array.Elements[int(offset)+index] = IntValue(int32(unit))
	}
	return Value{}, false, nil
}

func (vm *VM) primitiveText(value Value, kind byte) (string, error) {
	switch kind {
	case 'Z':
		integer, err := value.Int()
		return strconv.FormatBool(integer != 0), err
	case 'C':
		integer, err := value.Int()
		return string(rune(uint16(integer))), err
	case 'I':
		integer, err := value.Int()
		return strconv.FormatInt(int64(integer), 10), err
	case 'J':
		integer, err := value.Long()
		return strconv.FormatInt(integer, 10), err
	case 'F':
		floating, err := value.Float()
		return javaFloatText(float64(floating), 32), err
	case 'D':
		floating, err := value.Double()
		return javaFloatText(floating, 64), err
	default:
		return "", fmt.Errorf("unsupported primitive %c", kind)
	}
}

func (vm *VM) stringBufferInsert(receiver uint32, offset int32, text string) (Value, bool, error) {
	state, err := vm.stringBuffer(receiver)
	if err != nil {
		return Value{}, false, err
	}
	units := utf16.Encode([]rune(state.value))
	if offset < 0 || int(offset) > len(units) {
		return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
	}
	inserted := utf16.Encode([]rune(text))
	result := append([]uint16(nil), units[:offset]...)
	result = append(result, inserted...)
	result = append(result, units[offset:]...)
	state.value = string(utf16.Decode(result))
	return ReferenceValue(receiver), true, nil
}

func (vm *VM) installCLDCThreadExtras() {
	initialize := func(withTarget, withName bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			target, argument := receiver, 0
			if withTarget {
				var err error
				target, err = referenceArgument(args, argument)
				if err != nil {
					return Value{}, false, err
				}
				argument++
			}
			name := "Thread-" + strconv.FormatUint(uint64(receiver), 10)
			if withName {
				var err error
				name, err = vm.stringArgument(args, argument)
				if err != nil {
					return Value{}, false, err
				}
			}
			if err := vm.setNative(receiver, &threadState{target: target}); err != nil {
				return Value{}, false, err
			}
			object, _ := vm.Object(receiver)
			object.Fields[threadNameField] = ReferenceValue(vm.NewString(name))
			object.Fields[threadPriorityField] = IntValue(5)
			return Value{}, false, nil
		}
	}
	vm.RegisterNative("java/lang/Thread", "<init>", "()V", initialize(false, false))
	vm.RegisterNative("java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V", initialize(true, false))
	vm.RegisterNative("java/lang/Thread", "<init>", "(Ljava/lang/String;)V", initialize(false, true))
	vm.RegisterNative("java/lang/Thread", "<init>", "(Ljava/lang/Runnable;Ljava/lang/String;)V", initialize(true, true))
	mainThread := vm.NewObject("java/lang/Thread", &threadState{})
	mainObject, _ := vm.Object(mainThread)
	mainObject.Fields[threadNameField] = ReferenceValue(vm.NewString("main"))
	mainObject.Fields[threadPriorityField] = IntValue(5)
	vm.RegisterStaticField("java/lang/Thread", "__aramMainThread", "Ljava/lang/Thread;", ReferenceValue(mainThread))
	vm.RegisterNative("java/lang/Thread", "currentThread", "()Ljava/lang/Thread;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		if vm.runningThread != 0 {
			return ReferenceValue(vm.runningThread), true, nil
		}
		return ReferenceValue(mainThread), true, nil
	})
	vm.RegisterNative("java/lang/Thread", "getName", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid Thread")
		}
		name, err := object.Fields[threadNameField].Reference()
		if err != nil || name == 0 {
			name = vm.NewString("Thread-" + strconv.FormatUint(uint64(receiver), 10))
			object.Fields[threadNameField] = ReferenceValue(name)
		}
		return ReferenceValue(name), true, nil
	})
	vm.RegisterNative("java/lang/Thread", "interrupt", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.thread(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state.wakeAt = vm.services.Clock.Monotonic()
		return Value{}, false, nil
	})
	vm.RegisterNative("java/lang/Thread", "join", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.thread(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state.active && vm.runningThread != 0 {
			return Value{}, false, &threadYield{delay: time.Millisecond}
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("java/lang/Thread", "run", "()V", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.thread(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state.target == 0 || state.target == receiver {
			return Value{}, false, nil
		}
		_, _, err = vm.InvokeVirtual(ctx, state.target, "run", "()V")
		if errors.Is(err, ErrMethodNotFound) {
			err = nil
		}
		return Value{}, false, err
	})
	vm.RegisterNative("java/lang/Thread", "activeCount", "()I", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		count := int32(0)
		for _, object := range vm.heap {
			if state, ok := object.Native.(*threadState); ok && state.active {
				count++
			}
		}
		return IntValue(count), true, nil
	})
	vm.RegisterNative("java/lang/Thread", "setPriority", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		priority, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if priority < 1 || priority > 10 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid priority")
		}
		object, _ := vm.Object(receiver)
		object.Fields[threadPriorityField] = IntValue(priority)
		return Value{}, false, nil
	})
	vm.RegisterNative("java/lang/Thread", "getPriority", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid Thread")
		}
		priority, _ := object.Fields[threadPriorityField].Int()
		if priority == 0 {
			priority = 5
		}
		return IntValue(priority), true, nil
	})
	vm.RegisterNative("java/lang/Thread", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid Thread")
		}
		priority, _ := object.Fields[threadPriorityField].Int()
		if priority == 0 {
			priority = 5
		}
		return ReferenceValue(vm.NewString(fmt.Sprintf("Thread-%d,%d", receiver, priority))), true, nil
	})
}

func (vm *VM) randomState(reference uint32) (*randomState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Random")
	}
	state, ok := object.Native.(*randomState)
	if !ok {
		return nil, fmt.Errorf("invalid Random state")
	}
	return state, nil
}

func (vm *VM) installCLDCRandomExtras() {
	vm.RegisterNative("java/util/Random", "nextFloat", "()F", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.randomState(receiver)
		if err != nil {
			return Value{}, false, err
		}
		bits, err := vm.services.Random.JavaBits(state.stream, 24)
		return FloatValue(float32(bits) / (1 << 24)), err == nil, err
	})
	vm.RegisterNative("java/util/Random", "nextDouble", "()D", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.randomState(receiver)
		if err != nil {
			return Value{}, false, err
		}
		high, err := vm.services.Random.JavaBits(state.stream, 26)
		if err != nil {
			return Value{}, false, err
		}
		low, err := vm.services.Random.JavaBits(state.stream, 27)
		return DoubleValue(float64((uint64(high)<<27)+uint64(low)) / (1 << 53)), err == nil, err
	})
	vm.RegisterNative("java/util/Random", "next", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		bits, err := intArgument(args, 0)
		if err != nil || bits < 1 || bits > 32 {
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid random bit count")
		}
		state, err := vm.randomState(receiver)
		if err != nil {
			return Value{}, false, err
		}
		value, err := vm.services.Random.JavaBits(state.stream, uint8(bits))
		return IntValue(int32(value)), err == nil, err
	})
	vm.RegisterNative("java/util/Random", "nextBoolean", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.randomState(receiver)
		if err != nil {
			return Value{}, false, err
		}
		value, err := vm.services.Random.JavaBits(state.stream, 1)
		return boolValue(value != 0), err == nil, err
	})
	vm.RegisterNative("java/util/Random", "nextLong", "()J", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.randomState(receiver)
		if err != nil {
			return Value{}, false, err
		}
		high, err := vm.services.Random.JavaBits(state.stream, 32)
		if err != nil {
			return Value{}, false, err
		}
		low, err := vm.services.Random.JavaBits(state.stream, 32)
		return LongValue((int64(int32(high)) << 32) + int64(int32(low))), err == nil, err
	})
	vm.RegisterNative("java/util/Random", "nextInt", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		bound, err := intArgument(args, 0)
		if err != nil || bound <= 0 {
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "bound must be positive")
		}
		state, err := vm.randomState(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if bound&(bound-1) == 0 {
			bits, drawErr := vm.services.Random.JavaBits(state.stream, 31)
			return IntValue(int32((int64(bound) * int64(bits)) >> 31)), drawErr == nil, drawErr
		}
		for {
			bits, drawErr := vm.services.Random.JavaBits(state.stream, 31)
			if drawErr != nil {
				return Value{}, false, drawErr
			}
			value := int32(bits) % bound
			if int32(bits)-value+(bound-1) >= 0 {
				return IntValue(value), true, nil
			}
		}
	})
}
