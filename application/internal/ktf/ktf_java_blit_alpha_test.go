package ktf

import (
	"bytes"
	"image"
	"image/draw"
	"testing"
)

func ktfAlphaSprite(alpha string) *image.NRGBA {
	// Keep the source's origin and row stride different from the clipped blit.
	backing := image.NewNRGBA(image.Rect(-4, -3, 81, 75))
	for y := 0; y < backing.Rect.Dy(); y++ {
		for x := 0; x < backing.Rect.Dx(); x++ {
			p := y*backing.Stride + x*4
			backing.Pix[p] = byte(x*7 + y*11)
			backing.Pix[p+1] = byte(x*3 + y*23)
			backing.Pix[p+2] = byte(x*31 + y*5)
			backing.Pix[p+3] = 255
			switch alpha {
			case "binary":
				if (x+y)%3 == 0 {
					backing.Pix[p+3] = 0
				}
			case "partial":
				backing.Pix[p+3] = byte(x*13 + y*19)
			}
		}
	}
	return backing.SubImage(image.Rect(1, 2, 65, 66)).(*image.NRGBA)
}

func TestKTFBlitCachedAlphaAndClippingMatchDraw(t *testing.T) {
	for _, alpha := range []string{"opaque", "binary", "partial"} {
		t.Run(alpha, func(t *testing.T) {
			source := ktfAlphaSprite(alpha)
			cache := newKTFBlitCache(source)
			got := image.NewRGBA(image.Rect(-5, -7, 91, 89))
			for i := range got.Pix {
				got.Pix[i] = byte(i*29 + 17)
			}
			want := image.NewRGBA(got.Rect)
			copy(want.Pix, got.Pix)
			rect := image.Rect(7, 9, 49, 53)
			origin := image.Pt(11, 13)
			draw.Draw(want, rect, source, source.Rect.Min.Add(origin), draw.Over)
			drawKTFBlitOverCached(got, rect, cache, origin)
			if !bytes.Equal(got.Pix, want.Pix) {
				t.Fatal("cached clipped sprite differs from image/draw")
			}
		})
	}
}

func BenchmarkKTFBlitCachedAlpha(b *testing.B) {
	for _, alpha := range []string{"opaque", "binary", "partial"} {
		b.Run(alpha, func(b *testing.B) {
			source := ktfAlphaSprite(alpha)
			cache := newKTFBlitCache(source)
			destination := image.NewRGBA(image.Rect(0, 0, 64, 64))
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				drawKTFBlitOverCached(destination, destination.Rect, cache, image.Point{})
			}
		})
	}
}
