package runtime

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Issue #524: a marked KTF sprite can leave biClrImportant at the palette
// count and put its magenta key in entry zero, away from the top-left pixel.
func TestAssetsDecodeBMPPaletteCountMagentaKey(t *testing.T) {
	for _, test := range []struct {
		name       string
		reserved   uint16
		important  uint32
		colorsUsed uint32
		firstRed   byte
		wantAlpha  [2]byte
	}{
		{"marked full palette", 1, 256, 0, 255, [2]byte{255, 0}},
		{"marked explicit palette count", 1, 2, 2, 255, [2]byte{255, 0}},
		{"unmarked full palette", 0, 256, 0, 255, [2]byte{255, 255}},
		{"stale index", 1, 304, 0, 255, [2]byte{255, 255}},
		{"nonmagenta first entry", 1, 256, 0, 0, [2]byte{255, 255}},
		{"explicit key takes precedence", 1, 1, 0, 255, [2]byte{0, 255}},
		{"explicit zero key", 1, 0, 0, 255, [2]byte{255, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded := testKeyedBMP(test.reserved, test.important)
			binary.LittleEndian.PutUint32(encoded[46:50], test.colorsUsed)
			// Palette zero is magenta (or blue for the negative case), and
			// palette one is red. The first pixel is deliberately opaque red.
			copy(encoded[54:62], []byte{255, 0, test.firstRed, 0, 0, 0, 255, 0})
			encoded[1078], encoded[1079] = 1, 0
			services, err := NewServices(Config{})
			check(t, err)
			asset, err := services.Assets.Decode(3, encoded, DecodeOptions{})
			check(t, err)
			info, err := services.Assets.Info(3, asset)
			check(t, err)
			pixels, err := services.Graphics.RGBA(3, info.Frames[0].Surface)
			check(t, err)
			want := []byte{255, 0, 0, test.wantAlpha[0], test.firstRed, 0, 255, test.wantAlpha[1]}
			if !bytes.Equal(pixels, want) {
				t.Fatalf("decoded pixels = % x, want % x", pixels, want)
			}
		})
	}
}
