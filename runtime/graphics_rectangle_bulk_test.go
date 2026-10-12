package runtime

import (
	"fmt"
	"math"
	"testing"
)

func TestRectangleFillFastPathMatchesReference(t *testing.T) {
	key := RGB(0x21, 0x87, 0xc3)
	cases := []struct {
		name        string
		rect        Rectangle
		state       SurfaceDrawState
		color       Color
		transparent *Color
	}{
		{"plain", Rectangle{X: 2, Y: 3, Width: 6, Height: 4}, defaultDrawState(13, 11), key, nil},
		{"clipped", Rectangle{X: -2, Y: -3, Width: 9, Height: 10}, SurfaceDrawState{Clip: Rectangle{X: 3, Y: 2, Width: 4, Height: 5}, GlobalAlpha: 255}, key, nil},
		{"translated", Rectangle{X: -2, Y: -3, Width: 10, Height: 11}, SurfaceDrawState{Clip: Rectangle{Width: 13, Height: 11}, TranslateX: 4, TranslateY: 5, GlobalAlpha: 255}, key, nil},
		{"clipped away", Rectangle{X: 100, Y: 100, Width: 10, Height: 11}, defaultDrawState(13, 11), key, nil},
		{"keyed", Rectangle{Width: 13, Height: 11}, SurfaceDrawState{Clip: Rectangle{Width: 13, Height: 11}, GlobalAlpha: 255, Transparency: true}, key, &key},
		{"other key", Rectangle{Width: 13, Height: 11}, SurfaceDrawState{Clip: Rectangle{Width: 13, Height: 11}, GlobalAlpha: 255, Transparency: true}, RGB(4, 5, 6), &key},
		{"untranslated overflow", Rectangle{X: math.MaxInt32 - 2, Y: 1, Width: 9, Height: 5}, SurfaceDrawState{Clip: Rectangle{Width: 13, Height: 11}, TranslateX: -math.MaxInt32, GlobalAlpha: 255}, key, nil},
		{"untranslated y overflow", Rectangle{X: 1, Y: math.MaxInt32 - 2, Width: 5, Height: 9}, SurfaceDrawState{Clip: Rectangle{Width: 13, Height: 11}, TranslateY: -math.MaxInt32, GlobalAlpha: 255}, key, nil},
		{"translation overflow", Rectangle{X: math.MaxInt32 - 2, Y: 1, Width: 2, Height: 5}, SurfaceDrawState{Clip: Rectangle{Width: 13, Height: 11}, TranslateX: math.MaxInt32, GlobalAlpha: 255}, key, nil},
	}
	for _, format := range bulkFormats {
		for _, test := range cases {
			t.Run(fmt.Sprintf("%d/%s", format, test.name), func(t *testing.T) {
				got := bulkSurface(t, 13, 11, 7, format)
				got.state = test.state
				got.descriptor.Transparent = test.transparent
				got.dirty = Rectangle{X: 11, Y: 9, Width: 1, Height: 1}
				want := cloneSurface(got)
				for y := int64(test.rect.Y); y < test.rect.Bottom(); y++ {
					for x := int64(test.rect.X); x < test.rect.Right(); x++ {
						if x <= math.MaxInt32 && y <= math.MaxInt32 {
							check(t, referenceDrawSurfacePixel(want, int32(x), int32(y), test.color))
						}
					}
				}
				graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
				check(t, err)
				id := installBlitSurface(t, graphics, got)
				check(t, graphics.Rectangle(1, id, test.rect, test.color, true))
				compareSurfaces(t, "rectangle", got, want)
				if graphics.bulkFastPathCalls != 1 {
					t.Fatal("opaque fill missed bulk path")
				}
			})
		}
	}
}

func TestRectangleFillKeepsCompositingPath(t *testing.T) {
	for _, mutate := range []func(*surface, *Color){
		func(s *surface, _ *Color) { s.state.Raster = RasterXOR },
		func(s *surface, _ *Color) { s.state.GlobalAlpha = 127 },
		func(s *surface, _ *Color) { s.state.GlobalTransparency256 = 129 },
		func(_ *surface, c *Color) { c.A = 127 },
	} {
		current := bulkSurface(t, 8, 8, 3, PixelRGBA8888)
		color := RGB(10, 20, 30)
		mutate(current, &color)
		before := cloneSurface(current)
		if fillRectangleFastPath(current, Rectangle{Width: 8, Height: 8}, color) {
			t.Fatal("compositing fill used opaque copy")
		}
		compareSurfaces(t, "declined fill", current, before)
	}
}

func TestBulkFillRowGrowthKeepsPartialCopiesAndPadding(t *testing.T) {
	// Cross doubling boundaries using small rows. The production loop advances
	// by min(filled, rowBytes-filled), so it never exceeds the allocated row even
	// when a custom 32-bit graphics limit would put the next doubling past MaxInt.
	for _, format := range bulkFormats {
		for _, width := range []int32{1, 2, 3, 7, 8, 9, 31, 32, 33, 127, 128, 129} {
			for _, rectangle := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%d/rectangle=%t", format, width, rectangle), func(t *testing.T) {
					got := bulkSurface(t, width+2, 4, 7, format)
					want := cloneSurface(got)
					color := RGB(37, 139, 241)
					graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
					check(t, err)
					id := installBlitSurface(t, graphics, got)
					if rectangle {
						rect := Rectangle{X: 1, Y: 1, Width: width, Height: 2}
						check(t, graphics.Rectangle(1, id, rect, color, true))
						for y := int32(1); y < 3; y++ {
							for x := int32(1); x <= width; x++ {
								check(t, referenceDrawSurfacePixel(want, x, y, color))
							}
						}
					} else {
						check(t, graphics.Clear(1, id, color))
						referenceClear(want, color)
					}
					compareSurfaces(t, "row growth", got, want)
					if graphics.bulkFastPathCalls != 1 {
						t.Fatal("row fill did not use the bulk path")
					}
				})
			}
		}
	}
}
