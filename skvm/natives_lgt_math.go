package skvm

import (
	"context"
	"math/big"
)

const lgtMathClass = "mmpp/lang/MathFP"

// installLGTMathNatives implements only integer-based signed 20.12 operations.
// See docs/lgt-mathfp.md for documented guarantees versus emulator choices.
func (vm *VM) installLGTMathNatives() {
	vm.RegisterHostClass(lgtMathClass, "java/lang/Object")
	for name, value := range map[string]int32{
		"E": 11134, "PI": 12868,
		"MAX_VALUE": 2147483647, "MIN_VALUE": -2147483648,
		"MAX_VALUE_INT": 524287, "MIN_VALUE_INT": -524288,
	} {
		vm.hostStatic[fieldStorageKey(lgtMathClass, name, "I")] = IntValue(value)
	}
	for _, name := range []string{"parseFP", "toInt", "round", "abs"} {
		name := name
		vm.RegisterNative(lgtMathClass, name, "(I)I", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			a, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			var result int32
			switch name {
			case "parseFP":
				if a < -524288 || a > 524287 {
					return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", "MathFP integer out of range")
				}
				result = a * 4096
			case "toInt":
				result = int32((int64(a) + 2048) >> 12)
			case "round":
				result = int32(((int64(a) + 2048) >> 12) * 4096)
			case "abs":
				result = a
				if a < 0 {
					result = -a
				}
			}
			return IntValue(result), true, nil
		})
	}
	vm.RegisterNative(lgtMathClass, "parseFP", "(Ljava/lang/String;)I", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		text, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		value, ok := parseLGTMathDecimal(text)
		if !ok {
			return Value{}, false, vm.newThrowable("java/lang/NumberFormatException", "MathFP decimal out of range or malformed")
		}
		return IntValue(value), true, nil
	})
	for _, name := range []string{"add", "sub", "min", "max", "multiply", "divide"} {
		name := name
		vm.RegisterNative(lgtMathClass, name, "(II)I", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			a, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			b, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			var result int32
			switch name {
			case "add":
				result = a + b
			case "sub":
				result = a - b
			case "min":
				result = a
				if b < a {
					result = b
				}
			case "max":
				result = a
				if b > a {
					result = b
				}
			case "multiply":
				// Compare the exact wide product before quantization, including fractions
				// beyond an endpoint that truncation could otherwise hide.
				product := int64(a) * int64(b)
				if product < int64(-2147483648)*4096 || product > int64(2147483647)*4096 {
					return Value{}, false, vm.newThrowable("java/lang/ArithmeticException", "MathFP multiply overflow")
				}
				result = int32(product / 4096)
			case "divide":
				if b == 0 {
					return Value{}, false, vm.newThrowable("java/lang/ArithmeticException", "MathFP divide by zero")
				}
				result = int32((int64(a) * 4096) / int64(b))
			}
			return IntValue(result), true, nil
		})
	}
}

// parseLGTMathDecimal converts a plain signed decimal to a 20.12 word without
// floating-point rounding. Fractional raw units are discarded toward zero.
func parseLGTMathDecimal(text string) (int32, bool) {
	if len(text) == 0 || len(text) > 4096 {
		return 0, false
	}
	start := 0
	if text[0] == '+' || text[0] == '-' {
		start = 1
	}
	digits, dots := 0, 0
	for i := start; i < len(text); i++ {
		switch {
		case text[i] >= '0' && text[i] <= '9':
			digits++
		case text[i] == '.':
			dots++
		default:
			return 0, false
		}
	}
	if digits == 0 || dots > 1 {
		return 0, false
	}
	decimal, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, false
	}
	raw := new(big.Rat).Mul(decimal, big.NewRat(4096, 1))
	if raw.Cmp(new(big.Rat).SetInt64(-2147483648)) < 0 || raw.Cmp(new(big.Rat).SetInt64(2147483647)) > 0 {
		return 0, false
	}
	word := new(big.Int).Quo(raw.Num(), raw.Denom())
	return int32(word.Int64()), true
}
