package skvm

import (
	"fmt"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestCanvasHeightInsetQuirkLeavesFramebufferUnchanged(t *testing.T) {
	config := shared.DefaultConfig()
	config.Device.ScreenWidth = 120
	config.Device.ScreenHeight = 160
	config.Device.Quirks = []shared.DeviceQuirk{{
		Name:    CanvasHeightInset16Quirk,
		Enabled: true,
	}}
	services, err := shared.NewServices(config)
	check(t, err)
	owner, err := services.Coordinator.Register("skvm-test", 1_000_000)
	check(t, err)
	vm, err := NewWithServices(nil, services, owner)
	check(t, err)

	if got := vm.canvasHeight(); got != 144 {
		t.Fatalf("Canvas height = %d, want 144", got)
	}
	if vm.ScreenHeight != 160 {
		t.Fatalf("framebuffer height = %d, want 160", vm.ScreenHeight)
	}
}

func TestCanvasHeightInset240PreservesBottomDrawing(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			config := shared.DefaultConfig()
			config.Device.ScreenWidth, config.Device.ScreenHeight = 240, 320
			config.Device.Quirks = []shared.DeviceQuirk{{
				Name: CanvasHeightInset16Quirk, Enabled: enabled,
			}}
			services, err := shared.NewServices(config)
			check(t, err)
			owner, err := services.Coordinator.Register("canvas-test", 1_000_000)
			check(t, err)
			vm, err := NewWithServices(nil, services, owner)
			check(t, err)
			canvas := vm.NewObject("javax/microedition/lcdui/Canvas", nil)
			heightValue := invokeTestNative(t, vm, "javax/microedition/lcdui/Canvas", "getHeight", "()I", canvas)
			height, err := heightValue.Int()
			check(t, err)
			wantHeight := int32(320)
			if enabled {
				wantHeight = 304
			}
			if height != wantHeight || vm.ScreenWidth != 240 || vm.ScreenHeight != 320 {
				t.Fatalf("canvas height=%d, framebuffer=%dx%d", height, vm.ScreenWidth, vm.ScreenHeight)
			}
			graphics := vm.ScreenGraphics()
			black, white := shared.RGB(0, 0, 0), shared.RGB(255, 255, 255)
			check(t, services.Graphics.Clear(owner, vm.screenSurface, black))
			invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "setColor", "(I)V", graphics, IntValue(0xffffff))
			// The title restores the handset system strip before computing its
			// bottom edge. This must not resize or clip the physical surface.
			bottom := height + 16 - 1
			invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "drawLine", "(IIII)V", graphics,
				IntValue(0), IntValue(bottom), IntValue(239), IntValue(bottom))
			for x := int32(0); x < 240; x++ {
				pixel, err := services.Graphics.Pixel(owner, vm.screenSurface, x, 319)
				check(t, err)
				want := black
				if enabled {
					want = white
				}
				if pixel != want {
					t.Fatalf("bottom pixel (%d,319)=%+v, want %+v", x, pixel, want)
				}
			}
		})
	}
}

func TestCanvasHeightDefaultsToFramebufferHeight(t *testing.T) {
	vm, err := New(nil)
	check(t, err)
	if got := vm.canvasHeight(); got != vm.ScreenHeight {
		t.Fatalf("Canvas height = %d, want framebuffer height %d", got, vm.ScreenHeight)
	}
}
