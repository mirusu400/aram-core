package brewrt

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func callGraphicsForTest(t *testing.T, runtime *Runtime, slot uint32, r1, r2, r3 uint32) uint32 {
	t.Helper()
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: r1, cpu.RegisterR2: r2, cpu.RegisterR3: r3,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(graphicsTrapBase + slot*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IGraphics slot %d handled=%v err=%v", slot, handled, err)
	}
	value, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestGraphicsServiceDrawsAndPresentsGuestFramebuffer(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	out := heapBase + 0x3000
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: GraphicsClassID, cpu.RegisterR2: out,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(out, encoded); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded); got != graphicsObject {
		t.Fatalf("IGraphics object=0x%08x", got)
	}
	callGraphicsForTest(t, runtime, 4, 255, 0, 0)
	point := heapBase + 0x3010
	data := make([]byte, 4)
	binary.LittleEndian.PutUint16(data[0:], 2)
	binary.LittleEndian.PutUint16(data[2:], 3)
	if err := runtime.cpu.WriteMemory(point, data); err != nil {
		t.Fatal(err)
	}
	callGraphicsForTest(t, runtime, 20, point, 0, 0)
	callGraphicsForTest(t, runtime, 32, 0, 0, 0)
	pixel := make([]byte, 2)
	if err := runtime.cpu.ReadMemory(framebufferBase+(3*framebufferWidth+2)*2, pixel); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel); got != 0xf800 {
		t.Fatalf("IGraphics pixel=0x%04x", got)
	}
	if !runtime.guestFrame || runtime.updates != 1 {
		t.Fatalf("IGraphics presentation frame=%v updates=%d", runtime.guestFrame, runtime.updates)
	}
}

func TestGraphicsServiceDrawCircleHonorsFillAndStroke(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	callGraphicsForTest(t, runtime, 4, 255, 0, 0)
	callGraphicsForTest(t, runtime, 8, 0, 255, 0)
	callGraphicsForTest(t, runtime, 6, 1, 0, 0)
	circle := heapBase + 0x3010
	data := make([]byte, 6)
	binary.LittleEndian.PutUint16(data[0:], 10)
	binary.LittleEndian.PutUint16(data[2:], 12)
	binary.LittleEndian.PutUint16(data[4:], 3)
	if err := runtime.cpu.WriteMemory(circle, data); err != nil {
		t.Fatal(err)
	}
	callGraphicsForTest(t, runtime, 23, circle, 0, 0)

	readPixel := func(x, y uint32) uint16 {
		t.Helper()
		pixel := make([]byte, 2)
		if err := runtime.cpu.ReadMemory(framebufferBase+(y*framebufferWidth+x)*2, pixel); err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint16(pixel)
	}
	if got := readPixel(10, 12); got != 0x07e0 {
		t.Fatalf("circle center=0x%04x, want fill green", got)
	}
	if got := readPixel(13, 12); got != 0xf800 {
		t.Fatalf("circle edge=0x%04x, want stroke red", got)
	}
}

func TestGraphicsServiceDrawPolygonHonorsFillStrokeAndPackedLayout(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	callGraphicsForTest(t, runtime, 4, 255, 0, 0)
	callGraphicsForTest(t, runtime, 8, 0, 255, 0)
	callGraphicsForTest(t, runtime, 6, 1, 0, 0)

	polygon := heapBase + 0x3010
	points := heapBase + 0x3020
	header := make([]byte, 8)
	binary.LittleEndian.PutUint16(header, 3)
	binary.LittleEndian.PutUint32(header[4:], points)
	if err := runtime.cpu.WriteMemory(polygon, header); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 12)
	for index, coordinate := range []uint16{10, 10, 16, 10, 13, 16} {
		binary.LittleEndian.PutUint16(data[index*2:], coordinate)
	}
	if err := runtime.cpu.WriteMemory(points, data); err != nil {
		t.Fatal(err)
	}
	callGraphicsForTest(t, runtime, 28, polygon, 0, 0)

	readPixel := func(x, y uint32) uint16 {
		t.Helper()
		pixel := make([]byte, 2)
		if err := runtime.cpu.ReadMemory(framebufferBase+(y*framebufferWidth+x)*2, pixel); err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint16(pixel)
	}
	if got := readPixel(13, 12); got != 0x07e0 {
		t.Fatalf("polygon interior=0x%04x, want fill green", got)
	}
	if got := readPixel(10, 10); got != 0xf800 {
		t.Fatalf("polygon edge=0x%04x, want stroke red", got)
	}
}
