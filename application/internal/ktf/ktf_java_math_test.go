package ktf

import (
	"math"
	"strings"
	"testing"
)

func callKTFMath(
	t *testing.T,
	runtime *Runtime,
	name, descriptor string,
	words ...uint32,
) uint64 {
	t.Helper()
	runtime.NativeParameterBase = allocWords(t, runtime, uint32(len(words)))
	check(t, runtime.writeWords(runtime.NativeParameterBase, words))
	low, err := runtime.handleMathMethod(name, descriptor)
	check(t, err)
	return uint64(runtime.JavaReturnHigh)<<32 | uint64(low)
}

func ktfWideWords(bits uint64) (uint32, uint32) {
	return uint32(bits), uint32(bits >> 32)
}

func TestKTFMathIntegerAndLongOverloads(t *testing.T) {
	runtime := newTestRuntime(t)
	if got := uint32(callKTFMath(t, runtime, "abs", "(I)I", ^uint32(6))); got != 7 {
		t.Fatalf("Math.abs(-7) = %d", got)
	}

	negativeNine := int64(-9)
	low, high := ktfWideWords(uint64(negativeNine))
	if got := int64(callKTFMath(t, runtime, "abs", "(J)J", low, high)); got != 9 {
		t.Fatalf("Math.abs(-9L) = %d", got)
	}
	low, high = ktfWideWords(17)
	negativeFour := int64(-4)
	rightLow, rightHigh := ktfWideWords(uint64(negativeFour))
	if got := int64(callKTFMath(
		t,
		runtime,
		"min",
		"(JJ)J",
		low,
		high,
		rightLow,
		rightHigh,
	)); got != -4 {
		t.Fatalf("Math.min(17L, -4L) = %d", got)
	}
	if got := int64(callKTFMath(
		t,
		runtime,
		"max",
		"(JJ)J",
		low,
		high,
		rightLow,
		rightHigh,
	)); got != 17 {
		t.Fatalf("Math.max(17L, -4L) = %d", got)
	}
}

func TestKTFMathFloatingPointOverloads(t *testing.T) {
	runtime := newTestRuntime(t)
	if got := uint32(callKTFMath(
		t,
		runtime,
		"abs",
		"(F)F",
		math.Float32bits(-3.5),
	)); math.Float32frombits(got) != 3.5 {
		t.Fatalf("Math.abs(-3.5f) = %g", math.Float32frombits(got))
	}
	negativeZero32 := math.Float32bits(float32(math.Copysign(0, -1)))
	positiveZero32 := math.Float32bits(0)
	if got := uint32(callKTFMath(
		t,
		runtime,
		"max",
		"(FF)F",
		negativeZero32,
		positiveZero32,
	)); got != positiveZero32 {
		t.Fatalf("Math.max(-0f, +0f) bits = 0x%08x", got)
	}
	if got := uint32(callKTFMath(
		t,
		runtime,
		"min",
		"(FF)F",
		negativeZero32,
		positiveZero32,
	)); got != negativeZero32 {
		t.Fatalf("Math.min(-0f, +0f) bits = 0x%08x", got)
	}
	const floatNaN = uint32(0x7fc12345)
	if got := uint32(callKTFMath(
		t,
		runtime,
		"max",
		"(FF)F",
		math.Float32bits(1),
		floatNaN,
	)); got != floatNaN {
		t.Fatalf("Math.max(1f, NaN) bits = 0x%08x", got)
	}

	negativeZero64 := math.Float64bits(math.Copysign(0, -1))
	positiveZero64 := math.Float64bits(0)
	absLow, absHigh := ktfWideWords(math.Float64bits(-7.25))
	if got := math.Float64frombits(callKTFMath(
		t,
		runtime,
		"abs",
		"(D)D",
		absLow,
		absHigh,
	)); got != 7.25 {
		t.Fatalf("Math.abs(-7.25d) = %g", got)
	}
	leftLow, leftHigh := ktfWideWords(negativeZero64)
	rightLow, rightHigh := ktfWideWords(positiveZero64)
	if got := callKTFMath(
		t,
		runtime,
		"max",
		"(DD)D",
		leftLow,
		leftHigh,
		rightLow,
		rightHigh,
	); got != positiveZero64 {
		t.Fatalf("Math.max(-0d, +0d) bits = 0x%016x", got)
	}
	if got := callKTFMath(
		t,
		runtime,
		"min",
		"(DD)D",
		leftLow,
		leftHigh,
		rightLow,
		rightHigh,
	); got != negativeZero64 {
		t.Fatalf("Math.min(-0d, +0d) bits = 0x%016x", got)
	}
	const doubleNaN = uint64(0x7ff8123456789abc)
	nanLow, nanHigh := ktfWideWords(doubleNaN)
	oneLow, oneHigh := ktfWideWords(math.Float64bits(1))
	if got := callKTFMath(
		t,
		runtime,
		"min",
		"(DD)D",
		nanLow,
		nanHigh,
		oneLow,
		oneHigh,
	); got != doubleNaN {
		t.Fatalf("Math.min(NaN, 1d) bits = 0x%016x", got)
	}

	for _, test := range []struct {
		name  string
		input float64
		want  float64
	}{
		{name: "ceil", input: 1.25, want: 2},
		{name: "floor", input: -1.25, want: -2},
		{name: "sqrt", input: 9, want: 3},
		{name: "sin", input: math.Pi / 2, want: 1},
		{name: "cos", input: 0, want: 1},
		{name: "tan", input: 0, want: 0},
		{name: "toDegrees", input: math.Pi, want: 180},
		{name: "toRadians", input: 180, want: math.Pi},
	} {
		valueLow, valueHigh := ktfWideWords(math.Float64bits(test.input))
		got := math.Float64frombits(callKTFMath(
			t,
			runtime,
			test.name,
			"(D)D",
			valueLow,
			valueHigh,
		))
		if math.Abs(got-test.want) > 1e-15 {
			t.Errorf("Math.%s(%g) = %.17g, want %.17g", test.name, test.input, got, test.want)
		}
	}
}

func TestKTFMathHostSpecCoversHandlerAndRejectsUnknownMethods(t *testing.T) {
	for _, signature := range []string{
		"abs(I)I", "abs(J)J", "abs(F)F", "abs(D)D",
		"min(II)I", "min(JJ)J", "min(FF)F", "min(DD)D",
		"max(II)I", "max(JJ)J", "max(FF)F", "max(DD)D",
		"ceil(D)D", "floor(D)D", "sqrt(D)D", "sin(D)D", "cos(D)D",
		"tan(D)D", "toDegrees(D)D", "toRadians(D)D",
	} {
		if _, ok := ktfJavaNativeOverride("java/lang/Math." + signature); !ok {
			t.Errorf("native override missing for Math.%s", signature)
		}
	}

	runtime := newTestRuntime(t)
	runtime.NativeParameterBase = allocWords(t, runtime, 1)
	_, err := runtime.handleMathMethod("unknown", "(I)I")
	if err == nil || !strings.Contains(err.Error(), "Math.unknown(I)I") {
		t.Fatalf("unknown Math method error = %v", err)
	}
}
