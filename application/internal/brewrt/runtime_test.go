package brewrt

import (
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"bytes"
	"golang.org/x/image/bmp"
)

func TestRuntimeBootstrapsSyntheticARMModule(t *testing.T) {
	// ldr r0,[pc,#8]; str r0,[r2]; mov r0,#0; bx lr; .word heapBase
	module := make([]byte, 20)
	for offset, instruction := range []uint32{0xe59f0008, 0xe5820000, 0xe3a00000, 0xe12fff1e, heapBase} {
		binary.LittleEndian.PutUint32(module[offset*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap synthetic ARM module: %v", err)
	}
	if got := runtime.ModuleObject(); got != heapBase {
		t.Fatalf("module object = 0x%08x, want 0x%08x", got, heapBase)
	}
}

func TestDecodeSplashUsesEmbeddedBMPContract(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 120, 61))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 4), B: 0x55, A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	mif := make([]byte, mifSplashOffset+encoded.Len())
	copy(mif[mifSplashOffset:], encoded.Bytes())
	decoded, err := decodeSplash(mif)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != source.Bounds() {
		t.Fatalf("decoded bounds = %v, want %v", decoded.Bounds(), source.Bounds())
	}
}

func TestMatchRejectsEveryUnauthenticatedArchive(t *testing.T) {
	if _, matched, err := Match([]byte("PK\x03\x04synthetic")); err != nil || matched {
		t.Fatalf("unauthenticated archive matched=%v err=%v", matched, err)
	}
}
