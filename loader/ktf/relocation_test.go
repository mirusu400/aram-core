package ktf

import (
	"encoding/binary"
	"testing"
)

// TestSplitRelocatableClientReadsTheHeader covers the second shape a KTF
// client image comes in: a header of the BSS size, a count, and that many word
// offsets, ahead of the image proper. Running such an image as code executes
// the header.
func TestSplitRelocatableClientReadsTheHeader(t *testing.T) {
	const bss = 64
	offsets := []uint32{0, 8, 0x0c, 0x24}
	body := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(body[0x24:], 0x1234)

	image := make([]byte, 8+4*len(offsets))
	binary.LittleEndian.PutUint32(image, bss)
	binary.LittleEndian.PutUint32(image[4:], uint32(len(offsets)))
	for index, offset := range offsets {
		binary.LittleEndian.PutUint32(image[8+4*index:], offset)
	}
	image = append(image, body...)

	split, relocations, ok := SplitRelocatableClient(image, bss)
	if !ok {
		t.Fatal("a relocatable image was read as plain code")
	}
	if len(split) != len(body) {
		t.Fatalf("image is %d bytes, want %d", len(split), len(body))
	}
	if binary.LittleEndian.Uint32(split[0x24:]) != 0x1234 {
		t.Error("the image body was not split off where the header ends")
	}
	if len(relocations) != len(offsets) {
		t.Fatalf("relocations = %v, want %v", relocations, offsets)
	}
	for index, offset := range offsets {
		if relocations[index] != offset {
			t.Fatalf("relocations = %v, want %v", relocations, offsets)
		}
	}
}

// TestSplitRelocatableClientLeavesCodeAlone is the half that matters for every
// title that already works: a plain code image must not be mistaken for a
// relocatable one, and the BSS size appearing as the first word is what tells
// them apart.
func TestSplitRelocatableClientLeavesCodeAlone(t *testing.T) {
	// The first instructions of an ordinary KTF client: a branch and a nop.
	code := []byte{0x04, 0xe0, 0xc0, 0x46, 0x24, 0x02, 0x04, 0x20, 0x01, 0x00}
	for _, bss := range []uint32{0, 64, 1088} {
		split, relocations, ok := SplitRelocatableClient(code, bss)
		if ok || relocations != nil || len(split) != len(code) {
			t.Fatalf("bss %d: code image was split as relocatable", bss)
		}
	}
	// A header whose offsets do not name whole words inside the image is not a
	// relocation table either.
	bogus := make([]byte, 8+4+16)
	binary.LittleEndian.PutUint32(bogus, 64)
	binary.LittleEndian.PutUint32(bogus[4:], 1)
	binary.LittleEndian.PutUint32(bogus[8:], 0xffff)
	if _, _, ok := SplitRelocatableClient(bogus, 64); ok {
		t.Error("an out-of-range offset was accepted as a relocation")
	}
}
