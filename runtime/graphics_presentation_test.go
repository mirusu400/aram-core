package runtime

import (
	"crypto/sha256"
	"testing"
)

func presentedScreen(t *testing.T) (*Graphics, ServiceID) {
	t.Helper()
	graphics, err := NewGraphics(NewRegistry(16), GraphicsLimits{})
	check(t, err)
	surface, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width:  2,
		Height: 1,
		Format: PixelRGBA8888,
	})
	check(t, err)
	check(t, graphics.SetScreen(1, surface))
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

	check(t, graphics.SetPixel(1, surface, 0, 0, RGB(255, 0, 0)))
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

	check(t, graphics.SetPixel(1, surface, 1, 0, RGB(0, 255, 0)))
	if _, err := graphics.Present(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if _, changed := graphics.LastFramePresentation(); changed == hash {
		t.Fatal("redrawn pixels kept the previous content hash")
	}
}

func TestLastFrameImageDoesNotAliasTheService(t *testing.T) {
	graphics, surface := presentedScreen(t)
	check(t, graphics.SetPixel(1, surface, 0, 0, RGB(255, 0, 0)))
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

func TestPresentCommitReusesOwnedPixelsWithoutExposingThem(t *testing.T) {
	graphics, surface := presentedScreen(t)
	check(t, graphics.SetPixel(1, surface, 0, 0, RGB(255, 0, 0)))
	first, err := graphics.PresentCommit(1, surface, Rectangle{})
	check(t, err)
	backing := &graphics.lastFrame.RGBA[0]
	second, err := graphics.PresentCommit(1, surface, Rectangle{})
	check(t, err)
	if second.Sequence != first.Sequence+1 || !second.Dirty.Empty() {
		t.Fatalf("unchanged presentation = %+v after %+v", second, first)
	}
	// The commit path carries no digest - Sequence names the frame - so an
	// unchanged screen is recognized by the pixels the service still owns.
	if graphics.hashLastFrame() != graphics.LastFrame().Hash {
		t.Fatal("the frame on screen and its snapshot disagree on the digest")
	}
	if &graphics.lastFrame.RGBA[0] != backing {
		t.Fatal("unchanged PresentCommit replaced its owned RGBA buffer")
	}
	check(t, graphics.SetPixel(1, surface, 1, 0, RGB(0, 255, 0)))
	if _, err := graphics.PresentCommit(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if &graphics.lastFrame.RGBA[0] != backing {
		t.Fatal("dirty PresentCommit did not reuse its sized RGBA buffer")
	}
	destination := make([]byte, 2*4+8)
	check(t, graphics.CopyLastFrameRGBA(destination, 2*4+8))
	if destination[0] != 255 || destination[4+1] != 255 {
		t.Fatalf("copied committed RGBA = %v", destination[:8])
	}
	destination[0] = 0
	if graphics.lastFrame.RGBA[0] != 255 {
		t.Fatal("CopyLastFrameRGBA exposed the service backing pixels")
	}

	allocations := testing.AllocsPerRun(1000, func() {
		if _, err := graphics.PresentCommit(1, surface, Rectangle{}); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("unchanged PresentCommit allocations = %.2f, want 0", allocations)
	}
}

func BenchmarkLastFrameImageSingleCopy(b *testing.B) {
	graphics, err := NewGraphics(NewRegistry(16), GraphicsLimits{})
	check(b, err)
	surface, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 240, Height: 320, Format: PixelRGBA8888,
	})
	check(b, err)
	check(b, graphics.SetScreen(1, surface))
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

func BenchmarkPresentCommitOwnedBuffer(b *testing.B) {
	graphics, err := NewGraphics(NewRegistry(16), GraphicsLimits{})
	check(b, err)
	surface, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 240, Height: 320, Format: PixelRGB565,
	})
	check(b, err)
	check(b, graphics.SetScreen(1, surface))
	if _, err := graphics.PresentCommit(1, surface, Rectangle{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := graphics.PresentCommit(1, surface, Rectangle{}); err != nil {
			b.Fatal(err)
		}
	}
}

// A requested rectangle that lies outside the surface intersects to empty. The
// frame is still a full-surface copy, so pixels drawn before that present must
// survive it: reusing the previous frame there would drop them and clear the
// surface's dirty rectangle, so nothing would ever present them.
func TestPresentOutsideTheSurfaceKeepsDrawnPixels(t *testing.T) {
	graphics, surface := presentedScreen(t)
	check(t, graphics.SetPixel(1, surface, 0, 0, RGB(255, 0, 0)))
	if _, err := graphics.Present(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	_, first := graphics.LastFramePresentation()

	check(t, graphics.SetPixel(1, surface, 1, 0, RGB(0, 255, 0)))
	frame, err := graphics.Present(1, surface, Rectangle{
		X:      900,
		Y:      900,
		Width:  1,
		Height: 1,
	})
	check(t, err)
	if _, second := graphics.LastFramePresentation(); second == first {
		t.Fatalf("the drawn pixel was dropped: hash stayed %x", second)
	}
	if frame.RGBA[4] != 0 || frame.RGBA[5] != 255 {
		t.Fatalf("presented pixels lost the draw: %v", frame.RGBA)
	}
}

// A guest presents far more often than a host asks which frame it is looking
// at: a KTF title runs its paint loop at a thousand presents a second, and
// hashing a 240x320 frame on every one of them spent an eighth of the
// emulator's CPU on a digest nobody read. The commit path must therefore leave
// the digest alone until something asks for it.
func TestPresentCommitDefersTheFrameDigest(t *testing.T) {
	graphics, surface := presentedScreen(t)
	check(t, graphics.SetPixel(1, surface, 0, 0, RGB(255, 0, 0)))
	if _, err := graphics.PresentCommit(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if graphics.lastFrameHashed {
		t.Fatal("PresentCommit hashed the frame before anyone asked")
	}
	sequence, hash := graphics.LastFramePresentation()
	if sequence == 0 {
		t.Fatal("no frame was presented")
	}
	if hash != sha256.Sum256(graphics.lastFrame.RGBA) {
		t.Fatal("the deferred digest does not match the presented pixels")
	}
	if !graphics.lastFrameHashed {
		t.Fatal("the digest was not kept for the next caller")
	}
	// A second present of the same pixels reuses the frame, so the digest it
	// already has stays valid and is not recomputed.
	if _, err := graphics.PresentCommit(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if !graphics.lastFrameHashed {
		t.Fatal("an unchanged present threw its digest away")
	}
	// New pixels invalidate it again.
	check(t, graphics.SetPixel(1, surface, 1, 0, RGB(0, 255, 0)))
	if _, err := graphics.PresentCommit(1, surface, Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if graphics.lastFrameHashed {
		t.Fatal("a changed present kept the digest of the old pixels")
	}
	if _, again := graphics.LastFramePresentation(); again == hash {
		t.Fatal("the digest did not follow the new pixels")
	}
}
