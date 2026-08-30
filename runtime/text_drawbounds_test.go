package runtime

import "testing"

// TestTextDrawBoundsReportsTheRowsItInked pins what a caller mirroring the
// surface back into its own image may copy. The band has to be in the
// surface's own coordinates - the surface applies its draw-state translation
// before it stores a pixel - or the copy misses the rows below it: KTF titles
// drawing under a translated context lost the bottom of every line of text.
func TestTextDrawBoundsReportsTheRowsItInked(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	font, err := services.Text.CreateFont(3, FontDescriptor{
		Family: "aram-fallback",
		Size:   8,
	})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(3, SurfaceDescriptor{
		Width: 64, Height: 48, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	inked := func() (int, int) {
		t.Helper()
		pixels, rgbaErr := services.Graphics.RGBA(3, surface)
		if rgbaErr != nil {
			t.Fatal(rgbaErr)
		}
		top, bottom := -1, -1
		for y := 0; y < 48; y++ {
			for x := 0; x < 64; x++ {
				if pixels[(y*64+x)*4] != 0 {
					if top < 0 {
						top = y
					}
					bottom = y + 1
					break
				}
			}
		}
		return top, bottom
	}

	top, bottom, err := services.Text.DrawBounds(
		3, font, surface, "A", 2, 4, AnchorLeft|AnchorTop, RGB(255, 0, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	gotTop, gotBottom := inked()
	if top != gotTop || bottom != gotBottom {
		t.Fatalf("band = [%d,%d), inked rows = [%d,%d)", top, bottom, gotTop, gotBottom)
	}

	// The same run under a translated draw state has to report where the
	// pixels actually landed, not where the caller asked for them.
	state, err := services.Graphics.DrawState(3, surface)
	if err != nil {
		t.Fatal(err)
	}
	state.TranslateY = 9
	if err := services.Graphics.SetDrawState(3, surface, state); err != nil {
		t.Fatal(err)
	}
	if err := services.Graphics.Clear(3, surface, RGB(0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	top, bottom, err = services.Text.DrawBounds(
		3, font, surface, "A", 2, 4, AnchorLeft|AnchorTop, RGB(255, 0, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	gotTop, gotBottom = inked()
	if top != gotTop || bottom != gotBottom {
		t.Fatalf(
			"translated band = [%d,%d), inked rows = [%d,%d)",
			top, bottom, gotTop, gotBottom,
		)
	}
	if top < 9 {
		t.Fatalf("translated band top = %d, want at least the translation 9", top)
	}
}

// TestGraphicsRGBARowsIntoMatchesTheWholeSurface keeps the banded conversion
// honest against the full one.
func TestGraphicsRGBARowsIntoMatchesTheWholeSurface(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(4, SurfaceDescriptor{
		Width: 8, Height: 6, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	for y := int32(0); y < 6; y++ {
		for x := int32(0); x < 8; x++ {
			if err := services.Graphics.SetPixel(
				4, surface, x, y, RGB(uint8(x*8), uint8(y*8), 0x20),
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	whole, err := services.Graphics.RGBA(4, surface)
	if err != nil {
		t.Fatal(err)
	}
	band, err := services.Graphics.RGBARowsInto(4, surface, 2, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(band) != 8*3*4 {
		t.Fatalf("band has %d bytes, want %d", len(band), 8*3*4)
	}
	for index := range band {
		if band[index] != whole[2*8*4+index] {
			t.Fatalf("band byte %d differs from the whole-surface conversion", index)
		}
	}
	if empty, emptyErr := services.Graphics.RGBARowsInto(4, surface, 5, 5, nil); emptyErr != nil {
		t.Fatal(emptyErr)
	} else if len(empty) != 0 {
		t.Fatalf("empty band has %d bytes", len(empty))
	}
}
