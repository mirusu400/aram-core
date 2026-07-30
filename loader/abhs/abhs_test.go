package abhs

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestParseSyntheticModule(t *testing.T) {
	data := syntheticModule()
	module, err := Parse(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if module.Code.Size != 8 {
		t.Fatalf("code size = %d, want 8", module.Code.Size)
	}
	if len(module.RelocationOffsets) != 1 || module.RelocationOffsets[0] != 0 {
		t.Fatalf("relocations = %#v", module.RelocationOffsets)
	}
	if module.EntryOffset != 1 {
		t.Fatalf("entry offset = %#x, want 1", module.EntryOffset)
	}
}

func TestParseRejectsBadRelocation(t *testing.T) {
	data := syntheticModule()
	binary.LittleEndian.PutUint32(data[0x94:0x98], 2)
	if _, err := Parse(data, 0); err == nil {
		t.Fatal("Parse accepted an invalid relocation table")
	}
}

func TestLoadAppliesRelocationsAndEntryModes(t *testing.T) {
	data := syntheticModule()
	binary.LittleEndian.PutUint32(data[0x80:0x84], 0x24)
	module, err := Parse(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(data, module, 0x1000)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(loaded.Image[:4]); got != 0x1024 {
		t.Fatalf("relocated word = %#x, want %#x", got, 0x1024)
	}
	if loaded.GuestInit != 0x1001 || loaded.GuestFini != 0x1001 ||
		loaded.GuestEntry() != loaded.GuestFini {
		t.Fatalf("loaded entry points = init %#x fini %#x", loaded.GuestInit, loaded.GuestFini)
	}
	if got := binary.LittleEndian.Uint32(data[0x80:0x84]); got != 0x24 {
		t.Fatalf("Load modified source code: %#x", got)
	}
}

func TestLoadRejectsStaleMetadataAndGuestOverflow(t *testing.T) {
	data := syntheticModule()
	module, err := Parse(data, 0)
	if err != nil {
		t.Fatal(err)
	}

	stale := module
	stale.EntryOffset = 3
	if _, err := Load(data, stale, 0x1000); err == nil {
		t.Fatal("Load accepted stale module metadata")
	}
	if _, err := Load(data, module, ^uint32(0)-3); err == nil {
		t.Fatal("Load accepted a wrapping guest range")
	}
}

func TestFormatErrorCarriesOffendingOffset(t *testing.T) {
	data := syntheticModule()
	binary.LittleEndian.PutUint32(data[0xa0:0xa4], 2)
	_, err := Parse(data, 0)
	var formatErr *FormatError
	if !errors.As(err, &formatErr) {
		t.Fatalf("Parse error = %v, want FormatError", err)
	}
	if formatErr.Offset != 0xa0 {
		t.Fatalf("FormatError.Offset = %#x, want %#x", formatErr.Offset, 0xa0)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(syntheticModule(), uint32(0))
	f.Add([]byte("ABHS"), uint32(0))
	f.Fuzz(func(t *testing.T, data []byte, offset uint32) {
		if uint64(offset) > uint64(len(data)) {
			return
		}
		_, _ = Parse(data, offset)
	})
}

func syntheticModule() []byte {
	data := make([]byte, 0xac)
	copy(data, Magic)
	binary.LittleEndian.PutUint32(data[4:8], Version)
	binary.LittleEndian.PutUint32(data[8:12], 0x10)
	binary.LittleEndian.PutUint32(data[12:16], 3)
	putDescriptor := func(offset, kind, size, fileOffset uint32) {
		binary.LittleEndian.PutUint32(data[offset:offset+4], kind)
		binary.LittleEndian.PutUint32(data[offset+4:offset+8], size)
		binary.LittleEndian.PutUint32(data[offset+8:offset+12], fileOffset)
	}
	putDescriptor(0x10, 0, 60, 0x40)
	putDescriptor(0x1c, 1, 8, 0x80)
	putDescriptor(0x28, 2, 24, 0x90)
	binary.LittleEndian.PutUint32(data[0x40+52:0x40+56], 1)
	binary.LittleEndian.PutUint32(data[0x40+56:0x40+60], 1)
	binary.LittleEndian.PutUint32(data[0x90:0x94], 1)
	binary.LittleEndian.PutUint32(data[0x94:0x98], 1)
	binary.LittleEndian.PutUint32(data[0x98:0x9c], 24)
	binary.LittleEndian.PutUint32(data[0x9c:0xa0], RelocationMagic)
	binary.LittleEndian.PutUint32(data[0xa0:0xa4], 0)
	binary.LittleEndian.PutUint32(data[0xa4:0xa8], 0xffffffff)
	return data
}
