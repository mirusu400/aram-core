package application

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
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = runtime.cpu.Close() })
	if err := runtime.mapImageAndHost(); err != nil {
		b.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		b.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	graphics, err := runtime.ensureScreenGraphics()
	if err != nil {
		b.Fatal(err)
	}
	pixels, err := runtime.newJavaArray("[I", 1, 4)
	if err != nil {
		b.Fatal(err)
	}
	fields, err := runtime.readU32(pixels)
	if err != nil {
		b.Fatal(err)
	}
	if err := runtime.writeWords(fields+8, []uint32{0xff336699}); err != nil {
		b.Fatal(err)
	}
	parameters, err := runtime.allocateWords(13)
	if err != nil {
		b.Fatal(err)
	}
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
	runtime.nativeParameterBase = parameters
	handler := ktfHostJavaMethod(
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
