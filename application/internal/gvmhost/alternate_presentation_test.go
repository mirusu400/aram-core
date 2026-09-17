package gvmhost

import (
	"image/color"
	"testing"
)

func TestAlternateOrientationColor(t *testing.T) {
	tests := []struct {
		pixel byte
		want  color.RGBA
	}{
		{0x00, color.RGBA{A: 0xff}},
		{0x49, color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}},
		{0x92, color.RGBA{R: 0xc0, G: 0xc0, B: 0xc0, A: 0xff}},
		{0xff, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
		{0xe7, color.RGBA{R: 0xe0, G: 0x20, B: 0xc0, A: 0xff}},
		{0x1c, color.RGBA{G: 0xe0, A: 0xff}},
	}
	for _, test := range tests {
		if got := alternateOrientationColor(test.pixel); got != test.want {
			t.Fatalf("alternateOrientationColor(%#02x) = %#v, want %#v", test.pixel, got, test.want)
		}
	}
}

func TestRenderAlternateOrientationRotatesCounterclockwise(t *testing.T) {
	surface := newIndexedSurface(3, 2)
	surface.pixels = []byte{1, 2, 3, 5, 6, 7}
	frame := renderAlternateOrientation(surface)
	if got, want := frame.Bounds().Dx(), 2; got != want {
		t.Fatalf("width = %d, want %d", got, want)
	}
	if got, want := frame.Bounds().Dy(), 3; got != want {
		t.Fatalf("height = %d, want %d", got, want)
	}

	// Source rows [1 2 3], [5 6 7] become [3 7], [2 6], [1 5].
	want := [][]byte{{3, 7}, {2, 6}, {1, 5}}
	for y := range want {
		for x := range want[y] {
			if got := frame.RGBAAt(x, y); got != alternateOrientationColor(want[y][x]) {
				t.Fatalf("pixel (%d,%d) = %#v, want code %#02x", x, y, got, want[y][x])
			}
		}
	}
}
