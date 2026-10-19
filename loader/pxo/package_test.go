package pxo

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestInspectAndRelocateSyntheticPXO1(t *testing.T) {
	data := syntheticPXO1()
	pkg, err := Inspect(data)
	check(t, err)
	if pkg.Header.FixupCount != 6 ||
		len(pkg.Fixups[0]) != 3 || len(pkg.Fixups[1]) != 1 || len(pkg.Fixups[2]) != 2 {
		t.Fatalf("fixups = %#v", pkg.Fixups)
	}
	if pkg.Descriptor.Entry != 0x21 || pkg.Descriptor.GOT != 0x40 || pkg.MappedSize() != 0x160 {
		t.Fatalf("descriptor = %+v, mapped size %#x", pkg.Descriptor, pkg.MappedSize())
	}

	relocated, err := pkg.Relocate(0x10000000)
	check(t, err)
	for offset, want := range map[uint32]uint32{
		0x00: 0x10000080,
		0x08: 0x10000100,
		0x70: 0x10000024,
	} {
		if got := binary.LittleEndian.Uint32(relocated[offset : offset+4]); got != want {
			t.Fatalf("relocated word at %#x = %#x, want %#x", offset, got, want)
		}
	}
	if got := binary.LittleEndian.Uint32(relocated[0x0c:0x10]); got != 0x60 {
		t.Fatalf("non-absolute fixup changed: %#x", got)
	}
}

func TestInspectRejectsMalformedPXO1(t *testing.T) {
	t.Run("magic", func(t *testing.T) {
		_, err := Inspect([]byte("not pxo"))
		if !errors.Is(err, ErrNotPXO) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("size", func(t *testing.T) {
		data := syntheticPXO1()
		binary.LittleEndian.PutUint32(data[8:12], uint32(len(data)+1))
		if _, err := Inspect(data); err == nil {
			t.Fatal("malformed size was accepted")
		}
	})
	t.Run("marker", func(t *testing.T) {
		data := syntheticPXO1()
		data[len(data)-2] ^= 0xff
		if _, err := Inspect(data); err == nil {
			t.Fatal("malformed marker was accepted")
		}
	})
}

func syntheticPXO1() []byte {
	groups := [FixupGroups][]uint32{{0, 8, 0x70}, {4}, {0x0c, 0x14}}
	table := encodeFixups(groups)
	imageOffset := HeaderSize + len(table)
	imageOffset = (imageOffset + 3) &^ 3
	image := make([]byte, 0x100)
	values := []uint32{0x80, 0x80, 0x100, 0x60, 0x80, 0x21, 0x40}
	for index, value := range values {
		binary.LittleEndian.PutUint32(image[index*4:], value)
	}
	binary.LittleEndian.PutUint32(image[0x70:], 0x24)

	data := make([]byte, imageOffset+len(image)+len(endMarker))
	copy(data, Magic)
	binary.LittleEndian.PutUint32(data[0x08:], uint32(len(data)))
	binary.LittleEndian.PutUint32(data[0x0c:], uint32(imageOffset))
	binary.LittleEndian.PutUint32(data[0x10:], 6)
	binary.LittleEndian.PutUint32(data[0x14:], ^uint32(0))
	binary.LittleEndian.PutUint32(data[0x20:], ^uint32(0))
	binary.LittleEndian.PutUint32(data[0x24:], 0x20)
	binary.LittleEndian.PutUint32(data[0x2c:], ^uint32(0))
	copy(data[HeaderSize:], table)
	for index := HeaderSize + len(table); index < imageOffset; index++ {
		data[index] = 0xff
	}
	copy(data[imageOffset:], image)
	copy(data[imageOffset+len(image):], endMarker)
	return data
}

func encodeFixups(groups [FixupGroups][]uint32) []byte {
	var result []byte
	for _, group := range groups {
		if len(group) == 0 {
			result = append(result, 0xff)
			continue
		}
		result = appendU24(result, group[0])
		previous := group[0]
		for _, offset := range group[1:] {
			delta := offset - previous
			if delta > 0 && delta < 0xff {
				result = append(result, byte(delta))
			} else {
				result = append(result, 0)
				result = appendU24(result, delta)
			}
			previous = offset
		}
		result = append(result, 0xff)
	}
	return result
}

func appendU24(destination []byte, value uint32) []byte {
	return append(destination, byte(value), byte(value>>8), byte(value>>16))
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
