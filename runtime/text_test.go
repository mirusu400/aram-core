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
