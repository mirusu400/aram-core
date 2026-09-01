package samsung

import (
	"bytes"
	"testing"
)

func TestFlexOneNANDRawFBAGeometry(t *testing.T) {
	tests := []struct {
		block      uint32
		wantOffset uint64
		wantSize   uint64
	}{
		{block: 0, wantOffset: 0x00000000, wantSize: 0x00040000},
		{block: 15, wantOffset: 0x003c0000, wantSize: 0x00040000},
		{block: 16, wantOffset: 0x00400000, wantSize: 0x00080000},
		{block: 17, wantOffset: 0x00480000, wantSize: 0x00080000},
		{block: 1023, wantOffset: 0x1fb80000, wantSize: 0x00080000},
	}
	for _, test := range tests {
		offset, offsetOK := flexOneNANDRawFBAOffset(test.block)
		size, sizeOK := flexOneNANDRawFBASize(test.block)
		if !offsetOK || !sizeOK || offset != test.wantOffset || size != test.wantSize {
			t.Fatalf(
				"raw FBA %d geometry = %#x/%#x/%v/%v, want %#x/%#x",
				test.block, offset, size, offsetOK, sizeOK, test.wantOffset, test.wantSize,
			)
		}
	}
	if _, ok := flexOneNANDRawFBAOffset(flexOneNANDRawBlockCount); ok {
		t.Fatal("raw FBA immediately past the device was accepted")
	}
	if _, ok := flexOneNANDRawFBASize(flexOneNANDRawBlockCount); ok {
		t.Fatal("raw FBA size immediately past the device was accepted")
	}
}

func TestFlexOneNANDOEMSBLTargetsPackedRawFBAFour(t *testing.T) {
	spec := BootImageSpec{
		ID:           "oemsbl",
		BlockOffsets: []int64{4 * flexOneNANDMIBIBBlockSize},
		BlockSize:    uint32(flexOneNANDMIBIBBlockSize),
	}
	partitions := []Partition{{Name: "0:OEMSBL", StartBlock: 4, BlockCount: 1}}
	target, err := flexOneNANDBootTarget(spec, partitions)
	if err != nil {
		t.Fatal(err)
	}
	if target != 0x00100000 {
		t.Fatalf("OEMSBL raw FBA target = %#x, want 0x00100000", target)
	}
	containerOffset, ok := flexOneNANDOffset(4)
	if !ok || containerOffset != 0x00200000 {
		t.Fatalf("OEMSBL container offset = %#x/%v, want 0x00200000", containerOffset, ok)
	}
}

func TestPlaceFlexOneNANDBootKeepsHeaderAndStartsPayloadAtPageOne(t *testing.T) {
	const (
		source       = uint64(0x00200000)
		target       = uint64(0x00100000)
		headerSize   = uint64(0x00001000)
		rawBlockSize = uint64(0x00040000)
	)
	flash := bytes.Repeat([]byte{0xff}, 0x00210000)
	header := make([]byte, headerSize)
	for index := range header {
		header[index] = byte(index*11 + 5)
	}
	copy(flash[source:source+headerSize], header)
	payload := make([]byte, 0x2345)
	for index := range payload {
		payload[index] = byte(index*7 + 3)
	}
	image := BootImage{ID: "oemsbl", UsedSize: uint32(len(payload)), Bytes: payload}
	if err := placeFlexOneNANDBoot(flash, source, target, headerSize, rawBlockSize, image); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(flash[target:target+headerSize], header) {
		t.Fatal("raw OEMSBL FBA does not retain the physical header page")
	}
	payloadStart := target + headerSize
	if !bytes.Equal(flash[payloadStart:payloadStart+uint64(len(payload))], payload) {
		t.Fatal("raw OEMSBL payload does not begin at page one")
	}
}
