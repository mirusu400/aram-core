package runtime

import (
	"bytes"
	"testing"
)

// TestSurfaceRGBAFastPathsMatchGeneric checks the per-format loops in
// surfaceRGBA against the generic decodeSurfaceColor loop they bypass.
//
// surfaceRGBA runs over the whole screen on every presented frame, so it
// decides the pixel format once instead of re-dispatching inside
// decodeSurfaceColor per pixel. The specialised loops have to produce exactly
// what the general one did, including for a surface whose rows are padded.
func TestSurfaceRGBAFastPathsMatchGeneric(t *testing.T) {
	const width, height = 7, 5
	for _, format := range []PixelFormat{
		PixelRGBA8888,
		PixelARGB8888,
		PixelXRGB8888,
		PixelBGRX8888,
		PixelRGB565,
		PixelRGB555,
		PixelGray8,
	} {
		for _, padding := range []int32{0, 3} {
			stride := int32(width)*int32(format.BytesPerPixel()) + padding
			current := &surface{
				descriptor: SurfaceDescriptor{
					Width:  width,
					Height: height,
					Stride: stride,
					Format: format,
				},
				pixels: make([]byte, int(stride)*height),
			}
			for index := range current.pixels {
				current.pixels[index] = byte(index*37 + 11)
			}
			got, err := surfaceRGBA(current)
			if err != nil {
				t.Fatalf("format %d padding %d: %v", format, padding, err)
			}
			want := genericSurfaceRGBA(current)
			if !bytes.Equal(got, want) {
				t.Errorf(
					"format %d padding %d: fast path differs from generic",
					format,
					padding,
				)
			}
		}
	}
}

// genericSurfaceRGBA is the pixel-at-a-time conversion surfaceRGBA's
// specialised loops replace.
func genericSurfaceRGBA(current *surface) []byte {
	width := int(current.descriptor.Width)
	rgba := make([]byte, width*int(current.descriptor.Height)*4)
	for y := int32(0); y < current.descriptor.Height; y++ {
		for x := int32(0); x < current.descriptor.Width; x++ {
			color := decodeSurfaceColor(current, x, y)
			offset := (int(y)*width + int(x)) * 4
			rgba[offset+0] = color.R
			rgba[offset+1] = color.G
			rgba[offset+2] = color.B
			rgba[offset+3] = color.A
		}
	}
	return rgba
}
