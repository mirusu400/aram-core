package gnex

import (
	"bytes"
	"testing"
)

func TestDecodeGNEX32IndexedImage(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		bpp  uint8
		want []byte
	}{
		{
			name: "two bit with zero pad",
			data: []byte{
				0x0a, 3, 2, 0xff, 2, 1, 4,
				1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12,
				0x1b, 0x40, 0,
			},
			bpp: 2, want: []byte{0, 1, 2, 3, 1, 0},
		},
		{
			name: "four bit",
			data: []byte{
				0x0b, 3, 1, 1, 0xfe, 0, 3,
				1, 2, 3, 4, 5, 6, 7, 8, 9,
				0x01, 0x20,
			},
			bpp: 4, want: []byte{0, 1, 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeGNEX32IndexedImage(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if got.Width != int(tc.data[1]) || got.Height != int(tc.data[2]) ||
				got.AnchorX != int(int8(tc.data[3])) || got.AnchorY != int(int8(tc.data[4])) ||
				got.Mode != tc.data[5] || got.BitsPerPixel != tc.bpp ||
				!bytes.Equal(got.Pixels, tc.want) || !bytes.Equal(got.PaletteTriplets, tc.data[7:7+3*int(tc.data[6])]) {
				t.Fatalf("decoded image = %+v", got)
			}
			tc.data[7] = 99
			if got.PaletteTriplets[0] != 1 {
				t.Fatal("decoded palette aliases resource")
			}
		})
	}
}

func TestDecodeGNEX32MonoImage(t *testing.T) {
	data := []byte{0x05, 3, 2, 1, 0xff, 4, 3, 0xa8}
	got, err := DecodeGNEX32MonoImage(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 3 || got.Height != 2 || got.AnchorX != 1 || got.AnchorY != -1 ||
		got.Color0 != 4 || got.Color1 != 3 || !bytes.Equal(got.Pixels, []byte{1, 0, 1, 0, 1, 0}) {
		t.Fatalf("mono image = %+v", got)
	}
	data[7] = 0
	if got.Pixels[0] != 1 {
		t.Fatal("decoded pixels alias media")
	}
	for _, bad := range [][]byte{
		data[:6],
		{0x05, 0, 2, 0, 0, 4, 3, 0},
		{0x05, 3, 2, 0, 0, 4, 3},
		{0x05, 3, 2, 0, 0, 4, 3, 0, 0},
		{0x05, 3, 2, 0, 0, 4, 3, 0, 0, 0},
	} {
		if _, err := DecodeGNEX32MonoImage(bad); err == nil {
			t.Errorf("accepted malformed mono image: %x", bad)
		}
	}
}

func TestDecodeGNEX32IndexedImageRejectsBadLayout(t *testing.T) {
	valid := []byte{0x0a, 3, 2, 0, 0, 1, 4, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0x1b, 0x40, 0}
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"short header", func(b []byte) []byte { return b[:6] }},
		{"zero width", func(b []byte) []byte { b[1] = 0; return b }},
		{"too many palette entries", func(b []byte) []byte { b[6] = 5; return b }},
		{"truncated pixels", func(b []byte) []byte { return b[:20] }},
		{"extra bytes", func(b []byte) []byte { return append(b, 0, 0) }},
		{"nonzero padding", func(b []byte) []byte { b[len(b)-1] = 1; return b }},
		{"unknown type", func(b []byte) []byte { b[0] = 9; return b }},
		{"pixel beyond palette", func(b []byte) []byte {
			// Keep the three-color header and packed-pixel span internally
			// consistent; the fourth color index alone must be rejected.
			b[6] = 3
			return []byte{b[0], b[1], b[2], b[3], b[4], b[5], b[6],
				b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15],
				0x1b, 0x40}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.edit(bytes.Clone(valid))
			if _, err := DecodeGNEX32IndexedImage(data); err == nil {
				t.Fatalf("accepted malformed image: %x", data)
			}
		})
	}
}
