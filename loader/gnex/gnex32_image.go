package gnex

import (
	"bytes"
	"fmt"
)

// GNEX32MonoImage is the Mobile C one-bit raster layout. Kind 0x05 stores both
// local-palette selectors; kind 0x02 omits Color0 because zero bits are the
// transparent color and stores the selector for one bits in Color1.
type GNEX32MonoImage struct {
	Width, Height    int
	AnchorX, AnchorY int
	Color0, Color1   byte
	Pixels           []byte
}

func DecodeGNEX32MonoImage(data []byte) (GNEX32MonoImage, error) {
	if len(data) < 8 || (data[0] != 0x05 && data[0] != 0x02) || data[1] == 0 || data[2] == 0 {
		return GNEX32MonoImage{}, fmt.Errorf("gnex: invalid GNEX32 monochrome image header")
	}
	width, height := int(data[1]), int(data[2])
	pixelOffset := 7
	color0, color1 := data[5], data[6]
	if data[0] == 0x02 {
		pixelOffset = 6
		color0, color1 = 4, data[5]
	}
	end := pixelOffset + (width*height+7)/8
	if len(data) != end+(end&1) || (end&1 != 0 && data[end] != 0) {
		return GNEX32MonoImage{}, fmt.Errorf("gnex: GNEX32 monochrome image length or padding mismatch")
	}
	image := GNEX32MonoImage{
		Width: width, Height: height,
		AnchorX: int(int8(data[3])), AnchorY: int(int8(data[4])),
		Color0: color0, Color1: color1,
		Pixels: make([]byte, width*height),
	}
	for index := range image.Pixels {
		image.Pixels[index] = (data[pixelOffset+index/8] >> (7 - uint(index%8))) & 1
	}
	return image, nil
}

// GNEX32IndexedImage is the Mobile C indexed raster layout. Kinds 0x06/0x07
// store 2-bit/4-bit handset palette selectors. Kinds 0x09/0x0a/0x0b store
// 1-bit/2-bit/4-bit local RGB triplets; Mode 1 makes palette index zero
// transparent while Mode 0 draws it opaquely.
type GNEX32IndexedImage struct {
	Width           int
	Height          int
	AnchorX         int
	AnchorY         int
	Mode            byte
	BitsPerPixel    uint8
	PaletteTriplets []byte
	PaletteIndices  []byte
	Pixels          []byte
}

// DecodeGNEX32IndexedImage accepts only exact, even-padded layouts. Unrelated
// media records can begin with the same kind byte, so the complete length is
// mandatory.
func DecodeGNEX32IndexedImage(data []byte) (GNEX32IndexedImage, error) {
	if len(data) < 7 {
		return GNEX32IndexedImage{}, fmt.Errorf("gnex: truncated GNEX32 indexed image header")
	}
	if data[0] == 0x06 || data[0] == 0x07 {
		if data[1] == 0 || data[2] == 0 {
			return GNEX32IndexedImage{}, fmt.Errorf("gnex: invalid GNEX32 palette-index image header")
		}
		width, height := int(data[1]), int(data[2])
		bpp, colors := uint8(2), 4
		if data[0] == 0x07 {
			bpp, colors = 4, 16
		}
		pixelOffset := 5 + colors
		pixelBytes := (width*height*int(bpp) + 7) / 8
		end := pixelOffset + pixelBytes
		if len(data) != end+(end&1) || (end&1 != 0 && data[end] != 0) {
			return GNEX32IndexedImage{}, fmt.Errorf("gnex: GNEX32 palette-index image length or padding mismatch")
		}
		image := GNEX32IndexedImage{
			Width: width, Height: height, AnchorX: int(int8(data[3])), AnchorY: int(int8(data[4])),
			BitsPerPixel: bpp, PaletteIndices: bytes.Clone(data[5:pixelOffset]), Pixels: make([]byte, width*height),
		}
		for i := range image.Pixels {
			bit := i * int(bpp)
			value := (data[pixelOffset+bit/8] >> (8 - bpp - uint8(bit%8))) & byte((1<<bpp)-1)
			image.Pixels[i] = value
		}
		return image, nil
	}
	var bpp uint8
	var maxColors int
	switch data[0] {
	case 0x09:
		bpp, maxColors = 1, 2
	case 0x0a:
		bpp, maxColors = 2, 4
	case 0x0b:
		bpp, maxColors = 4, 16
	default:
		return GNEX32IndexedImage{}, fmt.Errorf("gnex: unsupported GNEX32 indexed image kind %#x", data[0])
	}
	width, height := int(data[1]), int(data[2])
	mode, colors := data[5], int(data[6])
	if data[0] == 0x07 {
		mode, colors = data[6], int(data[5])
	}
	if width == 0 || height == 0 || mode > 1 || colors == 0 || colors > maxColors {
		return GNEX32IndexedImage{}, fmt.Errorf("gnex: invalid GNEX32 indexed image dimensions, mode or palette")
	}
	pixelCount := width * height
	pixelBytes := (pixelCount*int(bpp) + 7) / 8
	pixelOffset := 7 + colors*3
	end := pixelOffset + pixelBytes
	if len(data) != end+(end&1) || (end&1 != 0 && data[end] != 0) {
		return GNEX32IndexedImage{}, fmt.Errorf("gnex: GNEX32 indexed image length or padding mismatch")
	}
	image := GNEX32IndexedImage{
		Width: width, Height: height,
		AnchorX: int(int8(data[3])), AnchorY: int(int8(data[4])),
		Mode: mode, BitsPerPixel: bpp,
		PaletteTriplets: bytes.Clone(data[7:pixelOffset]),
		Pixels:          make([]byte, pixelCount),
	}
	mask := byte((1 << bpp) - 1)
	packed := data[pixelOffset:end]
	for index := range image.Pixels {
		bit := index * int(bpp)
		color := (packed[bit/8] >> (8 - bpp - uint8(bit%8))) & mask
		if int(color) >= colors {
			return GNEX32IndexedImage{}, fmt.Errorf("gnex: GNEX32 pixel index exceeds palette")
		}
		image.Pixels[index] = color
	}
	return image, nil
}
