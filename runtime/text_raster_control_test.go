package runtime

import (
	"bytes"
	"testing"
)

// TestTextIgnoresControlPaddingInFixedLineBuffer is the 초밥의달인3 report. The
// title keeps every dialogue line in a fixed eighty-character buffer and hands
// the whole buffer to drawString, so the text it wrote is followed by a tail of
// NULs. Those were rendered as the missing-glyph box and filled the rest of the
// line with tofu. A control code has no glyph on the handset: it must draw
// nothing, and it must not widen the line a title measures to centre it.
func TestTextIgnoresControlPaddingInFixedLineBuffer(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	font, err := services.Text.CreateFont(2, FontDescriptor{
		Family: "aram-fallback",
		Size:   8,
	})
	if err != nil {
		t.Fatal(err)
	}
	line := "잡으세요!"
	padded := line + string(bytes.Repeat([]byte{0}, 68))

	bare, err := services.Text.Measure(2, font, line)
	if err != nil {
		t.Fatal(err)
	}
	full, err := services.Text.Measure(2, font, padded)
	if err != nil {
		t.Fatal(err)
	}
	if full != bare {
		t.Fatalf("padded line width = %d, want %d (the text alone)", full, bare)
	}

	surface, err := services.Graphics.CreateSurface(2, SurfaceDescriptor{
		Width: 240, Height: 16, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Text.Draw(
		2,
		font,
		surface,
		padded,
		0,
		0,
		AnchorLeft|AnchorTop,
		RGB(255, 255, 255),
	); err != nil {
		t.Fatal(err)
	}
	padPixels, err := services.Graphics.RGBA(2, surface)
	if err != nil {
		t.Fatal(err)
	}
	inked := append([]byte(nil), padPixels...)

	blank, err := services.Graphics.CreateSurface(2, SurfaceDescriptor{
		Width: 240, Height: 16, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Text.Draw(
		2,
		font,
		blank,
		line,
		0,
		0,
		AnchorLeft|AnchorTop,
		RGB(255, 255, 255),
	); err != nil {
		t.Fatal(err)
	}
	want, err := services.Graphics.RGBA(2, blank)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inked, want) {
		t.Fatal("the NUL padding drew ink the text alone does not")
	}
	if bytes.Count(want, []byte{255}) == 0 {
		t.Fatal("the text itself drew nothing, so the comparison proves nothing")
	}
}
