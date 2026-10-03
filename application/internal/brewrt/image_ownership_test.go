package brewrt

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"golang.org/x/image/bmp"
)

func TestNativeImageOwnershipFlagDoesNotLeaveBorrowedPixels(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	palette := make(color.Palette, 256)
	for index := range palette {
		palette[index] = color.RGBA{A: 255}
	}
	palette[1] = color.RGBA{R: 255, A: 255}
	source := image.NewPaletted(image.Rect(0, 0, 4, 2), palette)
	source.SetColorIndex(0, 0, 1)
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	pixelOffset := binary.LittleEndian.Uint32(encoded.Bytes()[10:14])
	bufferSize := pixelOffset + 8*2 // Enough room for an in-place RGB565 expansion.
	buffer, err := runtime.allocateGuest(bufferSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(buffer, encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: buffer, cpu.RegisterR3: outputAddr,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.setupNativeImage(); err != nil {
		t.Fatal(err)
	}
	object, _ := runtime.cpu.ReadRegister(cpu.RegisterR0)
	var flag [1]byte
	if err := runtime.cpu.ReadMemory(outputAddr, flag[:]); err != nil || flag[0] != 1 {
		t.Fatalf("native allocation flag=%d err=%v", flag[0], err)
	}
	var header [36]byte
	if err := runtime.cpu.ReadMemory(object, header[:]); err != nil {
		t.Fatal(err)
	}
	pixels := binary.LittleEndian.Uint32(header[8:12])
	if pixels >= buffer && pixels < buffer+bufferSize {
		t.Fatal("owned native bitmap still borrows the source allocation")
	}
	// This is the documented caller path after CONVERTBMP reports realloc:
	// free and reuse the original buffer while retaining the native bitmap.
	runtime.releaseGuest(buffer)
	reused, err := runtime.allocateGuest(bufferSize)
	if err != nil || reused != buffer {
		t.Fatalf("source allocation reuse=%08x err=%v", reused, err)
	}
	if err := runtime.refreshNativeBitmap(object); err != nil {
		t.Fatal(err)
	}
	var pixel [2]byte
	if err := runtime.cpu.ReadMemory(pixels, pixel[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0xf800 {
		t.Fatalf("native pixel after source reuse=%04x, want red", got)
	}
	runtime.releaseInterfaceObject(object)
	if _, live := runtime.heapAllocated[object]; live {
		t.Fatal("native bitmap release retained its owned allocation")
	}
}

func TestNativeBitmapLayoutIsStableAfterHeapFragmentation(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	hole, err := runtime.allocateGuest(nativeBitmapPixelsOffset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.allocateGuest(4096); err != nil {
		t.Fatal(err)
	}
	runtime.releaseGuest(hole)
	object, err := runtime.createNativeBitmap(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	var header [36]byte
	if err := runtime.cpu.ReadMemory(object, header[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(header[8:12]); got != object+nativeBitmapPixelsOffset {
		t.Fatalf("fragmented native pixel pointer=%08x, want header-relative storage", got)
	}
	if got := runtime.heapAllocated[object]; got != nativeBitmapPixelsOffset+8 {
		t.Fatalf("contiguous bitmap allocation=%d, want header plus pixels", got)
	}
}

func TestClientBitmapPhysicalFrameCopyKeepsNextObjectIntact(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	clientHeight := int(runtime.screenHeight) - 14
	clientImage := image.NewRGBA(image.Rect(0, 0, int(runtime.screenWidth), clientHeight))
	source, err := runtime.createNativeBitmap(clientImage)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := runtime.createNativeBitmap(clientImage)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.heapAllocated[source] - nativeBitmapPixelsOffset; got < runtime.screenBytes() {
		t.Fatalf("source pixel backing = %d, want at least %d", got, runtime.screenBytes())
	}
	if got := runtime.heapAllocated[destination] - nativeBitmapPixelsOffset; got < runtime.screenBytes() {
		t.Fatalf("destination pixel backing = %d, want at least %d", got, runtime.screenBytes())
	}
	nextObject, err := runtime.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := []byte{0xde, 0xad, 0xbe, 0xef, 0x12, 0x34, 0x56, 0x78}
	if err := runtime.cpu.WriteMemory(nextObject, sentinel); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: destination + nativeBitmapPixelsOffset,
		cpu.RegisterR1: source + nativeBitmapPixelsOffset,
		cpu.RegisterR2: runtime.screenBytes(),
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.moveGuestMemory(); err != nil {
		t.Fatal(err)
	}
	var got [8]byte
	if err := runtime.cpu.ReadMemory(nextObject, got[:]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:], sentinel) {
		t.Fatalf("physical-frame copy changed next object: %x", got)
	}
	var bitmap [24]byte
	if err := runtime.cpu.ReadMemory(destination, bitmap[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(bitmap[22:24]); int(got) != clientHeight {
		t.Fatalf("logical bitmap height = %d, want %d", got, clientHeight)
	}
}
