package skvm

import (
	"encoding/binary"
	"math"
	"path"
	"sort"
	"strings"
)

func inspectRecordStores(files map[string][]byte) ([]RecordStore, error) {
	metadataNames := make([]string, 0)
	for name := range files {
		if strings.EqualFold(path.Dir(name), "rs") &&
			strings.EqualFold(path.Ext(name), ".sb") {
			metadataNames = append(metadataNames, name)
		}
	}
	sort.Slice(metadataNames, func(i, j int) bool {
		return strings.ToLower(metadataNames[i]) < strings.ToLower(metadataNames[j])
	})
	stores := make([]RecordStore, 0, len(metadataNames))
	seen := make(map[string]bool, len(metadataNames))
	for _, metadataName := range metadataNames {
		databaseName := strings.TrimSuffix(
			metadataName,
			path.Ext(metadataName),
		) + ".db"
		matchedName, ok := findCaseInsensitive(files, databaseName)
		if !ok {
			return nil, formatError(metadataName, -1, "record store data file is missing")
		}
		store, err := inspectRecordStore(
			metadataName,
			files[metadataName],
			files[matchedName],
		)
		if err != nil {
			return nil, err
		}
		if seen[store.Name] {
			return nil, formatError(metadataName, -1, "duplicate record store name")
		}
		seen[store.Name] = true
		stores = append(stores, store)
	}
	sort.Slice(stores, func(i, j int) bool {
		return stores[i].Name < stores[j].Name
	})
	return stores, nil
}

func inspectRecordStore(
	metadataName string,
	metadata, database []byte,
) (RecordStore, error) {
	if len(metadata) < 6 || binary.BigEndian.Uint32(metadata) != 2 {
		return RecordStore{}, formatError(metadataName, 0, "unsupported record store metadata")
	}
	nameSize := int(binary.BigEndian.Uint16(metadata[4:]))
	header := 6 + nameSize
	if nameSize == 0 || header < 6 || header+20 > len(metadata) {
		return RecordStore{}, formatError(metadataName, 4, "truncated record store name")
	}
	name, ok := decodeModifiedUTF8(metadata[6:header])
	if !ok || strings.TrimSpace(name) == "" || strings.IndexByte(name, 0) >= 0 {
		return RecordStore{}, formatError(metadataName, 6, "invalid record store name")
	}
	nextID := binary.BigEndian.Uint32(metadata[header:])
	recordCount := binary.BigEndian.Uint32(metadata[header+4:])
	databaseSize := binary.BigEndian.Uint32(metadata[header+8:])
	expected := uint64(header) + 20 + uint64(recordCount)*12
	if nextID == 0 || nextID == math.MaxUint32 ||
		expected != uint64(len(metadata)) ||
		uint64(databaseSize) != uint64(len(database)) {
		return RecordStore{}, formatError(metadataName, int64(header), "invalid record store layout")
	}
	records := make([]Record, 0, recordCount)
	seen := make(map[uint32]bool, recordCount)
	for index := uint32(0); index < recordCount; index++ {
		offset := header + 20 + int(index)*12
		recordID := binary.BigEndian.Uint32(metadata[offset:])
		dataOffset := binary.BigEndian.Uint32(metadata[offset+4:])
		dataSize := binary.BigEndian.Uint32(metadata[offset+8:])
		dataEnd := uint64(dataOffset) + uint64(dataSize)
		if recordID == 0 || recordID >= nextID || seen[recordID] ||
			dataEnd > uint64(len(database)) {
			return RecordStore{}, formatError(
				metadataName,
				int64(offset),
				"invalid record store entry",
			)
		}
		seen[recordID] = true
		records = append(records, Record{
			ID: recordID,
			Data: append(
				[]byte(nil),
				database[dataOffset:uint32(dataEnd)]...,
			),
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
	return RecordStore{Name: name, NextID: nextID, Records: records}, nil
}
