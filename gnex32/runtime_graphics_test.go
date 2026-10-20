package gnex32

import (
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
