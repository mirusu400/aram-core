package skvm

import (
	"math"
	"testing"
)

func TestCLDCFloatingWrappersPreserveJavaBitSemantics(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	negativeZero := invokeTestNative(t, vm, "java/lang/Float", "floatToIntBits", "(F)I", 0, FloatValue(float32(math.Copysign(0, -1))))
	bits, err := negativeZero.Int()
	check(t, err)
	if uint32(bits) != 0x80000000 {
		t.Fatalf("Float.floatToIntBits(-0.0) = %#x", uint32(bits))
	}
	nan := invokeTestNative(t, vm, "java/lang/Double", "doubleToLongBits", "(D)J", 0, DoubleValue(math.Float64frombits(0x7ff0000000000001)))
	nanBits, err := nan.Long()
	check(t, err)
	if uint64(nanBits) != 0x7ff8000000000000 {
		t.Fatalf("Double.doubleToLongBits(NaN) = %#x", uint64(nanBits))
	}
}

func TestCLDCCharacterAndMathBehavior(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	digit := invokeTestNative(t, vm, "java/lang/Character", "digit", "(CI)I", 0, IntValue('F'), IntValue(16))
	gotDigit, err := digit.Int()
	check(t, err)
	if gotDigit != 15 {
		t.Fatalf("Character.digit('F', 16) = %d", gotDigit)
	}
	sqrt := invokeTestNative(t, vm, "java/lang/Math", "sqrt", "(D)D", 0, DoubleValue(81))
	gotSqrt, err := sqrt.Double()
	check(t, err)
	if gotSqrt != 9 {
		t.Fatalf("Math.sqrt(81) = %g", gotSqrt)
	}
}
