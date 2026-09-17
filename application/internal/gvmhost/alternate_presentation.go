package gvmhost

import (
	"image"
	"image/color"
)

// renderAlternateOrientation converts the instruction-defined nonzero-orientation
// GVM display path. It intentionally does not stand in for the unresolved default
// orientation component ramps.
func renderAlternateOrientation(surface indexedSurface) *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, surface.height, surface.width))
	for y := 0; y < surface.height; y++ {
		for x := 0; x < surface.width; x++ {
			pixel := surface.pixels[y*surface.width+x]
			frame.SetRGBA(y, surface.width-1-x, alternateOrientationColor(pixel))
		}
	}
	return frame
}

func alternateOrientationColor(pixel byte) color.RGBA {
	switch pixel {
	case 0x00:
		return color.RGBA{A: 0xff}
	case 0x49:
		return color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	case 0x92:
		return color.RGBA{R: 0xc0, G: 0xc0, B: 0xc0, A: 0xff}
	case 0xff:
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	default:
		return color.RGBA{
			R: pixel & 0xe0,
			G: (pixel & 0x1c) << 3,
			B: (pixel & 0x03) << 6,
			A: 0xff,
		}
	}
}
