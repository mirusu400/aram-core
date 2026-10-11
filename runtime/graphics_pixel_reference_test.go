package runtime

import (
	"math"
	"testing"
)

func TestDrawSurfacePixelMatchesReference(t *testing.T) {
	for _, format := range bulkFormats {
		for _, raster := range []RasterOperation{RasterCopy, RasterXOR, RasterAND, RasterOR} {
			for _, alpha := range []uint8{0, 1, 127, 254, 255} {
				for _, globalAlpha := range []uint8{0, 127, 255} {
					for _, transparency := range []uint16{0, 117, 256} {
						for _, keyed := range []bool{false, true} {
							for _, point := range []Point{{X: 2, Y: 2}, {X: -1, Y: 1}, {X: 7, Y: 6}, {X: math.MaxInt32, Y: math.MinInt32}} {
								got := bulkSurface(t, 8, 7, 5, format)
								got.state = SurfaceDrawState{Clip: Rectangle{X: 1, Y: 1, Width: 5, Height: 4},
									TranslateX: 2, TranslateY: 1, Raster: raster, GlobalAlpha: globalAlpha,
									GlobalTransparency256: transparency, Transparency: keyed}
								got.dirty = Rectangle{X: 7, Y: 6, Width: 1, Height: 1}
								key := Color{R: 33, G: 88, B: 144, A: 255}
								got.descriptor.Transparent = &key
								want := cloneSurface(got)
								color := key
								color.A = alpha
								check(t, drawSurfacePixel(got, point.X, point.Y, color))
								check(t, referenceDrawSurfacePixel(want, point.X, point.Y, color))
								compareSurfaces(t, "pixel compositor", got, want)
								if t.Failed() {
									t.Fatalf("format=%d raster=%d alpha=%d globalAlpha=%d transparency=%d keyed=%v point=%v",
										format, raster, alpha, globalAlpha, transparency, keyed, point)
								}
							}
						}
					}
				}
			}
		}
	}
}

// Frozen pre-optimization compositor: keep scaled-blit comparisons independent
// of the production per-pixel shortcut, including alpha, keys and dirty bounds.
func referenceDrawSurfacePixel(current *surface, x, y int32, color Color) error {
	translatedX := int64(x) + int64(current.state.TranslateX)
	translatedY := int64(y) + int64(current.state.TranslateY)
	if translatedX < math.MinInt32 || translatedX > math.MaxInt32 ||
		translatedY < math.MinInt32 || translatedY > math.MaxInt32 {
		return nil
	}
	x = int32(translatedX)
	y = int32(translatedY)
	if !surfaceContains(current, x, y) ||
		x < current.state.Clip.X || y < current.state.Clip.Y ||
		int64(x) >= current.state.Clip.Right() ||
		int64(y) >= current.state.Clip.Bottom() {
		return nil
	}
	if current.state.Transparency &&
		current.descriptor.Transparent != nil &&
		color == *current.descriptor.Transparent {
		return nil
	}
	destination := decodeSurfaceColor(current, x, y)
	color = applyRaster(current.state.Raster, destination, color)
	if current.state.GlobalAlpha != 0xff {
		color.A = uint8(uint16(color.A) * uint16(current.state.GlobalAlpha) / 0xff)
	}
	if current.state.GlobalTransparency256 != 0 {
		// Keep the 256-scale factor through the final blend, rather than
		// reducing 257 possible levels to an eight-bit source alpha. Rounding
		// to nearest (ties upward) is a deterministic emulator choice.
		const denominator uint32 = 255 * 256
		alpha := uint32(color.A) * (256 - uint32(current.state.GlobalTransparency256))
		if alpha == 0 {
			return nil
		}
		inverse := denominator - alpha
		channel := func(s, d uint8) uint8 {
			return uint8((uint32(s)*alpha + uint32(d)*inverse + denominator/2) / denominator)
		}
		color = Color{R: channel(color.R, destination.R), G: channel(color.G, destination.G), B: channel(color.B, destination.B), A: channel(255, destination.A)}
	} else if color.A != 0xff {
		color = blendColor(destination, color)
	}
	encodeSurfaceColor(current, x, y, color)
	markSurfaceDirty(current, x, y)
	return nil
}
