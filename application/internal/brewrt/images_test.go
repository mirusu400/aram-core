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
	buffer, info, ownership := heapBase+0x1000, heapBase+0x2000, heapBase+0x2100
	if err := runtime.cpu.WriteMemory(buffer, encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: buffer, cpu.RegisterR2: info, cpu.RegisterR3: ownership,
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
	flag := []byte{0}
	if err := runtime.cpu.ReadMemory(ownership, flag); err != nil || flag[0] != 1 {
		t.Fatalf("ownership=%v err=%v", flag, err)
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
