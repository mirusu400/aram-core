package skvm

import (
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
	if err != nil {
		t.Fatal(err)
	}
	owner, err := services.Coordinator.Register("skvm-test", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	vm, err := NewWithServices(nil, services, owner)
	if err != nil {
		t.Fatal(err)
	}

	if got := vm.canvasHeight(); got != 144 {
		t.Fatalf("Canvas height = %d, want 144", got)
	}
	if vm.ScreenHeight != 160 {
		t.Fatalf("framebuffer height = %d, want 160", vm.ScreenHeight)
	}
}

func TestCanvasHeightDefaultsToFramebufferHeight(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := vm.canvasHeight(); got != vm.ScreenHeight {
		t.Fatalf("Canvas height = %d, want framebuffer height %d", got, vm.ScreenHeight)
	}
}
