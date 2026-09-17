package gvmhost

import (
	"bytes"
	"errors"
	"testing"
)

func TestDecodeIndexedSpriteLayouts(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		palette []byte
		pixels  []byte
	}{
		{
			name:    "type 2 nibble palette and continuous 1bpp",
			data:    []byte{2, 3, 2, 0, 0, 0xa4, 0xb4},
			palette: []byte{10, 4},
			pixels:  []byte{1, 0, 1, 1, 0, 1},
		},
		{
			name:    "type 5 byte palette and continuous 1bpp",
			data:    []byte{5, 5, 2, 0, 0, 7, 9, 0xaa, 0x80},
			palette: []byte{7, 9},
			pixels:  []byte{1, 0, 1, 0, 1, 0, 1, 0, 1, 0},
		},
		{
			name:    "type 6 four entry palette and continuous 2bpp",
			data:    []byte{6, 3, 2, 0, 0, 10, 11, 12, 13, 0x1b, 0x10},
			palette: []byte{10, 11, 12, 13},
			pixels:  []byte{0, 1, 2, 3, 0, 1},
		},
		{
			name: "type 7 sixteen entry palette and continuous 4bpp",
			data: append(
				append([]byte{7, 3, 2, 0, 0}, sequence(16)...),
				0x01, 0x23, 0x45,
			),
			palette: sequence(16),
			pixels:  []byte{0, 1, 2, 3, 4, 5},
		},
		{
			name:    "type 8 raw bytes",
			data:    []byte{8, 3, 2, 0, 0, 0, 4, 181, 182, 240, 255},
			palette: nil,
			pixels:  []byte{0, 4, 181, 182, 240, 255},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sprite, err := decodeIndexedSprite(test.data)
			if err != nil {
				t.Fatalf("decodeIndexedSprite() error = %v", err)
			}
			if !bytes.Equal(sprite.palette, test.palette) {
				t.Fatalf("palette = %v, want %v", sprite.palette, test.palette)
			}
			if !bytes.Equal(sprite.pixels, test.pixels) {
				t.Fatalf("pixels = %v, want %v", sprite.pixels, test.pixels)
			}
		})
	}
}

func TestRasterizeIndexedSpriteAnchorAndInclusiveClipping(t *testing.T) {
	surface := newIndexedSurface(3, 3)
	for index := range surface.pixels {
		surface.pixels[index] = 99
	}
	// The anchor puts the 3x3 sprite at (-1,-1). Coordinates zero through one
	// on both axes remain visible, including both inclusive boundary pixels.
	data := []byte{8, 3, 3, 1, 1, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if err := rasterizeIndexedSprite(&surface, data, 0, 0); err != nil {
		t.Fatalf("rasterizeIndexedSprite() error = %v", err)
	}
	want := []byte{
		5, 6, 99,
		8, 9, 99,
		99, 99, 99,
	}
	if !bytes.Equal(surface.pixels, want) {
		t.Fatalf("surface = %v, want %v", surface.pixels, want)
	}
}

func TestRasterizeIndexedSpriteSignedNegativeAnchor(t *testing.T) {
	surface := newIndexedSurface(3, 1)
	data := []byte{8, 1, 1, 0xff, 0, 7}
	if err := rasterizeIndexedSprite(&surface, data, 0, 0); err != nil {
		t.Fatalf("rasterizeIndexedSprite() error = %v", err)
	}
	if want := []byte{0, 7, 0}; !bytes.Equal(surface.pixels, want) {
		t.Fatalf("surface = %v, want %v", surface.pixels, want)
	}
}

func TestRasterizeIndexedSpriteAnchorAndClippingEdges(t *testing.T) {
	t.Run("signed endpoints", func(t *testing.T) {
		sprite, err := decodeIndexedSprite([]byte{8, 1, 1, 0x80, 0x7f, 9})
		if err != nil {
			t.Fatalf("decodeIndexedSprite() error = %v", err)
		}
		if sprite.anchorX != -128 || sprite.anchorY != 127 {
			t.Fatalf("anchors = (%d,%d), want (-128,127)", sprite.anchorX, sprite.anchorY)
		}
	})

	t.Run("right and bottom clipping", func(t *testing.T) {
		surface := newIndexedSurface(2, 2)
		data := []byte{8, 2, 2, 0, 0, 1, 2, 3, 4}
		if err := rasterizeIndexedSprite(&surface, data, 1, 1); err != nil {
			t.Fatalf("rasterizeIndexedSprite() error = %v", err)
		}
		if want := []byte{0, 0, 0, 1}; !bytes.Equal(surface.pixels, want) {
			t.Fatalf("surface = %v, want %v", surface.pixels, want)
		}
	})

	t.Run("fully off screen", func(t *testing.T) {
		surface := newIndexedSurface(2, 2)
		for index := range surface.pixels {
			surface.pixels[index] = 6
		}
		before := append([]byte(nil), surface.pixels...)
		if err := rasterizeIndexedSprite(&surface, []byte{8, 1, 1, 0, 0, 9}, -3, -3); err != nil {
			t.Fatalf("rasterizeIndexedSprite() error = %v", err)
		}
		if !bytes.Equal(surface.pixels, before) {
			t.Fatalf("surface mutated: got %v, want %v", surface.pixels, before)
		}
	})
}

func TestRasterizeIndexedSpriteTransparency(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want []byte
	}{
		{"type 2 last palette index code 4", []byte{2, 2, 1, 0, 0, 0x94, 0x40}, []byte{9, 77}},
		{"type 5 last palette index code 4", []byte{5, 2, 1, 0, 0, 9, 4, 0x40}, []byte{9, 77}},
		{"type 6 last palette index code 4", []byte{6, 2, 1, 0, 0, 9, 8, 7, 4, 0x30}, []byte{9, 77}},
		{"type 7 last palette index code 4", append(append([]byte{7, 2, 1, 0, 0}, append(sequence(15), 4)...), 0x0f), []byte{0, 77}},
		{"type 8 raw index 4", []byte{8, 2, 1, 0, 0, 9, 4}, []byte{9, 77}},
		{"code 4 at non-last palette index is transparent", []byte{5, 2, 1, 0, 0, 4, 9, 0x40}, []byte{77, 9}},
		{"last code 4 occurrence wins", []byte{6, 2, 1, 0, 0, 4, 9, 4, 8, 0x20}, []byte{4, 77}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			surface := newIndexedSurface(2, 1)
			surface.pixels[0], surface.pixels[1] = 77, 77
			if err := rasterizeIndexedSprite(&surface, test.data, 0, 0); err != nil {
				t.Fatalf("rasterizeIndexedSprite() error = %v", err)
			}
			if !bytes.Equal(surface.pixels, test.want) {
				t.Fatalf("surface = %v, want %v", surface.pixels, test.want)
			}
		})
	}
}

func TestRasterizeIndexedSpriteRejectsInvalidInputAtomically(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		err  error
	}{
		{"short header", []byte{8, 1, 1}, errTruncatedSprite},
		{"invalid type", []byte{3, 1, 1, 0, 0, 0}, errInvalidSpriteType},
		{"zero width", []byte{8, 0, 1, 0, 0}, errInvalidSpriteDimensions},
		{"zero height", []byte{8, 1, 0, 0, 0}, errInvalidSpriteDimensions},
		{"truncated type 2 palette", []byte{2, 1, 1, 0, 0}, errTruncatedSprite},
		{"truncated type 5 pixels", []byte{5, 9, 1, 0, 0, 1, 2, 0xff}, errTruncatedSprite},
		{"truncated type 6 pixels", []byte{6, 5, 1, 0, 0, 1, 2, 3, 4, 0xff}, errTruncatedSprite},
		{"truncated type 7 palette", append([]byte{7, 1, 1, 0, 0}, sequence(15)...), errTruncatedSprite},
		{"truncated type 8 pixels", []byte{8, 2, 1, 0, 0, 1}, errTruncatedSprite},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			surface := newIndexedSurface(3, 2)
			for index := range surface.pixels {
				surface.pixels[index] = byte(index + 20)
			}
			before := append([]byte(nil), surface.pixels...)
			err := rasterizeIndexedSprite(&surface, test.data, 0, 0)
			if !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
			if !bytes.Equal(surface.pixels, before) {
				t.Fatalf("surface mutated: got %v, want %v", surface.pixels, before)
			}
		})
	}
}

func TestDecodeIndexedSpriteAcceptsType8ValuesAbove181(t *testing.T) {
	sprite, err := decodeIndexedSprite([]byte{8, 3, 1, 0, 0, 182, 240, 255})
	if err != nil {
		t.Fatalf("decodeIndexedSprite() error = %v", err)
	}
	if want := []byte{182, 240, 255}; !bytes.Equal(sprite.pixels, want) {
		t.Fatalf("pixels = %v, want %v", sprite.pixels, want)
	}
}

func sequence(length int) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = byte(index)
	}
	return result
}
