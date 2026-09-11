package ktf_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/ktf"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	loader "github.com/mirusu400/aram-core/loader/ktf"
)

// A Clet can initialize the returned RGB565 pointer directly before its first
// Java paint. DNF fighter (#272) uses that initialization to calibrate its
// native origin with Java pixel readback. No draw primitive commits the writes.
func TestKTFInitialNativeFramebufferDirectWritesArePresented(t *testing.T) {
	for _, bounds := range []image.Rectangle{image.Rect(0, 0, 240, 320), image.Rect(3, 5, 179, 225)} {
		t.Run(bounds.String(), func(t *testing.T) {
			frame := image.NewRGBA(bounds)
			r, err := ktf.NewRuntimeForProfile(interpreter.New(), loader.Package{ClientName: "client.bin0", Client: []byte{0x70, 0x47}}, frame, ktf.ProfileID, "", 0, 0)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(err)
			t.Cleanup(func() { _ = r.CPU.Close() })
			must(r.MapImageAndHost())
			handle, err := r.EnsureWIPICScreenFramebuffer()
			must(err)
			// Follow the guest-visible framebuffer object and nested pixel-array ID,
			// rather than reaching into the runtime's private framebuffer map.
			body, err := r.ReadU32(handle)
			must(err)
			stride, err := r.ReadU32(body + 16)
			must(err)
			pixelObject, err := r.ReadU32(body + 24)
			must(err)
			pixelHeader, err := r.ReadU32(pixelObject)
			must(err)
			must(r.CPU.WriteMemory(pixelHeader+8+20*stride, []byte{0, 0xf8}))
			must(r.RecordPresentation())
			point := bounds.Min.Add(image.Pt(0, 20))
			if got := frame.RGBAAt(point.X, point.Y); got != (color.RGBA{R: 255, A: 255}) {
				t.Fatalf("first native pixel = %#v, want direct-memory red at physical row 20", got)
			}
			// Merely asking for the existing handle must not replay old native pixels
			// over a later Java frame or move physical coordinates by a card origin.
			frame.SetRGBA(point.X, point.Y, color.RGBA{B: 255, A: 255})
			again, err := r.EnsureWIPICScreenFramebuffer()
			must(err)
			if again != handle {
				t.Fatalf("screen handle changed: %#x -> %#x", handle, again)
			}
			must(r.RecordPresentation())
			if got := frame.RGBAAt(point.X, point.Y); got != (color.RGBA{B: 255, A: 255}) {
				t.Fatalf("existing handle replayed native frame: %#v", got)
			}
		})
	}
}

func TestKTFInitialNativeFramebufferDoesNotReplaceDirtyJavaPresentation(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "JavaOnly"
		if native {
			name = "NativeAllocatedAfterJavaPaint"
		}
		t.Run(name, func(t *testing.T) {
			frame := image.NewRGBA(image.Rect(0, 0, 240, 320))
			r, err := ktf.NewRuntimeForProfile(interpreter.New(), loader.Package{
				ClientName: "client.bin0", Client: []byte{0x70, 0x47},
			}, frame, ktf.ProfileID, "", 0, 0)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(err)
			t.Cleanup(func() { _ = r.CPU.Close() })
			must(r.MapImageAndHost())
			_, err = r.EnsureScreenGraphics()
			must(err)
			// Model a Java paint that has not yet been presented.
			blue := color.RGBA{B: 255, A: 255}
			frame.SetRGBA(0, 20, blue)
			r.Graphics[r.ScreenGraphics].PixelsDirty = true
			if native {
				_, err = r.EnsureWIPICScreenFramebuffer()
				must(err)
			}
			must(r.RecordPresentation())
			if got := frame.RGBAAt(0, 20); got != blue {
				t.Fatalf("Java presentation was replaced by initial native pixels: %#v", got)
			}
		})
	}
}
