package skvm

import (
	"context"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestLGTGraphicsXPixelAndCapture(t *testing.T) {
	vm := mmppVM(t, NativePolicyLGT)
	graphics := vm.ScreenGraphics()
	draw, err := vm.services.Graphics.DrawState(vm.serviceOwner, vm.screenSurface)
	check(t, err)
	draw.TranslateX, draw.TranslateY = 4, 6
	draw.Clip = shared.Rectangle{X: 4, Y: 6, Width: 2, Height: 2}
	check(t, vm.services.Graphics.SetDrawState(vm.serviceOwner, vm.screenSurface, draw))
	color := shared.Color{A: 255, R: 0x12, G: 0x34, B: 0x56}
	check(t, vm.services.Graphics.SetPixel(vm.serviceOwner, vm.screenSurface, 0, 0, color))
	got := invokeTestNative(t, vm, lgtGraphicsClass, "getPixel", "(II)I", graphics, IntValue(0), IntValue(0))
	if pixel, err := got.Int(); err != nil || pixel != 0x123456 {
		t.Fatalf("getPixel = %#x, %v", pixel, err)
	}
	value := invokeTestNative(t, vm, lgtGraphicsClass, "capture", "(IIII)Ljavax/microedition/lcdui/Image;", graphics,
		IntValue(0), IntValue(0), IntValue(2), IntValue(2))
	reference, err := value.Reference()
	check(t, err)
	image, err := vm.image(reference)
	check(t, err)
	if image.width != 2 || image.height != 2 || vm.imageMutable(reference) {
		t.Fatalf("captured image = %dx%d mutable=%v", image.width, image.height, vm.imageMutable(reference))
	}
	copyColor, err := vm.services.Graphics.Pixel(vm.serviceOwner, image.surface, 0, 0)
	check(t, err)
	if copyColor != color {
		t.Fatalf("captured color = %+v", copyColor)
	}
	check(t, vm.services.Graphics.SetPixel(vm.serviceOwner, vm.screenSurface, 0, 0, shared.Color{}))
	copyColor, err = vm.services.Graphics.Pixel(vm.serviceOwner, image.surface, 0, 0)
	check(t, err)
	if copyColor != color {
		t.Fatal("captured image aliased source")
	}
	for _, call := range []struct {
		name, descriptor string
		args             []Value
	}{
		{"getPixel", "(II)I", []Value{IntValue(2), IntValue(0)}},
		{"capture", "(IIII)Ljavax/microedition/lcdui/Image;", []Value{IntValue(0), IntValue(0), IntValue(3), IntValue(1)}},
		{"capture", "(IIII)Ljavax/microedition/lcdui/Image;", []Value{IntValue(0), IntValue(0), IntValue(0), IntValue(1)}},
	} {
		if _, _, err := vm.natives[nativeKey{lgtGraphicsClass, call.name, call.descriptor}](context.Background(), vm, graphics, call.args); err == nil {
			t.Fatalf("%s accepted out-of-clip region", call.name)
		}
	}
}
