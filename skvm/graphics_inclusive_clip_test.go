package skvm

import (
	"fmt"
	shared "github.com/mirusu400/aram-core/runtime"
	"math"
	"testing"
)

func inclusiveClipTestVM(t *testing.T, enabled bool) *VM {
	t.Helper()
	config := shared.DefaultConfig()
	config.Device.ScreenWidth, config.Device.ScreenHeight = 32, 32
	config.Device.Quirks = []shared.DeviceQuirk{{Name: InclusiveSetClipQuirk, Enabled: enabled}}
	services, err := shared.NewServices(config)
	check(t, err)
	owner, err := services.Coordinator.Register("clip-test", 1000000)
	check(t, err)
	vm, err := NewWithServices(nil, services, owner)
	check(t, err)
	return vm
}

func TestInclusiveSetClipTilesAndStandardMIDP(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			vm := inclusiveClipTestVM(t, enabled)
			g := vm.ScreenGraphics()
			tile, err := vm.newImageState(16, 16)
			check(t, err)
			green := shared.RGB(0, 255, 0)
			check(t, vm.services.Graphics.Clear(vm.serviceOwner, tile.surface, green))
			ref := vm.NewObject("javax/microedition/lcdui/Image", tile)
			for y := 0; y < 32; y += 16 {
				for x := 0; x < 32; x += 16 {
					invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "setClip", "(IIII)V", g, IntValue(int32(x)), IntValue(int32(y)), IntValue(15), IntValue(15))
					invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "drawImage", "(Ljavax/microedition/lcdui/Image;III)V", g, ReferenceValue(ref), IntValue(int32(x)), IntValue(int32(y)), IntValue(0))
				}
			}
			for y := int32(0); y < 32; y++ {
				for x := int32(0); x < 32; x++ {
					pixel, err := vm.services.Graphics.Pixel(vm.serviceOwner, vm.screenSurface, x, y)
					check(t, err)
					wantGreen := enabled || (x%16 != 15 && y%16 != 15)
					if (pixel == green) != wantGreen {
						t.Fatalf("pixel %d,%d = %+v, green=%t", x, y, pixel, wantGreen)
					}
				}
			}
		})
	}
}

func TestInclusiveSetClipBoundsAndClipRect(t *testing.T) {
	vm := inclusiveClipTestVM(t, true)
	g := vm.ScreenGraphics()
	clip := func(x, y, w, h int32) shared.Rectangle {
		invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "setClip", "(IIII)V", g, IntValue(x), IntValue(y), IntValue(w), IntValue(h))
		state, err := vm.services.Graphics.DrawState(vm.serviceOwner, vm.screenSurface)
		check(t, err)
		return state.Clip
	}
	for _, tc := range []struct {
		x, y, w, h int32
		want       shared.Rectangle
	}{
		{0, 0, 0, 0, shared.Rectangle{Width: 1, Height: 1}},
		{0, 0, -1, 15, shared.Rectangle{}},
		{0, 0, 15, -1, shared.Rectangle{}},
		{0, 0, math.MaxInt32, math.MaxInt32, shared.Rectangle{Width: 32, Height: 32}},
		{-1, -1, 15, 15, shared.Rectangle{Width: 15, Height: 15}},
	} {
		if got := clip(tc.x, tc.y, tc.w, tc.h); got != tc.want {
			t.Fatalf("clip %+v = %+v", tc, got)
		}
	}
	invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "translate", "(II)V", g, IntValue(2), IntValue(3))
	if got := clip(0, 0, 0, 0); got != (shared.Rectangle{X: 2, Y: 3, Width: 1, Height: 1}) {
		t.Fatalf("translated clip=%+v", got)
	}
	clip(0, 0, 15, 15)
	invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "clipRect", "(IIII)V", g, IntValue(0), IntValue(0), IntValue(2), IntValue(2))
	state, err := vm.services.Graphics.DrawState(vm.serviceOwner, vm.screenSurface)
	check(t, err)
	if state.Clip != (shared.Rectangle{X: 2, Y: 3, Width: 2, Height: 2}) {
		t.Fatalf("clipRect must retain MIDP extents: %+v", state.Clip)
	}
}
