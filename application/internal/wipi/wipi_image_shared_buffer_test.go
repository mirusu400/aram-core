package wipi

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

func TestCreateImageKeepsPartiallyDecodedSourceForLaterImages(t *testing.T) {
	runtime := newPublicRuntime(t)
	encode := func(c color.RGBA) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.SetRGBA(0, 0, c)
		var out bytes.Buffer
		check(t, png.Encode(&out, img))
		return out.Bytes()
	}
	first := encode(color.RGBA{R: 0xff, A: 0xff})
	second := encode(color.RGBA{B: 0xff, A: 0xff})
	buffer, err := runtime.Heap.Allocate(uint32(len(first)+len(second)), false)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(buffer, append(first, second...)))
	output, err := runtime.Heap.Allocate(4, true)
	check(t, err)
	for _, part := range []struct{ offset, length int }{{0, len(first)}, {len(first), len(second)}} {
		result := dispatchPublicAPI(t, runtime, "MC_grpCreateImage", output, buffer, uint32(part.offset), uint32(part.length))
		if result.Low != uint32(guest.WIPIImageDone) {
			t.Fatalf("image at offset %d: result %d", part.offset, int32(result.Low))
		}
		if _, ok := runtime.Heap.Allocations[buffer]; !ok {
			t.Fatalf("source released after partial decode at offset %d", part.offset)
		}
		handle, err := runtime.ReadU32(output)
		check(t, err)
		if handle == 0 {
			t.Fatalf("image at offset %d has no handle", part.offset)
		}
		check(t, runtime.destroyImage(handle))
	}
	if !runtime.Heap.Release(buffer) {
		t.Fatal("caller could not release its shared source buffer")
	}
}
