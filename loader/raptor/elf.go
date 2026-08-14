package raptor

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// InspectELF parses the section-loaded ELF32 image emitted by Raptor. Raptor
// strips the symbol table while retaining REL records, so relocation sh_link
// values are deliberately not treated as symbol-table references. Fixed VMA
// loading preserves the already-linked values in the image.
func InspectELF(name string, data []byte) (Image, error) {
	if len(data) < elfHeaderSize {
		return Image{}, formatError(name, 0, "ELF header is truncated")
	}
	if !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return Image{}, formatError(name, 0, "ELF magic is missing")
	}
	if data[4] != elfClass32 || data[5] != elfDataLittle || data[6] != elfVersion {
		return Image{}, formatError(name, 4, "only little-endian ELF32 version 1 is supported")
	}
	if binary.LittleEndian.Uint16(data[16:18]) != elfTypeExec {
		return Image{}, formatError(name, 16, "ELF is not ET_EXEC")
	}
	if binary.LittleEndian.Uint16(data[18:20]) != elfMachineARM {
		return Image{}, formatError(name, 18, "ELF machine is not ARM")
	}
	if binary.LittleEndian.Uint32(data[20:24]) != elfVersion {
		return Image{}, formatError(name, 20, "ELF version is not 1")
	}
	if binary.LittleEndian.Uint16(data[40:42]) != elfHeaderSize {
		return Image{}, formatError(name, 40, "unexpected ELF header size")
	}
	sectionOffset := binary.LittleEndian.Uint32(data[32:36])
	sectionEntrySize := binary.LittleEndian.Uint16(data[46:48])
	sectionCount := binary.LittleEndian.Uint16(data[48:50])
	stringIndex := binary.LittleEndian.Uint16(data[50:52])
	if sectionEntrySize != sectionHdrSize {
		return Image{}, formatError(name, 46, "unexpected section-header size")
	}
	if sectionCount == 0 || sectionCount > MaxSections {
		return Image{}, formatError(name, 48, "invalid section count")
	}
	if stringIndex >= sectionCount {
		return Image{}, formatError(name, 50, "section-name table index is out of range")
	}
	tableSize := uint64(sectionEntrySize) * uint64(sectionCount)
	if !fileRange(data, uint64(sectionOffset), tableSize) {
		return Image{}, formatError(name, int64(sectionOffset), "section-header table is truncated")
	}

	headers := make([]sectionHeader, sectionCount)
	for index := range headers {
		offset := uint64(sectionOffset) + uint64(index)*sectionHdrSize
		raw := data[offset : offset+sectionHdrSize]
		headers[index] = sectionHeader{
			name:      binary.LittleEndian.Uint32(raw[0:4]),
			kind:      binary.LittleEndian.Uint32(raw[4:8]),
			flags:     binary.LittleEndian.Uint32(raw[8:12]),
			address:   binary.LittleEndian.Uint32(raw[12:16]),
			offset:    binary.LittleEndian.Uint32(raw[16:20]),
			size:      binary.LittleEndian.Uint32(raw[20:24]),
			link:      binary.LittleEndian.Uint32(raw[24:28]),
			info:      binary.LittleEndian.Uint32(raw[28:32]),
			alignment: binary.LittleEndian.Uint32(raw[32:36]),
			entrySize: binary.LittleEndian.Uint32(raw[36:40]),
		}
		if uint64(headers[index].size) > MaxSectionSize {
			return Image{}, formatError(name, int64(offset+20), "section exceeds size limit")
		}
	}
	namesHeader := headers[stringIndex]
	if namesHeader.kind != sectionString ||
		!fileRange(data, uint64(namesHeader.offset), uint64(namesHeader.size)) {
		return Image{}, formatError(name, int64(namesHeader.offset), "invalid section-name table")
	}
	names := data[namesHeader.offset : namesHeader.offset+namesHeader.size]

	image := Image{
		Entry: binary.LittleEndian.Uint32(data[24:28]),
		Flags: binary.LittleEndian.Uint32(data[36:40]),
	}
	seenNames := make(map[string]bool, len(headers))
	allocated := make([]addressRange, 0)
	var metadataSection []byte
	for index, header := range headers {
		sectionName, ok := readCString(names, header.name)
		if !ok {
			return Image{}, formatError(name, int64(sectionOffset)+int64(index)*sectionHdrSize, "invalid section name")
		}
		if sectionName != "" {
			if seenNames[sectionName] {
				return Image{}, formatError(name, -1, fmt.Sprintf("duplicate section %q", sectionName))
			}
			seenNames[sectionName] = true
		}
		if header.kind != sectionNoBits &&
			!fileRange(data, uint64(header.offset), uint64(header.size)) {
			return Image{}, formatError(name, int64(header.offset), fmt.Sprintf("section %q is truncated", sectionName))
		}
		if header.flags&sectionAlloc != 0 && header.size != 0 {
			end := uint64(header.address) + uint64(header.size)
			if end > 1<<32 {
				return Image{}, formatError(name, int64(header.offset), fmt.Sprintf("section %q address wraps", sectionName))
			}
			current := addressRange{start: uint64(header.address), end: end, name: sectionName}
			for _, previous := range allocated {
				if current.start < previous.end && previous.start < current.end {
					return Image{}, formatError(
						name,
						int64(header.offset),
						fmt.Sprintf("allocated sections %q and %q overlap", previous.name, current.name),
					)
				}
			}
			allocated = append(allocated, current)
		}
		section := Section{
			Index:     index,
			Name:      sectionName,
			Type:      header.kind,
			Flags:     header.flags,
			Address:   header.address,
			Alignment: header.alignment,
			Size:      header.size,
		}
		if header.kind != sectionNoBits && header.size != 0 {
			section.Data = append([]byte(nil), data[header.offset:header.offset+header.size]...)
		}
		image.Sections = append(image.Sections, section)
		if sectionName == ".raptor" {
			metadataSection = section.Data
		}
	}
	if len(metadataSection) == 0 {
		return Image{}, formatError(name, -1, ".raptor metadata section is missing")
	}
	metadata, err := parseMetadata(name, metadataSection)
	if err != nil {
		return Image{}, err
	}
	image.Metadata = metadata

	for index, header := range headers {
		if header.kind != sectionREL {
			continue
		}
		if header.entrySize != 8 || header.size%8 != 0 {
			return Image{}, formatError(name, int64(header.offset), "REL section has an invalid entry size")
		}
		if header.info >= uint32(len(headers)) {
			return Image{}, formatError(name, int64(header.offset), "REL target section is out of range")
		}
		targetHeader := headers[header.info]
		targetName := image.Sections[header.info].Name
		targetStart := uint64(targetHeader.address)
		targetEnd := targetStart + uint64(targetHeader.size)
		for offset := uint32(0); offset < header.size; offset += 8 {
			position := header.offset + offset
			relocationOffset := binary.LittleEndian.Uint32(data[position : position+4])
			info := binary.LittleEndian.Uint32(data[position+4 : position+8])
			if uint64(relocationOffset) < targetStart ||
				uint64(relocationOffset)+4 > targetEnd {
				return Image{}, formatError(name, int64(position), "REL relocation is outside its target section")
			}
			image.Relocations = append(image.Relocations, Relocation{
				Section: image.Sections[index].Name,
				Target:  targetName,
				Offset:  relocationOffset,
				Symbol:  info >> 8,
				Type:    uint8(info),
			})
		}
	}

	code, ok := image.CodeSection()
	if !ok {
		return Image{}, formatError(name, -1, "allocated executable code section is missing")
	}
	// The interworking bit belongs to the entry address, and modules disagree
	// about where it is written down: some carry the Thumb-flagged offset in
	// .raptor while the ELF header keeps the aligned address. Some SDKs also
	// leave a dummy header entry (the code base) and only .raptor is real
	// (SD한국전쟁), so the metadata offset is authoritative when they disagree.
	// The mode is left to the runtime, which enters Raptor code in Thumb
	// either way.
	metadataEntry := uint64(code.Address) + uint64(metadata.EntryOffset)
	if metadataEntry > math.MaxUint32 {
		return Image{}, formatError(name, 24, ".raptor entry offset is outside the address space")
	}
	if uint32(metadataEntry)&^1 != image.Entry&^1 {
		image.Entry = uint32(metadataEntry)
	}
	entry := image.Entry &^ 1
	if entry < code.Address || uint64(entry) >= uint64(code.Address)+uint64(code.Size) {
		return Image{}, formatError(name, 24, "ELF entry is outside the code section")
	}
	return image, nil
}

type sectionHeader struct {
	name      uint32
	kind      uint32
	flags     uint32
	address   uint32
	offset    uint32
	size      uint32
	link      uint32
	info      uint32
	alignment uint32
	entrySize uint32
}

type addressRange struct {
	start uint64
	end   uint64
	name  string
}
