package application

import (
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	"image"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFMachineFramebufferHidesUnpresentedPaint(t *testing.T) {
	drawBuffer := image.NewRGBA(image.Rect(0, 0, 2, 1))
	runtime, err := ktfrt.NewRuntimeForProfile(
		interpreter.New(),
		ktf.Package{
			ClientName: "client.bin0",
			Client:     []byte{0x70, 0x47},
		},
		drawBuffer,
		ktfrt.ProfileID,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	machine := &Machine{frame: drawBuffer, ktf: runtime}
	drawBuffer.SetRGBA(0, 0, color.RGBA{B: 0xff, A: 0xff})
	if got := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)); got != (color.RGBA{A: 0xff}) {
		t.Fatalf("frontend pixel before first present = %#v, want opaque black", got)
	}

	drawBuffer.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	runtime.Graphics[graphics].PixelsDirty = true
	if err := runtime.RecordPresentation(); err != nil {
		t.Fatal(err)
	}
	drawBuffer.SetRGBA(0, 0, color.RGBA{B: 0xff, A: 0xff})

	if got := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)); got != (color.RGBA{R: 0xff, A: 0xff}) {
		t.Fatalf("frontend pixel = %#v, want last presented red", got)
	}
}
