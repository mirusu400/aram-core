package brewrt

import (
	"bytes"
	"testing"
)

func TestConfiguredBREWFallbackFontChangesRenderedHangul(t *testing.T) {
	var glyphs [][]byte
	for _, name := range []string{"galmuri9", "neodgm"} {
		r, err := NewWithFallbackFont(Package{Module: make([]byte, 20)}, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		glyph, err := r.textRaster.Glyph(1, r.displayFont, '가')
		if err != nil {
			t.Fatal(err)
		}
		glyphs = append(glyphs, append([]byte(nil), glyph.Alpha...))
	}
	if bytes.Equal(glyphs[0], glyphs[1]) {
		t.Fatal("BREW ignored the selected handset font")
	}
}
