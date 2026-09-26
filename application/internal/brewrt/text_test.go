package brewrt

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestDisplayDrawTextRendersOEMFallback(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.displayColorSet[displayColorText] = true
	runtime.displayColors[displayColorText] = 0x01010100 // MAKE_RGB(1,1,1), still black in RGB565.
	textAt := heapBase + 0x200
	stackAt := heapBase + 0x300
	if err := runtime.cpu.WriteMemory(textAt, []byte("ARAM\x00")); err != nil {
		t.Fatal(err)
	}
	var arguments [16]byte
	binary.LittleEndian.PutUint32(arguments[0:4], 2)
	binary.LittleEndian.PutUint32(arguments[4:8], 3)
	binary.LittleEndian.PutUint32(arguments[12:16], displayTextFormatOEM)
	if err := runtime.cpu.WriteMemory(stackAt, arguments[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: displayObject,
		cpu.RegisterR1: 0x8001,
		cpu.RegisterR2: textAt,
		cpu.RegisterR3: ^uint32(0),
		cpu.RegisterSP: stackAt,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(displayTrapBase + 4*2 + 2)
	if err != nil || !handled {
		t.Fatalf("DrawText handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 0 {
		t.Fatalf("DrawText result=%d err=%v, want SUCCESS", got, err)
	}
	pixels := make([]byte, framebufferBytes)
	if err := runtime.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		t.Fatal(err)
	}
	nonzero := 0
	for _, value := range pixels {
		if value != 0 {
			nonzero++
		}
	}
	if nonzero == 0 {
		t.Fatal("DrawText did not rasterize any framebuffer pixels")
	}
}

func TestDisplaySetColorRGBNoneOnlyQueries(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: displayColorText,
		cpu.RegisterR2: ^uint32(0),
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(displayTrapBase + 10*2 + 2)
	if err != nil || !handled {
		t.Fatalf("SetColor RGB_NONE handled=%v err=%v", handled, err)
	}
	if runtime.displayColorSet[displayColorText] {
		t.Fatal("SetColor RGB_NONE incorrectly marked the active text color as selected")
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 0 {
		t.Fatalf("SetColor RGB_NONE returned 0x%08x err=%v, want default color", got, err)
	}
}

func TestDisplayRectangleUsesBREWRGBVALLayout(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	if err := runtime.fillDisplayRectangle(0, 0xffffff00); err != nil { // MAKE_RGB(255,255,255)
		t.Fatal(err)
	}
	pixel := make([]byte, 2)
	if err := runtime.cpu.ReadMemory(framebufferBase, pixel); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel); got != 0xffff {
		t.Fatalf("DrawRect RGB565 pixel = 0x%04x, want white", got)
	}
}

func TestDisplayDrawRectHonorsFrameAndFillFlags(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	rectAt, stackAt := heapBase+0x200, heapBase+0x300
	var rect [8]byte
	for index, value := range []uint16{1, 1, 3, 3} {
		binary.LittleEndian.PutUint16(rect[index*2:], value)
	}
	if err := runtime.cpu.WriteMemory(rectAt, rect[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: rectAt,
		cpu.RegisterR2: 0x0000ff00, // red frame
		cpu.RegisterR3: 0xff000000, // blue fill
		cpu.RegisterSP: stackAt,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	pixel := func(x, y uint32) uint16 {
		t.Helper()
		var encoded [2]byte
		if err := runtime.cpu.ReadMemory(framebufferBase+(y*framebufferWidth+x)*2, encoded[:]); err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint16(encoded[:])
	}
	for _, test := range []struct {
		flags uint32
		border, center uint16
	}{
		{1, 0xf800, 0},      // IDF_RECT_FRAME leaves the interior untouched.
		{2, 0x001f, 0x001f}, // IDF_RECT_FILL covers the whole rectangle.
		{3, 0xf800, 0x001f}, // Both flags fill the interior and retain the frame.
	} {
		var flags [4]byte
		binary.LittleEndian.PutUint32(flags[:], test.flags)
		if err := runtime.cpu.WriteMemory(stackAt, flags[:]); err != nil {
			t.Fatal(err)
		}
		if err := runtime.drawDisplayRect(); err != nil {
			t.Fatal(err)
		}
		if got := pixel(1, 1); got != test.border {
			t.Fatalf("flags=%d frame pixel=0x%04x, want 0x%04x", test.flags, got, test.border)
		}
		if got := pixel(2, 2); got != test.center {
			t.Fatalf("flags=%d center pixel=0x%04x, want 0x%04x", test.flags, got, test.center)
		}
	}
}

func TestDisplayTextDecodesOEMAndMeasuresHangulGlyph(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt := heapBase + 0x200
	if err := runtime.cpu.WriteMemory(textAt, []byte{0xb0, 0xa1, 0}); err != nil {
		t.Fatal(err)
	}
	text, err := runtime.displayText(textAt, ^uint32(0), true)
	if err != nil {
		t.Fatal(err)
	}
	if text != "\uac00" {
		t.Fatalf("decoded OEM text=%q, want U+AC00", text)
	}
	if got, err := runtime.displayTextWidth(text); err != nil || got <= 7 {
		t.Fatalf("Hangul display width=%d err=%v, want wider than ASCII fallback", got, err)
	}
}

func TestDecodeBREWAECHARPackedKoreanAndUnicode(t *testing.T) {
	for _, test := range []struct {
		units []uint16
		want  string
	}{
		{[]uint16{0xccc0, 0xeebe, 0xcfc7, 0xe2b1}, "이어하기"},
		{[]uint16{0x20, 0xcfa8, 'N', 'C', 'S', 'O', 'F', 'T'}, " ⓒNCSOFT"},
		{[]uint16{0xac00, 0xb098, 0xb2e4}, "가나다"},
		{[]uint16{'A', 'R', 'A', 'M'}, "ARAM"},
	} {
		if got := decodeBREWAECHAR(test.units); got != test.want {
			t.Fatalf("decodeBREWAECHAR(%x) = %q, want %q", test.units, got, test.want)
		}
	}
}

func TestDisplayDrawTextRasterizesHangulBeyondQuestionMarkCell(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt := heapBase + 0x200
	stackAt := heapBase + 0x300
	if err := runtime.cpu.WriteMemory(textAt, []byte{0xb0, 0xa1, 0}); err != nil {
		t.Fatal(err)
	}
	var arguments [16]byte
	binary.LittleEndian.PutUint32(arguments[12:16], displayTextFormatOEM)
	if err := runtime.cpu.WriteMemory(stackAt, arguments[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR2: textAt,
		cpu.RegisterR3: ^uint32(0),
		cpu.RegisterSP: stackAt,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.drawDisplayText(); err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, framebufferBytes)
	if err := runtime.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		t.Fatal(err)
	}
	paintedBeyondASCII := false
	for y := uint32(0); y < 13; y++ {
		for x := uint32(7); x < 13; x++ {
			if binary.LittleEndian.Uint16(pixels[(y*framebufferWidth+x)*2:]) != 0 {
				paintedBeyondASCII = true
			}
		}
	}
	if !paintedBeyondASCII {
		t.Fatal("Hangul DrawText remained confined to the seven-pixel question-mark cell")
	}
}

func TestDisplayDrawTextHonorsClippingRectangle(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt := heapBase + 0x200
	clipAt := heapBase + 0x280
	stackAt := heapBase + 0x300
	if err := runtime.cpu.WriteMemory(textAt, []byte("ARAM\x00")); err != nil {
		t.Fatal(err)
	}
	var clip [8]byte
	binary.LittleEndian.PutUint16(clip[0:2], 30)
	binary.LittleEndian.PutUint16(clip[2:4], 20)
	binary.LittleEndian.PutUint16(clip[4:6], 10)
	binary.LittleEndian.PutUint16(clip[6:8], 10)
	if err := runtime.cpu.WriteMemory(clipAt, clip[:]); err != nil {
		t.Fatal(err)
	}
	var arguments [16]byte
	binary.LittleEndian.PutUint32(arguments[0:4], 2)
	binary.LittleEndian.PutUint32(arguments[4:8], 3)
	binary.LittleEndian.PutUint32(arguments[8:12], clipAt)
	binary.LittleEndian.PutUint32(arguments[12:16], displayTextFormatOEM)
	if err := runtime.cpu.WriteMemory(stackAt, arguments[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR2: textAt,
		cpu.RegisterR3: ^uint32(0),
		cpu.RegisterSP: stackAt,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.drawDisplayText(); err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, framebufferBytes)
	if err := runtime.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		t.Fatal(err)
	}
	for _, value := range pixels {
		if value != 0 {
			t.Fatal("DrawText rendered outside a disjoint clipping rectangle")
		}
	}
}

func TestDisplayTextRejectsOversizedCounts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	for _, oem := range []bool{false, true} {
		if _, err := runtime.displayText(heapBase, 4097, oem); err == nil {
			t.Fatalf("displayText(oem=%v) accepted an oversized count", oem)
		}
	}
}

func TestDisplayTextExplicitUnicodeAndOEMCounts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt := heapBase + 0x200
	if err := runtime.cpu.WriteMemory(textAt, []byte{'A', 0, 'B', 0}); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.displayText(textAt, 2, false); err != nil || got != "AB" {
		t.Fatalf("explicit Unicode text=%q err=%v, want AB", got, err)
	}
	if err := runtime.cpu.WriteMemory(textAt, []byte{0xb0, 0xa1}); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.displayText(textAt, 2, true); err != nil || got != "가" {
		t.Fatalf("explicit OEM text=%q err=%v, want 가", got, err)
	}
}

func TestDisplayTextRejectsNegativeCountsOtherThanMinusOne(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	for _, oem := range []bool{false, true} {
		if _, err := runtime.displayText(heapBase, ^uint32(1), oem); err == nil {
			t.Fatalf("displayText(oem=%v) accepted count -2", oem)
		}
	}
}

func TestDisplayDrawTextHugeCoordinatesDoNotWrapOnscreen(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt := heapBase + 0x200
	stackAt := heapBase + 0x300
	if err := runtime.cpu.WriteMemory(textAt, []byte{'A', 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var arguments [16]byte
	binary.LittleEndian.PutUint32(arguments[0:4], 1<<26)
	binary.LittleEndian.PutUint32(arguments[4:8], 1<<26)
	if err := runtime.cpu.WriteMemory(stackAt, arguments[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR2: textAt,
		cpu.RegisterR3: 1,
		cpu.RegisterSP: stackAt,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.drawDisplayText(); err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, framebufferBytes)
	if err := runtime.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		t.Fatal(err)
	}
	for _, value := range pixels {
		if value != 0 {
			t.Fatal("huge DrawText coordinates wrapped onto the framebuffer")
		}
	}
}

func TestDisplayDrawTextUsesRGBVALAndLeftTopAlignment(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt := heapBase + 0x200
	clipAt := heapBase + 0x280
	stackAt := heapBase + 0x300
	if err := runtime.cpu.WriteMemory(textAt, []byte{'A', 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var clip [8]byte
	binary.LittleEndian.PutUint16(clip[0:2], 30)
	binary.LittleEndian.PutUint16(clip[2:4], 20)
	binary.LittleEndian.PutUint16(clip[4:6], 20)
	binary.LittleEndian.PutUint16(clip[6:8], 20)
	if err := runtime.cpu.WriteMemory(clipAt, clip[:]); err != nil {
		t.Fatal(err)
	}
	var arguments [16]byte
	binary.LittleEndian.PutUint32(arguments[0:4], 1<<26)
	binary.LittleEndian.PutUint32(arguments[4:8], 1<<26)
	binary.LittleEndian.PutUint32(arguments[8:12], clipAt)
	binary.LittleEndian.PutUint32(arguments[12:16], displayAlignLeft|displayAlignTop)
	if err := runtime.cpu.WriteMemory(stackAt, arguments[:]); err != nil {
		t.Fatal(err)
	}
	runtime.displayColors[displayColorText] = 0x0000ff00 // MAKE_RGB(255, 0, 0)
	runtime.displayColorSet[displayColorText] = true
	for register, value := range map[uint32]uint32{
		cpu.RegisterR2: textAt,
		cpu.RegisterR3: 1,
		cpu.RegisterSP: stackAt,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.drawDisplayText(); err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, framebufferBytes)
	if err := runtime.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		t.Fatal(err)
	}
	painted := 0
	for py := uint32(0); py < framebufferHeight; py++ {
		for px := uint32(0); px < framebufferWidth; px++ {
			native := binary.LittleEndian.Uint16(pixels[(py*framebufferWidth+px)*2:])
			if native == 0 {
				continue
			}
			painted++
			if px < 30 || px >= 50 || py < 20 || py >= 40 {
				t.Fatalf("aligned text pixel (%d,%d) escaped clipping rectangle", px, py)
			}
			if native != 0xf800 {
				t.Fatalf("aligned text pixel = 0x%04x, want RGB565 red", native)
			}
		}
	}
	if painted == 0 {
		t.Fatal("left/top-aligned text did not render")
	}
}
