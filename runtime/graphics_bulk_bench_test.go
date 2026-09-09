package runtime

import "testing"

// The benchmarks below cover the bulk pixel paths a title runs every frame: a
// full-screen clear, a sprite blit, and a scaled blit. They exist so the
// row-copy fast paths in graphics_bulk.go can be measured against the
// per-pixel path they replace, and so a later change that pushes these calls
// back onto the per-pixel path shows up as a regression.

const (
	benchScreenWidth  = 240
	benchScreenHeight = 320
)

func benchSurfaces(
	b *testing.B,
	format PixelFormat,
	sourceWidth, sourceHeight int32,
) (*Graphics, ServiceID, ServiceID) {
	b.Helper()
	graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
	check(b, err)
	destination, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  benchScreenWidth,
		Height: benchScreenHeight,
		Format: format,
	})
	check(b, err)
	source, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  sourceWidth,
		Height: sourceHeight,
		Format: format,
	})
	check(b, err)
	current, err := graphics.get(source, 1)
	check(b, err)
	for index := range current.pixels {
		current.pixels[index] = byte(index*31 + 7)
	}
	if format == PixelRGBA8888 {
		for index := 3; index < len(current.pixels); index += 4 {
			current.pixels[index] = 0xff
		}
	}
	return graphics, destination, source
}

func BenchmarkClear(b *testing.B) {
	graphics, destination, _ := benchSurfaces(b, PixelRGBA8888, 1, 1)
	color := RGB(0x20, 0x40, 0x60)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.Clear(1, destination, color))
	}
}

func BenchmarkClearRGB565(b *testing.B) {
	graphics, destination, _ := benchSurfaces(b, PixelRGB565, 1, 1)
	color := RGB(0x20, 0x40, 0x60)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.Clear(1, destination, color))
	}
}

func BenchmarkBlit(b *testing.B) {
	graphics, destination, source := benchSurfaces(b, PixelRGBA8888, 64, 64)
	region := Rectangle{Width: 64, Height: 64}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.Blit(1, destination, source, 8, 8, region))
	}
}

func BenchmarkBlitFullScreen(b *testing.B) {
	graphics, destination, source := benchSurfaces(
		b,
		PixelRGBA8888,
		benchScreenWidth,
		benchScreenHeight,
	)
	region := Rectangle{Width: benchScreenWidth, Height: benchScreenHeight}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.Blit(1, destination, source, 0, 0, region))
	}
}

// BenchmarkBlitRasterXOR keeps the surfaces and rectangles of BenchmarkBlit
// but sets a raster operation, which the fast path never handles. It is the
// control: the per-pixel path must stay usable and must not get slower.
func BenchmarkBlitRasterXOR(b *testing.B) {
	graphics, destination, source := benchSurfaces(b, PixelRGBA8888, 64, 64)
	check(b, graphics.SetDrawState(1, destination, SurfaceDrawState{
		Clip:        Rectangle{Width: benchScreenWidth, Height: benchScreenHeight},
		Raster:      RasterXOR,
		GlobalAlpha: 0xff,
	}))
	region := Rectangle{Width: 64, Height: 64}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.Blit(1, destination, source, 8, 8, region))
	}
}

// BenchmarkRectangleFill measures the per-pixel path on its own: a filled
// rectangle plots every pixel through drawSurfacePixel, so it is where the
// dirty-rectangle bookkeeping shows up undiluted.
func BenchmarkRectangleFill(b *testing.B) {
	graphics, destination, _ := benchSurfaces(b, PixelRGBA8888, 1, 1)
	region := Rectangle{X: 8, Y: 8, Width: 200, Height: 280}
	color := RGB(0x10, 0x20, 0x30)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.Rectangle(1, destination, region, color, true))
	}
}

func BenchmarkScaledBlit(b *testing.B) {
	graphics, destination, source := benchSurfaces(b, PixelRGBA8888, 64, 64)
	destinationRectangle := Rectangle{X: 8, Y: 8, Width: 128, Height: 128}
	sourceRectangle := Rectangle{Width: 64, Height: 64}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.ScaledBlit(
			1,
			destination,
			source,
			destinationRectangle,
			sourceRectangle,
		))
	}
}

// BenchmarkScaledBlitUnscaled is the case WIPI sprite drawing actually hits:
// ScaledBlit called with equal source and destination sizes.
func BenchmarkScaledBlitUnscaled(b *testing.B) {
	graphics, destination, source := benchSurfaces(b, PixelRGBA8888, 64, 64)
	destinationRectangle := Rectangle{X: 8, Y: 8, Width: 64, Height: 64}
	sourceRectangle := Rectangle{Width: 64, Height: 64}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		check(b, graphics.ScaledBlit(
			1,
			destination,
			source,
			destinationRectangle,
			sourceRectangle,
		))
	}
}
