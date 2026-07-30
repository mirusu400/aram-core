package loader

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestInspectContainerReturnsValidatedRecordsInFileOrder(t *testing.T) {
	data := append([]byte("ABHS-invalid"), syntheticABHSRecord()...)
	eadsOffset := len(data)
	data = append(data, syntheticEADSRecord()...)

	container, err := InspectContainer(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(container.Modules) != 1 || len(container.Images) != 1 ||
		len(container.Records) != 2 {
		t.Fatalf("container counts = modules %d images %d records %d",
			len(container.Modules), len(container.Images), len(container.Records))
	}
	moduleOffset := uint32(len("ABHS-invalid"))
	if container.FirstModuleOffset != moduleOffset ||
		container.ModuleChainEnd != container.Modules[0].RecordEnd() {
		t.Fatalf("module bounds = %#x..%#x",
			container.FirstModuleOffset, container.ModuleChainEnd)
	}
	if container.FirstImageOffset != uint32(eadsOffset) {
		t.Fatalf("FirstImageOffset = %#x, want %#x", container.FirstImageOffset, eadsOffset)
	}
	if container.Records[0].Kind != KindABHS ||
		container.Records[1].Kind != KindEADS {
		t.Fatalf("record order = %+v", container.Records)
	}
}

func TestInspectContainerReportsNoValidRecords(t *testing.T) {
	_, err := InspectContainer([]byte("ABHS EADS"))
	if !errors.Is(err, ErrNoContainerRecords) {
		t.Fatalf("InspectContainer error = %v", err)
	}
}

func FuzzInspectContainer(f *testing.F) {
	f.Add(append(syntheticABHSRecord(), syntheticEADSRecord()...))
	f.Add([]byte("ABHS EADS"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = InspectContainer(data)
	})
}

func syntheticABHSRecord() []byte {
	data := make([]byte, 0xac)
	copy(data, "ABHS")
	binary.LittleEndian.PutUint32(data[4:8], 0x1000)
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
	binary.LittleEndian.PutUint32(data[0x9c:0xa0], 0xf0123456)
	binary.LittleEndian.PutUint32(data[0xa0:0xa4], 0)
	binary.LittleEndian.PutUint32(data[0xa4:0xa8], 0xffffffff)
	return data
}

func syntheticEADSRecord() []byte {
	data := make([]byte, 0x30+8)
	copy(data, "EADS")
	binary.LittleEndian.PutUint32(data[12:16], 0xf4000000)
	binary.LittleEndian.PutUint32(data[16:20], 8)
	binary.LittleEndian.PutUint32(data[20:24], 0xf5000000)
	binary.LittleEndian.PutUint32(data[24:28], 0x1000)
	copy(data[0x20:0x30], "Synthetic")
	copy(data[0x30:], []byte{0x00, 0xb5, 0x70, 0x47})
	return data
}
