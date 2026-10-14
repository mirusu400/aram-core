package ktf

import (
	"encoding/binary"
	"testing"
)

func TestKTFWIPICImagePaletteCountMagentaKey(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		name := "unique key"
		if duplicate {
			name = "opaque entry shares magenta"
		}
		t.Run(name, func(t *testing.T) {
			// Three indexed pixels: opaque red, transparent palette zero,
			// and an opaque entry that may have exactly the same RGB value.
			encoded := make([]byte, 1082)
			copy(encoded[:2], "BM")
			binary.LittleEndian.PutUint32(encoded[2:6], uint32(len(encoded)))
			binary.LittleEndian.PutUint16(encoded[6:8], 1)
			binary.LittleEndian.PutUint32(encoded[10:14], 1078)
			binary.LittleEndian.PutUint32(encoded[14:18], 40)
			binary.LittleEndian.PutUint32(encoded[18:22], 3)
			binary.LittleEndian.PutUint32(encoded[22:26], 1)
			binary.LittleEndian.PutUint16(encoded[26:28], 1)
			binary.LittleEndian.PutUint16(encoded[28:30], 8)
			binary.LittleEndian.PutUint32(encoded[34:38], 4)
			binary.LittleEndian.PutUint32(encoded[50:54], 256)
			copy(encoded[54:66], []byte{255, 0, 255, 0, 0, 0, 255, 0, 0, 255, 0, 0})
			if duplicate {
				copy(encoded[62:66], encoded[54:58])
			}
			copy(encoded[1078:], []byte{1, 0, 2, 0})

			runtime := newScratchKTFRuntime(t)
			object := createKTFWIPICImage(t, runtime, encoded)
			image := runtime.wipicImages[object]
			recovered := runtime.wipicRestoredImageAlpha(
				runtime.wipicAssetServices[object], image.frameIndex, image.framebuffer,
			)
			if duplicate {
				if image.alpha.key != -1 || len(image.alpha.mask) == 0 ||
					recovered.key != -1 || len(recovered.mask) == 0 {
					t.Fatal("shared magenta needs an alpha mask before and after restore")
				}
			} else if image.alpha.key != 0xf81f || recovered.key != 0xf81f {
				t.Fatalf("created/recovered key = %#x/%#x, want 0xf81f", image.alpha.key, recovered.key)
			}
			image.alpha = recovered
			destination, err := runtime.createWIPICFramebuffer(3, 1, false)
			check(t, err)
			const background = uint16(0x001f)
			fillKTFWIPICFramebuffer(t, runtime, destination, background)
			drawKTFWIPICImage(t, runtime, object, destination, 3, 1)
			last := uint16(0x07e0)
			if duplicate {
				last = 0xf81f
			}
			for x, want := range []uint16{0xf800, background, last} {
				if got := readKTFWIPICPixel(t, runtime, destination, x, 0); got != want {
					t.Fatalf("pixel %d = %04x, want %04x", x, got, want)
				}
			}
		})
	}
}
