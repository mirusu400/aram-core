package raptor

import (
	"image"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorConfiguredPrimaryFramebufferHeightAffectsOnlyScreen(t *testing.T) {
	public := newPublicRuntime(t)
	public.Frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	runtime := &Runtime{
		CPU:                      public.CPU,
		Public:                   public,
		primaryFramebufferHeight: 320,
	}
	handle, err := public.EnsureScreenFramebuffer()
	check(t, err)
	screen := public.Framebuffers[handle]

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, handle))
	pixels, _, handled, err := runtime.DispatchPrivateImport(50)
	check(t, err)
	if !handled || pixels.Low != screen.Pixels {
		t.Fatalf("configured screen pixels = 0x%08x, want 0x%08x", pixels.Low, screen.Pixels)
	}

	callHeight := func(handle uint32) uint32 {
		t.Helper()
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, handle))
		got, name, handled, err := runtime.DispatchPrivateImport(52)
		if err != nil || !handled || name != "RAPTOR.grpGetFrameBufferHeight" {
			t.Fatalf("ordinal 52 = %#v, %q, handled=%t, err=%v", got, name, handled, err)
		}
		return got.Low
	}

	if got := callHeight(handle); got != 320 {
		t.Fatalf("configured primary height = %d, want 320", got)
	}

	offscreenHandle := uint32(0x12345678)
	offscreen := public.Framebuffers[handle]
	offscreen.Handle = offscreenHandle
	offscreen.Height = 88
	public.Framebuffers[offscreenHandle] = offscreen
	if got := callHeight(offscreenHandle); got != 88 {
		t.Fatalf("offscreen height = %d, want 88", got)
	}

	// A malformed compatibility setting cannot expose pixels beyond the
	// allocation and falls back to the normal 24-pixel client area.
	runtime.primaryFramebufferHeight = 321
	if got := callHeight(handle); got != 296 {
		t.Fatalf("invalid configured primary height = %d, want 296", got)
	}
}

func TestRaptorDefaultPrimaryFramebufferPixelsStartBelowHandsetStrip(t *testing.T) {
	public := newPublicRuntime(t)
	public.Frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	runtime := &Runtime{CPU: public.CPU, Public: public}
	handle, err := public.EnsureScreenFramebuffer()
	check(t, err)
	screen := public.Framebuffers[handle]
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, handle))

	result, name, handled, err := runtime.DispatchPrivateImport(50)
	check(t, err)
	want := screen.Pixels + uint32(raptorScreenOriginY*screen.Width*screen.BitsPerPixel/8)
	if !handled || name != "RAPTOR.grpGetFrameBufferPixels" || result.Low != want {
		t.Fatalf("default screen pixels = 0x%08x, %q, %t; want 0x%08x", result.Low, name, handled, want)
	}
}
