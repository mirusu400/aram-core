package skvm

import (
	"context"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestLGTTextFieldXEditsPaintsAndRestores(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	field, err := vm.allocateObject(lgtTextFieldClass)
	check(t, err)
	if !vm.IsInstance(field, lgtTextFieldClass) || !vm.IsInstance(field, "java/lang/Object") {
		t.Fatal("TextFieldX is not an LGT host object")
	}
	call := func(name, descriptor string, args ...Value) Value {
		t.Helper()
		return invokeTestNative(t, vm, lgtTextFieldClass, name, descriptor, field, args...)
	}
	read := func() string {
		t.Helper()
		value := call("getString", "()Ljava/lang/String;")
		reference, err := value.Reference()
		check(t, err)
		text, err := vm.String(reference)
		check(t, err)
		return text
	}
	call("<init>", "(Ljava/lang/String;Ljava/lang/String;II)V",
		ReferenceValue(vm.NewString("")), ReferenceValue(vm.NewString("AB")), IntValue(10), IntValue(0))
	if caret, err := call("getCaretPosition", "()I").Int(); err != nil || caret != 2 {
		t.Fatalf("initial caret = %d, %v", caret, err)
	}
	defaultFont, err := call("getFont", "()Ljavax/microedition/lcdui/Font;").Reference()
	check(t, err)
	if !vm.IsInstance(defaultFont, "javax/microedition/lcdui/Font") {
		t.Fatal("TextFieldX default font is not a MIDP Font")
	}
	customValue := invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "getFont",
		"(III)Ljavax/microedition/lcdui/Font;", 0, IntValue(0), IntValue(1), IntValue(16))
	customFont, err := customValue.Reference()
	check(t, err)
	call("setFont", "(Ljavax/microedition/lcdui/Font;)V", customValue)
	if got, err := call("getFont", "()Ljavax/microedition/lcdui/Font;").Reference(); err != nil || got != customFont {
		t.Fatalf("TextFieldX selected font = %d, %v, want %d", got, err, customFont)
	}
	if mode, err := call("getInputMode", "()I").Int(); err != nil || mode != lgtModeNone {
		t.Fatalf("unfocused mode = %d, %v", mode, err)
	}
	owner := vm.NewObject("javax/microedition/lcdui/Canvas", nil)
	call("setOwner", "(Ljavax/microedition/lcdui/Canvas;)V", ReferenceValue(owner))
	call("setWidth", "(I)V", IntValue(118))
	call("setMaxRow", "(I)V", IntValue(1))
	call("setFocus", "(Z)V", IntValue(1))
	if mode, err := call("nextInputMode", "()I").Int(); err != nil || mode != lgtModeCaps {
		t.Fatalf("next input mode = %d, %v", mode, err)
	}
	call("keyPressed", "(I)V", IntValue('2'))
	if got := read(); got != "ABA" {
		t.Fatalf("first multi-tap = %q", got)
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	if got, err := call("getFont", "()Ljavax/microedition/lcdui/Font;").Reference(); err != nil || got != customFont {
		t.Fatalf("restored TextFieldX font = %d, %v, want %d", got, err, customFont)
	}
	call("keyPressed", "(I)V", IntValue('2'))
	if got := read(); got != "ABB" {
		t.Fatalf("multi-tap after restore = %q", got)
	}
	if caret, err := call("getCaretPosition", "()I").Int(); err != nil || caret != 3 {
		t.Fatalf("restored caret = %d, %v", caret, err)
	}
	graphicsRef, graphics := midpGraphicsSurface(t, vm)
	previousFont := graphics.font
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, graphics.surface,
		shared.Color{R: 255, G: 255, B: 255, A: 255}))
	call("paint", "(Ljavax/microedition/lcdui/Graphics;)V", ReferenceValue(graphicsRef))
	if graphics.font != previousFont {
		t.Fatal("TextFieldX paint changed the caller's Graphics font")
	}
	changed := false
	for y := int32(0); y < 20 && !changed; y++ {
		for x := int32(0); x < 50; x++ {
			if pixel := midpPixel(t, vm, graphics.surface, x, y); pixel.R != 255 || pixel.G != 255 || pixel.B != 255 {
				changed = true
				break
			}
		}
	}
	if !changed {
		t.Fatal("TextFieldX paint did not draw its text")
	}
	call("setFont", "(Ljavax/microedition/lcdui/Font;)V", ReferenceValue(0))
	if got, err := call("getFont", "()Ljavax/microedition/lcdui/Font;").Reference(); err != nil || got == 0 || got == customFont {
		t.Fatalf("TextFieldX reset font = %d, %v", got, err)
	}
	call("setFocus", "(Z)V", IntValue(0))
	call("keyPressed", "(I)V", IntValue('3'))
	if got := read(); got != "ABB" {
		t.Fatalf("unfocused edit changed text to %q", got)
	}
	if mode, err := call("nextInputMode", "()I").Int(); err != nil || mode != lgtModeNone {
		t.Fatalf("unfocused next mode = %d, %v", mode, err)
	}
	if _, _, err := vm.natives[nativeKey{lgtTextFieldClass, "setWidth", "(I)V"}](
		context.Background(), vm, field, []Value{IntValue(0)}); err == nil {
		t.Fatal("zero TextFieldX width was accepted")
	}
}

func TestLGTTextFieldXSetStringResetsComposition(t *testing.T) {
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
		ReferenceValue(vm.NewString("")), ReferenceValue(vm.NewString("AB")), IntValue(3), IntValue(0))
	call("setFocus", "(Z)V", IntValue(1))
	call("nextInputMode", "()I")
	call("keyPressed", "(I)V", IntValue('2'))
	if got := read(); got != "ABA" {
		t.Fatalf("composing text = %q", got)
	}
	call("setString", "(Ljava/lang/String;)V", ReferenceValue(vm.NewString("XY")))
	call("keyPressed", "(I)V", IntValue('2'))
	if got := read(); got != "XYA" {
		t.Fatalf("text after replacement and key = %q", got)
	}
	_, _, err = vm.natives[nativeKey{lgtTextFieldClass, "setString", "(Ljava/lang/String;)V"}](
		context.Background(), vm, field, []Value{ReferenceValue(vm.NewString("ABCD"))})
	if err == nil || read() != "XYA" {
		t.Fatalf("oversized replacement changed text: %q, %v", read(), err)
	}
	call("setString", "(Ljava/lang/String;)V", ReferenceValue(0))
	if got := read(); got != "" {
		t.Fatalf("null replacement = %q", got)
	}
	if caret, err := call("getCaretPosition", "()I").Int(); err != nil || caret != 0 {
		t.Fatalf("caret after clearing = %d, %v", caret, err)
	}
}

func TestLGTTextFieldXDimensionsFocusAndSize(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	field, err := vm.allocateObject(lgtTextFieldClass)
	check(t, err)
	call := func(name, descriptor string, args ...Value) Value {
		t.Helper()
		return invokeTestNative(t, vm, lgtTextFieldClass, name, descriptor, field, args...)
	}
	read := func(name, descriptor string) int32 {
		t.Helper()
		value, err := call(name, descriptor).Int()
		check(t, err)
		return value
	}
	call("<init>", "(Ljava/lang/String;Ljava/lang/String;II)V",
		ReferenceValue(vm.NewString("")), ReferenceValue(vm.NewString("A😀")), IntValue(10), IntValue(0))
	if got := read("size", "()I"); got != 3 {
		t.Fatalf("UTF-16 TextFieldX size = %d, want 3", got)
	}
	if got := read("hasFocus", "()Z"); got != 0 {
		t.Fatalf("initial TextFieldX focus = %d, want 0", got)
	}
	call("setWidth", "(I)V", IntValue(118))
	call("setMaxRow", "(I)V", IntValue(2))
	call("setFocus", "(Z)V", IntValue(1))
	font := call("getFont", "()Ljavax/microedition/lcdui/Font;")
	fontReference, err := font.Reference()
	check(t, err)
	fontHeight, err := invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "getHeight", "()I", fontReference).Int()
	check(t, err)
	if got := read("getWidth", "()I"); got != 118 {
		t.Fatalf("TextFieldX width = %d, want 118", got)
	}
	if got := read("getHeight", "()I"); got != 2*fontHeight {
		t.Fatalf("TextFieldX height = %d, want %d", got, 2*fontHeight)
	}
	customFont := invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "getFont",
		"(III)Ljavax/microedition/lcdui/Font;", 0, IntValue(0), IntValue(1), IntValue(16))
	customReference, err := customFont.Reference()
	check(t, err)
	customHeight, err := invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "getHeight", "()I", customReference).Int()
	check(t, err)
	call("setFont", "(Ljavax/microedition/lcdui/Font;)V", customFont)
	if got := read("getHeight", "()I"); got != 2*customHeight {
		t.Fatalf("TextFieldX custom-font height = %d, want %d", got, 2*customHeight)
	}
	if got := read("hasFocus", "()Z"); got != 1 {
		t.Fatalf("focused TextFieldX state = %d, want 1", got)
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	if got := read("getWidth", "()I"); got != 118 {
		t.Fatalf("restored TextFieldX width = %d", got)
	}
	if got := read("hasFocus", "()Z"); got != 1 {
		t.Fatalf("restored TextFieldX focus = %d", got)
	}
	call("setFocus", "(Z)V", IntValue(0))
	call("setString", "(Ljava/lang/String;)V", ReferenceValue(vm.NewString("")))
	if got := read("hasFocus", "()Z"); got != 0 {
		t.Fatalf("cleared TextFieldX focus = %d", got)
	}
	if got := read("size", "()I"); got != 0 {
		t.Fatalf("cleared TextFieldX size = %d", got)
	}
}

func TestLGTTextFieldXDoesNotLeakToOtherPolicies(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME} {
		vm := policyRegressionVM(t, policy)
		if _, exists := vm.hostSupers[lgtTextFieldClass]; exists {
			t.Fatalf("TextFieldX host class leaked to policy %d", policy)
		}
		if vm.SupportsNativeReference(lgtTextFieldClass, "keyPressed", "(I)V") {
			t.Fatalf("TextFieldX keyboard native leaked to policy %d", policy)
		}
	}
}
