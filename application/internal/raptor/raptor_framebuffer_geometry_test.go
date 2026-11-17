package raptor

import (
	"bytes"
	"encoding/binary"
	"image"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func raptorScreenGeometry(t *testing.T, runtime *Runtime, handle uint32) (pixels, height uint32) {
	t.Helper()
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, handle))
	result, name, handled, err := runtime.DispatchPrivateImport(50)
	if err != nil || !handled || name != "RAPTOR.grpGetFrameBufferPixels" {
		t.Fatalf("ordinal 50 = %#v, %q, handled=%t, err=%v", result, name, handled, err)
	}
	pixels = result.Low
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, handle))
	result, name, handled, err = runtime.DispatchPrivateImport(52)
	if err != nil || !handled || name != "RAPTOR.grpGetFrameBufferHeight" {
		t.Fatalf("ordinal 52 = %#v, %q, handled=%t, err=%v", result, name, handled, err)
	}
	return pixels, result.Low
}

// The height a Clet reads from its primary framebuffer is the height
// MC_grpGetDisplayInfo reports, and its pixels start at the first row. A title
// that sees two different heights shifts its own layout by the difference
// (issue #587).
func TestRaptorPrimaryFramebufferIsTheWholeScreen(t *testing.T) {
	public := newPublicRuntime(t)
	public.Frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	runtime := &Runtime{CPU: public.CPU, Public: public}
	handle, err := public.EnsureScreenFramebuffer()
	check(t, err)
	screen := public.Framebuffers[handle]

	pixels, height := raptorScreenGeometry(t, runtime, handle)
	if pixels != screen.Pixels || height != 320 {
		t.Fatalf("screen = 0x%08x, %d rows; want 0x%08x, 320 rows", pixels, height, screen.Pixels)
	}

	offscreenHandle := uint32(0x12345678)
	offscreen := screen
	offscreen.Handle = offscreenHandle
	offscreen.Height = 88
	public.Framebuffers[offscreenHandle] = offscreen
	if _, height := raptorScreenGeometry(t, runtime, offscreenHandle); height != 88 {
		t.Fatalf("offscreen height = %d, want 88", height)
	}
}

// A state saved under the former handset-strip geometry keeps it: the Clet has
// already cached the shorter height and the shifted pixel pointer.
func TestRaptorLegacyScreenOriginAffectsOnlyScreen(t *testing.T) {
	public := newPublicRuntime(t)
	public.Frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	runtime := &Runtime{CPU: public.CPU, Public: public, screenOriginRows: raptorLegacyScreenOriginY}
	handle, err := public.EnsureScreenFramebuffer()
	check(t, err)
	screen := public.Framebuffers[handle]

	pixels, height := raptorScreenGeometry(t, runtime, handle)
	want := screen.Pixels + uint32(raptorLegacyScreenOriginY*screen.Width*screen.BitsPerPixel/8)
	if pixels != want || height != 296 {
		t.Fatalf("legacy screen = 0x%08x, %d rows; want 0x%08x, 296 rows", pixels, height, want)
	}

	offscreenHandle := uint32(0x12345678)
	offscreen := screen
	offscreen.Handle = offscreenHandle
	offscreen.Height = 88
	public.Framebuffers[offscreenHandle] = offscreen
	if pixels, height := raptorScreenGeometry(t, runtime, offscreenHandle); pixels != screen.Pixels || height != 88 {
		t.Fatalf("offscreen = 0x%08x, %d rows; want 0x%08x, 88 rows", pixels, height, screen.Pixels)
	}
}

func TestRaptorStateKeepsTheScreenOriginItWasSavedWith(t *testing.T) {
	public := newPublicRuntime(t)
	r := &Runtime{CPU: public.CPU, Public: public, legacyScreenOriginRows: raptorLegacyScreenOriginY}
	check(t, r.RestoreImage())
	check(t, r.InstallInterfaces())
	save := func() []byte {
		t.Helper()
		var saved bytes.Buffer
		writer := guest.NewStateWriter(&saved)
		check(t, WriteState(r, r.CPU, writer))
		check(t, writer.Err)
		return saved.Bytes()
	}
	load := func(data []byte) {
		t.Helper()
		state, err := ParseState(r, &guest.StateDecoder{Reader: bytes.NewReader(data)})
		check(t, err)
		check(t, RestoreState(r, r.CPU, state))
	}
	const originOffset = 10

	current := save()
	load(current)
	if r.screenOriginRows != 0 || public.ScreenDrawingOriginY != 0 {
		t.Fatalf("current state origin = %d/%d, want the whole screen", r.screenOriginRows, public.ScreenDrawingOriginY)
	}

	// A state from before the origin was recorded ran below the handset strip.
	legacy := append([]byte(nil), current[:28]...)
	binary.LittleEndian.PutUint32(legacy[4:], raptorStateSchemaCallbacks)
	load(legacy)
	if r.screenOriginRows != raptorLegacyScreenOriginY || public.ScreenDrawingOriginY != raptorLegacyScreenOriginY {
		t.Fatalf("legacy state origin = %d/%d, want %d", r.screenOriginRows, public.ScreenDrawingOriginY, raptorLegacyScreenOriginY)
	}
	// Saving it again must not lose that geometry.
	resaved := save()
	if resaved[originOffset] != raptorLegacyScreenOriginY {
		t.Fatalf("resaved origin = %d, want %d", resaved[originOffset], raptorLegacyScreenOriginY)
	}
	load(current)
	load(resaved)
	if r.screenOriginRows != raptorLegacyScreenOriginY {
		t.Fatalf("resaved state origin = %d, want %d", r.screenOriginRows, raptorLegacyScreenOriginY)
	}

	// Unless the exact package already had the whole surface back then.
	r.legacyScreenOriginRows = 0
	load(legacy)
	if r.screenOriginRows != 0 || public.ScreenDrawingOriginY != 0 {
		t.Fatalf("legacy full-surface origin = %d/%d, want 0", r.screenOriginRows, public.ScreenDrawingOriginY)
	}

	invalid := append([]byte(nil), current...)
	invalid[originOffset] = 7
	if _, err := ParseState(r, &guest.StateDecoder{Reader: bytes.NewReader(invalid)}); err == nil {
		t.Fatal("a state with an impossible screen origin was accepted")
	}

	// A restarted Clet reads the geometry again, so it gets the whole screen.
	load(resaved)
	check(t, r.RestoreImage())
	if r.screenOriginRows != 0 || public.ScreenDrawingOriginY != 0 {
		t.Fatalf("restarted origin = %d/%d, want 0", r.screenOriginRows, public.ScreenDrawingOriginY)
	}
}
