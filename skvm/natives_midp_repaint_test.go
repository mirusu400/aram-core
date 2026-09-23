package skvm

import (
	"context"
	"testing"
)

func TestMIDPRepaintRequestsAreScheduledAndSaved(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyJ2ME)
	display := vm.NewObject("javax/microedition/lcdui/Display", nil)
	canvas := vm.NewObject("javax/microedition/lcdui/Canvas", nil)
	hidden := vm.NewObject("javax/microedition/lcdui/Canvas", nil)

	invokeTestNative(
		t, vm,
		"javax/microedition/lcdui/Display", "setCurrent",
		"(Ljavax/microedition/lcdui/Displayable;)V",
		display, ReferenceValue(canvas),
	)
	if !vm.RepaintPending() {
		t.Fatal("setCurrent did not request the initial paint")
	}
	vm.setRepaintPending(false)

	invokeTestNative(
		t, vm,
		"javax/microedition/lcdui/Canvas", "repaint", "()V",
		hidden,
	)
	if vm.RepaintPending() {
		t.Fatal("a hidden Canvas scheduled a paint")
	}
	invokeTestNative(
		t, vm,
		"javax/microedition/lcdui/Canvas", "repaint", "()V",
		canvas,
	)
	if !vm.RepaintPending() {
		t.Fatal("the current Canvas did not schedule a paint")
	}

	saved, err := vm.MarshalBinary()
	check(t, err)
	vm.setRepaintPending(false)
	check(t, vm.UnmarshalBinary(saved))
	if !vm.RepaintPending() {
		t.Fatal("save-state restore lost a pending repaint")
	}

	// With no pending request serviceRepaints must return without trying to
	// invoke the abstract Canvas paint method.
	vm.setRepaintPending(false)
	service := vm.natives[nativeKey{
		class: "javax/microedition/lcdui/Canvas", name: "serviceRepaints", descriptor: "()V",
	}]
	if _, _, err := service(context.Background(), vm, canvas, nil); err != nil {
		t.Fatalf("serviceRepaints without a request: %v", err)
	}
}
