package gnex32

import (
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
