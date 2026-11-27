// Package daftest builds synthetic SKT native WIPI-C executables for tests.
// It writes the container the way the loader documents it and contains no
// title bytes.
package daftest

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/loader/daf"
)

// Relocation names one relocated word of a section.
type Relocation struct {
	Offset uint32
	Target daf.Target
}

// Spec describes one executable.
type Spec struct {
	Text            []byte
	Data            []byte
	BSSSize         uint32
	Entries         daf.Entries
	TextRelocations []Relocation
	DataRelocations []Relocation
	Resources       []daf.Resource
}

// Words encodes little-endian words.
func Words(values ...uint32) []byte {
	result := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(result[index*4:], value)
	}
	return result
}

// Build returns the ".daf" bytes for spec.
func Build(spec Spec) []byte {
	relocations := encodeRelocations(spec.TextRelocations, spec.DataRelocations)
	resources := encodeResources(spec.Resources)

	header := make([]byte, daf.HeaderSize)
	copy(header, daf.Magic)
	put := func(offset int, value uint32) { binary.LittleEndian.PutUint32(header[offset:], value) }
	put(0x08, daf.HeaderSize)
	put(0x1c, uint32(len(spec.Text)))
	put(0x20, uint32(len(spec.Data)))
	put(0x24, spec.BSSSize)
	put(0x28, uint32(len(relocations)))
	put(0x30, uint32(len(resources)))
	for index, entry := range []uint32{
		spec.Entries.Start, spec.Entries.Pause, spec.Entries.Resume,
		spec.Entries.Destroy, spec.Entries.Paint, spec.Entries.HandleEvent,
	} {
		put(0x38+index*4, entry)
	}

	outer := make([]byte, daf.OuterHeaderSize)
	binary.LittleEndian.PutUint32(outer, daf.OuterHeaderSize)
	for index := range 16 {
		outer[8+index] = byte(0xa0 + index)
	}
	result := append(outer, header...)
	result = append(result, spec.Text...)
	result = append(result, spec.Data...)
	result = append(result, relocations...)
	return append(result, resources...)
}

func encodeRelocations(text, data []Relocation) []byte {
	textStream := encodeStream(text)
	dataStream := encodeStream(data)
	result := make([]byte, 8, 8+len(textStream)+len(dataStream)+3)
	binary.LittleEndian.PutUint32(result, uint32(len(textStream)))
	binary.LittleEndian.PutUint32(result[4:], uint32(len(dataStream)))
	result = append(result, textStream...)
	result = append(result, dataStream...)
	for len(result)&3 != 0 {
		result = append(result, 0)
	}
	return result
}

// encodeStream writes each entry in the shortest form that holds its
// distance from the previous entry.
func encodeStream(relocations []Relocation) []byte {
	var result []byte
	position := uint32(0)
	for _, relocation := range relocations {
		distance := relocation.Offset - position
		position = relocation.Offset
		if relocation.Target == daf.TargetSelfRelative {
			result = append(result, 0x80|byte(distance>>24&0x0f),
				byte(distance>>16), byte(distance>>8), byte(distance))
			continue
		}
		kind := byte(0)
		if relocation.Target == daf.TargetData {
			kind = 4
		}
		switch {
		case distance < 0x10:
			result = append(result, kind<<4|byte(distance))
		case distance < 0x20:
			result = append(result, (kind|1)<<4|byte(distance-0x10))
		case distance < 0x1000:
			result = append(result, (kind|2)<<4|byte(distance>>8), byte(distance))
		default:
			result = append(result, (kind|3)<<4|byte(distance>>16&0x0f), byte(distance>>8), byte(distance))
		}
	}
	return result
}

func encodeResources(resources []daf.Resource) []byte {
	if len(resources) == 0 {
		return nil
	}
	result := make([]byte, 0x1c+len(resources)*8)
	binary.LittleEndian.PutUint32(result[0x18:], uint32(len(resources)))
	offset := uint32(0)
	for index, resource := range resources {
		binary.LittleEndian.PutUint32(result[0x1c+index*8:], offset)
		binary.LittleEndian.PutUint32(result[0x1c+index*8+4:], uint32(len(resource.Data)))
		offset += uint32(len(resource.Data))
	}
	for _, resource := range resources {
		result = append(result, resource.Name...)
		result = append(result, 0)
	}
	for _, resource := range resources {
		result = append(result, resource.Data...)
	}
	return result
}
