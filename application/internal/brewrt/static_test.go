package brewrt

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
)

func setStaticRegisters(t *testing.T, runtime *Runtime, values map[uint32]uint32) {
	t.Helper()
	for register, value := range values {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
}

func writeStaticAECHAR(t *testing.T, runtime *Runtime, text string) uint32 {
	t.Helper()
	units := append(utf16.Encode([]rune(text)), 0)
	address, err := runtime.allocateGuest(uint32(len(units) * 2))
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	if err := runtime.cpu.WriteMemory(address, encoded); err != nil {
		t.Fatal(err)
	}
	return address
}

func callStatic(t *testing.T, runtime *Runtime, object, slot uint32, values map[uint32]uint32) uint32 {
	t.Helper()
	values[cpu.RegisterR0] = object
	setStaticRegisters(t, runtime, values)
	handled, err := runtime.handleStaticControl(slot)
	if err != nil {
		t.Fatalf("IStatic slot %d: %v", slot, err)
	}
	if !handled {
		t.Fatalf("IStatic slot %d was not handled", slot)
	}
	result, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// 스케이드보드 creates an IStatic for its ranking-server message box and calls
// SetRect on the result without checking it. CreateInstance used to answer
// AEE_ECLASSNOTSUPPORT with a null object, and the title jumped to address 0.
func TestStaticControlCreateDrawAndRelease(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	out := stackBase + 0x200
	setStaticRegisters(t, runtime, map[uint32]uint32{
		cpu.RegisterR1: StaticClassID,
		cpu.RegisterR2: out,
	})
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	if status, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || status != 0 {
		t.Fatalf("IStatic CreateInstance status=%d err=%v", status, err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	object := binary.LittleEndian.Uint32(encoded[:])
	if object == 0 {
		t.Fatal("IStatic CreateInstance returned null")
	}
	if err := runtime.cpu.ReadMemory(object, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if vtable := binary.LittleEndian.Uint32(encoded[:]); vtable != staticCtlVTable {
		t.Fatalf("IStatic vtable=0x%08x, want 0x%08x", vtable, staticCtlVTable)
	}
	for _, slot := range []uint32{0, 1, 6, 11} {
		if err := runtime.cpu.ReadMemory(staticCtlVTable+slot*4, encoded[:]); err != nil {
			t.Fatal(err)
		}
		if got, want := binary.LittleEndian.Uint32(encoded[:]), (staticCtlTrapBase+slot*2)|1; got != want {
			t.Fatalf("IStatic vtable slot %d = 0x%08x, want 0x%08x", slot, got, want)
		}
	}

	// A white box the title framed itself, as 스케이드보드 does before Redraw.
	pixels := make([]byte, runtime.screenBytes())
	for index := 0; index < len(pixels); index += 2 {
		binary.LittleEndian.PutUint16(pixels[index:], 0xffff)
	}
	if err := runtime.cpu.WriteMemory(framebufferBase, pixels); err != nil {
		t.Fatal(err)
	}
	rect, err := runtime.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	var rectBytes [8]byte
	binary.LittleEndian.PutUint16(rectBytes[0:], 10)
	binary.LittleEndian.PutUint16(rectBytes[2:], 20)
	binary.LittleEndian.PutUint16(rectBytes[4:], 100)
	binary.LittleEndian.PutUint16(rectBytes[6:], 60)
	if err := runtime.cpu.WriteMemory(rect, rectBytes[:]); err != nil {
		t.Fatal(err)
	}
	callStatic(t, runtime, object, 6, map[uint32]uint32{cpu.RegisterR1: rect})
	callStatic(t, runtime, object, 8, map[uint32]uint32{
		cpu.RegisterR1: staticCenterText | staticCenterTitle | staticMiddleText,
	})
	if got := callStatic(t, runtime, object, 9, map[uint32]uint32{}); got != 0x00230000 {
		t.Fatalf("GetProperties = 0x%08x", got)
	}
	title := writeStaticAECHAR(t, runtime, "")
	text := writeStaticAECHAR(t, runtime, "Connection... Please Wait...")
	if got := callStatic(t, runtime, object, 11, map[uint32]uint32{
		cpu.RegisterR1: title,
		cpu.RegisterR2: text,
		cpu.RegisterR3: 0x8001,
	}); got != 1 {
		t.Fatalf("SetText = %d, want TRUE", got)
	}
	// The title frees both strings before it calls Redraw.
	if err := runtime.cpu.WriteMemory(text, make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	if got := callStatic(t, runtime, object, 3, map[uint32]uint32{}); got != 1 {
		t.Fatalf("Redraw = %d, want TRUE", got)
	}
	if err := runtime.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		t.Fatal(err)
	}
	inside, outside := 0, 0
	for y := 0; y < int(runtime.screenHeight); y++ {
		for x := 0; x < int(runtime.screenWidth); x++ {
			if binary.LittleEndian.Uint16(pixels[(y*int(runtime.screenWidth)+x)*2:]) == 0xffff {
				continue
			}
			if x >= 10 && x < 110 && y >= 20 && y < 80 {
				inside++
			} else {
				outside++
			}
		}
	}
	if inside == 0 {
		t.Fatal("Redraw drew no text inside the control rectangle")
	}
	if outside != 0 {
		t.Fatalf("Redraw drew %d pixels outside the control rectangle", outside)
	}

	setStaticRegisters(t, runtime, map[uint32]uint32{cpu.RegisterR0: object})
	if handled, err := runtime.handleStaticControl(12); err != nil || handled {
		t.Fatalf("unmodeled IStatic slot 12 handled=%t err=%v, want a boundary", handled, err)
	}
	if refs := callStatic(t, runtime, object, 1, map[uint32]uint32{}); refs != 0 {
		t.Fatalf("Release left %d references", refs)
	}
	if _, active := runtime.staticControls[object]; active {
		t.Fatal("released IStatic is still active")
	}
}

func TestStaticControlWrapsLongText(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	lines, err := runtime.wrapStaticText("one two three four", 7*9)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "one two" || lines[1] != "three" || lines[2] != "four" {
		t.Fatalf("wrapped %q", lines)
	}
	lines, err = runtime.wrapStaticText("abcdefghij", 7*4)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "abcd" || lines[2] != "ij" {
		t.Fatalf("wrapped unbroken run %q", lines)
	}
}
