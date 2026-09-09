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
