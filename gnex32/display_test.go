package gnex32

import (
	"image"
	"image/color"
	"testing"
)

func TestDisplayHasNoFrameBeforeFlush(t *testing.T) {
	if frame := NewDisplay(120, 149).Frame(); frame != nil {
		t.Fatalf("frame before first Flush = %v", frame)
	}
}

func TestCandidateDisplayPositionsImageAnchorAndPresentsCopy(t *testing.T) {
	display := NewDisplay(120, 80)
	display.Clear(color.White)
	mono := []byte{5, 3, 2, 1, 0xff, 4, 3, 0xa8}
	if err := display.CopyImage(60, 40, mono); err != nil {
		t.Fatal(err)
	}
	frame := display.Flush()
	if frame == nil {
		t.Fatal("Flush returned no frame")
	}
	if got := color.RGBAModel.Convert(frame.At(59, 41)).(color.RGBA); got != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("anchored one-bit pixel = %+v", got)
	}
	if got := color.RGBAModel.Convert(frame.At(60, 41)).(color.RGBA); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("zero-bit pixel = %+v", got)
	}
	display.Clear(color.RGBA{R: 255, A: 255})
	if got := color.RGBAModel.Convert(frame.At(59, 41)).(color.RGBA); got != (color.RGBA{0, 0, 0, 255}) {
		t.Fatal("published frame changed after drawing resumed")
	}
}

func TestDisplayFlushPartialPreservesPreviouslyPresentedPixels(t *testing.T) {
	display := NewDisplay(4, 3)
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	display.Clear(red)
	display.Flush()
	display.Clear(blue)
	frame := display.FlushPartial(1, 1, 2, 2)
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			want := red
			if x >= 1 && x <= 2 && y >= 1 && y <= 2 {
				want = blue
			}
			if got := color.RGBAModel.Convert(frame.At(x, y)).(color.RGBA); got != want {
				t.Fatalf("partial pixel (%d,%d) = %#v, want %#v", x, y, got, want)
			}
		}
	}
	display.Clear(color.White)
	if got := color.RGBAModel.Convert(frame.At(1, 1)).(color.RGBA); got != blue {
		t.Fatal("published partial frame changed with the working buffer")
	}
}

func TestDisplayFirstFlushPartialClipsToLCDAndUsesInclusiveCoordinates(t *testing.T) {
	display := NewDisplay(3, 2)
	display.Clear(color.White)
	frame := display.FlushPartial(-5, 0, 0, 0)
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("included edge pixel = %#v", got)
	}
	if got := color.RGBAModel.Convert(frame.At(1, 0)).(color.RGBA); got != (color.RGBA{}) {
		t.Fatalf("unpublished first-frame pixel = %#v", got)
	}
	before := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA)
	display.FlushPartial(2, 1, 1, 0)
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != before {
		t.Fatal("reversed partial rectangle changed the frame")
	}
}

func TestCopyImageDirSupportsDocumentedNormalAndMirrorModes(t *testing.T) {
	data := gnex32TransformTestImage()
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	normal := NewDisplay(12, 12)
	if err := normal.CopyImageDir(5, 5, data, 0); err != nil {
		t.Fatal(err)
	}
	if normal.working.RGBAAt(5, 5) != red || normal.working.RGBAAt(6, 5) != green {
		t.Fatal("normal CopyImageDir changed pixel order")
	}
	mirrored := NewDisplay(12, 12)
	if err := mirrored.CopyImageDir(5, 5, data, 1); err != nil {
		t.Fatal(err)
	}
	if mirrored.working.RGBAAt(5, 5) != red || mirrored.working.RGBAAt(4, 5) != green {
		t.Fatal("mirrored CopyImageDir did not preserve the hotspot")
	}
	if err := mirrored.CopyImageDir(5, 5, data, 2); err == nil {
		t.Fatal("CopyImageDir accepted an undocumented direction")
	}
}

func TestCopyImageExCombinesZoomMirrorAndRotation(t *testing.T) {
	display := NewDisplay(20, 20)
	if err := display.CopyImageEx(10, 10, gnex32TransformTestImage(), 0, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	want := map[image.Point]color.RGBA{
		image.Pt(10, 10): {R: 255, A: 255},
		image.Pt(10, 8):  {G: 255, A: 255},
		image.Pt(8, 10):  {B: 255, A: 255},
		image.Pt(8, 8):   {R: 255, G: 255, B: 255, A: 255},
	}
	for point, expected := range want {
		for dy := 0; dy < 2; dy++ {
			for dx := 0; dx < 2; dx++ {
				if got := display.working.RGBAAt(point.X+dx, point.Y+dy); got != expected {
					t.Fatalf("transformed pixel %v + (%d,%d) = %#v, want %#v", point, dx, dy, got, expected)
				}
			}
		}
	}
	for _, args := range [][4]uint32{{6, 0, 0, 0}, {0, 2, 0, 0}, {0, 0, 2, 0}, {0, 0, 0, 4}} {
		if err := display.CopyImageEx(10, 10, gnex32TransformTestImage(), args[0], args[1], args[2], args[3]); err == nil {
			t.Fatalf("CopyImageEx accepted alpha/zoom/mirror/rotation %v", args)
		}
	}
}

func TestCopyImageExSupportsInvertAndXORRasterModes(t *testing.T) {
	data := gnex32TransformTestImage()
	display := NewDisplay(4, 4)
	if err := display.CopyImageEx(1, 1, data, 4, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := display.working.RGBAAt(1, 1); got != (color.RGBA{G: 255, B: 255, A: 255}) {
		t.Fatalf("inverted red = %#v", got)
	}
	destination := color.RGBA{R: 0x0f, G: 0xf0, B: 0x55, A: 255}
	display.working.SetRGBA(1, 1, destination)
	if err := display.CopyImageEx(1, 1, data, 5, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := display.working.RGBAAt(1, 1); got != (color.RGBA{R: 0xf0, G: 0xf0, B: 0x55, A: 255}) {
		t.Fatalf("XOR red = %#v", got)
	}
}

func TestCopyImageExRotationAndBlendTables(t *testing.T) {
	data := gnex32TransformTestImage()
	green := color.RGBA{G: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	for rotation, points := range []struct{ green, blue image.Point }{
		{green: image.Pt(6, 5), blue: image.Pt(5, 6)},
		{green: image.Pt(5, 6), blue: image.Pt(4, 5)},
		{green: image.Pt(4, 5), blue: image.Pt(5, 4)},
		{green: image.Pt(5, 4), blue: image.Pt(6, 5)},
	} {
		display := NewDisplay(12, 12)
		if err := display.CopyImageEx(5, 5, data, 0, 0, 0, uint32(rotation)); err != nil {
			t.Fatal(err)
		}
		if got := display.working.RGBAAt(points.green.X, points.green.Y); got != green {
			t.Fatalf("rotation %d green = %#v at %v", rotation, got, points.green)
		}
		if got := display.working.RGBAAt(points.blue.X, points.blue.Y); got != blue {
			t.Fatalf("rotation %d blue = %#v at %v", rotation, got, points.blue)
		}
	}
	for alpha, wantRed := range []uint8{255, 191, 127, 63} {
		display := NewDisplay(4, 4)
		display.Clear(color.RGBA{A: 255})
		if err := display.CopyImageEx(1, 1, data, uint32(alpha), 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		if got := display.working.RGBAAt(1, 1); got != (color.RGBA{R: wantRed, A: 255}) {
			t.Fatalf("alpha %d red = %#v", alpha, got)
		}
	}
}

func gnex32TransformTestImage() []byte {
	return []byte{
		0x0a, 2, 2, 0, 0, 0, 4,
		255, 0, 0,
		0, 255, 0,
		0, 0, 255,
		255, 255, 255,
		0x1b,
	}
}

func TestCandidateDisplayClipLimitsImageWrites(t *testing.T) {
	display := NewDisplay(120, 80)
	display.Clear(color.White)
	display.SetClip(61, 41, 61, 41)
	mono := []byte{5, 3, 2, 1, 0xff, 4, 3, 0xa8}
	if err := display.CopyImage(60, 40, mono); err != nil {
		t.Fatal(err)
	}
	frame := display.Flush()
	if got := color.RGBAModel.Convert(frame.At(59, 41)).(color.RGBA); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("outside clip = %+v", got)
	}
	if got := color.RGBAModel.Convert(frame.At(61, 41)).(color.RGBA); got != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("inside clip one pixel = %+v", got)
	}
	display.ResetClip()
	if err := display.CopyImage(60, 40, mono); err != nil {
		t.Fatal(err)
	}
	frame = display.Flush()
	if got := color.RGBAModel.Convert(frame.At(59, 41)).(color.RGBA); got != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("reset clip = %+v", got)
	}
}

func TestCandidateDisplayLineUsesLCDCoordinates(t *testing.T) {
	display := NewDisplay(120, 128)
	display.Clear(color.White)
	display.DrawLine(0, 0, 119, 127, color.RGBA{A: 255})
	frame := display.Flush()
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != (color.RGBA{A: 255}) {
		t.Fatalf("top-left line pixel = %+v", got)
	}
	if got := color.RGBAModel.Convert(frame.At(119, 127)).(color.RGBA); got != (color.RGBA{A: 255}) {
		t.Fatalf("bottom-right line pixel = %+v", got)
	}
}

func TestDisplayRoundRectClipsCornersAndFillsInterior(t *testing.T) {
	ink := color.RGBA{R: 255, A: 255}
	for _, filled := range []bool{false, true} {
		display := NewDisplay(9, 9)
		if filled {
			display.FillRectRound(1, 1, 7, 7, 2, ink)
		} else {
			display.DrawRectRound(1, 1, 7, 7, 2, ink)
		}
		frame := display.Flush()
		if got := frame.At(1, 1); got != (color.RGBA{}) {
			t.Fatalf("filled=%v rounded corner = %v", filled, got)
		}
		if got := frame.At(3, 1); got != ink {
			t.Fatalf("filled=%v top edge = %v", filled, got)
		}
		wantCenter := color.RGBA{}
		if filled {
			wantCenter = ink
		}
		if got := frame.At(4, 4); got != wantCenter {
			t.Fatalf("filled=%v center = %v, want %v", filled, got, wantCenter)
		}
	}
}

func TestDisplayGammaMapsFixedAndDynamicImagePalettes(t *testing.T) {
	fixed := []byte{0x06, 1, 1, 0, 0, 16, 0, 0, 0, 0}
	dynamic := []byte{0x09, 1, 1, 0, 0, 0, 1, 0xff, 0x80, 0x40, 0, 0}

	neutral := NewDisplay(2, 1)
	if err := neutral.CopyImage(0, 0, fixed); err != nil {
		t.Fatal(err)
	}
	if err := neutral.CopyImage(1, 0, dynamic); err != nil {
		t.Fatal(err)
	}
	neutralFrame := neutral.Flush()
	if got := color.RGBAModel.Convert(neutralFrame.At(0, 0)).(color.RGBA); got == (color.RGBA{A: 255}) {
		t.Fatal("neutral fixed palette unexpectedly rendered black")
	}
	if got := color.RGBAModel.Convert(neutralFrame.At(1, 0)).(color.RGBA); got != (color.RGBA{R: 0xff, G: 0x80, B: 0x40, A: 0xff}) {
		t.Fatalf("neutral dynamic palette = %#v", got)
	}

	dark := NewDisplay(2, 1)
	if err := dark.SetGamma(6); err != nil {
		t.Fatal(err)
	}
	if err := dark.CopyImage(0, 0, fixed); err != nil {
		t.Fatal(err)
	}
	if err := dark.CopyImage(1, 0, dynamic); err != nil {
		t.Fatal(err)
	}
	darkFrame := dark.Flush()
	for x := 0; x < 2; x++ {
		if got := color.RGBAModel.Convert(darkFrame.At(x, 0)).(color.RGBA); got != (color.RGBA{A: 255}) {
			t.Fatalf("dark gamma pixel %d = %#v, want black", x, got)
		}
	}
	if err := dark.SetGamma(7); err == nil || dark.Gamma() != 6 {
		t.Fatalf("invalid gamma result = %v, current %d", err, dark.Gamma())
	}
}
