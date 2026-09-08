package skvm

import (
	"context"
	"math"
	"testing"
)

func runFloatOpcode(t *testing.T, opcode byte, stack ...Value) Value {
	t.Helper()
	current := &frame{
		class:  &Class{Name: "FloatOpcodes"},
		method: Method{Name: "test", Descriptor: "()V", Code: []byte{opcode}},
		stack:  append([]Value(nil), stack...),
	}
	budget := uint64(1)
	if _, err := (&VM{}).step(context.Background(), current, &budget); err != nil {
		t.Fatalf("opcode 0x%02x failed: %v", opcode, err)
	}
	if len(current.stack) != 1 {
		t.Fatalf("opcode 0x%02x left %d stack values, want 1", opcode, len(current.stack))
	}
	return current.stack[0]
}

func TestFloatAndDoubleArithmeticOpcodes(t *testing.T) {
	tests := []struct {
		name     string
		opcode   byte
		left     Value
		right    Value
		expected Value
	}{
		{"fadd", 0x62, FloatValue(1.5), FloatValue(2.25), FloatValue(3.75)},
		{"dadd", 0x63, DoubleValue(1.5), DoubleValue(2.25), DoubleValue(3.75)},
		{"fsub", 0x66, FloatValue(5.5), FloatValue(2), FloatValue(3.5)},
		{"dsub", 0x67, DoubleValue(5.5), DoubleValue(2), DoubleValue(3.5)},
		{"fmul", 0x6a, FloatValue(1.5), FloatValue(4), FloatValue(6)},
		{"dmul", 0x6b, DoubleValue(1.5), DoubleValue(4), DoubleValue(6)},
		{"fdiv", 0x6e, FloatValue(7), FloatValue(2), FloatValue(3.5)},
		{"ddiv", 0x6f, DoubleValue(7), DoubleValue(2), DoubleValue(3.5)},
		{"frem", 0x72, FloatValue(-7.5), FloatValue(2), FloatValue(-1.5)},
		{"drem", 0x73, DoubleValue(-7.5), DoubleValue(2), DoubleValue(-1.5)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := runFloatOpcode(t, test.opcode, test.left, test.right)
			if actual != test.expected {
				t.Fatalf("got %v, want %v", actual, test.expected)
			}
		})
	}
}

func TestFloatAndDoubleNegationOpcodes(t *testing.T) {
	float := runFloatOpcode(t, 0x76, FloatValue(0))
	floatValue, err := float.Float()
	if err != nil {
		t.Fatal(err)
	}
	if !math.Signbit(float64(floatValue)) {
		t.Fatalf("fneg +0 produced %v, want -0", floatValue)
	}

	double := runFloatOpcode(t, 0x77, DoubleValue(math.Copysign(0, -1)))
	doubleValue, err := double.Double()
	if err != nil {
		t.Fatal(err)
	}
	if math.Signbit(doubleValue) {
		t.Fatalf("dneg -0 produced %v, want +0", doubleValue)
	}
}

func TestFloatConversionOpcodes(t *testing.T) {
	tests := []struct {
		name     string
		opcode   byte
		input    Value
		expected Value
	}{
		{"i2f", 0x86, IntValue(16_777_217), FloatValue(16_777_216)},
		{"i2d", 0x87, IntValue(-123), DoubleValue(-123)},
		{"l2f", 0x89, LongValue(16_777_217), FloatValue(16_777_216)},
		{"l2d", 0x8a, LongValue(9_007_199_254_740_993), DoubleValue(9_007_199_254_740_992)},
		{"f2i", 0x8b, FloatValue(-12.75), IntValue(-12)},
		{"f2l", 0x8c, FloatValue(123.75), LongValue(123)},
		{"f2d", 0x8d, FloatValue(1.25), DoubleValue(1.25)},
		{"d2i", 0x8e, DoubleValue(-12.75), IntValue(-12)},
		{"d2l", 0x8f, DoubleValue(123.75), LongValue(123)},
		{"d2f", 0x90, DoubleValue(1.25), FloatValue(1.25)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := runFloatOpcode(t, test.opcode, test.input)
			if actual != test.expected {
				t.Fatalf("got %v, want %v", actual, test.expected)
			}
		})
	}
}

func TestFloatToIntegerConversionsFollowJVMSaturationRules(t *testing.T) {
	tests := []struct {
		name     string
		opcode   byte
		input    Value
		expected Value
	}{
		{"f2i NaN", 0x8b, FloatValue(float32(math.NaN())), IntValue(0)},
		{"f2i positive infinity", 0x8b, FloatValue(float32(math.Inf(1))), IntValue(math.MaxInt32)},
		{"f2i negative infinity", 0x8b, FloatValue(float32(math.Inf(-1))), IntValue(math.MinInt32)},
		{"f2l NaN", 0x8c, FloatValue(float32(math.NaN())), LongValue(0)},
		{"f2l positive infinity", 0x8c, FloatValue(float32(math.Inf(1))), LongValue(math.MaxInt64)},
		{"f2l negative infinity", 0x8c, FloatValue(float32(math.Inf(-1))), LongValue(math.MinInt64)},
		{"d2i NaN", 0x8e, DoubleValue(math.NaN()), IntValue(0)},
		{"d2i positive overflow", 0x8e, DoubleValue(math.MaxFloat64), IntValue(math.MaxInt32)},
		{"d2i negative overflow", 0x8e, DoubleValue(-math.MaxFloat64), IntValue(math.MinInt32)},
		{"d2l NaN", 0x8f, DoubleValue(math.NaN()), LongValue(0)},
		{"d2l positive infinity", 0x8f, DoubleValue(math.Inf(1)), LongValue(math.MaxInt64)},
		{"d2l negative infinity", 0x8f, DoubleValue(math.Inf(-1)), LongValue(math.MinInt64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := runFloatOpcode(t, test.opcode, test.input)
			if actual != test.expected {
				t.Fatalf("got %v, want %v", actual, test.expected)
			}
		})
	}
}

func TestFloatDivisionAndRemainderUseIEEE754Rules(t *testing.T) {
	floatInfinity := runFloatOpcode(t, 0x6e, FloatValue(1), FloatValue(0))
	value, err := floatInfinity.Float()
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(float64(value), 1) {
		t.Fatalf("fdiv by zero produced %v, want +Inf", value)
	}

	doubleNaN := runFloatOpcode(t, 0x6f, DoubleValue(0), DoubleValue(0))
	double, err := doubleNaN.Double()
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(double) {
		t.Fatalf("ddiv zero by zero produced %v, want NaN", double)
	}

	floatNaN := runFloatOpcode(t, 0x72, FloatValue(1), FloatValue(0))
	float, err := floatNaN.Float()
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(float64(float)) {
		t.Fatalf("frem by zero produced %v, want NaN", float)
	}

	doubleNaN = runFloatOpcode(t, 0x73, DoubleValue(math.Inf(1)), DoubleValue(2))
	double, err = doubleNaN.Double()
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(double) {
		t.Fatalf("drem infinity produced %v, want NaN", double)
	}
}

func TestFloatComparisonOpcodes(t *testing.T) {
	tests := []struct {
		name     string
		opcode   byte
		left     Value
		right    Value
		expected int32
	}{
		{"fcmpl less", 0x95, FloatValue(1), FloatValue(2), -1},
		{"fcmpg greater", 0x96, FloatValue(2), FloatValue(1), 1},
		{"dcmpl equal", 0x97, DoubleValue(2), DoubleValue(2), 0},
		{"dcmpg less", 0x98, DoubleValue(-1), DoubleValue(2), -1},
		{"fcmpl NaN", 0x95, FloatValue(float32(math.NaN())), FloatValue(0), -1},
		{"fcmpg NaN", 0x96, FloatValue(0), FloatValue(float32(math.NaN())), 1},
		{"dcmpl NaN", 0x97, DoubleValue(math.NaN()), DoubleValue(0), -1},
		{"dcmpg NaN", 0x98, DoubleValue(0), DoubleValue(math.NaN()), 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := runFloatOpcode(t, test.opcode, test.left, test.right)
			comparison, err := actual.Int()
			if err != nil {
				t.Fatal(err)
			}
			if comparison != test.expected {
				t.Fatalf("got %d, want %d", comparison, test.expected)
			}
		})
	}
}
