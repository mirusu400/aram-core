package wipi

import (
	"bytes"
	"image"
	"testing"

	"golang.org/x/image/bmp"
)

func TestScreenDrawingOriginMatchesOffscreenCoordinates(t *testing.T) {
	for _, operation := range []string{"MC_grpPutPixel", "MC_grpDrawLine", "MC_grpDrawRect", "MC_grpFillRect", "MC_grpDrawArc", "MC_grpFillArc", "MC_grpDrawString", "MC_grpDrawUnicodeString", "MC_grpDrawPolygon", "MC_grpDrawFillPolygon", "MC_grpSetRGBPixels", "MC_grpCopyFrameBuffer"} {
		t.Run(operation, func(t *testing.T) {
			r := newPublicRuntime(t)
			r.Frame = image.NewRGBA(image.Rect(0, 0, 16, 40))
			r.ScreenDrawingOriginY = 24
			screen, err := r.EnsureScreenFramebuffer()
			check(t, err)
			offscreen := dispatchPublicAPI(t, r, "MC_grpCreateOffScreenFrameBuffer", 16, 16).Low
			ctx, err := r.Heap.Allocate(60, true)
			check(t, err)
			dispatchPublicAPI(t, r, "MC_grpInitContext", ctx)
			dispatchPublicAPI(t, r, "MC_grpSetContext", ctx, 1, 0xffffff)
			data, err := r.Heap.Allocate(256, true)
			check(t, err)
			check(t, r.CPU.WriteMemory(data, []byte{'A', 0}))
			for i, v := range []uint32{1, 8, 3, 2, 3, 10} {
				check(t, r.WriteU32(data+16+uint32(i)*4, v))
			}
			check(t, r.WriteU32(data+64, 0x123456))
			paint := func(target uint32) {
				switch operation {
				case "MC_grpPutPixel":
					dispatchPublicAPI(t, r, operation, target, 2, 3, ctx)
				case "MC_grpDrawLine", "MC_grpDrawRect", "MC_grpFillRect":
					dispatchPublicAPI(t, r, operation, target, 1, 2, 8, 10, ctx)
				case "MC_grpDrawArc", "MC_grpFillArc":
					dispatchPublicAPI(t, r, operation, target, 1, 2, 10, 10, 0, 360, ctx)
				case "MC_grpDrawString", "MC_grpDrawUnicodeString":
					dispatchPublicAPI(t, r, operation, target, 1, 2, data, 1, ctx)
				case "MC_grpDrawPolygon", "MC_grpDrawFillPolygon":
					dispatchPublicAPI(t, r, operation, target, data+16, data+28, 3, ctx)
				case "MC_grpSetRGBPixels":
					dispatchPublicAPI(t, r, operation, target, 1, 2, 1, 1, data+64, 1, ctx)
				case "MC_grpCopyFrameBuffer":
					check(t, r.WriteU32(r.Framebuffers[offscreen].Pixels+15*16*4, 0x123456))
					dispatchPublicAPI(t, r, operation, target, 1, 2, 1, 1, offscreen, 0, 15, ctx)
				}
			}
			paint(screen)
			paint(offscreen)
			// The copy source marker is not part of either destination.
			if operation == "MC_grpCopyFrameBuffer" {
				check(t, r.WriteU32(r.Framebuffers[offscreen].Pixels+15*16*4, 0))
			}
			physical := make([]byte, 16*40*4)
			check(t, r.CPU.ReadMemory(r.Framebuffers[screen].Pixels, physical))
			want := make([]byte, 16*16*4)
			check(t, r.CPU.ReadMemory(r.Framebuffers[offscreen].Pixels, want))
			if bytes.Equal(want, make([]byte, len(want))) {
				t.Fatal("operation painted no pixels")
			}
			if !bytes.Equal(physical[:24*16*4], make([]byte, 24*16*4)) || !bytes.Equal(physical[24*16*4:], want) {
				t.Fatal("screen drawing differs from the offscreen result shifted by 24 rows")
			}
			check(t, r.WriteU32(r.Framebuffers[screen].Pixels+24*16*4, 0x123456))
			dispatchPublicAPI(t, r, "MC_grpGetRGBPixels", screen, 0, 0, 1, 1, data, 1)
			readback := make([]byte, 4)
			check(t, r.CPU.ReadMemory(data, readback))
			if !bytes.Equal(readback, []byte{0x56, 0x34, 0x12, 0}) {
				t.Fatal("screen readback used physical coordinates")
			}
			check(t, r.present(screen))
			if r.Frame.Bounds().Dy() != 40 || r.Framebuffers[screen].Height != 40 {
				t.Fatal("presentation or allocation lost the reserved strip")
			}
		})
	}
}

func TestScreenDrawingOriginReadCopyAndEncode(t *testing.T) {
	r := newPublicRuntime(t)
	r.Frame = image.NewRGBA(image.Rect(0, 0, 16, 40))
	r.ScreenDrawingOriginY = 24
	screen, err := r.EnsureScreenFramebuffer()
	check(t, err)
	physical := r.Framebuffers[screen]
	check(t, r.WriteU32(physical.Pixels, 0xff0000))
	check(t, r.WriteU32(physical.Pixels+24*16*4, 0x123456))
	offscreen := dispatchPublicAPI(t, r, "MC_grpCreateOffScreenFrameBuffer", 1, 1).Low
	check(t, r.copyFramebuffer([]uint32{offscreen, 0, 0, 1, 1, screen, 0, 0, 0}))
	got, err := r.ReadU32(r.Framebuffers[offscreen].Pixels)
	check(t, err)
	if got != 0x123456 {
		t.Fatalf("copied pixel = %x, want client-area pixel", got)
	}
	size, err := r.Heap.Allocate(4, true)
	check(t, err)
	encoded, err := r.encodeImage([]uint32{screen, 0, 0, 1, 1, size})
	check(t, err)
	length, err := r.ReadU32(size)
	check(t, err)
	data := make([]byte, length)
	check(t, r.CPU.ReadMemory(encoded, data))
	decoded, err := bmp.Decode(bytes.NewReader(data))
	check(t, err)
	red, green, blue, _ := decoded.At(0, 0).RGBA()
	if red>>8 != 0x12 || green>>8 != 0x34 || blue>>8 != 0x56 {
		t.Fatalf("encoded pixel = %x %x %x, want client-area pixel", red, green, blue)
	}
	check(t, r.present(screen))
	red, _, _, _ = r.Frame.At(0, 0).RGBA()
	if red>>8 != 0xff {
		t.Fatal("presentation cropped away the physical top strip")
	}
}
