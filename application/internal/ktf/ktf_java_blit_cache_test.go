package ktf

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"math/rand"
	"testing"
)

// TestKTFBlitFastPathMatchesDrawNRGBAOver is the regression issue #217 asks
// for: drawKTFJavaImageFast's hand-written composite has to be bit-identical
// to image/draw's drawNRGBAOver, not merely close - a cached 8-bit
// premultiplied copy was rejected for exactly this reason (it could not carry
// R=100, A=128 without losing a unit). It draws the same source pixels -
// the awkward alpha boundaries plus a spread of random ones - through
// draw.Draw and through drawKTFJavaImageRaw's fast path onto matching
// destinations, over a few different backgrounds, and compares every byte.
func TestKTFBlitFastPathMatchesDrawNRGBAOver(t *testing.T) {
	type nrgba struct{ r, g, b, a uint8 }
	pixels := []nrgba{
		{r: 0, g: 0, b: 0, a: 0},       // fully transparent, and black
		{r: 255, g: 255, b: 255, a: 0}, // fully transparent, but not black
		{r: 100, g: 0, b: 0, a: 128},   // the issue's own canary: R=100, A=128
		{r: 100, g: 200, b: 50, a: 128},
		{r: 255, g: 255, b: 255, a: 255}, // fully opaque
		{r: 0, g: 0, b: 0, a: 255},
		{r: 1, g: 254, b: 3, a: 1},
		{r: 254, g: 1, b: 251, a: 254},
	}
	random := rand.New(rand.NewSource(217))
	for i := 0; i < 64; i++ {
		pixels = append(pixels, nrgba{
			r: uint8(random.Intn(256)),
			g: uint8(random.Intn(256)),
			b: uint8(random.Intn(256)),
			a: uint8(random.Intn(256)),
		})
	}

	backgrounds := []color.RGBA{
		{R: 0, G: 0, B: 0, A: 255},
		{R: 255, G: 255, B: 255, A: 255},
		{R: 10, G: 20, B: 30, A: 255},
		{R: 200, G: 100, B: 50, A: 0},
	}

	for backgroundIndex, background := range backgrounds {
		source := image.NewNRGBA(image.Rect(0, 0, len(pixels), 1))
		for i, p := range pixels {
			source.SetNRGBA(i, 0, color.NRGBA{R: p.r, G: p.g, B: p.b, A: p.a})
		}

		reference := image.NewRGBA(image.Rect(0, 0, len(pixels), 1))
		draw.Draw(reference, reference.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
		draw.Draw(reference, reference.Bounds(), source, image.Point{}, draw.Over)

		got := image.NewRGBA(image.Rect(0, 0, len(pixels), 1))
		draw.Draw(got, got.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
		runtime := &Runtime{images: map[uint32]image.Image{1: source}}
		state := &ktfGraphics{Target: got, clip: got.Bounds()}
		runtime.drawKTFJavaImageRaw(state, 1, source, 0, 0, 0)

		if len(runtime.blitCaches) != 1 {
			t.Fatalf(
				"background %d %+v: fast path did not cache the source (fell back to draw.Draw, so this run proved nothing)",
				backgroundIndex, background,
			)
		}
		if !bytes.Equal(reference.Pix, got.Pix) {
			t.Fatalf(
				"background %d %+v: fast-path blit diverged from draw.Draw\n draw.Draw: %v\n fast path: %v",
				backgroundIndex, background, reference.Pix, got.Pix,
			)
		}
	}
}

// TestKTFBlitCacheDropsWhenGraphicsWritesIntoTheSourceImage covers the other
// half of issue #217: a cache this cheap to consult is worthless the moment
// it can go stale. Image.getGraphics() hands back a Graphics whose Target is
// the same pixels this Image is blitted from elsewhere (state.image links
// them, the same relationship ensureGraphicsSurface already uses to know
// which mirror to refresh), so any write through it - here simulated the way
// every real write site does it, via markKTFGraphicsDirty - has to invalidate
// the cached blit source, or a later draw of the same Image would keep
// presenting the pixels from before the write.
func TestKTFBlitCacheDropsWhenGraphicsWritesIntoTheSourceImage(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 10, A: 255})

	const imageAddress = uint32(7)
	runtime := &Runtime{images: map[uint32]image.Image{imageAddress: source}}

	dst := image.NewRGBA(image.Rect(0, 0, 1, 1))
	screen := &ktfGraphics{Target: dst, clip: dst.Bounds()}
	runtime.drawKTFJavaImageRaw(screen, imageAddress, source, 0, 0, 0)
	if got := dst.Pix[0]; got != 10 {
		t.Fatalf("first draw red = %d, want 10", got)
	}
	if len(runtime.blitCaches) != 1 {
		t.Fatal("first draw did not populate the blit cache")
	}

	// The guest mutates the sprite through a Graphics obtained from it.
	source.SetNRGBA(0, 0, color.NRGBA{R: 200, A: 255})
	imageGraphics := &ktfGraphics{
		Target: source,
		image:  imageAddress,
		clip:   source.Bounds(),
	}
	runtime.markKTFGraphicsDirty(imageGraphics)
	if _, stale := runtime.blitCaches[imageAddress]; stale {
		t.Fatal("blit cache survived a write into its source image")
	}

	dst2 := image.NewRGBA(image.Rect(0, 0, 1, 1))
	screen2 := &ktfGraphics{Target: dst2, clip: dst2.Bounds()}
	runtime.drawKTFJavaImageRaw(screen2, imageAddress, source, 0, 0, 0)
	if got := dst2.Pix[0]; got != 200 {
		t.Fatalf(
			"second draw red = %d, want 200 (the pixel written after the first draw); a stale cache served the old sprite",
			got,
		)
	}
}
