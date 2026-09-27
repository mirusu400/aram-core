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
	if err := runtime.cpu.ReadMemory(reallocated, flag); err != nil || flag[0] != 1 {
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

func TestSetupNativeImageUsesPalettelessCallerBackedRGB565(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	const width, height = uint32(2), uint32(2)
	const pixelOffset = uint32(54)
	const pitch = width * 2
	buffer, err := runtime.allocateGuest(pixelOffset + pitch*height)
	if err != nil {
		t.Fatal(err)
	}
	info, err := runtime.allocateGuest(10)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := runtime.allocateGuest(1)
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, pixelOffset)
	copy(header[:2], "BM")
	binary.LittleEndian.PutUint32(header[2:6], 0x1036) // This handset's stale BMP file size.
	binary.LittleEndian.PutUint32(header[10:14], pixelOffset)
	binary.LittleEndian.PutUint32(header[14:18], 40)
	binary.LittleEndian.PutUint32(header[18:22], width)
	binary.LittleEndian.PutUint32(header[22:26], height)
	binary.LittleEndian.PutUint16(header[26:28], 1)
	binary.LittleEndian.PutUint16(header[28:30], 8)
	if err := runtime.cpu.WriteMemory(buffer, header); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(owner, []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: buffer, cpu.RegisterR2: info, cpu.RegisterR3: owner,
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
		t.Fatalf("caller-backed bitmap=0x%08x err=%v", object, err)
	}
	bitmap := make([]byte, 36)
	if err := runtime.cpu.ReadMemory(object, bitmap); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(bitmap[8:12]); got != buffer+pixelOffset {
		t.Fatalf("bitmap pixels=0x%08x, want caller backing 0x%08x", got, buffer+pixelOffset)
	}
	if got := binary.LittleEndian.Uint16(bitmap[24:26]); got != uint16(pitch) || bitmap[28] != 16 || bitmap[29] != idibColorScheme565 {
		t.Fatalf("caller-backed bitmap format pitch=%d bits=%d scheme=%d", got, bitmap[28], bitmap[29])
	}
	var infoData [10]byte
	if err := runtime.cpu.ReadMemory(info, infoData[:]); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(infoData[:2]) != uint16(width) || binary.LittleEndian.Uint16(infoData[2:4]) != uint16(height) {
		t.Fatalf("image info=%x", infoData)
	}
	var ownership [1]byte
	if err := runtime.cpu.ReadMemory(owner, ownership[:]); err != nil || ownership[0] != 0 {
		t.Fatalf("ownership flag=%x err=%v", ownership, err)
	}
	if err := runtime.cpu.WriteMemory(buffer+pixelOffset, []byte{0x00, 0xf8}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.drawBitmapAt(object, 3, 4); err != nil {
		t.Fatal(err)
	}
	var pixel [2]byte
	if err := runtime.cpu.ReadMemory(framebufferBase+(4*framebufferWidth+3)*2, pixel[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0xf800 {
		t.Fatalf("displayed live caller pixel=0x%04x, want red", got)
	}
}

func TestSetupNativeImageRejectsPalettelessHeaderWithoutNativeBacking(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	buffer, err := runtime.allocateGuest(54)
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 54)
	copy(header[:2], "BM")
	binary.LittleEndian.PutUint32(header[10:14], 54)
	binary.LittleEndian.PutUint32(header[14:18], 40)
	binary.LittleEndian.PutUint32(header[18:22], 2)
	binary.LittleEndian.PutUint32(header[22:26], 2)
	binary.LittleEndian.PutUint16(header[26:28], 1)
	binary.LittleEndian.PutUint16(header[28:30], 8)
	if err := runtime.cpu.WriteMemory(buffer, header); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, buffer); err != nil {
		t.Fatal(err)
	}
	if err := runtime.setupNativeImage(); err != nil {
		t.Fatal(err)
	}
	if object, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || object != 0 {
		t.Fatalf("undersized caller-backed bitmap=0x%08x err=%v", object, err)
	}
}

func TestDisplayBitBltTransparentROPLeavesMagentaKeyOnBackground(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, B: 255, A: 255})
	source.SetRGBA(1, 0, color.RGBA{R: 255, A: 255})
	bitmap, err := runtime.createNativeBitmap(source)
	if err != nil {
		t.Fatal(err)
	}
	stack := stackBase + 0x400
	args := make([]byte, 20)
	binary.LittleEndian.PutUint32(args[0:], 1)
	binary.LittleEndian.PutUint32(args[4:], bitmap)
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: 3, cpu.RegisterR2: 4, cpu.RegisterR3: 2, cpu.RegisterSP: stack,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	frameAt := framebufferBase + (4*framebufferWidth+3)*2
	background := []byte{0x1f, 0x00, 0x1f, 0x00}
	if err := runtime.cpu.WriteMemory(frameAt, background); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(args[16:], aeeROTransparent)
	if err := runtime.cpu.WriteMemory(stack, args); err != nil {
		t.Fatal(err)
	}
	if err := runtime.blitDisplayBitmap(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(frameAt, got); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0x1f, 0x00, 0x00, 0xf8}; !bytes.Equal(got, want) {
		t.Fatalf("transparent BitBlt pixels=%x, want %x", got, want)
	}
	binary.LittleEndian.PutUint32(args[16:], 0) // AEE_RO_COPY
	if err := runtime.cpu.WriteMemory(stack, args); err != nil {
		t.Fatal(err)
	}
	if err := runtime.blitDisplayBitmap(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.ReadMemory(frameAt, got); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0x1f, 0xf8, 0x00, 0xf8}; !bytes.Equal(got, want) {
		t.Fatalf("copy BitBlt pixels=%x, want %x", got, want)
	}
}

func TestDisplayBitBltAcceptsGuestIndexedBMPAtlas(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	palette := color.Palette{
		color.RGBA{A: 255},
		color.RGBA{G: 255, A: 255},
		color.RGBA{R: 255, A: 255},
		color.RGBA{R: 255, B: 255, A: 255},
	}
	source := image.NewPaletted(image.Rect(0, 0, 32, 16), palette)
	for index, value := range []uint8{1, 2, 3} {
		source.SetColorIndex(16+index, 3, value)
	}
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	if depth := binary.LittleEndian.Uint16(encoded.Bytes()[28:30]); depth != 8 {
		t.Fatalf("atlas BMP depth = %d, want 8", depth)
	}
	bitmap, stack := heapBase+0x1000, stackBase+0x400
	if err := runtime.cpu.WriteMemory(bitmap, encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	args := make([]byte, 20)
	binary.LittleEndian.PutUint32(args[0:], 1)
	binary.LittleEndian.PutUint32(args[4:], bitmap)
	binary.LittleEndian.PutUint32(args[8:], 16)
	binary.LittleEndian.PutUint32(args[12:], 3)
	binary.LittleEndian.PutUint32(args[16:], 4)
	if err := runtime.cpu.WriteMemory(stack, args); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: 4, cpu.RegisterR2: 5, cpu.RegisterR3: 3, cpu.RegisterSP: stack,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.blitDisplayBitmap(); err != nil {
		t.Fatal(err)
	}
	frameAt := framebufferBase + (5*framebufferWidth+4)*2
	got := make([]byte, 6)
	if err := runtime.cpu.ReadMemory(frameAt, got); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xe0, 0x07, 0x00, 0xf8, 0x1f, 0xf8}; !bytes.Equal(got, want) {
		t.Fatalf("indexed BMP BitBlt pixels=%x, want %x", got, want)
	}
	blue := []byte{0x1f, 0x00}
	if err := runtime.cpu.WriteMemory(frameAt, blue); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(args[8:], 18)
	binary.LittleEndian.PutUint32(args[16:], aeeROTransparent)
	if err := runtime.cpu.WriteMemory(stack, args); err != nil {
		t.Fatal(err)
	}
	if err := runtime.blitDisplayBitmap(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.ReadMemory(frameAt, got[:2]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:2], blue) {
		t.Fatalf("transparent indexed BMP changed blue background to %x", got[:2])
	}
}

func TestSetupNativeImageReusesLiveFullscreenStagingBitmap(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	encode := func(fill color.RGBA) []byte {
		source := image.NewRGBA(image.Rect(0, 0, int(framebufferWidth), int(framebufferHeight)))
		for y := 0; y < int(framebufferHeight); y++ {
			for x := 0; x < int(framebufferWidth); x++ {
				source.SetRGBA(x, y, fill)
			}
		}
		var output bytes.Buffer
		if err := bmp.Encode(&output, source); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	red := encode(color.RGBA{R: 255, A: 255})
	blue := encode(color.RGBA{B: 255, A: 255})
	if len(red) != len(blue) {
		t.Fatal("BMP frame lengths differ")
	}
	buffer, err := runtime.allocateGuest(uint32(len(red)))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, buffer); err != nil {
		t.Fatal(err)
	}
	var first, highWater uint32
	for iteration := 0; iteration < 250; iteration++ {
		frame := red
		if iteration%2 != 0 {
			frame = blue
		}
		if err := runtime.cpu.WriteMemory(buffer, frame); err != nil {
			t.Fatal(err)
		}
		if err := runtime.setupNativeImage(); err != nil {
			t.Fatal(err)
		}
		object, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			first, highWater = object, runtime.heapNext
		} else if object != first || runtime.heapNext != highWater {
			t.Fatalf("iteration %d: object=0x%x heap=0x%x, want object=0x%x heap=0x%x", iteration, object, runtime.heapNext, first, highWater)
		}
	}
	var header [12]byte
	if err := runtime.cpu.ReadMemory(first, header[:]); err != nil {
		t.Fatal(err)
	}
	var pixel [2]byte
	if err := runtime.cpu.ReadMemory(binary.LittleEndian.Uint32(header[8:12]), pixel[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0x001f {
		t.Fatalf("last staging pixel=0x%04x, want blue", got)
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
