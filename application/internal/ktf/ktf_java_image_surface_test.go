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
