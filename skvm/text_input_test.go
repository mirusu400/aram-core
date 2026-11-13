package skvm

import "testing"

func TestLGTTextInputActivityTracksFocusAndStateRestore(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	field, err := vm.allocateObject(lgtTextFieldClass)
	check(t, err)
	call := func(name, descriptor string, args ...Value) {
		t.Helper()
		invokeTestNative(t, vm, lgtTextFieldClass, name, descriptor, field, args...)
	}
	call("<init>", "(Ljava/lang/String;Ljava/lang/String;II)V",
		ReferenceValue(vm.NewString("")), ReferenceValue(vm.NewString("")),
		IntValue(4), IntValue(0))
	if vm.TextInputActive() {
		t.Fatal("unfocused TextFieldX reported active text input")
	}

	call("setFocus", "(Z)V", IntValue(1))
	if !vm.TextInputActive() {
		t.Fatal("focused TextFieldX did not report active text input")
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	if !vm.TextInputActive() {
		t.Fatal("state restore lost focused text input")
	}

	call("setFocus", "(Z)V", IntValue(0))
	if vm.TextInputActive() {
		t.Fatal("blurred TextFieldX still reported active text input")
	}
}

func TestLGTTextInputActivityAcceptsTextFieldSubclass(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	const subclass = "GameTextField"
	vm.hostSupers[subclass] = lgtTextFieldClass
	field := vm.NewObject(subclass, nil)
	invokeTestNative(t, vm, lgtTextFieldClass, "<init>",
		"(Ljava/lang/String;Ljava/lang/String;II)V", field,
		ReferenceValue(vm.NewString("")), ReferenceValue(vm.NewString("")),
		IntValue(4), IntValue(0))
	invokeTestNative(t, vm, lgtTextFieldClass, "setFocus", "(Z)V", field,
		IntValue(1))
	if !vm.TextInputActive() {
		t.Fatal("focused TextFieldX subclass did not report active text input")
	}
}

func TestLGTTextInputKeySelectionKeepsFunctionKeysOnCanvas(t *testing.T) {
	for _, key := range []int32{'0', '5', '9', '*', '#'} {
		if !lgtTextInputKey(key) {
			t.Errorf("phone text key %d was not selected", key)
		}
	}
	for _, key := range []int32{-8, -7, -6, -5, -4, -3, -2, -1} {
		if lgtTextInputKey(key) {
			t.Errorf("function key %d was stolen from the owner Canvas", key)
		}
	}
}

func TestLGTFocusedTextInputReceivesPhoneKeysBeforeCanvas(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	field, err := vm.allocateObject(lgtTextFieldClass)
	check(t, err)
	call := func(name, descriptor string, args ...Value) Value {
		t.Helper()
		return invokeTestNative(t, vm, lgtTextFieldClass, name, descriptor, field, args...)
	}
	call("<init>", "(Ljava/lang/String;Ljava/lang/String;II)V",
		ReferenceValue(vm.NewString("")), ReferenceValue(vm.NewString("")),
		IntValue(4), IntValue(0))
	call("setFocus", "(Z)V", IntValue(1))
	vm.currentDisplay = vm.NewObject("javax/microedition/lcdui/Canvas", nil)
	for _, key := range []int32{'5', '1'} {
		check(t, vm.KeyEvent(t.Context(), key, true))
		check(t, vm.KeyEvent(t.Context(), key, false))
	}
	reference, err := call("getString", "()Ljava/lang/String;").Reference()
	check(t, err)
	text, err := vm.String(reference)
	check(t, err)
	if text != "니" {
		t.Fatalf("focused phone-key input = %q, want %q", text, "니")
	}
}
