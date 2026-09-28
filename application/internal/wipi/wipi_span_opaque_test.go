package wipi

import (
	"encoding/binary"
	"image/color"
	"testing"
)

func TestOpaqueCompositeSpanPreservesPixelsAndPresentation(t *testing.T) {
	for _, test := range []struct {
		name              string
		bits              int
		colour, untouched uint32
		alpha             int32
	}{
		{name: "rgb565", bits: 16, colour: 0x5aeb, untouched: 0x0abc, alpha: 255},
		{name: "rgb888", bits: 32, colour: 0x002468ac, untouched: 0x00c0ffee, alpha: 300},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newPublicRuntime(t)
			r.framebufferBits = test.bits
			screen := dispatchPublicAPI(t, r, "MC_grpGetScreenFrameBuffer", 0).Low
			fb := r.Framebuffers[screen]
			row := make([]byte, fb.Width*int(fb.bytesPerPixel()))
			for x := 0; x < fb.Width; x++ {
				if test.bits == 16 {
					binary.LittleEndian.PutUint16(row[x*2:], uint16(test.untouched))
				} else {
					binary.LittleEndian.PutUint32(row[x*4:], test.untouched)
				}
			}
			check(t, r.CPU.WriteMemory(fb.Pixels+uint32(3*len(row)), row))
			context := &wipiGraphicsContext{foreground: test.colour, alpha: test.alpha}
			check(t, r.compositeSpan(fb, 2, 3, 7, context, nil, nil))
			for x := 0; x < fb.Width; x++ {
				want := test.untouched
				if x >= 2 && x < 9 {
					want = test.colour
				}
				got, err := r.framebufferPixel(fb, x, 3)
				check(t, err)
				if got != want {
					t.Fatalf("pixel %d = %#x, want %#x", x, got, want)
				}
			}
			check(t, r.present(screen))
			red, green, blue := r.rgbFromDevicePixel(test.colour)
			want := color.RGBA{R: byte(red), G: byte(green), B: byte(blue), A: 0xff}
			if got := r.Frame.RGBAAt(3, 3); got != want {
				t.Fatalf("presented pixel = %#v, want %#v", got, want)
			}
		})
	}
}

func TestCompositeSpanKeepsDestinationDependentModes(t *testing.T) {
	for _, test := range []struct {
		name        string
		alpha       int32
		xor         bool
		values      []uint32
		transparent []bool
	}{
		{name: "alpha", alpha: 128},
		{name: "xor", alpha: 255, xor: true},
		{name: "source_pixels", alpha: 255, values: []uint32{0x123456, 0xabcdef, 0x654321}},
		{name: "transparent", alpha: 255, transparent: []bool{false, true, false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newPublicRuntime(t)
			screen := dispatchPublicAPI(t, r, "MC_grpGetScreenFrameBuffer", 0).Low
			fb := r.Framebuffers[screen]
			const destination = uint32(0x29384a)
			var seed [12]byte
			for index := 0; index < 3; index++ {
				binary.LittleEndian.PutUint32(seed[index*4:], destination)
			}
			check(t, r.CPU.WriteMemory(fb.Pixels+uint32(2*fb.Width+1)*4, seed[:]))
			graphics := &wipiGraphicsContext{
				foreground: 0x719bce,
				alpha:      test.alpha,
				xor:        test.xor,
			}
			var expected [3]uint32
			for index := range expected {
				expected[index] = destination
				if test.transparent != nil && test.transparent[index] {
					continue
				}
				foreground := graphics.foreground
				if test.values != nil {
					foreground = test.values[index]
				}
				var err error
				expected[index], err = r.compositePixel(graphics, foreground, destination, 0xff)
				check(t, err)
			}
			check(t, r.compositeSpan(fb, 1, 2, 3, graphics, test.values, test.transparent))
			for index, want := range expected {
				got, err := r.framebufferPixel(fb, index+1, 2)
				check(t, err)
				if got != want {
					t.Fatalf("pixel %d = %#x, want %#x", index, got, want)
				}
			}
		})
	}
}
