package ktf

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// TestKTFSaveCarriesImagesWithNoMirror covers what the mirror budget means for
// a save: the service surface an Image is mirrored into is a cache the budget
// gives back, so it is no longer where every Image's pixels can be read from.
// A save that could only describe mirrored Images refused to write at all
// ("image 0x... has no shared surface"), which is every KTF title that decodes
// a sprite and never draws through it.
func TestKTFSaveCarriesImagesWithNoMirror(t *testing.T) {
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
	source := image.NewRGBA(image.Rect(0, 0, 3, 2))
	source.Set(2, 1, color.RGBA{R: 0x40, G: 0x50, B: 0x60, A: 0xff})
	object, err := runtime.newJavaImage(source)
	if err != nil {
		t.Fatal(err)
	}
	mirrored := image.NewRGBA(image.Rect(0, 0, 2, 2))
	mirrored.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	withMirror, err := runtime.newJavaImage(mirrored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureJavaImageSurface(withMirror); err != nil {
		t.Fatal(err)
	}

	var buffer bytes.Buffer
	writer := guest.NewStateWriter(&buffer)
	if err := WriteState(runtime, runtime.CPU, true, writer); err != nil {
		t.Fatalf("save with an unmirrored image: %v", err)
	}

	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if carried := saved.imagePixels[object]; carried == nil {
		t.Fatal("the unmirrored image carried no pixels")
	}
	if _, duplicated := saved.imagePixels[withMirror]; duplicated {
		t.Fatal("a mirrored image was saved twice")
	}

	runtime.images = nil
	started := false
	if err := RestoreState(runtime, runtime.CPU, saved, &started); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !started {
		t.Fatal("restore lost the started flag")
	}
	restored, ok := runtime.images[object].(*image.RGBA)
	if !ok {
		t.Fatalf("restored image is %T", runtime.images[object])
	}
	if restored.Bounds() != source.Bounds() {
		t.Fatalf("restored bounds %v, want %v",
			restored.Bounds(), source.Bounds())
	}
	if !bytes.Equal(restored.Pix, source.Pix) {
		t.Fatal("restored pixels differ from the saved image")
	}
	if mirroredBack, ok := runtime.images[withMirror].(*image.RGBA); !ok ||
		!bytes.Equal(mirroredBack.Pix, mirrored.Pix) {
		t.Fatal("the mirrored image did not come back from its surface")
	}
}

// TestKTFRestoreAcceptsSaveWithoutTheImageBlock covers the saves written
// before Images carried their own pixels. Every Image such a save holds was
// mirrored - it could not have been written otherwise - so its pixels still
// come back from the surface store.
func TestKTFRestoreAcceptsSaveWithoutTheImageBlock(t *testing.T) {
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
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.Set(1, 0, color.RGBA{G: 0x7f, A: 0xff})
	object, err := runtime.newJavaImage(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureJavaImageSurface(object); err != nil {
		t.Fatal(err)
	}

	var buffer bytes.Buffer
	writer := guest.NewStateWriter(&buffer)
	if err := WriteState(runtime, runtime.CPU, true, writer); err != nil {
		t.Fatal(err)
	}
	saved := buffer.Bytes()
	if count := binary.LittleEndian.Uint32(saved[len(saved)-4:]); count != 0 {
		t.Fatalf("the mirrored image took %d entries in the image block", count)
	}
	// A schema-5 save is exactly this one without its image block.
	older := append([]byte(nil), saved[:len(saved)-4]...)
	binary.LittleEndian.PutUint32(older[4:8], ktfStateSchemaV5)

	decoder := guest.StateDecoder{Reader: bytes.NewReader(older)}
	restored, err := ParseState(runtime, &decoder)
	if err != nil {
		t.Fatalf("parse a schema-%d save: %v", ktfStateSchemaV5, err)
	}
	if len(restored.imagePixels) != 0 {
		t.Fatalf("an older save carried %d images", len(restored.imagePixels))
	}
	runtime.images = nil
	started := false
	if err := RestoreState(runtime, runtime.CPU, restored, &started); err != nil {
		t.Fatalf("restore a schema-%d save: %v", ktfStateSchemaV5, err)
	}
	back, ok := runtime.images[object].(*image.RGBA)
	if !ok || !bytes.Equal(back.Pix, source.Pix) {
		t.Fatal("the mirrored image did not come back from its surface")
	}
}
