package gnex32

import (
	"errors"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
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

func TestDrawRectServiceDrawsInclusiveClippedOutline(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	red := color.RGBA{R: 255, A: 255}
	runtime.paint = red
	runtime.display.Clear(color.RGBA{A: 255})
	runtime.display.SetClip(2, 2, 4, 4)

	result, err := runtime.service(0x45, []uint32{1, 1, 4, 4})
	if err != nil || result.Pop != 4 || len(result.Push) != 0 {
		t.Fatalf("DrawRect result = %+v, %v", result, err)
	}
	for _, point := range [][2]int{{4, 2}, {2, 4}, {3, 4}, {4, 4}} {
		if got := runtime.display.working.RGBAAt(point[0], point[1]); got != red {
			t.Fatalf("outline pixel %v = %#v", point, got)
		}
	}
	for _, point := range [][2]int{{2, 2}, {3, 2}, {3, 3}} {
		if got := runtime.display.working.RGBAAt(point[0], point[1]); got != (color.RGBA{A: 255}) {
			t.Fatalf("clipped/interior pixel %v = %#v", point, got)
		}
	}

	runtime.display.DrawRect(4, 4, 3, 3, color.RGBA{G: 255, A: 255})
	if got := runtime.display.working.RGBAAt(3, 3); got != (color.RGBA{A: 255}) {
		t.Fatalf("reversed DrawRect changed interior pixel to %#v", got)
	}
	if _, err := runtime.service(0x45, []uint32{1, 2, 3}); !errors.Is(err, ErrStackUnderflow) {
		t.Fatalf("DrawRect underflow error = %v", err)
	}
}

func TestSetPaletteColorRGBRendersSelectorImagesAndSurvivesSave(t *testing.T) {
	data := []byte{0x07, 1, 1, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0x10}
	symbols := make([]gnex.GNEX32Symbol, 10)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: []byte{0x86, 0}, Symbols: symbols,
		Media: []gnex.GNEX32Media{{Flags: 0x100, Data: data}, {Flags: 0, Data: []byte("not an image")}},
	}
	runtime, err := NewRuntime(image, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	result, err := runtime.service(0x2f, []uint32{0, 1, 25, 144, 38})
	if err != nil || result.Pop != 5 || len(result.Push) != 1 || result.Push[0] != 0 {
		t.Fatalf("SetPaletteColorRGB result = %+v, %v", result, err)
	}
	for name, draw := range map[string]func() error{
		"CopyImage":    func() error { return runtime.copyImage(0, 0, 0) },
		"CopyImageDir": func() error { return runtime.copyImageDir(0, 0, 0, 1) },
		"CopyImageEx":  func() error { return runtime.copyImageEx(0, 0, 0, 0, 0, 1, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			runtime.display.Clear(color.RGBA{A: 255})
			if err := draw(); err != nil {
				t.Fatal(err)
			}
			if got := runtime.display.working.RGBAAt(0, 0); got != (color.RGBA{R: 25, G: 144, B: 38, A: 255}) {
				t.Fatalf("rendered override = %#v", got)
			}
		})
	}
	for _, stack := range [][]uint32{
		{0, 16, 1, 2, 3},
		{0, ^uint32(0), 1, 2, 3},
		{1, 0, 1, 2, 3},
		{2, 0, 1, 2, 3},
	} {
		result, err := runtime.service(0x2f, stack)
		if err != nil || len(result.Push) != 1 || result.Push[0] != ^uint32(0) {
			t.Fatalf("invalid SetPaletteColorRGB(%v) = %+v, %v", stack, result, err)
		}
	}

	saved, err := runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntime(image, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.UnmarshalBinary(saved); err != nil {
		t.Fatal(err)
	}
	restored.display.Clear(color.RGBA{A: 255})
	if err := restored.copyImage(0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := restored.display.working.RGBAAt(0, 0); got != (color.RGBA{R: 25, G: 144, B: 38, A: 255}) {
		t.Fatalf("restored palette override = %#v", got)
	}
}
