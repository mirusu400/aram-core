package ktf

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestKTFJavaImageCollectionReleasesUnreachablePixelsAndMirrors(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)

	pixels := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	dead, err := r.newJavaImage(pixels)
	check(t, err)
	mirror, err := r.ensureJavaImageSurface(dead)
	check(t, err)
	r.ensureKTFBlitCache(dead, pixels)

	live, err := r.newJavaImage(image.NewRGBA(pixels.Bounds()))
	check(t, err)
	parameters := allocWords(t, r, 2)
	r.NativeParameterBase = parameters
	check(t, r.writeWords(parameters, []uint32{live, 0}))
	graphics, err := r.handleImageMethod("getGraphics", "()Lorg/kwis/msp/lcdui/Graphics;")
	check(t, err)
	if graphics == 0 {
		t.Fatal("image returned no Graphics object")
	}
	check(t, r.writeWords(parameters, []uint32{0, 0}))
	r.NativeParameterBase = 0
	check(t, r.CPU.WriteRegister(cpu.RegisterR8, graphics))

	r.CollectJavaHeapForTest()
	if r.images[dead] != nil || r.blitCaches[dead] != nil ||
		r.imageServices[dead] != 0 || r.imageSurfaceUse[dead] != 0 {
		t.Fatal("unreachable image still has pixels or cache entries")
	}
	if _, err := r.Services.Graphics.Descriptor(r.ServiceOwner, mirror); err == nil {
		t.Fatal("unreachable image's service surface is still allocated")
	}
	if r.images[live] == nil || r.Graphics[graphics] == nil || r.Graphics[graphics].Target != r.images[live] {
		t.Fatal("collection lost the image reachable through live Graphics")
	}
}

func TestKTFJavaImageGrowthTriggersCollection(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	const count = 64
	for range count {
		_, err := r.newJavaImage(image.NewRGBA(image.Rect(0, 0, 512, 512)))
		check(t, err)
	}
	if len(r.images) > count/2 {
		t.Fatalf("host pixel growth retained %d of %d unreachable images", len(r.images), count)
	}
}

func TestKTFJavaImageCollectionKeepsSharedLegacyAssetUntilLastImage(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var encoded bytes.Buffer
	check(t, png.Encode(&encoded, frame))
	asset, err := r.Services.Assets.Decode(r.ServiceOwner, encoded.Bytes(), shared.DecodeOptions{})
	check(t, err)
	check(t, r.Services.Assets.Retain(r.ServiceOwner, asset))
	info, err := r.Services.Assets.Info(r.ServiceOwner, asset)
	check(t, err)
	surface := info.Frames[0].Surface

	dead, err := r.newJavaImage(frame)
	check(t, err)
	live, err := r.newJavaImage(frame)
	check(t, err)
	for _, instance := range []uint32{dead, live} {
		r.imageServices[instance] = surface
		r.javaAssetServices[instance] = asset
	}

	check(t, r.CPU.WriteRegister(cpu.RegisterR8, live))
	r.CollectJavaHeapForTest()
	if r.javaAssetServices[dead] != 0 || r.javaAssetServices[live] != asset {
		t.Fatal("collection failed to drop just the dead image's asset reference")
	}
	_, err = r.Services.Graphics.Descriptor(r.ServiceOwner, surface)
	check(t, err)

	check(t, r.CPU.WriteRegister(cpu.RegisterR8, 0))
	r.CollectJavaHeapForTest()
	if _, err := r.Services.Assets.Info(r.ServiceOwner, asset); err == nil {
		t.Fatal("asset survived after its last image was collected")
	}
	if _, err := r.Services.Graphics.Descriptor(r.ServiceOwner, surface); err == nil {
		t.Fatal("asset-owned surface survived after its last image was collected")
	}
}

// TestKTFJavaImagesDoNotTakeASurfaceUntilDrawnThrough covers what random key
// input found on 다이하드4: newJavaImage gave every Image a mirrored service
// surface, and KTF Java has no collector, so a title that decodes sprites while
// it plays climbed to "surface count reached 1024" with mirrors nothing ever
// read. Drawing runs on the Go image; the surface is only the target a
// Graphics obtained from the Image syncs to.
func TestKTFJavaImagesDoNotTakeASurfaceUntilDrawnThrough(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

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
	check(t, err)
	if surface == 0 {
		t.Fatal("no surface was materialised")
	}
	again, err := runtime.ensureJavaImageSurface(last)
	check(t, err)
	if again != surface {
		t.Fatalf("second ask made a new surface: %s then %s", surface, again)
	}
}

// TestKTFDecodedImagesReleaseTheirAsset pins the other half: a decoded Image
// copies its pixels out, so holding the asset - and the surface the asset owns
// - was one of each per decoded image for the lifetime of the title.
func TestKTFDecodedImagesReleaseTheirAsset(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	const images = 2000
	for created := 0; created < images; created++ {
		frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
		frame.Set(0, 0, color.RGBA{
			R: uint8(created), G: uint8(created >> 8), A: 0xff,
		})
		var encoded bytes.Buffer
		check(t, png.Encode(&encoded, frame))
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

// TestKTFJavaImageKeysOutOpaqueMagentaCorner covers issues #197 and #198:
// 요구르팅 ships an org.kwis.msp.lcdui.Image asset as a paletted PNG whose
// sole palette entry is pure magenta with no tRNS chunk, the classic KTF
// color-keyed-sprite convention with no PNG alpha to carry it. A
// standards-compliant decode is therefore fully opaque, and without a
// fallback the sprite painted as a solid magenta block instead of the
// transparent placeholder it was meant to be. newJavaEncodedImage must key
// the corner color out exactly the way the sibling WIPI-C image path already
// does for the same convention.
func TestKTFJavaImageKeysOutOpaqueMagentaCorner(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	palette := color.Palette{color.NRGBA{R: 0xff, G: 0x00, B: 0xff, A: 0xff}}
	frame := image.NewPaletted(image.Rect(0, 0, 16, 16), palette)
	var encoded bytes.Buffer
	check(t, png.Encode(&encoded, frame))

	instance, err := runtime.newJavaEncodedImage(encoded.Bytes())
	check(t, err)
	source, ok := runtime.images[instance].(*image.NRGBA)
	if !ok {
		t.Fatalf("decoded image is %#v, want *image.NRGBA", runtime.images[instance])
	}
	for p := 0; p+3 < len(source.Pix); p += 4 {
		if source.Pix[p+3] != 0 {
			t.Fatalf("pixel %d kept alpha %#x, want fully keyed out", p/4, source.Pix[p+3])
		}
	}
}

// TestKTFJavaImageLeavesRealAlphaAlone guards the fallback's condition: an
// image that already decoded with any real transparency must not have its
// opaque magenta pixels keyed out too, the same way the WIPI-C path only
// falls back to the corner heuristic when nothing else decoded transparent.
func TestKTFJavaImageLeavesRealAlphaAlone(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	frame := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	frame.SetNRGBA(0, 0, color.NRGBA{R: 0xff, G: 0x00, B: 0xff, A: 0xff}) // opaque magenta corner
	frame.SetNRGBA(1, 0, color.NRGBA{A: 0})                               // a real transparent pixel elsewhere
	frame.SetNRGBA(0, 1, color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff})
	frame.SetNRGBA(1, 1, color.NRGBA{R: 0x40, G: 0x50, B: 0x60, A: 0xff})
	var encoded bytes.Buffer
	check(t, png.Encode(&encoded, frame))

	instance, err := runtime.newJavaEncodedImage(encoded.Bytes())
	check(t, err)
	source, ok := runtime.images[instance].(*image.NRGBA)
	if !ok {
		t.Fatalf("decoded image is %#v, want *image.NRGBA", runtime.images[instance])
	}
	if source.NRGBAAt(0, 0).A != 0xff {
		t.Fatal("opaque magenta corner was keyed out even though the image already carried real alpha")
	}
	if source.NRGBAAt(1, 0).A != 0 {
		t.Fatal("the genuinely transparent pixel lost its transparency")
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
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

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
	check(t, err)
	pixels, err := runtime.Services.Graphics.RGBA(runtime.ServiceOwner, surface)
	check(t, err)
	if len(pixels) != 2*2*4 || pixels[3] != 0xff || pixels[0] != 0 {
		t.Fatalf("re-made mirror does not hold the image: %v", pixels[:4])
	}
}

// TestKTFEvictedMirrorLeavesNoGraphicsMapping covers the other half of the
// budget: a Graphics obtained from an Image names the Image's mirror, so a
// mirror that is given back has to take that mapping with it or the next draw
// reads a destroyed surface.
func TestKTFEvictedMirrorLeavesNoGraphicsMapping(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	frame.Set(1, 1, color.RGBA{B: 0x80, A: 0xff})
	target, err := runtime.newJavaImage(frame)
	check(t, err)
	graphics, err := runtime.newJavaInstance("org/kwis/msp/lcdui/Graphics", 4)
	check(t, err)
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
		check(t, err)
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
