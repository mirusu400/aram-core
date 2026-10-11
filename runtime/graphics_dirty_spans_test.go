package runtime

import "testing"

// Rows may contain only key pixels, only blended pixels, or disjoint opaque
// spans. Batching their dirty updates must match the original per-pixel bounds.
func TestScaledBlitMixedOpacityDirtyBounds(t *testing.T) {
	key := Color{R: 1, G: 2, B: 3, A: 255}
	for _, format := range []PixelFormat{PixelRGBA8888, PixelRGB565, PixelIndexed8} {
		for _, initial := range []Rectangle{{}, {X: 9, Y: 7, Width: 1, Height: 1}} {
			for _, rectangle := range []Rectangle{
				{X: -2, Y: -1, Width: 12, Height: 9},
				{X: 2, Y: 1, Width: 4, Height: 3},
				{X: 1, Y: 1, Width: 8, Height: 6},
			} {
				graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
				check(t, err)
				destination := bulkSurface(t, 11, 9, 7, format)
				destination.dirty = initial
				destination.state.TranslateX = 1
				destination.state.TranslateY = 1
				destination.state.Clip = Rectangle{X: 2, Y: 1, Width: 7, Height: 6}
				source := bulkSurface(t, 8, 6, 5, PixelRGBA8888)
				source.descriptor.Transparent = &key
				for y := int32(0); y < 6; y++ {
					for x := int32(0); x < 8; x++ {
						color := key
						switch y {
						case 1, 4:
							if x == 1 || x == 6 {
								color = RGB(90, 110, 130)
							}
						case 2:
							color = Color{R: 40, G: 80, B: 120, A: 127}
						case 3:
							color = Color{R: 60, G: 100, B: 140, A: uint8(x % 3 * 127)}
						}
						encodeSurfaceColor(source, x, y, color)
					}
				}
				want := cloneSurface(destination)
				destinationID := installBlitSurface(t, graphics, destination)
				sourceID := installBlitSurface(t, graphics, source)
				check(t, graphics.ScaledBlit(1, destinationID, sourceID, rectangle, Rectangle{Width: 8, Height: 6}))
				check(t, referenceScaledBlit(want, source, rectangle, Rectangle{Width: 8, Height: 6}))
				compareSurfaces(t, "mixed opacity and keyed rows", destination, want)
			}
		}
	}
}
