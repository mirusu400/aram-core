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

func TestSetupNativeImagePublishesOwnedRGB565Bitmap(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	source.Set(1, 0, color.RGBA{G: 255, A: 255})
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	buffer, info, reallocated := heapBase+0x1000, heapBase+0x2000, heapBase+0x2100
	if err := runtime.cpu.WriteMemory(buffer, encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(reallocated, []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: buffer, cpu.RegisterR2: info, cpu.RegisterR3: reallocated,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperSetupImageSlot*2 + 2)
	if err != nil || !handled {
		t.Fatalf("SetupNativeImage handled=%v err=%v", handled, err)
	}
	object, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || object < heapBase {
		t.Fatalf("bitmap object=0x%08x err=%v", object, err)
	}
	header := make([]byte, 36)
	if err := runtime.cpu.ReadMemory(object, header); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(header); got != bitmapVTable {
		t.Fatalf("bitmap vtable=0x%08x", got)
	}
	if got := binary.LittleEndian.Uint16(header[20:]); got != 2 {
		t.Fatalf("bitmap width=%d", got)
	}
	if header[28] != 16 || header[29] != idibColorScheme565 {
		t.Fatalf("bitmap format=%v", header[28:30])
	}
	pixels := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(binary.LittleEndian.Uint32(header[8:]), pixels); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pixels, []byte{0x00, 0xf8, 0xe0, 0x07}) {
		t.Fatalf("RGB565 pixels=%x", pixels)
	}
	flag := []byte{0xff}
	if err := runtime.cpu.ReadMemory(reallocated, flag); err != nil || flag[0] != 0 {
		t.Fatalf("reallocated=%v err=%v", flag, err)
	}
	stack := stackBase + 0x400
	args := make([]byte, 20)
	binary.LittleEndian.PutUint32(args[0:], 1)
	binary.LittleEndian.PutUint32(args[4:], object)
	binary.LittleEndian.PutUint32(args[16:], 0)
	if err := runtime.cpu.WriteMemory(stack, args); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: 3, cpu.RegisterR2: 4, cpu.RegisterR3: 2,
		cpu.RegisterSP: stack, cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err = runtime.handleAppletMethodTrap(displayTrapBase + 6*2 + 2)
	if err != nil || !handled {
		t.Fatalf("BitBlt handled=%v err=%v", handled, err)
	}
	frame := make([]byte, 4)
	frameAt := framebufferBase + (4*framebufferWidth+3)*2
	if err := runtime.cpu.ReadMemory(frameAt, frame); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame, []byte{0x00, 0xf8, 0xe0, 0x07}) {
		t.Fatalf("BitBlt framebuffer pixels=%x", frame)
	}
}

func TestSetupNativeImageUsesGeometryWhenIndexedBMPSizeIsStale(t *testing.T) {
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
	data := append([]byte(nil), encoded.Bytes()...)
	pixelOffset := binary.LittleEndian.Uint32(data[10:14])
	binary.LittleEndian.PutUint32(data[2:6], pixelOffset+4)
	binary.LittleEndian.PutUint32(data[46:50], 255)

	buffer := heapBase + 0x1000
	if err := runtime.cpu.WriteMemory(buffer, data); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: buffer,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.setupNativeImage(); err != nil {
		t.Fatal(err)
	}
	object, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || object == 0 {
		t.Fatalf("stale-size indexed BMP object=0x%08x err=%v", object, err)
	}
	header := make([]byte, 36)
	if err := runtime.cpu.ReadMemory(object, header); err != nil {
		t.Fatal(err)
	}
	pixel := make([]byte, 2)
	if err := runtime.cpu.ReadMemory(binary.LittleEndian.Uint32(header[8:12]), pixel); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel); got != 0xf800 {
		t.Fatalf("first stale-size indexed BMP pixel=0x%04x, want red", got)
	}
	if err := runtime.cpu.WriteMemory(buffer+pixelOffset+4, []byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.refreshNativeBitmap(object); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.ReadMemory(binary.LittleEndian.Uint32(header[8:12]), pixel); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel); got != 0 {
		t.Fatalf("live indexed BMP pixel=0x%04x, want black", got)
	}
}

func TestSetupNativeImageExpandsIndexedHeapBufferInPlace(t *testing.T) {
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
	pitch := uint32(8)
	buffer, err := runtime.allocateGuest(pixelOffset + pitch*2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(buffer, encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: buffer,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.setupNativeImage(); err != nil {
		t.Fatal(err)
	}
	object, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || object == 0 {
		t.Fatalf("expanded indexed BMP object=0x%08x err=%v", object, err)
	}
	header := make([]byte, 36)
	if err := runtime.cpu.ReadMemory(object, header); err != nil {
		t.Fatal(err)
	}
	pixels := binary.LittleEndian.Uint32(header[8:12])
	if want := buffer + pixelOffset; pixels != want {
		t.Fatalf("expanded indexed BMP pixels=0x%08x, want caller storage 0x%08x", pixels, want)
	}
	if _, tracked := runtime.nativeImages[object]; tracked {
		t.Fatal("in-place expanded indexed BMP retained a decode-on-blit source")
	}
	var pixel [2]byte
	if err := runtime.cpu.ReadMemory(pixels, pixel[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0xf800 {
		t.Fatalf("expanded indexed BMP first pixel=0x%04x, want red", got)
	}
	var blue [2]byte
	binary.LittleEndian.PutUint16(blue[:], 0x001f)
	if err := runtime.cpu.WriteMemory(pixels, blue[:]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.ReadMemory(pixels, pixel[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0x001f {
		t.Fatalf("caller-owned RGB565 update=0x%04x, want blue", got)
	}
}

func TestNativeBitmapRetainsDecodedPixelsAfterSourceReused(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	source := image.NewRGBA(image.Rect(0, 0, 1, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	buffer, err := runtime.allocateGuest(uint32(encoded.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(buffer, encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, buffer); err != nil {
		t.Fatal(err)
	}
	if err := runtime.setupNativeImage(); err != nil {
		t.Fatal(err)
	}
	object, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || object == 0 {
		t.Fatalf("bitmap object=0x%08x err=%v", object, err)
	}
	if _, ok := runtime.nativeImages[object]; !ok {
		t.Fatal("expected live native-image source")
	}
	runtime.releaseGuest(buffer)
	if err := runtime.cpu.WriteMemory(buffer, make([]byte, encoded.Len())); err != nil {
		t.Fatal(err)
	}
	if err := runtime.refreshNativeBitmap(object); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.nativeImages[object]; ok {
		t.Fatal("reused encoded source remained live")
	}
	var header [12]byte
	if err := runtime.cpu.ReadMemory(object, header[:]); err != nil {
		t.Fatal(err)
	}
	pixels := binary.LittleEndian.Uint32(header[8:12])
	var pixel [2]byte
	if err := runtime.cpu.ReadMemory(pixels, pixel[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0xf800 {
		t.Fatalf("decoded pixel=0x%04x, want red", got)
	}
}

func TestNativeBitmapStopsRefreshingWhenGuestReshapesIDIB(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	source := image.NewRGBA(image.Rect(0, 0, 1, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	buffer, err := runtime.allocateGuest(uint32(encoded.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(buffer, encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, buffer); err != nil {
		t.Fatal(err)
	}
	if err := runtime.setupNativeImage(); err != nil {
		t.Fatal(err)
	}
	object, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || object == 0 {
		t.Fatalf("bitmap object=0x%08x err=%v", object, err)
	}
	var header [12]byte
	if err := runtime.cpu.ReadMemory(object, header[:]); err != nil {
		t.Fatal(err)
	}
	pixels := binary.LittleEndian.Uint32(header[8:12])
	// The applet adjusts IDIB geometry and writes its own pixel data.
	if err := runtime.cpu.WriteMemory(object+20, []byte{2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(pixels, []byte{0x1f, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.refreshNativeBitmap(object); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.nativeImages[object]; ok {
		t.Fatal("guest-shaped IDIB remained bound to encoded BMP")
	}
	var pixel [2]byte
	if err := runtime.cpu.ReadMemory(pixels, pixel[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0x001f {
		t.Fatalf("guest-owned pixel=0x%04x, want blue", got)
	}
}

func TestDrawBitmapTreatsRGB565MagentaAsTransparent(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	source := image.NewRGBA(image.Rect(0, 0, 1, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, B: 255, A: 255})
	bitmap, err := runtime.createNativeBitmap(source)
	if err != nil {
		t.Fatal(err)
	}
	var blue [2]byte
	binary.LittleEndian.PutUint16(blue[:], 0x001f)
	if err := runtime.cpu.WriteMemory(framebufferBase, blue[:]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.drawBitmapAt(bitmap, 0, 0); err != nil {
		t.Fatal(err)
	}
	var got [2]byte
	if err := runtime.cpu.ReadMemory(framebufferBase, got[:]); err != nil {
		t.Fatal(err)
	}
	if got != blue {
		t.Fatalf("transparent magenta draw=%x, want background %x", got, blue)
	}
}

func TestImageSetParmTreatsFourthArgumentAsValue(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	bitmap := heapBase + 0x1000
	object := heapBase + 0x1100
	bitmapHeader := make([]byte, 36)
	binary.LittleEndian.PutUint32(bitmapHeader, bitmapVTable)
	if err := runtime.cpu.WriteMemory(bitmap, bitmapHeader); err != nil {
		t.Fatal(err)
	}
	imageObject := make([]byte, 8)
	binary.LittleEndian.PutUint32(imageObject, imageVTable)
	binary.LittleEndian.PutUint32(imageObject[4:], bitmap)
	if err := runtime.cpu.WriteMemory(object, imageObject); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterR1: 1, // IPARM_SIZE
		cpu.RegisterR2: 120,
		cpu.RegisterR3: 9, // height value, not a writable pointer
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.setImageParameter(); err != nil {
		t.Fatalf("SetParm with scalar p2 failed: %v", err)
	}
}
