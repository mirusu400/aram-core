package skvm

import (
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestLGTGraphicsXXORIsContextScopedAndSaved(t *testing.T) {
	vm := mmppVM(t, NativePolicyLGT)
	first := vm.ScreenGraphics()
	second := vm.newGraphicsObject(&graphicsState{
		width: vm.ScreenWidth, height: vm.ScreenHeight, surface: vm.screenSurface,
		font: vm.defaultFont, color: 0xff000000,
	})
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, vm.screenSurface, shared.Color{A: 255}))
	invokeTestNative(t, vm, lgtGraphicsClass, "setXORMode", "(I)V", first, IntValue(0xff0000))
	if mode, err := invokeTestNative(t, vm, lgtGraphicsClass, "isXORMode", "()Z", first).Int(); err != nil || mode != 1 {
		t.Fatalf("XOR mode = %d, %v", mode, err)
	}
	fill := func(ref uint32, x int32) {
		invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "fillRect", "(IIII)V", ref,
			IntValue(x), IntValue(0), IntValue(1), IntValue(1))
	}
	fill(first, 0)
	if got := midpPixel(t, vm, vm.screenSurface, 0, 0); got != (shared.Color{R: 255, A: 255}) {
		t.Fatalf("first XOR draw = %+v", got)
	}
	state, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(state))
	fill(first, 0)
	if got := midpPixel(t, vm, vm.screenSurface, 0, 0); got != (shared.Color{A: 255}) {
		t.Fatalf("XOR replay = %+v", got)
	}
	invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "setColor", "(I)V", second, IntValue(0xffffff))
	fill(second, 1)
	if got := midpPixel(t, vm, vm.screenSurface, 1, 0); got != (shared.Color{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("XOR leaked to second context: %+v", got)
	}
	invokeTestNative(t, vm, lgtGraphicsClass, "setPaintMode", "()V", first)
	if mode, err := invokeTestNative(t, vm, lgtGraphicsClass, "isXORMode", "()Z", first).Int(); err != nil || mode != 0 {
		t.Fatalf("paint mode = %d, %v", mode, err)
	}
	fill(first, 2)
	if got := midpPixel(t, vm, vm.screenSurface, 2, 0); got != (shared.Color{R: 255, A: 255}) {
		t.Fatalf("paint draw = %+v", got)
	}
}
