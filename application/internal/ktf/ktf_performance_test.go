package ktf

import (
	"context"
	"image"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// BenchmarkKTFJavaSetRGBPixelsHostCall models the hot path observed in a KTF
// title that submits most of a frame through one-pixel setRGBPixels calls.
func BenchmarkKTFJavaSetRGBPixelsHostCall(b *testing.B) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	check(b, err)
	b.Cleanup(func() { _ = runtime.CPU.Close() })
	check(b, runtime.MapImageAndHost())
	runtime.JvmContext = allocWords(b, runtime, 3+128)
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	graphics, err := runtime.EnsureScreenGraphics()
	check(b, err)
	pixels, err := runtime.NewJavaArray("[I", 1, 4)
	check(b, err)
	fields := readU32(b, runtime, pixels)
	check(b, runtime.writeWords(fields+8, []uint32{0xff336699}))
	parameters := allocWords(b, runtime, 13)
	if err := runtime.writeWords(parameters, []uint32{
		0,
		graphics,
		1,
		1,
		1,
		1,
		pixels,
		0,
		4,
	}); err != nil {
		b.Fatal(err)
	}
	runtime.NativeParameterBase = parameters
	handler := HostJavaMethod(
		"org/kwis/msp/lcdui/Graphics",
		"setRGBPixels",
		"(IIII[III)V",
	)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := handler(context.Background(), runtime); err != nil {
			b.Fatal(err)
		}
	}
}
