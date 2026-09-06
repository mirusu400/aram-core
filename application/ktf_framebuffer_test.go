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
		0,
	)
	check(t, err)
	defer runtime.CPU.Close()
	check(t, runtime.MapImageAndHost())
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	check(t, err)
	graphics, err := runtime.EnsureScreenGraphics()
	check(t, err)
	machine := &Machine{frame: drawBuffer, ktf: runtime}
	drawBuffer.SetRGBA(0, 0, color.RGBA{B: 0xff, A: 0xff})
	if got := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)); got != (color.RGBA{A: 0xff}) {
		t.Fatalf("frontend pixel before first present = %#v, want opaque black", got)
	}

	drawBuffer.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	runtime.Graphics[graphics].PixelsDirty = true
	check(t, runtime.RecordPresentation())
	drawBuffer.SetRGBA(0, 0, color.RGBA{B: 0xff, A: 0xff})

	if got := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)); got != (color.RGBA{R: 0xff, A: 0xff}) {
		t.Fatalf("frontend pixel = %#v, want last presented red", got)
	}
}
