package abhs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
)

const (
	Version         = 0x1000
	RelocationMagic = 0xf0123456
	maxDescriptors  = 16
)

var Magic = []byte("ABHS")

type Descriptor struct {
	Type       uint32
	Size       uint32
	FileOffset uint32
}

type Module struct {
	RecordOffset      uint32
	RecordSize        uint32
	Version           uint32
	Descriptors       []Descriptor
	Metadata          Descriptor
	Code              Descriptor
	Relocations       Descriptor
	RelocationOffsets []uint32
	EntryOffset       uint32
	MetadataMode      uint32
}

func (m Module) RecordEnd() uint32 {
	return m.RecordOffset + m.RecordSize
}

func (m Module) Thumb() bool {
	return m.EntryOffset&1 != 0
}

func (m Module) AbsoluteSectionOffset(descriptor Descriptor) uint64 {
	return uint64(m.RecordOffset) + uint64(descriptor.FileOffset)
}

type LoadedModule struct {
	Module    Module
	GuestBase uint32
	GuestInit uint32
	GuestFini uint32
	Image     []byte
}

// GuestEntry is a compatibility alias for the metadata-provided finalizer.
func (m LoadedModule) GuestEntry() uint32 {
	return m.GuestFini
}

type FormatError struct {
	Offset uint64
	Reason string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("ABHS at 0x%x: %s", e.Offset, e.Reason)
}

func Parse(data []byte, recordOffset uint32) (Module, error) {
	fail := func(offset uint64, reason string) (Module, error) {
		return Module{}, &FormatError{Offset: offset, Reason: reason}
	}
	recordStart := uint64(recordOffset)
	if !validRange(recordStart, 16, uint64(len(data))) {
		return fail(recordStart, "truncated header")
	}
	if !bytes.Equal(data[recordStart:recordStart+4], Magic) {
		return fail(recordStart, "magic mismatch")
	}

	version := u32(data, recordStart+4)
	tableRelative := u32(data, recordStart+8)
	count := u32(data, recordStart+12)
	if version != Version {
		return fail(recordStart+4, fmt.Sprintf("unsupported version 0x%08x", version))
	}
	if tableRelative < 16 || count == 0 || count > maxDescriptors {
		return fail(recordStart+8, "invalid descriptor table geometry")
	}

	tableOffset := recordStart + uint64(tableRelative)
	if !validRange(tableOffset, uint64(count)*12, uint64(len(data))) {
		return fail(tableOffset, "descriptor table exceeds input")
	}

	module := Module{
		RecordOffset: recordOffset,
		Version:      version,
		Descriptors:  make([]Descriptor, 0, count),
	}
	found := map[uint32]bool{}
	recordEnd := tableOffset + uint64(count)*12
	for index := uint32(0); index < count; index++ {
		position := tableOffset + uint64(index)*12
		descriptor := Descriptor{
			Type:       u32(data, position),
			Size:       u32(data, position+4),
			FileOffset: u32(data, position+8),
		}
		if descriptor.Type > 2 || found[descriptor.Type] {
			return fail(position, "invalid or duplicate descriptor type")
		}
		absolute := recordStart + uint64(descriptor.FileOffset)
		if !validRange(absolute, uint64(descriptor.Size), uint64(len(data))) {
			return fail(position+8, fmt.Sprintf("section %d exceeds input", descriptor.Type))
		}
		if end := absolute + uint64(descriptor.Size); end > recordEnd {
			recordEnd = end
		}
		found[descriptor.Type] = true
		module.Descriptors = append(module.Descriptors, descriptor)
		switch descriptor.Type {
		case 0:
			module.Metadata = descriptor
		case 1:
			module.Code = descriptor
		case 2:
			module.Relocations = descriptor
		}
	}
	if len(found) != 3 {
		return fail(tableOffset, "metadata, code, and relocation sections are required")
	}
	if module.Metadata.Size < 60 || module.Code.Size < 4 || module.Relocations.Size < 20 {
		return fail(tableOffset, "section is smaller than its fixed header")
	}

	relocationStart := recordStart + uint64(module.Relocations.FileOffset)
	kind := u32(data, relocationStart)
	relocationCount := u32(data, relocationStart+4)
	encodedSize := u32(data, relocationStart+8)
	magic := u32(data, relocationStart+12)
	expectedSize := uint64(16) + uint64(relocationCount)*4 + 4
	if kind != 1 || encodedSize != module.Relocations.Size ||
		magic != RelocationMagic || expectedSize != uint64(module.Relocations.Size) {
		return fail(relocationStart, "invalid relocation header")
	}
	terminator := relocationStart + 16 + uint64(relocationCount)*4
	if u32(data, terminator) != 0xffffffff {
		return fail(terminator, "missing relocation terminator")
	}
	module.RelocationOffsets = make([]uint32, relocationCount)
	for index := uint32(0); index < relocationCount; index++ {
		position := relocationStart + 16 + uint64(index)*4
		value := u32(data, position)
		if value&3 != 0 || value > module.Code.Size-4 {
			return fail(position, fmt.Sprintf("relocation 0x%x is outside aligned code", value))
		}
		module.RelocationOffsets[index] = value
	}

	metadataStart := recordStart + uint64(module.Metadata.FileOffset)
	module.MetadataMode = u32(data, metadataStart+52)
	module.EntryOffset = u32(data, metadataStart+56)
	if module.EntryOffset&^uint32(1) >= module.Code.Size {
		return fail(metadataStart+56, "entry offset exceeds code")
	}
	recordSize := recordEnd - recordStart
	if recordEnd > math.MaxUint32 || recordSize > math.MaxUint32 {
		return fail(recordStart, "record end exceeds 32-bit format")
	}
	module.RecordSize = uint32(recordSize)
	return module, nil
}

// Load copies a previously parsed module's code and applies the validated
// firmware relocation operation:
//
//	*(guestBase + relocationOffset) += guestBase
//
// The returned image is a 32-bit guest image. It is never executable host
// memory.
func Load(data []byte, module Module, guestBase uint32) (LoadedModule, error) {
	verified, err := Parse(data, module.RecordOffset)
	if err != nil {
		return LoadedModule{}, err
	}
	if !reflect.DeepEqual(verified, module) {
		return LoadedModule{}, &FormatError{
			Offset: uint64(module.RecordOffset),
			Reason: "module metadata does not match input",
		}
	}
	if uint64(guestBase)+uint64(module.Code.Size) > 1<<32 {
		return LoadedModule{}, &FormatError{
			Offset: uint64(module.RecordOffset),
			Reason: "loaded code exceeds 32-bit guest address space",
		}
	}

	codeStart := module.AbsoluteSectionOffset(module.Code)
	codeEnd := codeStart + uint64(module.Code.Size)
	image := append([]byte(nil), data[codeStart:codeEnd]...)
	for _, relocation := range module.RelocationOffsets {
		position := uint64(relocation)
		value := binary.LittleEndian.Uint32(image[position : position+4])
		binary.LittleEndian.PutUint32(image[position:position+4], value+guestBase)
	}

	guestInit := guestBase
	if module.MetadataMode != 0 {
		guestInit++
	}
	guestFini := guestBase + (module.EntryOffset &^ uint32(1))
	if module.Thumb() {
		guestFini++
	}
	return LoadedModule{
		Module:    module,
		GuestBase: guestBase,
		GuestInit: guestInit,
		GuestFini: guestFini,
		Image:     image,
	}, nil
}

func Inspect(data []byte) []Module {
	var modules []Module
	for start := 0; start < len(data); {
		index := bytes.Index(data[start:], Magic)
		if index < 0 {
			break
		}
		offset := start + index
		module, err := Parse(data, uint32(offset))
		if err == nil {
			modules = append(modules, module)
			start = int(module.RecordEnd())
			continue
		}
		start = offset + 1
	}
	return modules
}

func validRange(offset, size, total uint64) bool {
	return offset <= total && size <= total-offset
}

func u32(data []byte, offset uint64) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}
