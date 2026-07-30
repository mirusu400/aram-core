package eads

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestParseSyntheticImage(t *testing.T) {
	data := make([]byte, HeaderSize+8)
	copy(data, Magic)
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint32(data[8:12], 2)
	binary.LittleEndian.PutUint32(data[12:16], 0xf4000000)
	binary.LittleEndian.PutUint32(data[16:20], 8)
	binary.LittleEndian.PutUint32(data[20:24], 0xf5000000)
	binary.LittleEndian.PutUint32(data[24:28], 0x1000)
	copy(data[0x20:0x30], []byte("Synthetic"))
	copy(data[HeaderSize:], []byte{0x00, 0xb5, 0x70, 0x47})
	image, err := Parse(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if image.Name != "Synthetic" {
		t.Fatalf("name = %q", image.Name)
	}
}

func TestParseRejectsOverlappingRanges(t *testing.T) {
	data := make([]byte, HeaderSize+8)
	copy(data, Magic)
	binary.LittleEndian.PutUint32(data[12:16], 0x1000)
	binary.LittleEndian.PutUint32(data[16:20], 8)
	binary.LittleEndian.PutUint32(data[20:24], 0x1000)
	binary.LittleEndian.PutUint32(data[24:28], 0x1000)
	copy(data[0x20:0x30], []byte("Overlap"))
	copy(data[HeaderSize:], []byte{0x00, 0xb5, 0x70, 0x47})
	if _, err := Parse(data, 0); err == nil {
		t.Fatal("Parse accepted overlapping guest ranges")
	}
}

func TestExtractAndLoadImage(t *testing.T) {
	data := syntheticImage()
	image, err := Parse(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ExtractText(data, image)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != 8 || text[0] != 0x00 || text[1] != 0xb5 {
		t.Fatalf("extracted text = %x", text)
	}

	destination := make([]byte, 8)
	bss := make([]byte, 0x1000)
	for index := range bss {
		bss[index] = 0xaa
	}
	loaded, err := Load(data, image, destination, bss)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GuestEntry != 0xf4000001 ||
		loaded.TextAddress != 0xf4000000 ||
		loaded.DataAddress != 0xf5000000 {
		t.Fatalf("loaded image = %+v", loaded)
	}
	for index, value := range bss {
		if value != 0 {
			t.Fatalf("BSS byte %d = %#x, want zero", index, value)
		}
	}
}

func TestLoadRejectsStaleMetadataAndSmallDestinations(t *testing.T) {
	data := syntheticImage()
	image, err := Parse(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	stale := image
	stale.TextBase += 0x1000
	if _, err := Load(data, stale, make([]byte, 8), make([]byte, 0x1000)); err == nil {
		t.Fatal("Load accepted stale image metadata")
	}
	if _, err := Load(data, image, make([]byte, 7), make([]byte, 0x1000)); err == nil {
		t.Fatal("Load accepted a short text destination")
	}
	if _, err := Load(data, image, make([]byte, 8), make([]byte, 0xfff)); err == nil {
		t.Fatal("Load accepted a short BSS destination")
	}
}

func TestFormatErrorCarriesOffendingOffset(t *testing.T) {
	data := syntheticImage()
	binary.LittleEndian.PutUint32(data[12:16], 0xf4000001)
	_, err := Parse(data, 0)
	var formatErr *FormatError
	if !errors.As(err, &formatErr) {
		t.Fatalf("Parse error = %v, want FormatError", err)
	}
	if formatErr.Offset != 12 {
		t.Fatalf("FormatError.Offset = %d, want 12", formatErr.Offset)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(syntheticImage(), uint32(0))
	f.Add([]byte("EADS"), uint32(0))
	f.Fuzz(func(t *testing.T, data []byte, offset uint32) {
		if uint64(offset) > uint64(len(data)) {
			return
		}
		_, _ = Parse(data, offset)
	})
}

func syntheticImage() []byte {
	data := make([]byte, HeaderSize+8)
	copy(data, Magic)
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint32(data[8:12], 2)
	binary.LittleEndian.PutUint32(data[12:16], 0xf4000000)
	binary.LittleEndian.PutUint32(data[16:20], 8)
	binary.LittleEndian.PutUint32(data[20:24], 0xf5000000)
	binary.LittleEndian.PutUint32(data[24:28], 0x1000)
	copy(data[0x20:0x30], []byte("Synthetic"))
	copy(data[HeaderSize:], []byte{0x00, 0xb5, 0x70, 0x47})
	return data
}
