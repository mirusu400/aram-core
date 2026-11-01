package skvm

import (
	"context"
	"math"
	"strconv"
	"strings"
)

const sktMathClass = "com/skt/m/MathFP"

// installSKTMathNatives implements com/skt/m/MathFP. Unlike LGT's 20.12
// mmpp/lang/MathFP, an SKT MathFP value is the IEEE-754 binary64 bit pattern
// of a double carried in a long: titles compiled against the SKT class inline
// MathFP.PI as 0x400921fb54442d18, which is exactly Double.doubleToLongBits(PI).
// Every operation therefore decodes its operands, does the double arithmetic,
// and re-encodes the result.
func (vm *VM) installSKTMathNatives() {
	vm.RegisterHostClass(sktMathClass, "java/lang/Object")
	vm.RegisterStaticField(sktMathClass, "PI", "J", LongValue(int64(math.Float64bits(math.Pi))))
	vm.RegisterStaticField(sktMathClass, "E", "J", LongValue(int64(math.Float64bits(math.E))))

	unary := []struct {
		name string
		fn   func(float64) float64
	}{
		{"abs", math.Abs},
		{"acos", math.Acos},
		{"asin", math.Asin},
		{"atan", math.Atan},
		{"ceil", math.Ceil},
		{"cos", math.Cos},
		{"exp", math.Exp},
		{"floor", math.Floor},
		{"log", math.Log},
		// round keeps the value in MathFP form; toLong converts it.
		{"round", func(value float64) float64 { return math.Floor(value + 0.5) }},
		{"sin", math.Sin},
		{"sqrt", math.Sqrt},
		{"tan", math.Tan},
	}
	for _, method := range unary {
		fn := method.fn
		vm.RegisterNative(sktMathClass, method.name, "(J)J", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := sktMathArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return sktMathValue(fn(value)), true, nil
		})
	}

	binary := []struct {
		name string
		fn   func(float64, float64) float64
	}{
		{"add", func(left, right float64) float64 { return left + right }},
		{"atan2", math.Atan2},
		{"divide", func(left, right float64) float64 { return left / right }},
		{"max", math.Max},
		{"min", math.Min},
		{"multiply", func(left, right float64) float64 { return left * right }},
		{"pow", math.Pow},
		{"sub", func(left, right float64) float64 { return left - right }},
	}
	for _, method := range binary {
		fn := method.fn
		vm.RegisterNative(sktMathClass, method.name, "(JJ)J", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			left, err := sktMathArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			right, err := sktMathArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			return sktMathValue(fn(left, right)), true, nil
		})
	}

	vm.RegisterNative(sktMathClass, "parseFP", "(J)J", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := longArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return sktMathValue(float64(value)), true, nil
	})
	vm.RegisterNative(sktMathClass, "parseFPString", "(Ljava/lang/String;)J", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		text, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		value, parseErr := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if parseErr != nil {
			return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", text)
		}
		return sktMathValue(value), true, nil
	})
	vm.RegisterNative(sktMathClass, "toLong", "(J)J", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := sktMathArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		// A Java (long) cast: truncate toward zero, NaN to 0, saturate.
		return LongValue(javaLongFromFloat(value)), true, nil
	})
	vm.RegisterNative(sktMathClass, "toString", "(J)Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		value, err := sktMathArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(javaFloatText(value, 64))), true, nil
	})
}

func sktMathArgument(args []Value, index int) (float64, error) {
	bits, err := longArgument(args, index)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(uint64(bits)), nil
}

func sktMathValue(value float64) Value {
	return LongValue(int64(math.Float64bits(value)))
}
