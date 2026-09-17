package j2me

import (
	"encoding/binary"
	"testing"
)

func testExternalRMS(records map[uint32][]byte, nextID uint32) []byte {
	data := make([]byte, 48)
	copy(data, lgtRMSMagic)
	binary.BigEndian.PutUint32(data[8:], uint32(len(records)))
	binary.BigEndian.PutUint32(data[20:], nextID)
	binary.BigEndian.PutUint32(data[40:], 48)
	previous := uint32(0)
	for id := uint32(1); id < nextID; id++ {
		payload, ok := records[id]
		if !ok {
			continue
		}
		blockSize := (16 + len(payload) + 15) &^ 15
		block := make([]byte, blockSize)
		binary.BigEndian.PutUint32(block, id)
		binary.BigEndian.PutUint32(block[4:], previous)
		binary.BigEndian.PutUint32(block[8:], uint32(blockSize))
		binary.BigEndian.PutUint32(block[12:], uint32(len(payload)))
		copy(block[16:], payload)
		previous = uint32(len(data))
		data = append(data, block...)
	}
	binary.BigEndian.PutUint32(data[24:], previous)
	binary.BigEndian.PutUint32(data[44:], uint32(len(data)))
	return data
}

func TestInspectExternalRecordStore(t *testing.T) {
	payload := testExternalRMS(map[uint32][]byte{2: []byte{1, 2, 3}, 4: []byte("save")}, 5)
	store, err := inspectExternalRecordStore("state.db", payload)
	if err != nil {
		t.Fatal(err)
	}
	if store.Name != "state" || store.NextID != 5 || len(store.Records) != 2 ||
		store.Records[0].ID != 2 || store.Records[1].ID != 4 ||
		string(store.Records[1].Data) != "save" {
		t.Fatalf("decoded store = %+v", store)
	}

	for _, mutate := range []func([]byte){
		func(data []byte) { data[0] = 0 },
		func(data []byte) { binary.BigEndian.PutUint32(data[20:], 0) },
		func(data []byte) { binary.BigEndian.PutUint32(data[56:], 8) },
		func(data []byte) { binary.BigEndian.PutUint32(data[60:], uint32(len(data))) },
	} {
		bad := append([]byte(nil), payload...)
		mutate(bad)
		if _, err := inspectExternalRecordStore("state.db", bad); err == nil {
			t.Fatal("malformed external RMS accepted")
		}
	}
}

func TestInspectExternalRecordStoreAllowsOnlyOmittedFinalPadding(t *testing.T) {
	payload := testExternalRMS(map[uint32][]byte{1: []byte{1, 2, 3}}, 2)
	withoutPadding := payload[:len(payload)-13]
	store, err := inspectExternalRecordStore("state.db", withoutPadding)
	if err != nil {
		t.Fatalf("documented omitted final padding rejected: %v", err)
	}
	if len(store.Records) != 1 || len(store.Records[0].Data) != 3 {
		t.Fatalf("decoded store = %+v", store)
	}

	declaredExtraBlock := append([]byte(nil), payload...)
	logicalEnd := binary.BigEndian.Uint32(declaredExtraBlock[44:]) + 16
	binary.BigEndian.PutUint32(declaredExtraBlock[56:], 48)
	binary.BigEndian.PutUint32(declaredExtraBlock[44:], logicalEnd)
	if _, err := inspectExternalRecordStore("state.db", declaredExtraBlock); err == nil {
		t.Fatal("external RMS accepted more than 15 omitted final bytes")
	}
}

func TestInspectExternalRecordStoreCapsPhysicalBlocks(t *testing.T) {
	const headerSize = 48
	data := make([]byte, headerSize+16*(maxExternalRMSBlocks+1))
	copy(data, lgtRMSMagic)
	binary.BigEndian.PutUint32(data[20:], 1)
	binary.BigEndian.PutUint32(data[28:], headerSize)
	binary.BigEndian.PutUint32(data[40:], headerSize)
	binary.BigEndian.PutUint32(data[44:], uint32(len(data)))
	previous := uint32(0)
	for index := 0; index <= maxExternalRMSBlocks; index++ {
		offset := headerSize + index*16
		binary.BigEndian.PutUint32(data[offset:], ^uint32(0))
		binary.BigEndian.PutUint32(data[offset+4:], previous)
		binary.BigEndian.PutUint32(data[offset+8:], 16)
		if index < maxExternalRMSBlocks {
			binary.BigEndian.PutUint32(data[offset+12:], uint32(offset+16))
		}
		previous = uint32(offset)
	}
	binary.BigEndian.PutUint32(data[24:], previous)
	if _, err := inspectExternalRecordStore("many.db", data); err == nil {
		t.Fatal("external RMS accepted excessive physical block count")
	}
}

func TestInspectExternalRecordStoreFreeList(t *testing.T) {
	payload := testExternalRMS(map[uint32][]byte{1: []byte("one"), 3: []byte("three")}, 4)
	// Turn the second physical block into a free block and link it from the header.
	second := binary.BigEndian.Uint32(payload[48+8:]) + 48
	binary.BigEndian.PutUint32(payload[second:], ^uint32(0))
	binary.BigEndian.PutUint32(payload[second+12:], 0)
	binary.BigEndian.PutUint32(payload[28:], second)
	binary.BigEndian.PutUint32(payload[8:], 1)
	store, err := inspectExternalRecordStore("free.db", payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Records) != 1 || store.Records[0].ID != 1 {
		t.Fatalf("free-list store = %+v", store)
	}
}
