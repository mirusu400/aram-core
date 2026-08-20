package runtime

import (
	"bytes"
	"testing"
)

// TestHandsetFontRegistryDefaultsToGalmuri9 documents the shipped default and
// guards the lenient lookup that keeps rasterization from ever losing a font.
func TestHandsetFontRegistryDefaultsToGalmuri9(t *testing.T) {
	if defaultHandsetFontName != "galmuri9" {
		t.Fatalf("default handset font = %q, want galmuri9", defaultHandsetFontName)
	}
	for _, name := range []string{"", "does-not-exist", "NEODGM"} {
		if got := lookupHandsetFont(name).name; got != defaultHandsetFontName {
			t.Fatalf("lookupHandsetFont(%q).name = %q, want default", name, got)
		}
	}
	if got := lookupHandsetFont("neodgm").name; got != "neodgm" {
		t.Fatalf("lookupHandsetFont(neodgm).name = %q", got)
	}
	if got := lookupHandsetFont("mulmaru").name; got != "mulmaru" {
		t.Fatalf("lookupHandsetFont(mulmaru).name = %q", got)
	}
	names := handsetFontNames()
	if len(names) != 3 || names[0] != "galmuri9" || names[1] != "mulmaru" || names[2] != "neodgm" {
		t.Fatalf("handsetFontNames() = %v, want [galmuri9 mulmaru neodgm]", names)
	}
}

// TestConfigNormalizesFallbackFont ensures an empty or unknown selection is
// canonicalized to the default so the hashed configuration stays deterministic.
func TestConfigNormalizesFallbackFont(t *testing.T) {
	cases := map[string]string{
		"":         "galmuri9",
		"bogus":    "galmuri9",
		"neodgm":   "neodgm",
		"galmuri9": "galmuri9",
		"mulmaru":  "mulmaru",
	}
	for in, want := range cases {
		services, err := NewServices(Config{FallbackFont: in})
		if err != nil {
			t.Fatalf("NewServices(FallbackFont=%q): %v", in, err)
		}
		if got := services.Config.FallbackFont; got != want {
			t.Fatalf("normalized FallbackFont for %q = %q, want %q", in, got, want)
		}
	}
}

// TestHandsetFontsRasterizeDistinctly proves the selection reaches the raster
// path: galmuri9 (default) is 1-bit crisp, neodgm keeps antialiased edges, and
// the same syllable renders differently under each font.
func TestHandsetFontsRasterizeDistinctly(t *testing.T) {
	glyphFor := func(font string) Glyph {
		services, err := NewServices(Config{FallbackFont: font})
		if err != nil {
			t.Fatalf("NewServices(%q): %v", font, err)
		}
		id, err := services.Text.CreateFont(1, FontDescriptor{Size: 12})
		if err != nil {
			t.Fatalf("CreateFont(%q): %v", font, err)
		}
		g, err := services.Text.Glyph(1, id, '가')
		if err != nil {
			t.Fatalf("Glyph(%q): %v", font, err)
		}
		return g
	}

	crisp := glyphFor("galmuri9")
	for _, a := range crisp.Alpha {
		if a != 0 && a != 0xff {
			t.Fatalf("galmuri9 glyph has partial alpha %#02x, expected 1-bit", a)
		}
	}
	if bytes.Count(crisp.Alpha, []byte{0xff}) < 12 {
		t.Fatal("galmuri9 glyph has too few visible pixels")
	}

	soft := glyphFor("neodgm")
	partial := 0
	for _, a := range soft.Alpha {
		if a != 0 && a != 0xff {
			partial++
		}
	}
	if partial == 0 {
		t.Fatal("neodgm glyph has no antialiased edges")
	}

	if bytes.Equal(crisp.Alpha, soft.Alpha) {
		t.Fatal("galmuri9 and neodgm rasterized the same syllable identically")
	}

	// Mulmaru is an integer-grid pixel font rasterized 1:1, so it is crisp
	// like galmuri9 yet a distinct typeface.
	bold := glyphFor("mulmaru")
	for _, a := range bold.Alpha {
		if a != 0 && a != 0xff {
			t.Fatalf("mulmaru glyph has partial alpha %#02x, expected 1-bit", a)
		}
	}
	if bytes.Count(bold.Alpha, []byte{0xff}) < 12 {
		t.Fatal("mulmaru glyph has too few visible pixels")
	}
	if bytes.Equal(bold.Alpha, crisp.Alpha) {
		t.Fatal("mulmaru and galmuri9 rasterized the same syllable identically")
	}
}
