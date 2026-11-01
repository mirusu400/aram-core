package skvm

import "testing"

// 게임빌 2006프로야구 builds its one-digit answer box as
// TextFieldX("", "", 1, 2) and feeds getString() straight to
// Integer.parseInt. Constraint 2 is MIDP NUMERIC: the field must start in the
// numeric mode, refuse to leave it, and accept digits only. Ignoring it let a
// Hangul syllable in and the parse threw NumberFormatException.
func TestLGTTextFieldXNumericConstraintAcceptsDigitsOnly(t *testing.T) {
	for _, constraints := range []int32{2, 0x10000 | 2} {
		vm := policyRegressionVM(t, NativePolicyLGT)
		field, err := vm.allocateObject(lgtTextFieldClass)
		check(t, err)
		call := func(name, descriptor string, args ...Value) Value {
			t.Helper()
			return invokeTestNative(t, vm, lgtTextFieldClass, name, descriptor, field, args...)
		}
		read := func() string {
			t.Helper()
			ref, err := call("getString", "()Ljava/lang/String;").Reference()
			check(t, err)
			value, err := vm.String(ref)
			check(t, err)
			return value
		}
		call("<init>", "(Ljava/lang/String;Ljava/lang/String;II)V",
			ReferenceValue(vm.NewString("")), ReferenceValue(vm.NewString("")),
			IntValue(2), IntValue(constraints))
		call("setFocus", "(Z)V", IntValue(1))
		if mode, err := call("getInputMode", "()I").Int(); err != nil || mode != lgtModeNumeric {
			t.Fatalf("constraints %#x: initial mode = %d, %v; want numeric", constraints, mode, err)
		}
		if mode, err := call("nextInputMode", "()I").Int(); err != nil || mode != lgtModeNumeric {
			t.Fatalf("constraints %#x: next mode = %d, %v; want numeric", constraints, mode, err)
		}
		// The mode key, the space key and the soft keys must not smuggle in
		// anything a parseInt would reject.
		for _, key := range []int32{'*', '#', -6, -7} {
			call("keyPressed", "(I)V", IntValue(key))
		}
		if got := read(); got != "" {
			t.Fatalf("constraints %#x: non-digit keys produced %q", constraints, got)
		}
		if mode, err := call("getInputMode", "()I").Int(); err != nil || mode != lgtModeNumeric {
			t.Fatalf("constraints %#x: mode after mode key = %d, %v", constraints, mode, err)
		}
		for _, key := range []int32{'1', '1', '7'} {
			call("keyPressed", "(I)V", IntValue(key))
		}
		if got := read(); got != "11" {
			t.Fatalf("constraints %#x: digits = %q, want %q", constraints, got, "11")
		}
		call("keyPressed", "(I)V", IntValue(-8))
		call("keyPressed", "(I)V", IntValue('4'))
		if got := read(); got != "14" {
			t.Fatalf("constraints %#x: after clear and digit = %q", constraints, got)
		}
	}
}
