package ktf

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// TestKTFSaveCarriesImagesWithNoMirror covers what the mirror budget means for
// a save: the service surface an Image is mirrored into is a cache the budget
// gives back, so it is no longer where every Image's pixels can be read from.
// A save that could only describe mirrored Images refused to write at all
// ("image 0x... has no shared surface"), which is every KTF title that decodes
// a sprite and never draws through it.
func TestKTFSaveCarriesImagesWithNoMirror(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	source := image.NewRGBA(image.Rect(0, 0, 3, 2))
	source.Set(2, 1, color.RGBA{R: 0x40, G: 0x50, B: 0x60, A: 0xff})
	object, err := runtime.newJavaImage(source)
	check(t, err)
	mirrored := image.NewRGBA(image.Rect(0, 0, 2, 2))
	mirrored.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	withMirror, err := runtime.newJavaImage(mirrored)
	check(t, err)
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

func TestKTFRestoreDropsDerivedBlitCaches(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 100, A: 128})
	object, err := runtime.newJavaImage(source)
	check(t, err)
	target := image.NewRGBA(source.Bounds())
	runtime.drawKTFJavaImageRaw(
		&ktfGraphics{Target: target, clip: target.Bounds()},
		object,
		source,
		0,
		0,
		0,
	)
	if len(runtime.blitCaches) != 1 {
		t.Fatal("fixture did not populate the derived blit cache")
	}

	var buffer bytes.Buffer
	writer := guest.NewStateWriter(&buffer)
	check(t, WriteState(runtime, runtime.CPU, true, writer))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	if len(runtime.blitCaches) != 0 {
		t.Fatalf("restore retained %d derived blit cache entries", len(runtime.blitCaches))
	}
}

// TestKTFSaveCarriesWIPICFramebuffersWithNoMirror covers the WIPI-C sibling
// of the Image case above: ensureWIPICSurface mirrors a framebuffer lazily,
// only once something actually presents, merges, or encodes it, so a title
// that only ever draws into an offscreen framebuffer never gets one. A save
// that required every framebuffer to have a mirror refused to write at all
// ("invalid WIPI-C framebuffer 0x..."), which is any title using an
// offscreen framebuffer as a draw target it has not yet shown.
func TestKTFSaveCarriesWIPICFramebuffersWithNoMirror(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	handle, err := runtime.createWIPICFramebuffer(4, 4, false)
	check(t, err)
	if runtime.wipicSurfaceServices[handle] != 0 {
		t.Fatal("fixture framebuffer already has a surface")
	}

	var buffer bytes.Buffer
	writer := guest.NewStateWriter(&buffer)
	if err := WriteState(runtime, runtime.CPU, true, writer); err != nil {
		t.Fatalf("save with an unmirrored WIPI-C framebuffer: %v", err)
	}

	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	runtime.wipicFramebuffers = nil
	runtime.wipicSurfaceServices = nil
	started := false
	if err := RestoreState(runtime, runtime.CPU, saved, &started); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored := runtime.wipicFramebuffers[handle]
	if restored == nil {
		t.Fatal("restored WIPI-C framebuffer is missing")
	}
	if runtime.wipicSurfaceServices[handle] != 0 {
		t.Fatal("restored framebuffer carries a surface it never had before save")
	}
	if err := runtime.syncKTFWIPICFramebuffer(handle); err != nil {
		t.Fatalf("sync after restore: %v", err)
	}
	if runtime.wipicSurfaceServices[handle] == 0 {
		t.Fatal("sync after restore did not lazily create the surface")
	}
}

// TestKTFRestoreAcceptsSaveWithoutTheImageBlock covers the saves written
// before Images carried their own pixels. Every Image such a save holds was
// mirrored - it could not have been written otherwise - so its pixels still
// come back from the surface store.
func TestKTFRestoreAcceptsSaveWithoutTheImageBlock(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.Set(1, 0, color.RGBA{G: 0x7f, A: 0xff})
	object, err := runtime.newJavaImage(source)
	check(t, err)
	if _, err := runtime.ensureJavaImageSurface(object); err != nil {
		t.Fatal(err)
	}

	var buffer bytes.Buffer
	writer := guest.NewStateWriter(&buffer)
	check(t, WriteState(runtime, runtime.CPU, true, writer))
	saved := buffer.Bytes()
	if count := binary.LittleEndian.Uint32(saved[len(saved)-8:]); count != 0 {
		t.Fatalf("the mirrored image took %d entries in the image block", count)
	}
	// A schema-5 save is exactly this one without its image block or the
	// schema-8 input-mode pointer.
	older := append([]byte(nil), saved[:len(saved)-8]...)
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
