package ktf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

func ktfRGBABlitFixture(bounds image.Rectangle, pattern string) *image.RGBA {
	result := image.NewRGBA(bounds)
	for offset := 0; offset+3 < len(result.Pix); offset += 4 {
		pixel := offset / 4
		s := result.Pix[offset : offset+4]
		s[0], s[1], s[2], s[3] = byte(pixel*7+19), byte(pixel*11+31), byte(pixel*29+47), byte(pixel*37+15)
		switch pattern {
		case "opaque":
			s[3] = 255
		case "clear":
			clear(s)
		case "binary":
			if pixel%3 == 0 {
				clear(s)
			} else {
				s[3] = 255
			}
		case "mixed":
			switch pixel % 5 {
			case 0:
				clear(s)
			case 1:
				// Alpha zero with nonzero RGB is invalid premultiplied data,
				// but image/draw still defines the byte result we preserve.
				s[3] = 0
			case 2:
				s[3] = 255
			}
		}
	}
	return result
}

func cloneKTFRGBABlitFixture(source *image.RGBA) *image.RGBA {
	return &image.RGBA{Pix: append([]byte(nil), source.Pix...), Stride: source.Stride, Rect: source.Rect}
}

func TestKTFRGBABlitMatchesDrawWithClippingAndPadding(t *testing.T) {
	for _, pattern := range []string{"opaque", "clear", "binary", "partial", "mixed"} {
		for _, geometry := range []struct {
			name string
			rect image.Rectangle
			from image.Point
		}{
			{"forward", image.Rect(3, 4, 58, 62), image.Pt(4, 5)},
			{"reverse", image.Rect(7, 8, 62, 66), image.Pt(2, 3)},
			{"destination clipped", image.Rect(-20, -30, 100, 110), image.Pt(-10, -20)},
			{"source left clipped", image.Rect(3, 4, 58, 62), image.Pt(-20, -10)},
			{"source right clipped", image.Rect(1, 2, 40, 50), image.Pt(60, 66)},
			{"empty", image.Rect(3, 4, 3, 8), image.Pt(4, 5)},
			{"fully clipped", image.Rect(200, 300, 250, 350), image.Pt(4, 5)},
			{"extreme coordinates", image.Rect(math.MaxInt-40, math.MaxInt-40, math.MaxInt-20, math.MaxInt-20), image.Pt(4, 5)},
		} {
			t.Run(pattern+"/"+geometry.name, func(t *testing.T) {
				sourceBacking := ktfRGBABlitFixture(image.Rect(-7, -9, 86, 88), pattern)
				source := sourceBacking.SubImage(image.Rect(1, 2, 67, 70)).(*image.RGBA)
				gotBacking := ktfRGBABlitFixture(image.Rect(-11, -13, 93, 99), "partial")
				wantBacking := cloneKTFRGBABlitFixture(gotBacking)
				got := gotBacking.SubImage(image.Rect(-2, -3, 76, 79)).(*image.RGBA)
				want := wantBacking.SubImage(got.Rect).(*image.RGBA)
				draw.Draw(want, geometry.rect, source, geometry.from, draw.Over)
				drawKTFRGBAOver(got, geometry.rect, source, geometry.from)
				if !bytes.Equal(gotBacking.Pix, wantBacking.Pix) {
					t.Fatal("RGBA composite differs from image/draw or changed padding")
				}
			})
		}
	}
}

func TestKTFRGBABlitEveryAlphaMatchesDraw(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 256, 17))
	for y := 0; y < 17; y++ {
		for alpha := 0; alpha < 256; alpha++ {
			source.SetRGBA(alpha, y, color.RGBA{R: byte(y * 17), G: byte(alpha + y*31), B: byte(255 - y*13), A: byte(alpha)})
		}
	}
	got := ktfRGBABlitFixture(source.Rect, "partial")
	want := cloneKTFRGBABlitFixture(got)
	draw.Draw(want, want.Rect, source, source.Rect.Min, draw.Over)
	drawKTFRGBAOver(got, got.Rect, source, source.Rect.Min)
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Fatal("an alpha value changed RGBA composite rounding")
	}
}

func TestKTFRGBAOverChannelExhaustive(t *testing.T) {
	// Independently retain image/draw.drawCopyOver's 16-bit channel formula.
	// Include invalid premultiplied colors and their uint8 wraparound, not
	// merely channels <= alpha. All 256^3 input tuples fit without allocation.
	for alpha := uint32(0); alpha < 256; alpha++ {
		inverse := 255 - alpha
		scaledInverse := (0xffff - alpha*0x101) * 0x101
		for source := uint32(0); source < 256; source++ {
			for destination := uint32(0); destination < 256; destination++ {
				want := byte((destination*scaledInverse/0xffff + source*0x101) >> 8)
				got := ktfRGBAOverChannel(byte(destination), byte(source), inverse)
				if got != want {
					t.Fatalf("d=%d s=%d alpha=%d: got %d, want %d", destination, source, alpha, got, want)
				}
			}
		}
	}
}

func TestKTFRGBABlitMatchesDrawWhenPixelsAlias(t *testing.T) {
	for _, pattern := range []string{"opaque", "binary", "partial", "mixed"} {
		for _, views := range []string{"same image", "subimages", "offset slice", "unaligned offset slice"} {
			for _, geometry := range []struct {
				rect image.Rectangle
				from image.Point
			}{
				{image.Rect(2, 3, 29, 29), image.Pt(0, 0)},
				{image.Rect(0, 0, 27, 26), image.Pt(2, 3)},
				{image.Rect(3, 2, 30, 28), image.Pt(1, 2)},
				{image.Rect(1, 2, 28, 28), image.Pt(3, 2)},
				{image.Rect(1, 1, 28, 27), image.Pt(1, 1)},
			} {
				t.Run(fmt.Sprintf("%s/%s/%v", pattern, views, geometry.rect), func(t *testing.T) {
					gotBacking := ktfRGBABlitFixture(image.Rect(0, 0, 34, 32), pattern)
					wantBacking := cloneKTFRGBABlitFixture(gotBacking)
					gotDst, gotSource := gotBacking, gotBacking
					wantDst, wantSource := wantBacking, wantBacking
					switch views {
					case "subimages":
						gotDst = gotBacking.SubImage(image.Rect(2, 3, 32, 31)).(*image.RGBA)
						gotSource = gotBacking.SubImage(image.Rect(1, 1, 31, 29)).(*image.RGBA)
						wantDst = wantBacking.SubImage(gotDst.Rect).(*image.RGBA)
						wantSource = wantBacking.SubImage(gotSource.Rect).(*image.RGBA)
					case "offset slice", "unaligned offset slice":
						start := 4
						if views == "unaligned offset slice" {
							start = 1
						}
						gotSource = &image.RGBA{Pix: gotBacking.Pix[start:], Stride: gotBacking.Stride, Rect: image.Rect(0, 0, 33, 32)}
						wantSource = &image.RGBA{Pix: wantBacking.Pix[start:], Stride: wantBacking.Stride, Rect: gotSource.Rect}
					}
					draw.Draw(wantDst, geometry.rect, wantSource, geometry.from, draw.Over)
					drawKTFRGBAOver(gotDst, geometry.rect, gotSource, geometry.from)
					if !bytes.Equal(gotBacking.Pix, wantBacking.Pix) {
						t.Fatal("overlapping composite changed image/draw's traversal")
					}
				})
			}
		}
	}
}

func TestKTFMutableRGBABlitDoesNotCachePixels(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	source := ktfRGBABlitFixture(image.Rect(0, 0, 4, 4), "opaque")
	instance, err := runtime.newJavaImage(source)
	check(t, err)
	got := ktfRGBABlitFixture(image.Rect(0, 0, 12, 12), "partial")
	want := cloneKTFRGBABlitFixture(got)
	state := &ktfGraphics{Target: got, clip: got.Rect}
	for pass := 0; pass < 3; pass++ {
		source.SetRGBA(1, 2, color.RGBA{R: byte(30 + pass*70), A: byte(255 - pass*63)})
		runtime.drawKTFJavaImageRaw(state, instance, source, 3, 4, 0)
		draw.Draw(want, source.Rect.Add(image.Pt(3, 4)), source, source.Rect.Min, draw.Over)
		if !bytes.Equal(got.Pix, want.Pix) || !state.PixelsDirty {
			t.Fatal("mutable RGBA source used stale pixels or failed to mark the target dirty")
		}
		if len(runtime.blitCaches) != 0 {
			t.Fatal("mutable RGBA pixels entered the immutable sprite cache")
		}
		state.PixelsDirty = false
	}
}

func BenchmarkKTFRGBABlit(b *testing.B) {
	for _, pattern := range []string{"opaque", "clear", "binary", "partial"} {
		for _, fast := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/fast=%t", pattern, fast), func(b *testing.B) {
				source := ktfRGBABlitFixture(image.Rect(0, 0, 240, 320), pattern)
				destination := image.NewRGBA(source.Rect)
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					if fast {
						drawKTFRGBAOver(destination, destination.Rect, source, source.Rect.Min)
					} else {
						draw.Draw(destination, destination.Rect, source, source.Rect.Min, draw.Over)
					}
				}
			})
		}
	}
}
