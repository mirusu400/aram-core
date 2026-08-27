package runtime

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestTextEuckrConversionAndFallbackRaster(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := services.Text.Encode("ARAM 한글", EncodingEUCKR)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := services.Text.Decode(encoded, EncodingEUCKR)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "ARAM 한글" {
		t.Fatalf("EUC-KR round-trip = %q", decoded)
	}
	font, err := services.Text.CreateFont(2, FontDescriptor{
		Family: "aram-fallback",
		Size:   8,
	})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(2, SurfaceDescriptor{
		Width: 64, Height: 16, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Text.Draw(
		2,
		font,
		surface,
		"A한",
		0,
		0,
		AnchorLeft|AnchorTop,
		RGB(255, 255, 255),
	); err != nil {
		t.Fatal(err)
	}
	pixels, err := services.Graphics.RGBA(2, surface)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(pixels, []byte{255}) == 0 {
		t.Fatal("fallback text raster produced no visible pixels")
	}
}

func TestTextEnsureFontReusesMatchingDescriptor(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := FontDescriptor{Size: 12, Style: FontBold}
	first, err := services.Text.EnsureFont(1, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := services.Text.EnsureFont(1, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("matching font service = %s, want %s", second, first)
	}
	otherOwner, err := services.Text.EnsureFont(2, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if otherOwner == first {
		t.Fatalf("font service %s was reused across owners", first)
	}
}

func TestTextStateRoundTripPreservesGlyphCache(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	font, err := services.Text.CreateFont(1, FontDescriptor{Size: 12})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Text.Measure(1, font, "State 상태"); err != nil {
		t.Fatal(err)
	}
	state := services.Snapshot()
	clone, err := NewServices(state.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Text.Snapshot(), services.Text.Snapshot()) {
		t.Fatal("text state did not round-trip")
	}
}

func TestTextRasterizesReadableDoubleWidthHangul(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	font, err := services.Text.CreateFont(1, FontDescriptor{Size: 12})
	if err != nil {
		t.Fatal(err)
	}
	hangul, err := services.Text.Glyph(1, font, '한')
	if err != nil {
		t.Fatal(err)
	}
	if hangul.Width != 12 || hangul.Height != 12 || hangul.Advance != 12 {
		t.Fatalf(
			"Hangul geometry = %dx%d advance %d, want 12x12 advance 12",
			hangul.Width,
			hangul.Height,
			hangul.Advance,
		)
	}
	if bytes.Count(hangul.Alpha, []byte{0xff}) < 12 {
		t.Fatal("Hangul glyph has too few visible pixels")
	}
	other, err := services.Text.Glyph(1, font, '글')
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hangul.Alpha, other.Alpha) {
		t.Fatal("different Hangul syllables rasterized identically")
	}
	width, err := services.Text.Measure(1, font, "A한")
	if err != nil {
		t.Fatal(err)
	}
	if width != 18 {
		t.Fatalf("mixed text width = %d, want 18", width)
	}
}

func TestTextHandsetGlyphsPreserveAntialiasedEdgesAndSymbols(t *testing.T) {
	// Pinned to neodgm: this test asserts antialiased edge alphas and metrics
	// that the softer NeoDunggeunmo fallback produces. The default font is now
	// the 1-bit-crisp galmuri9, which has no partial-alpha edges.
	services, err := NewServices(Config{FallbackFont: "neodgm"})
	if err != nil {
		t.Fatal(err)
	}
	font, err := services.Text.CreateFont(1, FontDescriptor{Size: 12})
	if err != nil {
		t.Fatal(err)
	}
	ga, err := services.Text.Glyph(1, font, '\uac00')
	if err != nil {
		t.Fatal(err)
	}
	for offset, want := range map[int]byte{
		8: 0x22, 9: 0x44, 10*12 + 8: 0x44, 10*12 + 9: 0x88,
	} {
		if ga.Alpha[offset] != want {
			t.Fatalf(
				"antialiased Hangul edge stroke %d = %02x, want %02x",
				offset,
				ga.Alpha[offset],
				want,
			)
		}
	}
	partial := 0
	for _, alpha := range ga.Alpha {
		if alpha != 0 && alpha != 0xff {
			partial++
		}
	}
	if partial < 20 {
		t.Fatalf("Hangul glyph has only %d antialiased edge pixels", partial)
	}
	exclamation, err := services.Text.Glyph(1, font, '!')
	if err != nil {
		t.Fatal(err)
	}
	if exclamation.Width != 6 ||
		exclamation.Height != 12 ||
		exclamation.Advance != 6 ||
		bytes.Count(exclamation.Alpha, []byte{0xff}) < 3 {
		t.Fatalf(
			"exclamation glyph = %dx%d advance %d, visible %d",
			exclamation.Width,
			exclamation.Height,
			exclamation.Advance,
			bytes.Count(exclamation.Alpha, []byte{0xff}),
		)
	}
	leftQuote, err := services.Text.Glyph(1, font, '\u201c')
	if err != nil {
		t.Fatal(err)
	}
	rightQuote, err := services.Text.Glyph(1, font, '\u201d')
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(leftQuote.Alpha, rightQuote.Alpha) {
		t.Fatal("curly quotation marks rasterized identically")
	}
	width, err := services.Text.Measure(1, font, "\u201c\uac04\uc218!\u201d")
	if err != nil {
		t.Fatal(err)
	}
	if width != 42 {
		t.Fatalf("symbol-rich handset text width = %d, want 42", width)
	}
}

func TestTextDrawsReadableAntialiasedHandsetDialogue(t *testing.T) {
	// Pinned to neodgm: asserts NeoDunggeunmo advance widths. See the note on
	// TestTextHandsetGlyphsPreserveAntialiasedEdgesAndSymbols.
	services, err := NewServices(Config{FallbackFont: "neodgm"})
	if err != nil {
		t.Fatal(err)
	}
	font, err := services.Text.CreateFont(1, FontDescriptor{Size: 12})
	if err != nil {
		t.Fatal(err)
	}
	const dialogue = "\uc5b4\ub9b0! \uc544\uc774\ub9b0!"
	width, err := services.Text.Measure(1, font, dialogue)
	if err != nil {
		t.Fatal(err)
	}
	if width != 78 {
		t.Fatalf("reported dialogue width = %d, want 78", width)
	}
	surface, err := services.Graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 96, Height: 16, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Graphics.Clear(1, surface, RGB(0, 0, 64)); err != nil {
		t.Fatal(err)
	}
	if err := services.Text.Draw(
		1,
		font,
		surface,
		dialogue,
		0,
		0,
		AnchorLeft|AnchorTop,
		RGB(255, 255, 255),
	); err != nil {
		t.Fatal(err)
	}
	pixels, err := services.Graphics.RGBA(1, surface)
	if err != nil {
		t.Fatal(err)
	}
	opaque, antialiased := 0, 0
	for offset := 0; offset < len(pixels); offset += 4 {
		red, green, blue := pixels[offset], pixels[offset+1], pixels[offset+2]
		switch {
		case red == 0xff && green == 0xff && blue == 0xff:
			opaque++
		case red != 0 && red == green && blue > red:
			antialiased++
		}
	}
	if opaque < 50 || antialiased < 100 {
		t.Fatalf(
			"reported dialogue has %d opaque and %d antialiased pixels",
			opaque,
			antialiased,
		)
	}
}

func TestTextDecodeRejectsMalformedAndOversizedUTF16(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range [][]byte{
		{0x00, 0xdc},
		{0x00, 0xd8},
		{0x00, 0xd8, 0x00, 0xdc, 0x00, 0xdc},
	} {
		if _, err := services.Text.Decode(
			encoded,
			EncodingUTF16LE,
		); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("malformed UTF-16 % x error = %v", encoded, err)
		}
	}

	limits := DefaultTextLimits()
	limits.MaxStringBytes = 2
	registry := NewRegistry(4)
	graphics, err := NewGraphics(registry, GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := NewText(
		registry,
		graphics,
		limits,
		defaultHandsetFontName,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := text.Decode(
		[]byte{0x00, 0xac},
		EncodingUTF16LE,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expanded UTF-16 error = %v", err)
	}
}
