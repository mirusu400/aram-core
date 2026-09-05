package ktf

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// TestKTFJavaImagesDoNotTakeASurfaceUntilDrawnThrough covers what random key
// input found on 다이하드4: newJavaImage gave every Image a mirrored service
// surface, and KTF Java has no collector, so a title that decodes sprites while
// it plays climbed to "surface count reached 1024" with mirrors nothing ever
// read. Drawing runs on the Go image; the surface is only the target a
// Graphics obtained from the Image syncs to.
func TestKTFJavaImagesDoNotTakeASurfaceUntilDrawnThrough(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
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

	// Far more images than the 1024-surface table holds.
	const images = 3000
	var last uint32
	for created := 0; created < images; created++ {
		frame := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		frame.Set(0, 0, color.NRGBA{R: uint8(created), A: 0xff})
		instance, err := runtime.newJavaImage(frame)
		if err != nil {
			t.Fatalf("image %d: %v", created, err)
		}
		last = instance
	}
	if surfaces := len(runtime.imageServices); surfaces != 0 {
		t.Fatalf("%d images took %d surfaces before anything drew through them",
			images, surfaces)
	}

	// The surface still appears the moment one is needed, and is the same one
	// on a second ask.
	surface, err := runtime.ensureJavaImageSurface(last)
	if err != nil {
		t.Fatal(err)
	}
	if surface == 0 {
		t.Fatal("no surface was materialised")
	}
	again, err := runtime.ensureJavaImageSurface(last)
	if err != nil {
		t.Fatal(err)
	}
	if again != surface {
		t.Fatalf("second ask made a new surface: %s then %s", surface, again)
	}
}

// TestKTFDecodedImagesReleaseTheirAsset pins the other half: a decoded Image
// copies its pixels out, so holding the asset - and the surface the asset owns
// - was one of each per decoded image for the lifetime of the title.
func TestKTFDecodedImagesReleaseTheirAsset(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
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

	const images = 2000
	for created := 0; created < images; created++ {
		frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
		frame.Set(0, 0, color.RGBA{
			R: uint8(created), G: uint8(created >> 8), A: 0xff,
		})
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, frame); err != nil {
			t.Fatal(err)
		}
		instance, err := runtime.newJavaEncodedImage(encoded.Bytes())
		if err != nil {
			t.Fatalf("image %d: %v", created, err)
		}
		if source := runtime.images[instance]; source == nil ||
			source.Bounds().Dx() != 2 {
			t.Fatalf("image %d decoded to %#v", created, source)
		}
	}
	if held := len(runtime.javaAssetServices); held != 0 {
		t.Fatalf("%d decoded images still hold %d assets", images, held)
	}
}

// TestKTFJavaImageMirrorsStayInsideTheBudget covers issue #149, which random
// key input found on 에스테반루크 after 4451 frames of play: every
// Image.getGraphics() took a mirror of its own and kept it, so a session long
// enough to work through a thousand Images filled the 1024-surface table and
// the next getGraphics() failed the title. The mirror is a cache of pixels the
// Go image already holds, so the ones nothing has drawn through lately are
// given back and made again on demand.
func TestKTFJavaImageMirrorsStayInsideTheBudget(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
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

	// Far more mirrors than the surface table holds, one per Image, in the
	// order a title works through its sprites.
	const images = 3000
	instances := make([]uint32, 0, images)
	for created := 0; created < images; created++ {
		frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
		frame.Set(0, 0, color.RGBA{R: uint8(created), G: uint8(created >> 8), A: 0xff})
		instance, err := runtime.newJavaImage(frame)
		if err != nil {
			t.Fatalf("image %d: %v", created, err)
		}
		if _, err := runtime.ensureJavaImageSurface(instance); err != nil {
			t.Fatalf("mirror %d: %v", created, err)
		}
		if live := len(runtime.imageServices); live > ktfJavaImageSurfaceBudget {
			t.Fatalf("mirror %d left %d live, budget is %d",
				created, live, ktfJavaImageSurfaceBudget)
		}
		instances = append(instances, instance)
	}

	// An Image whose mirror was given back gets one again, holding the pixels
	// the Go image has now rather than whatever was in the old surface.
	first := instances[0]
	if _, held := runtime.imageServices[first]; held {
		t.Fatal("the first mirror of 3000 was never evicted")
	}
	surface, err := runtime.ensureJavaImageSurface(first)
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := runtime.Services.Graphics.RGBA(runtime.ServiceOwner, surface)
	if err != nil {
		t.Fatal(err)
	}
	if len(pixels) != 2*2*4 || pixels[3] != 0xff || pixels[0] != 0 {
		t.Fatalf("re-made mirror does not hold the image: %v", pixels[:4])
	}
}

// TestKTFEvictedMirrorLeavesNoGraphicsMapping covers the other half of the
// budget: a Graphics obtained from an Image names the Image's mirror, so a
// mirror that is given back has to take that mapping with it or the next draw
// reads a destroyed surface.
func TestKTFEvictedMirrorLeavesNoGraphicsMapping(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
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
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	frame.Set(1, 1, color.RGBA{B: 0x80, A: 0xff})
	target, err := runtime.newJavaImage(frame)
	if err != nil {
		t.Fatal(err)
	}
	graphics, err := runtime.newJavaInstance("org/kwis/msp/lcdui/Graphics", 4)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Graphics[graphics] = &ktfGraphics{
		Target: frame,
		image:  target,
		clip:   frame.Bounds(),
	}
	surface, err := runtime.ensureGraphicsSurface(graphics)
	if err != nil || surface == 0 {
		t.Fatalf("ensureGraphicsSurface = %s, %v", surface, err)
	}

	for created := 0; created < ktfJavaImageSurfaceBudget*2; created++ {
		other := image.NewRGBA(image.Rect(0, 0, 2, 2))
		other.Set(0, 0, color.RGBA{G: uint8(created), A: 0xff})
		instance, err := runtime.newJavaImage(other)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.ensureJavaImageSurface(instance); err != nil {
			t.Fatal(err)
		}
	}
	if _, held := runtime.imageServices[target]; held {
		t.Fatal("the mirror under test was never evicted")
	}
	if mapped := runtime.GraphicsServices[graphics]; mapped != 0 {
		t.Fatalf("Graphics still names the evicted surface %s", mapped)
	}
	again, err := runtime.ensureGraphicsSurface(graphics)
	if err != nil || again == 0 {
		t.Fatalf("second ensureGraphicsSurface = %s, %v", again, err)
	}
	if again == surface {
		t.Fatal("a destroyed surface came back")
	}
	if mapped := runtime.GraphicsServices[graphics]; mapped != again {
		t.Fatalf("Graphics maps %s, want %s", mapped, again)
	}
}
