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

func createSyntheticMemAStream(t *testing.T, runtime *Runtime) uint32 {
	t.Helper()
	out := stackBase + 0x200
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: MemAStreamClassID,
		cpu.RegisterR2: out,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 0 {
		t.Fatalf("IMemAStream CreateInstance status=%d err=%v", got, err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	object := binary.LittleEndian.Uint32(encoded[:])
	if object == 0 {
		t.Fatal("IMemAStream CreateInstance returned null")
	}
	if err := runtime.cpu.ReadMemory(object, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != memAStreamVTable {
		t.Fatalf("IMemAStream vtable=0x%08x, want 0x%08x", got, memAStreamVTable)
	}
	return object
}

func TestMemAStreamSetReadAndRelease(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	object := createSyntheticMemAStream(t, runtime)
	buffer, err := runtime.allocateGuest(6)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(buffer, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	stack := stackBase + 0x300
	if err := runtime.cpu.WriteMemory(stack, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterR1: buffer,
		cpu.RegisterR2: 6,
		cpu.RegisterR3: 2,
		cpu.RegisterSP: stack,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.handleMemAStream(5); err != nil {
		t.Fatal(err)
	}

	destination := stackBase + 0x340
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterR1: destination,
		cpu.RegisterR2: 3,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.handleMemAStream(3); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 3 {
		t.Fatalf("first IMemAStream read=%d err=%v, want 3", got, err)
	}
	data := make([]byte, 3)
	if err := runtime.cpu.ReadMemory(destination, data); err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "cde" {
		t.Fatalf("first IMemAStream data=%q, want cde", got)
	}

	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, object); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 3); err != nil {
		t.Fatal(err)
	}
	if err := runtime.handleMemAStream(3); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 1 {
		t.Fatalf("second IMemAStream read=%d err=%v, want 1", got, err)
	}

	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, object); err != nil {
		t.Fatal(err)
	}
	if err := runtime.handleMemAStream(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.memAStreams[object]; ok {
		t.Fatal("released IMemAStream remains active")
	}
	if _, ok := runtime.heapAllocated[object]; ok {
		t.Fatal("released IMemAStream object remains allocated")
	}
	if _, ok := runtime.heapAllocated[buffer]; ok {
		t.Fatal("IMemAStream Set buffer was not released with the stream")
	}
}

func TestMemAStreamSetExSchedulesUserFreeCallback(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	object := createSyntheticMemAStream(t, runtime)
	buffer, err := runtime.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	stack := stackBase + 0x380
	var arguments [8]byte
	binary.LittleEndian.PutUint32(arguments[0:4], returnTrap|1)
	binary.LittleEndian.PutUint32(arguments[4:8], 0x12345678)
	if err := runtime.cpu.WriteMemory(stack, arguments[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterR1: buffer,
		cpu.RegisterR2: 8,
		cpu.RegisterR3: 0,
		cpu.RegisterSP: stack,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.handleMemAStream(6); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, object); err != nil {
		t.Fatal(err)
	}
	if err := runtime.handleMemAStream(1); err != nil {
		t.Fatal(err)
	}
	if len(runtime.cleanupCallbacks) != 1 {
		t.Fatalf("SetEx free callback count=%d, want 1", len(runtime.cleanupCallbacks))
	}
	if got := runtime.cleanupCallbacks[0]; got.function != returnTrap|1 || got.context != 0x12345678 {
		t.Fatalf("SetEx free callback=%+v", got)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0x12345678); err != nil {
		t.Fatal(err)
	}
	if handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 12*2 + 2); err != nil || !handled {
		t.Fatalf("CancelTimer handled=%v err=%v", handled, err)
	}
	if len(runtime.cleanupCallbacks) != 1 {
		t.Fatal("CancelTimer suppressed the SetEx ownership callback")
	}
	if _, ok := runtime.heapAllocated[buffer]; !ok {
		t.Fatal("SetEx user-owned buffer was freed before its callback")
	}
}

func TestMemAStreamReadBoundsGuestControlledCount(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	object := createSyntheticMemAStream(t, runtime)
	source, err := runtime.allocateGuest(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := runtime.allocateGuest(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	stream := runtime.memAStreams[object]
	stream.buffer = source
	stream.size = ^uint32(0)
	runtime.memAStreams[object] = stream
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterR1: destination,
		cpu.RegisterR2: ^uint32(0),
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.handleMemAStream(3); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 64<<10 {
		t.Fatalf("bounded IMemAStream read=%d err=%v, want %d", got, err, 64<<10)
	}
}

func TestNativeBitmapRejectsUnrepresentableGeometry(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	if _, err := runtime.createNativeBitmap(image.NewRGBA(image.Rect(0, 0, 65536, 1))); err == nil {
		t.Fatal("createNativeBitmap accepted width that truncates in the BREW uint16 header")
	}
}

func TestWinBMPDecodesAndRetainsMemAStream(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	streamObject := createSyntheticMemAStream(t, runtime)
	source := image.NewRGBA(image.Rect(0, 0, 2, 3))
	source.SetRGBA(1, 2, color.RGBA{R: 0xff, G: 0x80, B: 0x20, A: 0xff})
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
	stack := stackBase + 0x3c0
	if err := runtime.cpu.WriteMemory(stack, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: streamObject,
		cpu.RegisterR1: buffer,
		cpu.RegisterR2: uint32(encoded.Len()),
		cpu.RegisterR3: 0,
		cpu.RegisterSP: stack,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.handleMemAStream(5); err != nil {
		t.Fatal(err)
	}

	imageObject, err := runtime.createWinBMPImage()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, imageObject); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, streamObject); err != nil {
		t.Fatal(err)
	}
	if err := runtime.setImageStream(); err != nil {
		t.Fatal(err)
	}
	bitmap, err := runtime.imageBitmap(imageObject)
	if err != nil {
		t.Fatal(err)
	}
	var header [24]byte
	if err := runtime.cpu.ReadMemory(bitmap, header[:]); err != nil {
		t.Fatal(err)
	}
	if width, height := binary.LittleEndian.Uint16(header[20:22]), binary.LittleEndian.Uint16(header[22:24]); width != 2 || height != 3 {
		t.Fatalf("decoded WinBMP size=%dx%d, want 2x3", width, height)
	}
	if got := runtime.memAStreams[streamObject].refs; got != 2 {
		t.Fatalf("retained stream refs=%d, want 2", got)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, imageObject); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, streamObject); err != nil {
		t.Fatal(err)
	}
	if err := runtime.setImageStream(); err != nil {
		t.Fatal(err)
	}
	if got := runtime.memAStreams[streamObject].refs; got != 2 {
		t.Fatalf("repeated SetStream refs=%d, want 2", got)
	}
	if got := runtime.releaseMemAStream(streamObject); got != 1 {
		t.Fatalf("guest stream release refs=%d, want 1", got)
	}
	runtime.releaseInterfaceObject(imageObject)
	if _, ok := runtime.memAStreams[streamObject]; ok {
		t.Fatal("image release did not release retained stream")
	}
	if _, ok := runtime.heapAllocated[buffer]; ok {
		t.Fatal("final stream release did not free Set buffer")
	}
}
