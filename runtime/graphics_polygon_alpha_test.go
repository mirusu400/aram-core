package runtime_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	rt "github.com/mirusu400/aram-core/runtime"
)

func TestPolygonAlphaSlopedOutlineAddsCoverage(t *testing.T) {
	g, id := polygonSurface(t, 128, 255)
	if err := g.Polygon(1, id, []rt.Point{{X: 1, Y: 1}, {X: 6, Y: 2}, {X: 3, Y: 6}}, rt.RGB(255, 255, 255), true); err != nil {
		t.Fatal(err)
	}
	// The outline adds (2,1), (3,1), (4,5), and (3,6) beyond the
	// scanlines. Removing the outline is not a valid alpha correction.
	rows := []string{"........", ".###....", ".######.", ".#####..", "..###...", "..###...", "...#....", "........"}
	for y, row := range rows {
		for x, c := range row {
			want := rt.RGB(0, 0, 0)
			if c == '#' {
				want = rt.RGB(128, 128, 128)
			}
			got, err := g.Pixel(1, id, int32(x), int32(y))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("(%d,%d) = %+v, want %+v", x, y, got, want)
			}
		}
	}
}

func TestPolygonAlphaPreservesLegacyXOR(t *testing.T) {
	for _, inverse := range []uint16{0, 128} {
		t.Run(fmt.Sprint(inverse), func(t *testing.T) {
			g, id := polygonSurface(t, inverse, 255)
			state, err := g.DrawState(1, id)
			if err != nil {
				t.Fatal(err)
			}
			state.Raster = rt.RasterXOR
			if err := g.SetDrawState(1, id, state); err != nil {
				t.Fatal(err)
			}
			if err := g.Polygon(1, id, []rt.Point{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 1, Y: 5}}, rt.RGB(255, 255, 255), true); err != nil {
				t.Fatal(err)
			}
			rows := []string{"........", ".#...#..", "..##....", "..#.....", "........", "........", "........", "........"}
			value := uint8(255)
			if inverse != 0 {
				rows = []string{"........", ".#####..", ".####...", ".###....", ".##.....", ".#......", "........", "........"}
				value = 128
			}
			for y, row := range rows {
				for x, c := range row {
					want := rt.RGB(0, 0, 0)
					if c == '#' {
						want = rt.RGB(value, value, value)
					}
					got, err := g.Pixel(1, id, int32(x), int32(y))
					if err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Errorf("(%d,%d) = %+v, want %+v", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestPolygonAlphaTranslationBoundsAndLimits(t *testing.T) {
	g, id := polygonSurface(t, 128, 255)
	state, err := g.DrawState(1, id)
	if err != nil {
		t.Fatal(err)
	}
	state.TranslateX, state.TranslateY = -math.MaxInt32, -math.MaxInt32
	if err := g.SetDrawState(1, id, state); err != nil {
		t.Fatal(err)
	}
	if err := g.Polygon(1, id, []rt.Point{{X: math.MaxInt32, Y: math.MaxInt32}}, rt.RGB(255, 255, 255), true); err != nil {
		t.Fatal(err)
	}
	got, err := g.Pixel(1, id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != rt.RGB(128, 128, 128) {
		t.Fatalf("translated maximum coordinate = %+v", got)
	}
	before := g.Snapshot()
	// These translate outside int32 and must remain invisible without wrapping.
	if err := g.Polygon(1, id, []rt.Point{{X: math.MinInt32, Y: math.MinInt32}}, rt.RGB(255, 255, 255), true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, g.Snapshot()) {
		t.Fatal("offscreen overflow mutated surface")
	}

	limits := rt.DefaultGraphicsLimits()
	limits.MaxPixels = 64
	g, err = rt.NewGraphics(rt.NewRegistry(4), limits)
	if err != nil {
		t.Fatal(err)
	}
	id, err = g.CreateSurface(1, rt.SurfaceDescriptor{Width: 8, Height: 8, Format: rt.PixelRGBA8888})
	if err != nil {
		t.Fatal(err)
	}
	state, err = g.DrawState(1, id)
	if err != nil {
		t.Fatal(err)
	}
	state.GlobalTransparency256 = 128
	state.Clip = rt.Rectangle{}
	if err := g.SetDrawState(1, id, state); err != nil {
		t.Fatal(err)
	}
	before = g.Snapshot()
	for _, points := range [][]rt.Point{
		{{X: 0, Y: 0}, {X: 9, Y: 0}, {X: 0, Y: 9}}, // fill area exceeds 64, perimeter does not
		{{X: 0, Y: 0}, {X: 64, Y: 0}},              // outline exceeds 64
		make([]rt.Point, 65),                       // point count exceeds 64
	} {
		if err := g.Polygon(1, id, points, rt.RGB(255, 255, 255), true); !errors.Is(err, rt.ErrLimitExceeded) {
			t.Fatalf("invisible oversized polygon error = %v", err)
		}
		if !reflect.DeepEqual(before, g.Snapshot()) {
			t.Fatal("rejected polygon mutated surface")
		}
	}
}

func polygonSurface(t *testing.T, inverse uint16, global uint8) (*rt.Graphics, rt.ServiceID) {
	t.Helper()
	g, err := rt.NewGraphics(rt.NewRegistry(4), rt.GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := g.CreateSurface(1, rt.SurfaceDescriptor{Width: 8, Height: 8, Format: rt.PixelRGBA8888})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Clear(1, id, rt.RGB(0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	state, err := g.DrawState(1, id)
	if err != nil {
		t.Fatal(err)
	}
	state.GlobalTransparency256, state.GlobalAlpha = inverse, global
	if err := g.SetDrawState(1, id, state); err != nil {
		t.Fatal(err)
	}
	return g, id
}

func TestPolygonAlphaTriangleExactPixels(t *testing.T) {
	// Literal inclusive raster footprint for (1,1), (5,1), (1,5).
	mask := []string{"........", ".#####..", ".####...", ".###....", ".##.....", ".#......", "........", "........"}
	legacy := [8][8]uint8{
		{}, {0, 224, 192, 192, 192, 224}, {0, 192, 128, 128, 192},
		{0, 192, 128, 192}, {0, 192, 192}, {0, 192},
	}
	for _, tc := range []struct {
		name                  string
		inverse               uint16
		global, source, value uint8
		legacy                bool
	}{
		{"half", 128, 255, 255, 128, false},
		{"transparent", 256, 255, 255, 0, false},
		{"nearOpaque", 1, 255, 255, 254, false},
		{"nearTransparent", 255, 255, 255, 1, false},
		{"sourceAlpha", 128, 255, 128, 64, false},
		{"globalAlpha", 128, 128, 255, 64, false},
		{"legacyOpaque", 0, 255, 255, 255, false},
		{"legacyGlobal", 0, 128, 255, 128, true},
		{"legacySource", 0, 255, 128, 128, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, id := polygonSurface(t, tc.inverse, tc.global)
			if err := g.Polygon(1, id, []rt.Point{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 1, Y: 5}}, rt.Color{R: 255, G: 255, B: 255, A: tc.source}, true); err != nil {
				t.Fatal(err)
			}
			for y, row := range mask {
				for x, c := range row {
					value := uint8(0)
					if c == '#' {
						value = tc.value
					}
					if tc.legacy {
						value = legacy[y][x]
					}
					got, err := g.Pixel(1, id, int32(x), int32(y))
					if err != nil {
						t.Fatal(err)
					}
					if want := rt.RGB(value, value, value); got != want {
						t.Errorf("(%d,%d) = %+v, want %+v", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestPolygonAlphaCoverageMatchesOpaqueRaster(t *testing.T) {
	// Differential footprint checks supplement, not replace, the literal triangle
	// above. Sloped outlines can cover pixels outside the scanline fill.
	shapes := []struct {
		name   string
		points []rt.Point
	}{
		{"sloped", []rt.Point{{X: 1, Y: 1}, {X: 6, Y: 2}, {X: 3, Y: 6}}},
		{"reverse", []rt.Point{{X: 3, Y: 6}, {X: 6, Y: 2}, {X: 1, Y: 1}}},
		{"concave", []rt.Point{{X: 1, Y: 1}, {X: 6, Y: 1}, {X: 3, Y: 3}, {X: 6, Y: 6}, {X: 1, Y: 6}}},
		{"crossing", []rt.Point{{X: 1, Y: 1}, {X: 6, Y: 6}, {X: 1, Y: 6}, {X: 6, Y: 1}}},
		{"repeatedVertex", []rt.Point{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 1, Y: 5}, {X: 1, Y: 1}}},
		{"collinear", []rt.Point{{X: 1, Y: 1}, {X: 3, Y: 3}, {X: 6, Y: 6}}},
		{"segment", []rt.Point{{X: 1, Y: 1}, {X: 6, Y: 3}}},
		{"point", []rt.Point{{X: 3, Y: 3}}},
		{"empty", nil},
	}
	for _, shape := range shapes {
		for _, fill := range []bool{false, true} {
			for _, clipped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/fill=%t/clipped=%t", shape.name, fill, clipped), func(t *testing.T) {
					opaque, oid := polygonSurface(t, 0, 255)
					alpha, aid := polygonSurface(t, 128, 255)
					if clipped {
						for _, surface := range []struct {
							g  *rt.Graphics
							id rt.ServiceID
						}{{opaque, oid}, {alpha, aid}} {
							state, err := surface.g.DrawState(1, surface.id)
							if err != nil {
								t.Fatal(err)
							}
							state.TranslateX, state.TranslateY = -2, 1
							state.Clip = rt.Rectangle{X: 1, Y: 2, Width: 4, Height: 4}
							if err := surface.g.SetDrawState(1, surface.id, state); err != nil {
								t.Fatal(err)
							}
						}
					}
					if err := opaque.Polygon(1, oid, shape.points, rt.RGB(255, 255, 255), fill); err != nil {
						t.Fatal(err)
					}
					for pass, value := range []uint8{128, 192} {
						if err := alpha.Polygon(1, aid, shape.points, rt.RGB(255, 255, 255), fill); err != nil {
							t.Fatal(err)
						}
						for y := int32(0); y < 8; y++ {
							for x := int32(0); x < 8; x++ {
								base, err := opaque.Pixel(1, oid, x, y)
								if err != nil {
									t.Fatal(err)
								}
								got, err := alpha.Pixel(1, aid, x, y)
								if err != nil {
									t.Fatal(err)
								}
								want := rt.RGB(0, 0, 0)
								if base.R == 255 {
									want = rt.RGB(value, value, value)
								}
								if got != want {
									t.Errorf("pass %d (%d,%d) = %+v, want %+v", pass+1, x, y, got, want)
								}
							}
						}
					}
				})
			}
		}
	}
}
