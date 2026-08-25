package runtime

import (
	"testing"
)

func presentedScreen(t *testing.T) (*Graphics, ServiceID) {
	t.Helper()
	graphics, err := NewGraphics(NewRegistry(16), GraphicsLimits{})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  2,
		Height: 1,
		Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphics.SetScreen(1, surface); err != nil {
		t.Fatal(err)
	}
	return graphics, surface
}

// A driver asks every host tick whether the presented frame is the one it
// already holds, so the answer must not cost a surface copy.
func TestLastFramePresentationIdentifiesTheCommittedFrame(t *testing.T) {
	graphics, surface := presentedScreen(t)

	if sequence, _ := graphics.LastFramePresentation(); sequence != 0 {
		t.Fatalf("sequence before any present = %d", sequence)
	}
	if graphics.LastFrameImage() != nil {
		t.Fatal("an image was materialized before any present")
	}

	if err := graphics.SetPixel(1, surface, 0, 0, RGB(255, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := graphics.Present(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	sequence, hash := graphics.LastFramePresentation()
	if sequence != 1 || hash == ([32]byte{}) {
		t.Fatalf("presented identity = (%d, %x)", sequence, hash)
	}

	// Presenting the same pixels again keeps the content identity, which is
	// what lets a driver skip re-uploading an unchanged screen.
	if _, err := graphics.Present(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	repeatSequence, repeatHash := graphics.LastFramePresentation()
	if repeatSequence == sequence {
		t.Fatal("a second present did not advance the presentation sequence")
	}
	if repeatHash != hash {
		t.Fatal("identical pixels produced a different content hash")
	}

	if err := graphics.SetPixel(1, surface, 1, 0, RGB(0, 255, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := graphics.Present(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if _, changed := graphics.LastFramePresentation(); changed == hash {
		t.Fatal("redrawn pixels kept the previous content hash")
	}
}

func TestLastFrameImageDoesNotAliasTheService(t *testing.T) {
	graphics, surface := presentedScreen(t)
	if err := graphics.SetPixel(1, surface, 0, 0, RGB(255, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := graphics.Present(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}

	image := graphics.LastFrameImage()
	if got := image.RGBAAt(0, 0); got.R != 255 || got.A != 255 {
		t.Fatalf("materialized pixel = %+v", got)
	}
	image.Pix[0] = 0
	if graphics.LastFrame().RGBA[0] != 255 {
		t.Fatal("the materialized image aliases the graphics service")
	}
}

func BenchmarkLastFrameImageSingleCopy(b *testing.B) {
	graphics, err := NewGraphics(NewRegistry(16), GraphicsLimits{})
	if err != nil {
		b.Fatal(err)
	}
	surface, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 240, Height: 320, Format: PixelRGBA8888,
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := graphics.SetScreen(1, surface); err != nil {
		b.Fatal(err)
	}
	if _, err := graphics.Present(1, surface, Rectangle{}); err != nil {
		b.Fatal(err)
	}
	b.Run("LastFrameImage", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = graphics.LastFrameImage()
		}
	})
	b.Run("LastFrameThenImage", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = graphics.LastFrame().Image()
		}
	})
}
