package brewrt

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
)

func TestBREWResourceFooterKeepsWideTextWithOEMFlag(t *testing.T) {
	// Include spaces and ASCII: interpreting these UTF-16 bytes as a C string
	// used to truncate the label at the first zero high byte.
	units := utf16.Encode([]rune("한글 메뉴 A"))
	var frames [][]byte
	for _, oem := range []bool{false, true} {
		r := newSyntheticRuntime(t)
		r.wideOEMText = true
		textAt, stackAt := heapBase+0x200, heapBase+0x300
		encoded := make([]byte, (len(units)+1)*2)
		for i, unit := range units {
			binary.LittleEndian.PutUint16(encoded[i*2:], unit)
		}
		if err := r.cpu.WriteMemory(textAt, encoded); err != nil {
			t.Fatal(err)
		}
		var args [16]byte
		if oem {
			binary.LittleEndian.PutUint32(args[12:], displayTextFormatOEM)
		}
		if err := r.cpu.WriteMemory(stackAt, args[:]); err != nil {
			t.Fatal(err)
		}
		for reg, value := range map[uint32]uint32{
			cpu.RegisterR2: textAt, cpu.RegisterR3: ^uint32(0), cpu.RegisterSP: stackAt,
		} {
			if err := r.cpu.WriteRegister(reg, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.drawDisplayText(); err != nil {
			t.Fatal(err)
		}
		pixels := make([]byte, framebufferBytes)
		if err := r.cpu.ReadMemory(framebufferBase, pixels); err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(pixels, make([]byte, len(pixels))) {
			t.Fatal("footer did not draw")
		}
		frames = append(frames, pixels)
	}
	if !bytes.Equal(frames[0], frames[1]) {
		t.Fatal("OEM footer changed or truncated the resource label")
	}
}

func TestBREWWideOEMFooterRequiresExactPackage(t *testing.T) {
	if !wideOEMTextForDigest(bigFishingArchiveSHA256) {
		t.Fatal("reported package did not select wide footer text")
	}
	for _, digest := range []string{"", bigFishingArchiveSHA256[:63] + "0", unlimitedWarArchiveSHA256} {
		if wideOEMTextForDigest(digest) {
			t.Fatalf("unrelated package %s selected wide footer text", digest)
		}
	}
}
