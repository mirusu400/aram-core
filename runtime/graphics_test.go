package runtime

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestGraphicsPreservesRGB565StorageAndPresentsRGBA(t *testing.T) {
	registry := NewRegistry(16)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	surfaceID, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  2,
		Height: 1,
		Format: PixelRGB565,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetScreen(1, surfaceID); err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetPixel(1, surfaceID, 0, 0, RGB(255, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetPixel(1, surfaceID, 1, 0, RGB(0, 255, 0)); err != nil {
		t.Fatal(err)
	}
	storage, err := graphics.Pixels(1, surfaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storage, []byte{0x00, 0xf8, 0xe0, 0x07}) {
		t.Fatalf("RGB565 storage = % x", storage)
	}
	frame, err := graphics.Present(1, surfaceID, Rectangle{})
	if err != nil {
		t.Fatal(err)
	}
	wantRGBA := []byte{
		255, 0, 0, 255,
		0, 255, 0, 255,
	}
	if !bytes.Equal(frame.RGBA, wantRGBA) ||
		frame.Width != 2 || frame.Height != 1 ||
		frame.Dirty != (Rectangle{Width: 2, Height: 1}) {
		t.Fatalf("presented frame = %+v, RGBA % x", frame, frame.RGBA)
	}
	frame.RGBA[0] = 0
	if graphics.LastFrame().RGBA[0] != 255 {
		t.Fatal("returned frame aliases the graphics service")
	}
}

func TestGraphicsClipTranslationRasterAndAlpha(t *testing.T) {
	registry := NewRegistry(16)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := graphics.CreateSurface(4, SurfaceDescriptor{
		Width:  4,
		Height: 4,
		Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.Clear(4, id, RGB(0x10, 0x20, 0x30)); err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetDrawState(4, id, SurfaceDrawState{
		Clip:        Rectangle{X: 1, Y: 1, Width: 2, Height: 2},
		TranslateX:  1,
		TranslateY:  1,
		Raster:      RasterXOR,
		GlobalAlpha: 0xff,
	}); err != nil {
		t.Fatal(err)
	}
	if err := graphics.Rectangle(4, id, Rectangle{Width: 3, Height: 3}, RGB(0xff, 0, 0), true); err != nil {
		t.Fatal(err)
	}
	inside, err := graphics.Pixel(4, id, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	outside, err := graphics.Pixel(4, id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if inside != (Color{R: 0xef, G: 0x20, B: 0x30, A: 0xff}) {
		t.Fatalf("inside clipped XOR pixel = %+v", inside)
	}
	if outside != RGB(0x10, 0x20, 0x30) {
		t.Fatalf("outside clipped pixel = %+v", outside)
	}
}

func TestGraphicsBlitHandlesOverlapAndScaling(t *testing.T) {
	registry := NewRegistry(16)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  4,
		Height: 1,
		Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	for x, value := range []uint8{10, 20, 30, 40} {
		if err := graphics.SetPixel(1, id, int32(x), 0, RGB(value, 0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := graphics.Blit(
		1,
		id,
		id,
		1,
		0,
		Rectangle{Width: 3, Height: 1},
	); err != nil {
		t.Fatal(err)
	}
	var got []uint8
	for x := int32(0); x < 4; x++ {
		color, err := graphics.Pixel(1, id, x, 0)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, color.R)
	}
	if !bytes.Equal(got, []byte{10, 10, 20, 30}) {
		t.Fatalf("overlapping blit pixels = %v", got)
	}
}

func TestGraphicsRestoreRejectsInvalidObjectGraphBeforeMutation(t *testing.T) {
	registry := NewRegistry(16)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  2,
		Height: 2,
		Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetPixel(1, id, 0, 0, RGB(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	before := graphics.Snapshot()
	invalid := graphics.Snapshot()
	invalid.Surfaces[0].Pixels = invalid.Surfaces[0].Pixels[:1]
	if err := graphics.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore invalid graphics error = %v", err)
	}
	after := graphics.Snapshot()
	if !bytes.Equal(after.Surfaces[0].Pixels, before.Surfaces[0].Pixels) ||
		after.PresentSequence != before.PresentSequence {
		t.Fatal("graphics mutated after rejected restore")
	}
}

func TestGraphicsArcAndPolygonRasterizeDeterministically(t *testing.T) {
	registry := NewRegistry(8)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	arc, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 8, Height: 8, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.Arc(
		1,
		arc,
		Rectangle{X: 1, Y: 1, Width: 6, Height: 6},
		0,
		180,
		RGB(255, 0, 0),
		true,
	); err != nil {
		t.Fatal(err)
	}
	bottom, err := graphics.Pixel(1, arc, 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	top, err := graphics.Pixel(1, arc, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if bottom != RGB(255, 0, 0) || top != (Color{}) {
		t.Fatalf("half arc pixels bottom=%+v top=%+v", bottom, top)
	}

	polygon, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 8, Height: 8, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.Polygon(
		1,
		polygon,
		[]Point{{X: 1, Y: 1}, {X: 6, Y: 1}, {X: 3, Y: 6}},
		RGB(0, 255, 0),
		true,
	); err != nil {
		t.Fatal(err)
	}
	inside, err := graphics.Pixel(1, polygon, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	outside, err := graphics.Pixel(1, polygon, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if inside != RGB(0, 255, 0) || outside != (Color{}) {
		t.Fatalf("polygon pixels inside=%+v outside=%+v", inside, outside)
	}
}

func TestGraphicsRejectsUnboundedRasterWorkBeforeMutation(t *testing.T) {
	registry := NewRegistry(2)
	limits := GraphicsLimits{
		MaxSurfaces: 2,
		MaxWidth:    4,
		MaxHeight:   4,
		MaxPixels:   16,
		MaxBytes:    64,
	}
	graphics, err := NewGraphics(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	id, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 4, Height: 4, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := graphics.Snapshot()
	operations := []func() error{
		func() error {
			return graphics.Line(
				1,
				id,
				-1<<31,
				0,
				1<<31-1,
				0,
				RGB(1, 2, 3),
			)
		},
		func() error {
			return graphics.Rectangle(
				1,
				id,
				Rectangle{Width: 17, Height: 1},
				RGB(1, 2, 3),
				true,
			)
		},
		func() error {
			return graphics.Arc(
				1,
				id,
				Rectangle{Width: 17, Height: 1},
				0,
				360,
				RGB(1, 2, 3),
				true,
			)
		},
		func() error {
			return graphics.Polygon(
				1,
				id,
				[]Point{{X: -1 << 31}, {X: 1<<31 - 1}},
				RGB(1, 2, 3),
				false,
			)
		},
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("operation %d error = %v", index, err)
		}
		if after := graphics.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Fatalf("operation %d mutated graphics", index)
		}
	}
}

func TestGraphicsStateKeepsImmutableFrameAfterSurfaceDestruction(t *testing.T) {
	registry := NewRegistry(8)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 2, Height: 2, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetScreen(1, first); err != nil {
		t.Fatal(err)
	}
	frame, err := graphics.Present(1, first, Rectangle{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 1, Height: 1, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetScreen(1, second); err != nil {
		t.Fatal(err)
	}
	if err := graphics.DestroySurface(1, first); err != nil {
		t.Fatal(err)
	}
	state := graphics.Snapshot()
	clone, err := NewGraphics(registry, state.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if got := clone.LastFrame(); !reflect.DeepEqual(got, frame) {
		t.Fatalf("restored immutable frame = %+v, want %+v", got, frame)
	}
}

func TestGraphicsRestoreRejectsImplicitSavedStride(t *testing.T) {
	registry := NewRegistry(4)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 1, Height: 1, Format: PixelRGBA8888,
	}); err != nil {
		t.Fatal(err)
	}
	before := graphics.Snapshot()
	invalid := graphics.Snapshot()
	invalid.Surfaces[0].Descriptor.Stride = 0
	invalid.Surfaces[0].Pixels = nil
	if err := graphics.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore implicit saved stride error = %v", err)
	}
	if after := graphics.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected implicit stride mutated graphics")
	}
}

func TestScaledBlitRejectsHostAddressSpaceOverflow(t *testing.T) {
	limits := DefaultGraphicsLimits()
	limits.MaxPixels = math.MaxUint64
	registry := NewRegistry(4)
	graphics, err := NewGraphics(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 1, Height: 1, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 1, Height: 1, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.ScaledBlit(
		1,
		destination,
		source,
		Rectangle{Width: math.MaxInt32, Height: math.MaxInt32},
		Rectangle{Width: 1, Height: 1},
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized scaled blit error = %v", err)
	}
}
