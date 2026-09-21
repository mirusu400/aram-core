package brewrt

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"slices"
	"testing"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
	"golang.org/x/image/bmp"
)

func buildResourceFile(kind, id uint16, payload []byte) []byte {
	const indexOffset = 0x20
	const tableOffset = 0x28
	const dataOffset = 0x30
	data := make([]byte, dataOffset+len(payload))
	binary.LittleEndian.PutUint16(data[0:], brewResourceMagic)
	binary.LittleEndian.PutUint16(data[6:], 1)
	binary.LittleEndian.PutUint32(data[8:], indexOffset)
	binary.LittleEndian.PutUint32(data[12:], 8)
	binary.LittleEndian.PutUint32(data[16:], tableOffset)
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint32(data[24:], dataOffset)
	binary.LittleEndian.PutUint16(data[indexOffset:], kind)
	binary.LittleEndian.PutUint16(data[indexOffset+2:], id)
	binary.LittleEndian.PutUint16(data[indexOffset+4:], 0)
	binary.LittleEndian.PutUint16(data[indexOffset+6:], 0)
	binary.LittleEndian.PutUint32(data[tableOffset:], dataOffset)
	binary.LittleEndian.PutUint32(data[tableOffset+4:], uint32(len(data)))
	copy(data[dataOffset:], payload)
	return data
}

func TestResourceDataValidatesRangesAndReturnsExactSection(t *testing.T) {
	file := buildResourceFile(0x5000, 7, []byte("payload"))
	got, ok := resourceData(file, 0x5000, 7)
	if !ok || !bytes.Equal(got, []byte("payload")) {
		t.Fatalf("resource data = %q ok=%v", got, ok)
	}
	if _, ok := resourceData(file[:len(file)-1], 0x5000, 7); ok {
		t.Fatal("truncated resource section was accepted")
	}
	if _, ok := resourceData(file, 6, 7); ok {
		t.Fatal("wrong resource type was accepted")
	}
}

func TestLoadShellResourceDataCopiesOwnedGuestBlock(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.files = map[string][]byte{"assets/game.bar": buildResourceFile(6, 42, []byte{1, 2, 3, 4})}
	pathAt := heapBase + 0x100
	if err := runtime.cpu.WriteMemory(pathAt, []byte("game.bar\x00")); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: pathAt,
		cpu.RegisterR2: 42,
		cpu.RegisterR3: 6,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 18*2 + 2)
	if err != nil || !handled {
		t.Fatalf("LoadResData handled=%v err=%v", handled, err)
	}
	pointer, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || pointer < heapBase {
		t.Fatalf("LoadResData pointer=0x%08x err=%v", pointer, err)
	}
	got := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(pointer, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("loaded resource = %v", got)
	}
}

func TestLoadShellResourceStringCopiesBoundedUCS2(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.files = map[string][]byte{"assets/game.bar": buildResourceFile(brewStringResourceKind, 7, []byte{'A', 0, 'B', 0, 'C', 0, 0, 0})}
	pathAt, destination := heapBase+0x100, heapBase+0x200
	if err := runtime.cpu.WriteMemory(pathAt, []byte("assets/game.bar\x00")); err != nil {
		t.Fatal(err)
	}
	sp := heapBase + 0x300
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, sp); err != nil {
		t.Fatal(err)
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], 6)
	if err := runtime.cpu.WriteMemory(sp, size[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: pathAt,
		cpu.RegisterR2: 7,
		cpu.RegisterR3: destination,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 17*2 + 2)
	if err != nil || !handled {
		t.Fatalf("LoadResString handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 2 {
		t.Fatalf("LoadResString count=%d err=%v, want 2", got, err)
	}
	got := make([]byte, 6)
	if err := runtime.cpu.ReadMemory(destination, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{'A', 0, 'B', 0, 0, 0}) {
		t.Fatalf("loaded string = %v", got)
	}
}

func TestDecodeBREWResourceStringExpandsKoreanCompressedData(t *testing.T) {
	// 0xfefe marks BREW's compressed string representation. The payload is the
	// handset OEM encoding, here EUC-KR, and keeps ASCII delimiters intact.
	got := decodeBREWResourceString([]byte{0xfe, 0xfe, '[', 0xb9, 0xdd, ']', '@', 0, 'X'})
	want := utf16.Encode([]rune("[반]@"))
	if !slices.Equal(got, want) {
		t.Fatalf("decoded compressed resource=%04x, want %04x", got, want)
	}
}

func TestLoadShellResourceStringExpandsCompressedData(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.files = map[string][]byte{
		"assets/game.bar": buildResourceFile(brewStringResourceKind, 9, []byte{0xfe, 0xfe, '[', 0xb9, 0xdd, ']', '@', 0, 'X'}),
	}
	pathAt, destination := heapBase+0x100, heapBase+0x200
	if err := runtime.cpu.WriteMemory(pathAt, []byte("assets/game.bar\x00")); err != nil {
		t.Fatal(err)
	}
	sp := heapBase + 0x300
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, sp); err != nil {
		t.Fatal(err)
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], 12)
	if err := runtime.cpu.WriteMemory(sp, size[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: pathAt,
		cpu.RegisterR2: 9,
		cpu.RegisterR3: destination,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 17*2 + 2)
	if err != nil || !handled {
		t.Fatalf("LoadResString handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 4 {
		t.Fatalf("LoadResString count=%d err=%v, want 4", got, err)
	}
	got := make([]byte, 10)
	if err := runtime.cpu.ReadMemory(destination, got); err != nil {
		t.Fatal(err)
	}
	wantUnits := append(utf16.Encode([]rune("[반]@")), 0)
	want := make([]byte, len(wantUnits)*2)
	for index, unit := range wantUnits {
		binary.LittleEndian.PutUint16(want[index*2:], unit)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("loaded compressed string = % x, want % x", got, want)
	}
}

func TestDecodeBREWResourceImageHonorsBlobOffset(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	resource := append([]byte{12, 0}, []byte("image/bmp\x00")...)
	resource = append(resource, encoded.Bytes()...)
	decoded, ok := decodeBREWResourceImage(resource)
	if !ok || decoded.Bounds() != source.Bounds() {
		t.Fatalf("decoded=%v ok=%v", decoded, ok)
	}
}

func TestDecodeBREWResourceImageProvidesSAFSurface(t *testing.T) {
	resource := append([]byte{12, 0}, []byte("image/sis\x00")...)
	resource = append(resource, []byte("SAF\x00\x02\x01")...)
	decoded, ok := decodeBREWResourceImage(resource)
	want := image.Rect(0, 0, int(framebufferWidth), int(framebufferHeight))
	if !ok || decoded == nil {
		t.Fatalf("SAF surface=%v ok=%v, want non-nil/true", decoded, ok)
	}
	if decoded.Bounds() != want {
		t.Fatalf("SAF surface bounds=%v, want %v", decoded.Bounds(), want)
	}
}
