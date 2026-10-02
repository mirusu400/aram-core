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
	call("keyPressed", "(I)V", IntValue('2'))
	if got := read(); got != "ABB" {
		t.Fatalf("multi-tap after restore = %q", got)
	}
	graphicsRef, graphics := midpGraphicsSurface(t, vm)
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, graphics.surface,
		shared.Color{R: 255, G: 255, B: 255, A: 255}))
	call("paint", "(Ljavax/microedition/lcdui/Graphics;)V", ReferenceValue(graphicsRef))
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
