package application

import (
	"image"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

func benchMachine() *Machine {
	return &Machine{frame: image.NewRGBA(image.Rect(0, 0, 240, 320))}
}

func BenchmarkFramebufferWIPI(b *testing.B) {
	m := benchMachine()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Framebuffer()
	}
}

func BenchmarkFramePresentationWIPI(b *testing.B) {
	m := benchMachine()
	m.FramePresentation()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.FramePresentation()
	}
}

func benchKTF(b *testing.B) *Machine {
	graphics, err := shared.NewGraphics(shared.NewRegistry(16), shared.GraphicsLimits{})
	check(b, err)
	surface, err := graphics.CreateSurface(1, shared.SurfaceDescriptor{
		Width: 240, Height: 320, Format: shared.PixelRGBA8888,
	})
	check(b, err)
	check(b, graphics.SetScreen(1, surface))
	check(b, graphics.SetPixel(1, surface, 4, 4, shared.RGB(255, 0, 0)))
	if _, err := graphics.Present(1, surface, shared.Rectangle{}); err != nil {
		b.Fatal(err)
	}
	return &Machine{
		frame: image.NewRGBA(image.Rect(0, 0, 240, 320)),
		ktf:   &ktfrt.Runtime{Services: &shared.Services{Graphics: graphics}},
	}
}

func BenchmarkFramebufferKTF(b *testing.B) {
	m := benchKTF(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Framebuffer()
	}
}

func BenchmarkFramePresentationKTF(b *testing.B) {
	m := benchKTF(b)
	m.FramePresentation()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.FramePresentation()
	}
}
