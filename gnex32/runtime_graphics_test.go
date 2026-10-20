package gnex32

import (
	"errors"
	"image/color"
	"testing"
)

func TestSetGammaReappliesSelectedDrawingAndFontColors(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	if _, err := runtime.service(0x26, []uint32{0}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.service(0x28, []uint32{2, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if runtime.paint != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("default paint = %#v", runtime.paint)
	}

	result, err := runtime.service(0x25, []uint32{6})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 1 || len(result.Push) != 0 {
		t.Fatalf("SetGamma stack result = %+v", result)
	}
	black := color.RGBA{A: 255}
	if runtime.paint != black || runtime.fontColor != black || runtime.fontBack != black {
		t.Fatalf("dark gamma colors = paint %#v font %#v back %#v", runtime.paint, runtime.fontColor, runtime.fontBack)
	}
	if runtime.display.Gamma() != 6 {
		t.Fatalf("gamma = %d, want 6", runtime.display.Gamma())
	}
	if _, err := runtime.service(0x25, []uint32{7}); err == nil {
		t.Fatal("SetGamma accepted value 7")
	}
	if runtime.display.Gamma() != 6 || runtime.paint != black {
		t.Fatal("invalid SetGamma changed runtime state")
	}
}

func TestSetGammaReappliesTrueColorDrawingState(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	if _, err := runtime.service(0x27, []uint32{201, 129, 67}); err != nil {
		t.Fatal(err)
	}
	want := color.RGBA{R: 201, G: 129, B: 67, A: 255}
	if runtime.paint != want {
		t.Fatalf("neutral RGB paint = %#v, want %#v", runtime.paint, want)
	}
	if _, err := runtime.service(0x25, []uint32{6}); err != nil {
		t.Fatal(err)
	}
	if runtime.paint != (color.RGBA{A: 255}) {
		t.Fatalf("dark RGB paint = %#v, want black", runtime.paint)
	}
}

func TestSetFontChangesOnlySizeAndValidatesRange(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	if _, err := runtime.service(0x28, []uint32{2, 0, 1, 2}); err != nil {
		t.Fatal(err)
	}
	wantColor, wantBack, wantAlign := runtime.fontColor, runtime.fontBack, runtime.fontAlign
	for size := uint32(0); size < uint32(len(gnex32FontHeights)); size++ {
		result, err := runtime.service(0x29, []uint32{size})
		if err != nil {
			t.Fatalf("SetFont(%d): %v", size, err)
		}
		if result.Pop != 1 || len(result.Push) != 0 || runtime.fontSize != size {
			t.Fatalf("SetFont(%d) result = %+v, stored %d", size, result, runtime.fontSize)
		}
		if runtime.fontColor != wantColor || runtime.fontBack != wantBack || runtime.fontAlign != wantAlign {
			t.Fatalf("SetFont(%d) changed color/background/alignment", size)
		}
	}
	previous := runtime.fontSize
	if _, err := runtime.service(0x29, []uint32{uint32(len(gnex32FontHeights))}); err == nil {
		t.Fatal("SetFont accepted an out-of-range size")
	}
	if runtime.fontSize != previous {
		t.Fatal("invalid SetFont changed font size")
	}
	if _, err := runtime.service(0x29, nil); !errors.Is(err, ErrStackUnderflow) {
		t.Fatalf("SetFont underflow error = %v", err)
	}
}

func TestSetFontTypeRejectsInvalidSizeAtomically(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	beforeSize, beforeColor := runtime.fontSize, runtime.fontColor
	if _, err := runtime.service(0x28, []uint32{6, 0, 1, 2}); err == nil {
		t.Fatal("SetFontType accepted an out-of-range size")
	}
	if runtime.fontSize != beforeSize || runtime.fontColor != beforeColor {
		t.Fatal("invalid SetFontType changed font state")
	}
}

func TestFlushPartialServiceUsesGuestRectangle(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	defer runtime.Close()
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	runtime.display.Clear(red)
	runtime.display.Flush()
	runtime.display.Clear(blue)
	result, err := runtime.service(0x60, []uint32{10, 20, 11, 21})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 4 || len(result.Push) != 0 || runtime.PresentCount() != 1 {
		t.Fatalf("FlushPartial result = %+v, presents %d", result, runtime.PresentCount())
	}
	frame := runtime.Frame()
	if got := color.RGBAModel.Convert(frame.At(10, 20)).(color.RGBA); got != blue {
		t.Fatalf("inside partial = %#v", got)
	}
	if got := color.RGBAModel.Convert(frame.At(9, 20)).(color.RGBA); got != red {
		t.Fatalf("outside partial = %#v", got)
	}
}
