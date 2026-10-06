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

func TestNativeImageOwnershipFlagDetachesBorrowedPixelsBeforeSourceFree(t *testing.T) {
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
	if pixels < buffer || pixels >= buffer+bufferSize {
		t.Fatal("native bitmap did not expose the caller's live RGB565 backing")
	}
	var green [2]byte
	binary.LittleEndian.PutUint16(green[:], 0x07e0)
	if err := runtime.cpu.WriteMemory(pixels, green[:]); err != nil {
		t.Fatal(err)
	}
	// This is the documented caller path after CONVERTBMP reports realloc:
	// free and reuse the original buffer while retaining the native bitmap.
	runtime.releaseGuest(buffer)
	if err := runtime.cpu.ReadMemory(object, header[:]); err != nil {
		t.Fatal(err)
	}
	pixels = binary.LittleEndian.Uint32(header[8:12])
	if pixels >= buffer && pixels < buffer+bufferSize {
		t.Fatal("native bitmap still borrows the released source allocation")
	}
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
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0x07e0 {
		t.Fatalf("native pixel after source reuse=%04x, want green", got)
	}
	runtime.releaseInterfaceObject(object)
	if _, live := runtime.heapAllocated[object]; live {
		t.Fatal("native bitmap release retained its owned allocation")
	}
	if _, live := runtime.heapAllocated[reused]; !live {
		t.Fatal("native bitmap release freed the caller's reused allocation")
	}
}

func TestMenuRetainsImageThroughCallerReleaseAndStringAllocation(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	bitmap, err := runtime.createNativeBitmap(image.NewRGBA(image.Rect(0, 0, 2, 3)))
	if err != nil {
		t.Fatal(err)
	}
	object, err := runtime.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper [8]byte
	binary.LittleEndian.PutUint32(wrapper[:4], imageVTable)
	binary.LittleEndian.PutUint32(wrapper[4:], bitmap)
	if err := runtime.cpu.WriteMemory(object, wrapper[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.allocateGuest(32); err != nil {
		t.Fatal(err)
	}

	var item [28]byte
	binary.LittleEndian.PutUint32(item[4:8], object)
	binary.LittleEndian.PutUint16(item[22:24], 7)
	if err := runtime.cpu.WriteMemory(stackBase+0x200, item[:]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, stackBase+0x200); err != nil {
		t.Fatal(err)
	}
	if handled, err := runtime.handleMenuControl(13); !handled || err != nil {
		t.Fatalf("AddItemEx handled=%v err=%v", handled, err)
	}
	if got := runtime.interfaceRefs[object]; got != 2 {
		t.Fatalf("image references after AddItemEx = %d, want 2", got)
	}
	if got := runtime.releaseInterfaceObject(object); got != 1 {
		t.Fatalf("caller image Release = %d, want 1", got)
	}
	if _, err := runtime.allocateGuest(16); err != nil {
		t.Fatal(err)
	}
	text, err := runtime.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(text, []byte{0x54, 0xcc, 0x44, 0xbe, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if text == object {
		t.Fatal("wide string reused a menu-owned image")
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterR1: stackBase + 0x100,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.returnImageInfo(); err != nil {
		t.Fatalf("GetInfo on menu-owned image: %v", err)
	}
	var info [4]byte
	if err := runtime.cpu.ReadMemory(stackBase+0x100, info[:]); err != nil {
		t.Fatal(err)
	}
	if width, height := binary.LittleEndian.Uint16(info[:2]), binary.LittleEndian.Uint16(info[2:]); width != 2 || height != 3 {
		t.Fatalf("image info = %dx%d, want 2x3", width, height)
	}

	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 7); err != nil {
		t.Fatal(err)
	}
	if handled, err := runtime.handleMenuControl(15); !handled || err != nil {
		t.Fatalf("DeleteItem handled=%v err=%v", handled, err)
	}
	if _, live := runtime.heapAllocated[object]; live {
		t.Fatal("deleted menu item retained its image")
	}
	if _, live := runtime.heapAllocated[bitmap]; live {
		t.Fatal("deleted menu item retained its image bitmap")
	}
}

func TestInterfaceAddRefRetainsObjectUntilFinalRelease(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	object, err := runtime.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.retainInterfaceObject(object); got != 2 {
		t.Fatalf("AddRef = %d, want 2", got)
	}
	if got := runtime.releaseInterfaceObject(object); got != 1 {
		t.Fatalf("first Release = %d, want 1", got)
	}
	if _, live := runtime.heapAllocated[object]; !live {
		t.Fatal("first Release freed an interface with another reference")
	}
	if got := runtime.releaseInterfaceObject(object); got != 0 {
		t.Fatalf("final Release = %d, want 0", got)
	}
	if _, live := runtime.heapAllocated[object]; live {
		t.Fatal("final Release retained the interface")
	}
}

func TestMenuImageReplacementAndClearReleaseReferences(t *testing.T) {
	for _, clearSlot := range []uint32{10, 16} {
		t.Run(map[uint32]string{10: "Reset", 16: "DeleteAll"}[clearSlot], func(t *testing.T) {
			runtime := newSyntheticRuntime(t)
			newImage := func() uint32 {
				object, err := runtime.allocateGuest(8)
				if err != nil {
					t.Fatal(err)
				}
				var wrapper [8]byte
				binary.LittleEndian.PutUint32(wrapper[:4], imageVTable)
				if err := runtime.cpu.WriteMemory(object, wrapper[:]); err != nil {
					t.Fatal(err)
				}
				return object
			}
			first := newImage()
			runtime.setMenuItem(7, 0, first)
			runtime.releaseInterfaceObject(first)
			second := newImage()
			runtime.setMenuItem(7, 0, second)
			if _, live := runtime.heapAllocated[first]; live {
				t.Fatal("replaced menu image remained allocated")
			}
			runtime.releaseInterfaceObject(second)
			if handled, err := runtime.handleMenuControl(clearSlot); !handled || err != nil {
				t.Fatalf("clear slot %d handled=%v err=%v", clearSlot, handled, err)
			}
			if _, live := runtime.heapAllocated[second]; live {
				t.Fatal("cleared menu image remained allocated")
			}
		})
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
