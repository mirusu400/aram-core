package runtime

import (
	"reflect"
	"testing"
)

func TestGraphics256AlphaAndLegacyIsolation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		inverse        uint16
		global, source uint8
		want           Color
	}{
		{"legacyOpaque", 0, 255, 255, Color{255, 255, 255, 255}},
		{"legacyGlobal", 0, 128, 255, Color{128, 128, 128, 255}},
		{"legacySource", 0, 255, 128, Color{128, 128, 128, 255}},
		{"transparent", 256, 255, 255, Color{0, 0, 0, 255}},
		{"half", 128, 255, 255, Color{128, 128, 128, 255}},
		{"nearOpaque", 1, 255, 255, Color{254, 254, 254, 255}},
		{"halfSource", 128, 255, 128, Color{64, 64, 64, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewRegistry(16)
			g, err := NewGraphics(registry, GraphicsLimits{})
			check(t, err)
			dst, err := g.CreateSurface(1, SurfaceDescriptor{Width: 3, Height: 3, Format: PixelRGBA8888})
			check(t, err)
			src, err := g.CreateSurface(1, SurfaceDescriptor{Width: 3, Height: 3, Format: PixelRGBA8888})
			check(t, err)
			check(t, g.Clear(1, dst, Color{A: 255}))
			// Clear writes raw color without drawing state, preserving source alpha.
			check(t, g.Clear(1, src, Color{255, 255, 255, tc.source}))
			draw, err := g.DrawState(1, dst)
			check(t, err)
			draw.GlobalAlpha = tc.global
			draw.GlobalTransparency256 = tc.inverse
			check(t, g.SetDrawState(1, dst, draw))
			check(t, g.Blit(1, dst, src, 0, 0, Rectangle{Width: 3, Height: 3}))
			pixel, err := g.Pixel(1, dst, 1, 1)
			check(t, err)
			if pixel != tc.want {
				t.Fatalf("blit = %+v, want %+v", pixel, tc.want)
			}
			snapshot := g.Snapshot()
			check(t, g.Restore(snapshot))
			if got := g.Snapshot(); !reflect.DeepEqual(got, snapshot) {
				t.Fatal("draw state restore changed snapshot")
			}
			check(t, g.Clear(1, dst, Color{A: 255}))
			check(t, g.Rectangle(1, dst, Rectangle{Width: 3, Height: 3}, Color{255, 255, 255, tc.source}, false))
			corner, err := g.Pixel(1, dst, 0, 0)
			check(t, err)
			want := tc.want
			// Keep legacy double-inclusive-corner behavior byte-identical. Only the
			// opt-in 256-scale path corrects coverage for its new blending contract.
			if tc.name == "legacyGlobal" || tc.name == "legacySource" {
				want = Color{192, 192, 192, 255}
			}
			if corner != want {
				t.Fatalf("corner = %+v, want %+v", corner, want)
			}
		})
	}
}

func TestGraphics256AlphaRejectsMalformedStateTransactionally(t *testing.T) {
	registry := NewRegistry(16)
	g, err := NewGraphics(registry, GraphicsLimits{})
	check(t, err)
	dst, err := g.CreateSurface(1, SurfaceDescriptor{Width: 1, Height: 1, Format: PixelRGBA8888})
	check(t, err)
	original := g.Snapshot()
	for _, invalid := range []uint16{257, 65535} {
		draw, err := g.DrawState(1, dst)
		check(t, err)
		draw.GlobalTransparency256 = invalid
		if err := g.SetDrawState(1, dst, draw); err == nil {
			t.Fatalf("accepted invalid transparency%d", invalid)
		}
		bad := g.Snapshot()
		bad.Surfaces[0].Draw.GlobalTransparency256 = invalid
		if err := g.Restore(bad); err == nil {
			t.Fatalf("restored invalid transparency%d", invalid)
		}
		if got := g.Snapshot(); !reflect.DeepEqual(got, original) {
			t.Fatal("invalid restore mutated graphics")
		}
	}
}
