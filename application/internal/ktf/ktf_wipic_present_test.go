package ktf

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func TestKTFWIPICPresentationDestinationLayout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source image.Point
		parent image.Rectangle
		target image.Rectangle
	}{
		{"equal", image.Pt(3, 2), image.Rect(0, 0, 3, 2), image.Rect(0, 0, 3, 2)},
		{"preserve_margins", image.Pt(2, 1), image.Rect(0, 0, 4, 3), image.Rect(0, 0, 4, 3)},
		{"clip_oversized", image.Pt(4, 3), image.Rect(0, 0, 2, 2), image.Rect(0, 0, 2, 2)},
		{"nonzero_origin", image.Pt(3, 2), image.Rect(7, 11, 10, 13), image.Rect(7, 11, 10, 13)},
		{"padded_subimage", image.Pt(3, 2), image.Rect(4, 6, 11, 12), image.Rect(6, 7, 9, 9)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRuntime(t)
			r.frame = image.NewRGBA(image.Rectangle{Max: tc.source})
			graphics, err := r.EnsureScreenGraphics()
			check(t, err)
			handle, err := r.EnsureWIPICScreenFramebuffer()
			check(t, err)
			fb := r.wipicFramebuffers[handle]
			words := [...]uint16{0xf800, 0x07e0, 0x001f, 0xffff, 0, 0xffe0}
			colors := [...]color.RGBA{
				{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255},
				{R: 255, G: 255, B: 255, A: 255}, {A: 255}, {R: 255, G: 255, A: 255},
			}
			pixels := make([]byte, fb.stride*fb.height)
			for y := 0; y < fb.height; y++ {
				for x := 0; x < fb.width; x++ {
					binary.LittleEndian.PutUint16(pixels[y*fb.stride+x*2:], words[(y*fb.width+x)%len(words)])
				}
			}
			check(t, r.CPU.WriteMemory(fb.pixels, pixels))
			backing := image.NewRGBA(tc.parent)
			for i := range backing.Pix {
				backing.Pix[i] = 0x5a
			}
			r.frame = backing.SubImage(tc.target).(*image.RGBA)
			want := image.NewRGBA(tc.parent)
			copy(want.Pix, backing.Pix)
			for y := 0; y < min(fb.height, tc.target.Dy()); y++ {
				for x := 0; x < min(fb.width, tc.target.Dx()); x++ {
					want.SetRGBA(tc.target.Min.X+x, tc.target.Min.Y+y, colors[(y*fb.width+x)%len(colors)])
				}
			}
			r.Graphics[graphics].PixelsDirty = false
			check(t, r.presentWIPICFramebuffer(handle))
			if !bytes.Equal(backing.Pix, want.Pix) {
				t.Fatal("presentation changed pixels outside the overlap or used the wrong row layout")
			}
			if r.WipicScreenPending || !r.Graphics[graphics].PixelsDirty || r.PresentCount != 1 {
				t.Fatalf("presentation state: pending=%v dirty=%v count=%d", r.WipicScreenPending, r.Graphics[graphics].PixelsDirty, r.PresentCount)
			}
			committed := r.Services.Graphics.LastFrame().RGBA
			r.frame.Pix[0] ^= 0xff
			if !bytes.Equal(committed, r.Services.Graphics.LastFrame().RGBA) {
				t.Fatal("host destination aliases committed pixels")
			}
			// Even an unqueued guest write is imported at consumption, not
			// hidden by a cached frame or by WipicScreenPending being false.
			check(t, r.CPU.WriteMemory(fb.pixels, []byte{0, 0}))
			if !bytes.Equal(committed, r.Services.Graphics.LastFrame().RGBA) {
				t.Fatal("unpresented guest write changed committed pixels")
			}
			check(t, r.presentWIPICFramebuffer(handle))
			if got := r.frame.RGBAAt(tc.target.Min.X, tc.target.Min.Y); got != (color.RGBA{A: 255}) {
				t.Fatalf("fresh guest write was not consumed: %v", got)
			}
			if r.PresentCount != 2 {
				t.Fatalf("second presentation count = %d", r.PresentCount)
			}
		})
	}
}

// Includes guest-memory synchronization and RGB565 conversion, not just the
// destination copy. A changing final pixel prevents unchanged-frame shortcuts.
func BenchmarkKTFWIPICPresentation(b *testing.B) {
	r := newTestRuntime(b)
	r.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	handle, err := r.EnsureWIPICScreenFramebuffer()
	check(b, err)
	fb := r.wipicFramebuffers[handle]
	last := fb.pixels + uint32((fb.height-1)*fb.stride+(fb.width-1)*2)
	check(b, r.presentWIPICFramebuffer(handle))
	var pixel [2]byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint16(pixel[:], uint16(i))
		check(b, r.CPU.WriteMemory(last, pixel[:]))
		check(b, r.presentWIPICFramebuffer(handle))
	}
}
