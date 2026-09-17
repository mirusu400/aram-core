package j2me

import (
	"bytes"
	"encoding/binary"
	"path"
	"sort"
	"strings"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
)

var lgtRMSMagic = []byte("midp-rms")

const maxExternalRMSBlocks = MaxArchiveEntries

// inspectExternalRecordStores decodes the LGT MIDP installer database carried
// beside a JAD/JAR pair. Each record occupies a big-endian block whose declared
// extent includes its 16-byte header and alignment padding. The final padding is
// commonly omitted from the ZIP member, so only the record payload must be
// physically present for the last block.
func inspectExternalRecordStores(files map[string][]byte) ([]skloader.RecordStore, error) {
	var names []string
	for name := range files {
		if strings.EqualFold(path.Ext(name), ".db") {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	stores := make([]skloader.RecordStore, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		store, err := inspectExternalRecordStore(name, files[name])
		if err != nil {
			return nil, err
		}
		if seen[store.Name] {
			return nil, malformed(name, 0, "duplicate external RMS store name")
		}
		seen[store.Name] = true
		stores = append(stores, store)
	}
	return stores, nil
}

func inspectExternalRecordStore(name string, data []byte) (skloader.RecordStore, error) {
	const headerSize = 48
	if len(data) < headerSize || !bytes.Equal(data[:len(lgtRMSMagic)], lgtRMSMagic) {
		return skloader.RecordStore{}, malformed(name, 0, "invalid external RMS header")
	}
	recordCount := binary.BigEndian.Uint32(data[8:12])
	authorizationMode := binary.BigEndian.Uint32(data[12:16])
	nextID := binary.BigEndian.Uint32(data[20:24])
	finalBlock := binary.BigEndian.Uint32(data[24:28])
	freeListHead := binary.BigEndian.Uint32(data[28:32])
	cursor := uint64(binary.BigEndian.Uint32(data[40:44]))
	logicalEnd := uint64(binary.BigEndian.Uint32(data[44:48]))
	if authorizationMode != 0 {
		return skloader.RecordStore{}, &UnsupportedFeatureError{
			Path: name, Offset: 12, Feature: "shared external RMS authorization mode",
		}
	}
	if cursor != headerSize || logicalEnd < cursor || logicalEnd%16 != 0 ||
		nextID == 0 || uint64(recordCount) > uint64(MaxArchiveEntries) {
		return skloader.RecordStore{}, malformed(name, 8, "invalid external RMS layout")
	}
	records := make([]skloader.Record, 0, recordCount)
	seen := make(map[uint32]bool, recordCount)
	freeBlocks := make(map[uint32]uint32)
	previous := uint32(0)
	blockCount := 0
	for cursor < logicalEnd {
		blockCount++
		if blockCount > maxExternalRMSBlocks {
			return skloader.RecordStore{}, malformed(name, int64(cursor), "external RMS block count exceeds limit")
		}
		if cursor+16 > uint64(len(data)) {
			return skloader.RecordStore{}, malformed(name, int64(cursor), "truncated external RMS record header")
		}
		offset := int(cursor)
		recordID := int32(binary.BigEndian.Uint32(data[offset : offset+4]))
		previousBlock := binary.BigEndian.Uint32(data[offset+4 : offset+8])
		blockSize := binary.BigEndian.Uint32(data[offset+8 : offset+12])
		dataSizeOrNextFree := binary.BigEndian.Uint32(data[offset+12 : offset+16])
		dataStart := cursor + 16
		nextBlock := cursor + uint64(blockSize)
		if previousBlock != previous || blockSize < 16 || blockSize%16 != 0 ||
			nextBlock <= cursor || nextBlock > logicalEnd ||
			(nextBlock < logicalEnd && nextBlock > uint64(len(data))) {
			return skloader.RecordStore{}, malformed(name, int64(cursor), "invalid external RMS record")
		}
		switch {
		case recordID == -1:
			if nextBlock > uint64(len(data)) {
				return skloader.RecordStore{}, malformed(name, int64(cursor), "truncated external RMS free block")
			}
			freeBlocks[uint32(cursor)] = dataSizeOrNextFree
		case recordID > 0:
			id := uint32(recordID)
			dataEnd := dataStart + uint64(dataSizeOrNextFree)
			if id >= nextID || seen[id] || uint64(dataSizeOrNextFree) > uint64(blockSize-16) ||
				dataEnd > uint64(len(data)) ||
				(nextBlock > uint64(len(data)) && nextBlock-uint64(len(data)) > 15) {
				return skloader.RecordStore{}, malformed(name, int64(cursor), "invalid external RMS record")
			}
			seen[id] = true
			records = append(records, skloader.Record{ID: id, Data: append([]byte(nil), data[dataStart:dataEnd]...)})
		default:
			return skloader.RecordStore{}, malformed(name, int64(cursor), "invalid external RMS record identifier")
		}
		previous = uint32(cursor)
		cursor = nextBlock
	}
	if cursor != logicalEnd || previous != finalBlock || len(records) != int(recordCount) {
		return skloader.RecordStore{}, malformed(name, int64(cursor), "external RMS block summary mismatch")
	}
	visitedFree := make(map[uint32]bool, len(freeBlocks))
	for current := freeListHead; current != 0; {
		next, ok := freeBlocks[current]
		if !ok || visitedFree[current] {
			return skloader.RecordStore{}, malformed(name, int64(current), "invalid external RMS free list")
		}
		visitedFree[current] = true
		current = next
	}
	if len(visitedFree) != len(freeBlocks) {
		return skloader.RecordStore{}, malformed(name, int64(freeListHead), "external RMS free list does not cover free blocks")
	}
	storeName := strings.TrimSuffix(path.Base(name), path.Ext(name))
	if strings.TrimSpace(storeName) == "" || strings.IndexByte(storeName, 0) >= 0 {
		return skloader.RecordStore{}, malformed(name, 0, "invalid external RMS store name")
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return skloader.RecordStore{Name: storeName, NextID: nextID, Records: records}, nil
}
