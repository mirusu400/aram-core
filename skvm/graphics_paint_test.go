package skvm

import (
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestDefaultFontUsesReadableMediumHandsetMetrics(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := vm.services.Text.Metrics(vm.serviceOwner, vm.defaultFont)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Height != 12 {
		t.Fatalf("default font height = %d, want medium height 12", metrics.Height)
	}
	glyph, err := vm.services.Text.Glyph(vm.serviceOwner, vm.defaultFont, '아')
	if err != nil {
		t.Fatal(err)
	}
	if glyph.Width != 12 || glyph.Height != 12 || glyph.Advance != 12 {
		t.Fatalf("default Hangul glyph metrics = %+v, want 12x12 advance 12", glyph)
	}
}

func TestResetScreenGraphicsRestoresCanvasPaintState(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	graphics, err := vm.graphics(vm.ScreenGraphics())
	if err != nil {
		t.Fatal(err)
	}
	graphics.font = 0
	graphics.color = 0xffffffff
	if err := vm.services.Graphics.SetDrawState(
		vm.serviceOwner,
		graphics.surface,
		shared.SurfaceDrawState{
			Clip:         shared.Rectangle{X: 3, Y: 4, Width: 1, Height: 2},
			TranslateX:   7,
			TranslateY:   9,
			Raster:       shared.RasterXOR,
			GlobalAlpha:  1,
			Transparency: true,
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := vm.resetScreenGraphics(); err != nil {
		t.Fatal(err)
	}
	if graphics.font != vm.defaultFont || graphics.color != 0xff000000 {
		t.Fatalf("Java graphics state = font %d color %#08x", graphics.font, graphics.color)
	}
	want := shared.SurfaceDrawState{
		Clip: shared.Rectangle{
			Width:  int32(vm.ScreenWidth),
			Height: int32(vm.ScreenHeight),
		},
		Raster:      shared.RasterCopy,
		GlobalAlpha: 0xff,
	}
	got, err := vm.services.Graphics.DrawState(vm.serviceOwner, graphics.surface)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("surface draw state = %+v, want %+v", got, want)
	}
}
