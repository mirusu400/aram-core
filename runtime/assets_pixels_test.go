package runtime

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

// This intentionally uses the public color model as the conversion oracle.
func referenceAssetRGBA(source image.Image) []byte {
	bounds := source.Bounds()
	var pixels []byte
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			pixels = append(pixels, c.R, c.G, c.B, c.A)
		}
	}
	return pixels
}

func TestAssetRGBARowsMatchColorModel(t *testing.T) {
	rect := image.Rect(-7, 11, 265, 16)
	rgba := image.NewRGBA(rect)
	nrgba := image.NewNRGBA(rect)
	// Cover every alpha, valid premultiplied channels, and hidden RGB at A=0.
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			a := byte(x - rect.Min.X)
			c := color.RGBA{a, a / 2, a / 3, a}
			if a == 0 {
				c = color.RGBA{231, 71, 42, 0}
			}
			rgba.SetRGBA(x, y, c)
			nrgba.SetNRGBA(x, y, color.NRGBA{231, 71, 42, a})
		}
	}
	palette := color.Palette{color.NRGBA{123, 231, 42, 0}, color.RGBA{31, 17, 2, 79}, color.Gray16{Y: 0x1234}, color.White}
	paletted := image.NewPaletted(rect, palette)
	for index := range paletted.Pix {
		paletted.Pix[index] = byte(index % len(palette))
	}
	gray := image.NewGray(rect)
	for index := range gray.Pix {
		gray.Pix[index] = byte(index)
	}
	for _, input := range []struct {
		name  string
		image image.Image
	}{
		{"rgba", rgba}, {"nrgba", nrgba}, {"palette", paletted}, {"fallback", gray},
		{"rgba-subimage", rgba.SubImage(image.Rect(-3, 12, 259, 15))},
		{"nrgba-subimage", nrgba.SubImage(image.Rect(-3, 12, 259, 15))},
		{"palette-subimage", paletted.SubImage(image.Rect(-3, 12, 259, 15))},
		{"empty", image.NewRGBA(image.Rectangle{})},
	} {
		t.Run(input.name, func(t *testing.T) {
			got, want := rgbaBytes(input.image), referenceAssetRGBA(input.image)
			if !bytes.Equal(got, want) {
				t.Fatal("decoded pixel rows differ from color-model conversion")
			}
			if len(got) > 0 {
				got[0] ^= 0xff
				if !bytes.Equal(rgbaBytes(input.image), want) {
					t.Fatal("converted pixels alias the source image")
				}
			}
		})
	}
}
