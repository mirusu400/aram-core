package skvm

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const wrapperValueField = "\x00aram-wrapper-value"

func (vm *VM) setWrapperValue(reference uint32, value Value) error {
	object, ok := vm.Object(reference)
	if !ok {
		return fmt.Errorf("invalid wrapper reference %d", reference)
	}
	object.Fields[wrapperValueField] = value
	return nil
}

func (vm *VM) wrapperValue(reference uint32, class string, kind ValueKind) (Value, error) {
	object, ok := vm.Object(reference)
	if !ok || object.Class != class {
		return Value{}, fmt.Errorf("object %d is not a %s", reference, class)
	}
	value, ok := object.Fields[wrapperValueField]
	if !ok || value.Kind != kind {
		return Value{}, fmt.Errorf("object %d has invalid %s state", reference, class)
	}
	return value, nil
}

func (vm *VM) newWrapper(class string, value Value) uint32 {
	reference := vm.NewObject(class, nil)
	_ = vm.setWrapperValue(reference, value)
	return reference
}

func javaFloatText(value float64, bits int) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(value, 'g', -1, bits)
}

func canonicalFloatBits(value float32) uint32 {
	if math.IsNaN(float64(value)) {
		return 0x7fc00000
	}
	return math.Float32bits(value)
}

func canonicalDoubleBits(value float64) uint64 {
	if math.IsNaN(value) {
		return 0x7ff8000000000000
	}
	return math.Float64bits(value)
}

func (vm *VM) installCLDCNumberNatives() {
	vm.installBooleanNatives()
	vm.installCharacterNatives()
	vm.installShortNatives()
	vm.installFloatNatives()
	vm.installDoubleNatives()
}

func (vm *VM) installBooleanNatives() {
	falseRef := vm.newWrapper("java/lang/Boolean", IntValue(0))
	trueRef := vm.newWrapper("java/lang/Boolean", IntValue(1))
	vm.RegisterStaticField("java/lang/Boolean", "FALSE", "Ljava/lang/Boolean;", ReferenceValue(falseRef))
	vm.RegisterStaticField("java/lang/Boolean", "TRUE", "Ljava/lang/Boolean;", ReferenceValue(trueRef))
	vm.RegisterNative("java/lang/Boolean", "<init>", "(Z)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setWrapperValue(receiver, boolValue(value != 0))
	})
	vm.RegisterNative("java/lang/Boolean", "booleanValue", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := vm.wrapperValue(receiver, "java/lang/Boolean", ValueInt)
		return value, err == nil, err
	})
	vm.RegisterNative("java/lang/Boolean", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := vm.wrapperValue(receiver, "java/lang/Boolean", ValueInt)
		if err != nil {
			return Value{}, false, err
		}
		integer, _ := value.Int()
		return ReferenceValue(vm.NewString(strconv.FormatBool(integer != 0))), true, nil
	})
	vm.RegisterNative("java/lang/Boolean", "hashCode", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := vm.wrapperValue(receiver, "java/lang/Boolean", ValueInt)
		if err != nil {
			return Value{}, false, err
		}
		integer, _ := value.Int()
		if integer != 0 {
			return IntValue(1231), true, nil
		}
		return IntValue(1237), true, nil
	})
	vm.registerWrapperEquals("java/lang/Boolean", ValueInt)
}

func (vm *VM) installCharacterNatives() {
	vm.RegisterStaticField("java/lang/Character", "MIN_VALUE", "C", IntValue(0))
	vm.RegisterStaticField("java/lang/Character", "MAX_VALUE", "C", IntValue(0xffff))
	vm.RegisterNative("java/lang/Character", "<init>", "(C)V", vm.wrapperIntConstructor)
	vm.RegisterNative("java/lang/Character", "charValue", "()C", vm.wrapperIntGetter("java/lang/Character"))
	vm.RegisterNative("java/lang/Character", "hashCode", "()I", vm.wrapperIntGetter("java/lang/Character"))
	vm.RegisterNative("java/lang/Character", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := vm.wrapperValue(receiver, "java/lang/Character", ValueInt)
		if err != nil {
			return Value{}, false, err
		}
		integer, _ := value.Int()
		return ReferenceValue(vm.NewString(string(rune(uint16(integer))))), true, nil
	})
	vm.registerWrapperEquals("java/lang/Character", ValueInt)
	for _, method := range []struct {
		name string
		fn   func(rune) rune
	}{{"toLowerCase", unicode.ToLower}, {"toUpperCase", unicode.ToUpper}} {
		method := method
		vm.RegisterNative("java/lang/Character", method.name, "(C)C", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(uint16(method.fn(rune(uint16(value)))))), true, nil
		})
	}
	for _, method := range []struct {
		name string
		fn   func(rune) bool
	}{{"isDigit", unicode.IsDigit}, {"isLowerCase", unicode.IsLower}, {"isUpperCase", unicode.IsUpper}} {
		method := method
		vm.RegisterNative("java/lang/Character", method.name, "(C)Z", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return boolValue(method.fn(rune(uint16(value)))), true, nil
		})
	}
	vm.RegisterNative("java/lang/Character", "digit", "(CI)I", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		character, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		radix, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		r := unicode.ToLower(rune(uint16(character)))
		value := int32(-1)
		switch {
		case r >= '0' && r <= '9':
			value = int32(r - '0')
		case r >= 'a' && r <= 'z':
			value = int32(r-'a') + 10
		case r >= 0xff10 && r <= 0xff19:
			value = int32(r - 0xff10)
		}
		if radix < 2 || radix > 36 || value >= radix {
			value = -1
		}
		return IntValue(value), true, nil
	})
}

func (vm *VM) installShortNatives() {
	vm.RegisterStaticField("java/lang/Short", "MIN_VALUE", "S", IntValue(math.MinInt16))
	vm.RegisterStaticField("java/lang/Short", "MAX_VALUE", "S", IntValue(math.MaxInt16))
	vm.RegisterNative("java/lang/Short", "<init>", "(S)V", vm.wrapperIntConstructor)
	vm.RegisterNative("java/lang/Short", "shortValue", "()S", vm.wrapperIntGetter("java/lang/Short"))
	vm.RegisterNative("java/lang/Short", "hashCode", "()I", vm.wrapperIntGetter("java/lang/Short"))
	vm.RegisterNative("java/lang/Short", "toString", "()Ljava/lang/String;", vm.wrapperIntToString("java/lang/Short", 16))
	vm.registerWrapperEquals("java/lang/Short", ValueInt)
	parse := func(withRadix bool) NativeFunc {
		return func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			text, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			radix := int32(10)
			if withRadix {
				radix, err = intArgument(args, 1)
			}
			parsed, parseErr := strconv.ParseInt(text, int(radix), 16)
			if err != nil || parseErr != nil {
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", parseErr.Error())
			}
			return IntValue(int32(parsed)), true, nil
		}
	}
	vm.RegisterNative("java/lang/Short", "parseShort", "(Ljava/lang/String;)S", parse(false))
	vm.RegisterNative("java/lang/Short", "parseShort", "(Ljava/lang/String;I)S", parse(true))
}

func (vm *VM) wrapperIntConstructor(_ context.Context, vm2 *VM, receiver uint32, args []Value) (Value, bool, error) {
	value, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	return Value{}, false, vm2.setWrapperValue(receiver, IntValue(value))
}

func (vm *VM) wrapperIntGetter(class string) NativeFunc {
	return func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := vm.wrapperValue(receiver, class, ValueInt)
		return value, err == nil, err
	}
}

func (vm *VM) wrapperIntToString(class string, bits int) NativeFunc {
	return func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := vm.wrapperValue(receiver, class, ValueInt)
		if err != nil {
			return Value{}, false, err
		}
		integer, _ := value.Int()
		if bits == 8 {
			integer = int32(int8(integer))
		} else if bits == 16 {
			integer = int32(int16(integer))
		}
		return ReferenceValue(vm.NewString(strconv.FormatInt(int64(integer), 10))), true, nil
	}
}

func (vm *VM) registerWrapperEquals(class string, kind ValueKind) {
	vm.RegisterNative(class, "equals", "(Ljava/lang/Object;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		other, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		left, err := vm.wrapperValue(receiver, class, kind)
		if err != nil {
			return Value{}, false, err
		}
		right, err := vm.wrapperValue(other, class, kind)
		if err != nil {
			return boolValue(false), true, nil
		}
		return boolValue(left.bits == right.bits), true, nil
	})
}

func (vm *VM) installFloatNatives() {
	vm.RegisterStaticField("java/lang/Float", "MAX_VALUE", "F", FloatValue(math.MaxFloat32))
	vm.RegisterStaticField("java/lang/Float", "MIN_VALUE", "F", FloatValue(math.SmallestNonzeroFloat32))
	vm.RegisterStaticField("java/lang/Float", "NaN", "F", FloatValue(float32(math.NaN())))
	vm.RegisterStaticField("java/lang/Float", "NEGATIVE_INFINITY", "F", FloatValue(float32(math.Inf(-1))))
	vm.RegisterStaticField("java/lang/Float", "POSITIVE_INFINITY", "F", FloatValue(float32(math.Inf(1))))
	for _, descriptor := range []string{"(F)V", "(D)V"} {
		descriptor := descriptor
		vm.RegisterNative("java/lang/Float", "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			var value float32
			var err error
			if descriptor == "(F)V" {
				value, err = args[0].Float()
			} else {
				var double float64
				double, err = args[0].Double()
				value = float32(double)
			}
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setWrapperValue(receiver, FloatValue(value))
		})
	}
	vm.installFloatingWrapper("java/lang/Float", ValueFloat)
	vm.RegisterNative("java/lang/Float", "floatToIntBits", "(F)I", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := args[0].Float()
		return IntValue(int32(canonicalFloatBits(value))), err == nil, err
	})
	vm.RegisterNative("java/lang/Float", "intBitsToFloat", "(I)F", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := intArgument(args, 0)
		return FloatValue(math.Float32frombits(uint32(value))), err == nil, err
	})
	vm.RegisterNative("java/lang/Float", "parseFloat", "(Ljava/lang/String;)F", vm.parseFloating(false, false))
	vm.RegisterNative("java/lang/Float", "valueOf", "(Ljava/lang/String;)Ljava/lang/Float;", vm.parseFloating(true, false))
	vm.RegisterNative("java/lang/Float", "toString", "(F)Ljava/lang/String;", vm.staticFloatingToString(false))
}

func (vm *VM) installDoubleNatives() {
	vm.RegisterStaticField("java/lang/Double", "MAX_VALUE", "D", DoubleValue(math.MaxFloat64))
	vm.RegisterStaticField("java/lang/Double", "MIN_VALUE", "D", DoubleValue(math.SmallestNonzeroFloat64))
	vm.RegisterStaticField("java/lang/Double", "NaN", "D", DoubleValue(math.NaN()))
	vm.RegisterStaticField("java/lang/Double", "NEGATIVE_INFINITY", "D", DoubleValue(math.Inf(-1)))
	vm.RegisterStaticField("java/lang/Double", "POSITIVE_INFINITY", "D", DoubleValue(math.Inf(1)))
	vm.RegisterNative("java/lang/Double", "<init>", "(D)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, err := args[0].Double()
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setWrapperValue(receiver, DoubleValue(value))
	})
	vm.installFloatingWrapper("java/lang/Double", ValueDouble)
	vm.RegisterNative("java/lang/Double", "doubleToLongBits", "(D)J", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := args[0].Double()
		return LongValue(int64(canonicalDoubleBits(value))), err == nil, err
	})
	vm.RegisterNative("java/lang/Double", "longBitsToDouble", "(J)D", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := args[0].Long()
		return DoubleValue(math.Float64frombits(uint64(value))), err == nil, err
	})
	vm.RegisterNative("java/lang/Double", "parseDouble", "(Ljava/lang/String;)D", vm.parseFloating(false, true))
	vm.RegisterNative("java/lang/Double", "valueOf", "(Ljava/lang/String;)Ljava/lang/Double;", vm.parseFloating(true, true))
	vm.RegisterNative("java/lang/Double", "toString", "(D)Ljava/lang/String;", vm.staticFloatingToString(true))
}

func (vm *VM) installFloatingWrapper(class string, kind ValueKind) {
	read := func(reference uint32) (float64, Value, error) {
		value, err := vm.wrapperValue(reference, class, kind)
		if err != nil {
			return 0, Value{}, err
		}
		if kind == ValueFloat {
			v, _ := value.Float()
			return float64(v), value, nil
		}
		v, _ := value.Double()
		return v, value, nil
	}
	for _, method := range []struct{ name, descriptor string }{
		{"byteValue", "()B"}, {"shortValue", "()S"}, {"intValue", "()I"},
		{"longValue", "()J"}, {"floatValue", "()F"}, {"doubleValue", "()D"},
	} {
		method := method
		vm.RegisterNative(class, method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, _, err := read(receiver)
			if err != nil {
				return Value{}, false, err
			}
			switch method.descriptor {
			case "()B":
				return IntValue(int32(int8(value))), true, nil
			case "()S":
				return IntValue(int32(int16(value))), true, nil
			case "()I":
				return IntValue(int32(value)), true, nil
			case "()J":
				return LongValue(int64(value)), true, nil
			case "()F":
				return FloatValue(float32(value)), true, nil
			default:
				return DoubleValue(value), true, nil
			}
		})
	}
	vm.RegisterNative(class, "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, _, err := read(receiver)
		if err != nil {
			return Value{}, false, err
		}
		bits := 64
		if kind == ValueFloat {
			bits = 32
		}
		return ReferenceValue(vm.NewString(javaFloatText(value, bits))), true, nil
	})
	vm.RegisterNative(class, "hashCode", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, _, err := read(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if kind == ValueFloat {
			return IntValue(int32(canonicalFloatBits(float32(value)))), true, nil
		}
		bits := canonicalDoubleBits(value)
		return IntValue(int32(bits ^ (bits >> 32))), true, nil
	})
	vm.RegisterNative(class, "equals", "(Ljava/lang/Object;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		other, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		left, _, err := read(receiver)
		if err != nil {
			return Value{}, false, err
		}
		right, _, err := read(other)
		if err != nil {
			return boolValue(false), true, nil
		}
		if kind == ValueFloat {
			return boolValue(canonicalFloatBits(float32(left)) == canonicalFloatBits(float32(right))), true, nil
		}
		return boolValue(canonicalDoubleBits(left) == canonicalDoubleBits(right)), true, nil
	})
	for _, name := range []string{"isNaN", "isInfinite"} {
		name := name
		descriptor := "()Z"
		staticDescriptor := "(F)Z"
		if kind == ValueDouble {
			staticDescriptor = "(D)Z"
		}
		vm.RegisterNative(class, name, descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, _, err := read(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if name == "isNaN" {
				return boolValue(math.IsNaN(value)), true, nil
			}
			return boolValue(math.IsInf(value, 0)), true, nil
		})
		vm.RegisterNative(class, name, staticDescriptor, func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			var value float64
			var err error
			if kind == ValueFloat {
				var single float32
				single, err = args[0].Float()
				value = float64(single)
			} else {
				value, err = args[0].Double()
			}
			if err != nil {
				return Value{}, false, err
			}
			if name == "isNaN" {
				return boolValue(math.IsNaN(value)), true, nil
			}
			return boolValue(math.IsInf(value, 0)), true, nil
		})
	}
}

func (vm *VM) parseFloating(boxed, double bool) NativeFunc {
	return func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		text, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		bits := 32
		class := "java/lang/Float"
		if double {
			bits, class = 64, "java/lang/Double"
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(text), bits)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", err.Error())
		}
		result := DoubleValue(value)
		if !double {
			result = FloatValue(float32(value))
		}
		if boxed {
			return ReferenceValue(vm.newWrapper(class, result)), true, nil
		}
		return result, true, nil
	}
}

func (vm *VM) staticFloatingToString(double bool) NativeFunc {
	return func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		bits := 32
		var value float64
		var err error
		if double {
			bits = 64
			value, err = args[0].Double()
		} else {
			var single float32
			single, err = args[0].Float()
			value = float64(single)
		}
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(javaFloatText(value, bits))), true, nil
	}
}

func (vm *VM) installCLDCMathNatives() {
	vm.RegisterStaticField("java/lang/Math", "E", "D", DoubleValue(math.E))
	vm.RegisterStaticField("java/lang/Math", "PI", "D", DoubleValue(math.Pi))
	for _, method := range []struct {
		name string
		fn   func(float64) float64
	}{{"ceil", math.Ceil}, {"cos", math.Cos}, {"floor", math.Floor}, {"sin", math.Sin}, {"sqrt", math.Sqrt}, {"tan", math.Tan}, {"toDegrees", func(v float64) float64 { return v * 180 / math.Pi }}, {"toRadians", func(v float64) float64 { return v * math.Pi / 180 }}} {
		method := method
		vm.RegisterNative("java/lang/Math", method.name, "(D)D", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := args[0].Double()
			if err != nil {
				return Value{}, false, err
			}
			return DoubleValue(method.fn(value)), true, nil
		})
	}
	vm.RegisterNative("java/lang/Math", "abs", "(F)F", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := args[0].Float()
		return FloatValue(float32(math.Abs(float64(value)))), err == nil, err
	})
	vm.RegisterNative("java/lang/Math", "abs", "(D)D", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := args[0].Double()
		return DoubleValue(math.Abs(value)), err == nil, err
	})
	for _, descriptor := range []string{"(FF)F", "(DD)D"} {
		for _, name := range []string{"min", "max"} {
			descriptor, name := descriptor, name
			vm.RegisterNative("java/lang/Math", name, descriptor, func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
				if descriptor == "(FF)F" {
					left, err := args[0].Float()
					if err != nil {
						return Value{}, false, err
					}
					right, err := args[1].Float()
					if err != nil {
						return Value{}, false, err
					}
					if name == "min" {
						return FloatValue(float32(math.Min(float64(left), float64(right)))), true, nil
					}
					return FloatValue(float32(math.Max(float64(left), float64(right)))), true, nil
				}
				left, err := args[0].Double()
				if err != nil {
					return Value{}, false, err
				}
				right, err := args[1].Double()
				if err != nil {
					return Value{}, false, err
				}
				if name == "min" {
					return DoubleValue(math.Min(left, right)), true, nil
				}
				return DoubleValue(math.Max(left, right)), true, nil
			})
		}
	}
}
