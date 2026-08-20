package runtime

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// syntheticBDF is a minimal valid BDF with one Latin glyph ('A') and one Hangul
// syllable ('가', U+AC00) so tests can exercise the runtime font builder without
// shipping a font file.
const syntheticBDF = `STARTFONT 2.1
FONT test
SIZE 16 75 75
FONTBOUNDINGBOX 12 12 0 0
STARTPROPERTIES 2
FONT_ASCENT 9
FONT_DESCENT 3
ENDPROPERTIES
CHARS 2
STARTCHAR A
ENCODING 65
DWIDTH 6 0
BBX 5 7 0 0
BITMAP
70
88
88
F8
88
88
88
ENDCHAR
STARTCHAR uac00
ENCODING 44032
DWIDTH 12 0
BBX 9 9 0 0
BITMAP
FF80
FF80
FF80
FF80
FF80
FF80
FF80
FF80
FF80
ENDCHAR
ENDFONT
`

func TestRegisterHandsetFontFromBDF(t *testing.T) {
	name, err := RegisterHandsetFont([]byte(syntheticBDF))
	if err != nil {
		t.Fatalf("RegisterHandsetFont: %v", err)
	}
	if !strings.HasPrefix(name, customHandsetFontPrefix) {
		t.Fatalf("custom font name = %q, want %q prefix", name, customHandsetFontPrefix)
	}

	// Re-registering identical bytes is idempotent and content-addressed.
	again, err := RegisterHandsetFont([]byte(syntheticBDF))
	if err != nil {
		t.Fatal(err)
	}
	if again != name {
		t.Fatalf("re-register name = %q, want stable %q", again, name)
	}

	// The custom font is excluded from the built-in name list but resolvable.
	for _, n := range handsetFontNames() {
		if n == name {
			t.Fatal("custom font leaked into built-in handsetFontNames()")
		}
	}
	if lookupHandsetFont(name).name != name {
		t.Fatalf("lookupHandsetFont(%q) did not resolve the custom font", name)
	}

	// The selection reaches rasterization: 'A' has visible pixels, and the
	// Hangul block glyph differs from a blank cell.
	services, err := NewServices(Config{FallbackFont: name})
	if err != nil {
		t.Fatal(err)
	}
	if services.Config.FallbackFont != name {
		t.Fatalf("normalized FallbackFont = %q, want %q", services.Config.FallbackFont, name)
	}
	id, err := services.Text.CreateFont(1, FontDescriptor{Size: 12})
	if err != nil {
		t.Fatal(err)
	}
	glyphA, err := services.Text.Glyph(1, id, 'A')
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(glyphA.Alpha, []byte{0xff}) < 3 {
		t.Fatalf("custom 'A' glyph has too few visible pixels: %d", bytes.Count(glyphA.Alpha, []byte{0xff}))
	}
	glyphGa, err := services.Text.Glyph(1, id, '가')
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(glyphGa.Alpha, []byte{0xff}) == 0 {
		t.Fatal("custom '가' glyph rendered blank")
	}
}

func TestBuildHandsetPackRejectsUnknownFormat(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("not a font at all"),
		{0x01, 0x02, 0x03, 0x04, 0x05},
	} {
		if _, _, err := BuildHandsetPack(data); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("BuildHandsetPack(%q) error = %v, want ErrInvalidArgument", data, err)
		}
	}
}

func TestBuildHandsetPackBDFMatchesEmbeddedFormat(t *testing.T) {
	payload, extraCount, err := BuildHandsetPack([]byte(syntheticBDF))
	if err != nil {
		t.Fatal(err)
	}
	if extraCount != 1 { // only 'A' is a non-Hangul extra
		t.Fatalf("extra count = %d, want 1", extraCount)
	}
	want := handsetHangulDataBytes + extraCount*handsetExtraRecordBytes
	if len(payload) != want {
		t.Fatalf("payload = %d bytes, want %d", len(payload), want)
	}
}
