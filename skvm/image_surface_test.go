package skvm

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// testPNG encodes a distinct image per index. Assets caches a decode by
// content, so repeating one image would reuse a single asset and never reach
// the surface table at all.
func testPNG(t *testing.T, index int) []byte {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	frame.Set(1, 1, color.RGBA{
		R: uint8(index), G: uint8(index >> 8), B: uint8(index >> 16), A: 0xff,
	})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, frame); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestSKVMImagesCollectWhenTheSurfaceTableFills covers what random key input
// found on 웰루시아: SKVM only collects when the guest calls System.gc, and most
// MIDlets never do, so a title that decodes images while it plays filled the
// 1024-surface table with images nothing referenced any more and died with
// "surface count reached 1024". A VM allocates, collects, retries, and only
// then gives up.
//
// Each image here is dropped immediately, so every one after the table fills
// is only creatable if a collection actually happened.
func TestSKVMImagesCollectWhenTheSurfaceTableFills(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	for created := 0; created < 4096; created++ {
		if _, err := vm.newImage(testPNG(t, created)); err != nil {
			t.Fatalf("image %d: %v", created, err)
		}
	}
}

// TestSKVMMutableImagesCollectToo covers the second allocation site: an image
// the MIDlet creates by geometry takes a surface of its own.
func TestSKVMMutableImagesCollectToo(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	for created := 0; created < 4096; created++ {
		state, err := vm.newImageState(8, 8)
		if err != nil {
			t.Fatalf("image %d: %v", created, err)
		}
		vm.NewObject("javax/microedition/lcdui/Image", state)
	}
}
