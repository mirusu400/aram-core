package ktf

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

type wrappedPlotRGBA struct{ *image.RGBA }

func TestKTFPlotRGBAFastPathMatchesGenericImage(t *testing.T) {
	for _, xor := range []bool{false, true} {
		for _, ink := range []color.RGBA{{0x12, 0x34, 0x56, 0xff}, {0xff, 0x80, 0x01, 0x80}, {0, 0, 0, 0}} {
			fastImage := image.NewRGBA(image.Rect(-3, -2, 8, 9))
			genericImage := image.NewRGBA(fastImage.Bounds())
			for i := range fastImage.Pix {
				fastImage.Pix[i] = byte(i * 37)
			}
			copy(genericImage.Pix, fastImage.Pix)
			fast := ktfGraphics{Target: fastImage, color: ink, xorMode: xor}
			generic := ktfGraphics{Target: wrappedPlotRGBA{genericImage}, color: ink, xorMode: xor}
			for y := -4; y < 12; y++ {
				for x := -5; x < 11; x++ {
					fast.plot(x, y)
					generic.plot(x, y)
				}
			}
			if !bytes.Equal(fastImage.Pix, genericImage.Pix) {
				t.Fatalf("plot differs: xor=%t ink=%+v", xor, ink)
			}
		}
	}
}
