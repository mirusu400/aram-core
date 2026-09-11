package ktf

import (
	"bytes"
	"image/color"
	"testing"
)

// DNF fighter writes the native row index as an RGB565 halfword, then reads
// byte zero of Graphics.getPixels to recover the physical Card origin. A
// luminance conversion changes marker 0x0014 into 18 instead of returning 20.
func TestKTFGraphicsGetPixelsPreservesNativeOriginMarker(t *testing.T) {
	r := newCardOriginRuntime(t)
	showAnnunciator(r)
	handle, err := r.EnsureWIPICScreenFramebuffer()
	check(t, err)
	fb := r.wipicFramebuffers[handle]
	check(t, r.CPU.WriteMemory(fb.pixels+uint32(20*fb.stride), []byte{20, 0}))
	check(t, r.RecordPresentation())
	g, err := r.EnsureScreenGraphics()
	check(t, err)
	r.ResetScreenGraphics(g)
	dst, err := r.newJavaByteArray(bytes.Repeat([]byte{0xa5}, 5))
	check(t, err)
	parameters := allocWords(t, r, 8)
	r.NativeParameterBase = parameters
	check(t, r.writeWords(parameters, []uint32{g, 0, 0, 1, 1, dst, 1, 4}))
	_, err = r.handleGraphicsMethod("getPixels", "(IIII[BII)V")
	check(t, err)
	got, err := r.readJavaByteArray(dst)
	check(t, err)
	if want := []byte{0xa5, 20, 0, 0xa5, 0xa5}; !bytes.Equal(got, want) {
		t.Fatalf("native marker readback=%x, want %x", got, want)
	}
}

func TestKTFGraphicsGetPixelsRGB565StrideAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name           string
		stride, length uint32
		wantError      bool
	}{
		{"padded", 6, 11, false}, {"short stride", 3, 11, true}, {"short destination", 6, 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCardOriginRuntime(t)
			g, err := r.EnsureScreenGraphics()
			check(t, err)
			r.frame.SetRGBA(1, 2, color.RGBA{R: 255, A: 255})
			r.frame.SetRGBA(2, 2, color.RGBA{G: 255, A: 255})
			r.frame.SetRGBA(1, 3, color.RGBA{B: 255, A: 255})
			r.frame.SetRGBA(2, 3, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			dst, err := r.newJavaByteArray(bytes.Repeat([]byte{0xa5}, int(tc.length)))
			check(t, err)
			parameters := allocWords(t, r, 8)
			r.NativeParameterBase = parameters
			check(t, r.writeWords(parameters, []uint32{g, 1, 2, 2, 2, dst, 1, tc.stride}))
			_, err = r.handleGraphicsMethod("getPixels", "(IIII[BII)V")
			if tc.wantError {
				if err == nil {
					t.Fatal("invalid packed byte range accepted")
				}
				return
			}
			check(t, err)
			got, err := r.readJavaByteArray(dst)
			check(t, err)
			want := []byte{0xa5, 0, 0xf8, 0xe0, 7, 0xa5, 0xa5, 0x1f, 0, 0xff, 0xff}
			if !bytes.Equal(got, want) {
				t.Fatalf("RGB565 readback=%x, want %x", got, want)
			}
		})
	}
}
