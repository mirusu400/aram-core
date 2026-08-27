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

func TestReplacePixelRowsCopiesPackedPixelsAcrossPadding(t *testing.T) {
	graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 2, Height: 2, Stride: 12, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := make([]byte, 24)
	copy(source[0:8], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	copy(source[16:24], []byte{9, 10, 11, 12, 13, 14, 15, 16})
	if err := graphics.ReplacePixelRows(1, surface, source, 16); err != nil {
		t.Fatal(err)
	}
	pixels, err := graphics.Pixels(1, surface)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		1, 2, 3, 4, 5, 6, 7, 8, 0, 0, 0, 0,
		9, 10, 11, 12, 13, 14, 15, 16, 0, 0, 0, 0,
	}
	if !bytes.Equal(pixels, want) {
		t.Fatalf("padded surface pixels = %v, want %v", pixels, want)
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
