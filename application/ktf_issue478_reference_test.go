package application

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestIssue478BaseballKeepsNativeFieldWithWidescreenRequested(t *testing.T) {
	path, data := findAuthorizedPackage(t, "dc0d66e4b30063449eff465e2b2f453a4c81892835b5660754cf6c1ae71e4d14")
	newMachine := func(width int) *Machine {
		t.Helper()
		factory := NewFactory()
		factory.GuestWidthOverride = width
		factory.FrameRunBudget = DefaultHandsetRunBudget
		factory.KTFRunBudget = DefaultKTFHandsetRunBudget
		created, err := factory.Create(context.Background(), machinecore.Source{
			Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		})
		if err != nil {
			t.Fatal(err)
		}
		machine := created.(*Machine)
		t.Cleanup(func() { _ = machine.Close() })
		wantWidth := 240
		if width > wantWidth {
			wantWidth = width
		}
		if got := machine.Framebuffer().Bounds(); got != image.Rect(0, 0, wantWidth, 320) {
			t.Fatalf("requested width %d produced framebuffer %v, want %dx320", width, got, wantWidth)
		}
		if err := machine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		return machine
	}

	native := newMachine(0)
	wide := newMachine(480)
	for frame := 0; frame < 3450; frame++ {
		for _, machine := range []*Machine{native, wide} {
			if frame > 0 && frame%70 == 0 {
				for _, pressed := range []bool{true, false} {
					if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: pressed}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Fatalf("frame %d: %v", frame, err)
			}
		}
	}
	if !bytes.Equal(native.frame.Pix, wide.frame.Pix) {
		t.Fatal("widescreen request changed the title's native baseball field")
	}
	nativeOutput := native.Framebuffer().(*image.RGBA)
	wideOutput := wide.Framebuffer().(*image.RGBA)
	if got := wideOutput.Bounds(); got != image.Rect(0, 0, 480, 320) {
		t.Fatalf("wide output bounds = %v", got)
	}
	presented, sequence := wide.FramePresentation()
	if sequence == 0 || presented.Bounds() != wideOutput.Bounds() {
		t.Fatalf("wide presentation sequence=%d bounds=%v", sequence, presented.Bounds())
	}
	for y := 0; y < 320; y++ {
		for x := 0; x < 480; x++ {
			if got, want := presented.(*image.RGBA).RGBAAt(x, y), wideOutput.RGBAAt(x, y); got != want {
				t.Fatalf("presentation pixel (%d,%d) = %v, framebuffer = %v", x, y, got, want)
			}
		}
	}
	for y := 0; y < 320; y++ {
		for x := 0; x < 480; x++ {
			pixel := wideOutput.RGBAAt(x, y)
			if x >= 120 && x < 360 {
				if want := nativeOutput.RGBAAt(x-120, y); pixel != want {
					t.Fatalf("wide pixel (%d,%d) = %v, native = %v", x, y, pixel, want)
				}
			} else if pixel != (color.RGBA{A: 255}) {
				t.Fatalf("letterbox pixel (%d,%d) = %v", x, y, pixel)
			}
		}
	}
	coloured := 0
	for offset := 0; offset < len(native.frame.Pix); offset += 4 {
		if native.frame.Pix[offset] != 0 || native.frame.Pix[offset+1] != 0 || native.frame.Pix[offset+2] != 0 {
			coloured++
		}
	}
	if coloured < 1000 {
		t.Fatalf("baseball field rendered only %d coloured pixels", coloured)
	}
}
