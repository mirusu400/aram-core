// Package pxo parses the native PXO1 executable format used by classic LG
// handsets. It treats every offset and compressed fixup stream as untrusted.
package pxo

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Magic          = "PXO1"
	HeaderSize     = 0x30
	DescriptorSize = 7 * 4
	FixupGroups    = 3
)

var (
	ErrNotPXO = errors.New("not a PXO1 executable")
	endMarker = []byte("EndOfPXE\x00")
)

// FormatError reports the file offset at which a malformed PXO1 structure was
// observed.
type FormatError struct {
	Offset int
	Reason string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("PXO1 at offset 0x%x: %s", e.Offset, e.Reason)
}

// Header is the fixed PXO1 file header. The meaning of the reserved fields is
// not assumed; observed C260 executables use the exact values validated here.
type Header struct {
	Flags          uint32
	FileSize       uint32
	ImageOffset    uint32
	FixupCount     uint32
	Reserved14     uint32
	Reserved18     uint32
	Reserved1C     uint32
	Reserved20     uint32
	HeaderSizeHint uint32
	Reserved28     uint32
	Reserved2C     uint32
}

// Descriptor is stored at the start of the load image. DataEnd bytes are
// copied from the file and ZeroSize additional bytes are mapped as zero-filled
// writable memory. Entry is a Thumb function pointer and GOT identifies the
// twelve-word host dispatch table populated by the handset loader.
type Descriptor struct {
	DataStart uint32
	DataSize  uint32
	DataEnd   uint32
	ZeroSize  uint32
	DataLoad  uint32
	Entry     uint32
	GOT       uint32
}

// Package is one validated PXO1 executable. Image excludes the trailing
// EndOfPXE marker and includes exactly Descriptor.DataEnd load bytes.
type Package struct {
	Header     Header
	Descriptor Descriptor
	Fixups     [FixupGroups][]uint32
	Image      []byte
}

// Inspect validates and copies a PXO1 executable.
func Inspect(data []byte) (*Package, error) {
	if len(data) < 4 || string(data[:4]) != Magic {
		return nil, ErrNotPXO
	}
	if len(data) < HeaderSize {
		return nil, malformed(len(data), "header is truncated")
	}
	header := Header{
		Flags:          word(data, 0x04),
		FileSize:       word(data, 0x08),
		ImageOffset:    word(data, 0x0c),
		FixupCount:     word(data, 0x10),
		Reserved14:     word(data, 0x14),
		Reserved18:     word(data, 0x18),
		Reserved1C:     word(data, 0x1c),
		Reserved20:     word(data, 0x20),
		HeaderSizeHint: word(data, 0x24),
		Reserved28:     word(data, 0x28),
		Reserved2C:     word(data, 0x2c),
	}
	if uint64(header.FileSize) != uint64(len(data)) {
		return nil, malformed(0x08, fmt.Sprintf("declared size %d does not match file size %d", header.FileSize, len(data)))
	}
	if header.Flags != 0 || header.Reserved14 != ^uint32(0) ||
		header.Reserved18 != 0 || header.Reserved1C != 0 ||
		header.Reserved20 != ^uint32(0) || header.HeaderSizeHint != 0x20 ||
		header.Reserved28 != 0 || header.Reserved2C != ^uint32(0) {
		return nil, malformed(0x04, "unsupported PXO1 header variant")
	}
	imageOffset := uint64(header.ImageOffset)
	if imageOffset < HeaderSize || imageOffset+DescriptorSize+uint64(len(endMarker)) > uint64(len(data)) {
		return nil, malformed(0x0c, "load image offset is outside the file")
	}

	fixups, parsed, err := parseFixups(data, HeaderSize, int(header.ImageOffset))
	if err != nil {
		return nil, err
	}
	if parsed != int(header.ImageOffset) {
		return nil, malformed(parsed, "unexpected bytes after fixup streams")
	}
	count := 0
	for _, group := range fixups {
		count += len(group)
	}
	if uint64(count) != uint64(header.FixupCount) {
		return nil, malformed(0x10, fmt.Sprintf("declared %d fixups, decoded %d", header.FixupCount, count))
	}

	descriptorOffset := int(header.ImageOffset)
	descriptor := Descriptor{
		DataStart: word(data, descriptorOffset+0x00),
		DataSize:  word(data, descriptorOffset+0x04),
		DataEnd:   word(data, descriptorOffset+0x08),
		ZeroSize:  word(data, descriptorOffset+0x0c),
		DataLoad:  word(data, descriptorOffset+0x10),
		Entry:     word(data, descriptorOffset+0x14),
		GOT:       word(data, descriptorOffset+0x18),
	}
	if uint64(descriptor.DataStart)+uint64(descriptor.DataSize) != uint64(descriptor.DataEnd) {
		return nil, malformed(descriptorOffset, "data start and size do not reach data end")
	}
	if descriptor.DataLoad != descriptor.DataStart {
		return nil, malformed(descriptorOffset+0x10, "split data load addresses are unsupported")
	}
	if descriptor.Entry&1 == 0 || descriptor.Entry >= descriptor.DataEnd {
		return nil, malformed(descriptorOffset+0x14, "entry is not an in-image Thumb function")
	}
	if descriptor.GOT&3 != 0 || uint64(descriptor.GOT)+12*4 > uint64(descriptor.DataEnd) {
		return nil, malformed(descriptorOffset+0x18, "host dispatch table is outside the load image")
	}
	markerOffset := imageOffset + uint64(descriptor.DataEnd)
	if markerOffset+uint64(len(endMarker)) != uint64(len(data)) {
		return nil, malformed(descriptorOffset+0x08, "data end does not precede the EndOfPXE marker")
	}
	if string(data[markerOffset:]) != string(endMarker) {
		return nil, malformed(int(markerOffset), "EndOfPXE marker is missing")
	}
	if uint64(descriptor.DataEnd)+uint64(descriptor.ZeroSize) > uint64(^uint32(0)) {
		return nil, malformed(descriptorOffset+0x0c, "mapped image size overflows 32-bit address space")
	}
	for kind, group := range fixups {
		for _, offset := range group {
			if offset&3 != 0 || uint64(offset)+4 > uint64(descriptor.DataEnd) {
				return nil, malformed(HeaderSize, fmt.Sprintf("fixup group %d contains invalid image offset 0x%x", kind, offset))
			}
		}
	}

	image := append([]byte(nil), data[imageOffset:markerOffset]...)
	return &Package{Header: header, Descriptor: descriptor, Fixups: fixups, Image: image}, nil
}

// MappedSize returns initialized plus zero-filled guest memory bytes.
func (p *Package) MappedSize() uint32 {
	return p.Descriptor.DataEnd + p.Descriptor.ZeroSize
}

// Relocate copies the load image and applies the established absolute-word
// fixup stream for base. The semantics of the two remaining streams have not
// been established, so they are validated by Inspect and otherwise preserved.
func (p *Package) Relocate(base uint32) ([]byte, error) {
	if p == nil || len(p.Image) == 0 {
		return nil, fmt.Errorf("relocate PXO1: package is empty")
	}
	result := append([]byte(nil), p.Image...)
	for _, offset := range p.Fixups[0] {
		value := binary.LittleEndian.Uint32(result[offset : offset+4])
		if uint64(value)+uint64(base) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("relocate PXO1 word at image offset 0x%x: address overflows", offset)
		}
		binary.LittleEndian.PutUint32(result[offset:offset+4], value+base)
	}
	return result, nil
}

func parseFixups(data []byte, start, end int) ([FixupGroups][]uint32, int, error) {
	var groups [FixupGroups][]uint32
	cursor := start
	for kind := range groups {
		if cursor >= end {
			continue
		}
		if data[cursor] == 0xff {
			cursor++
			continue
		}
		if cursor+3 > end {
			return groups, cursor, malformed(cursor, "truncated initial fixup offset")
		}
		current := uint32(data[cursor]) | uint32(data[cursor+1])<<8 | uint32(data[cursor+2])<<16
		cursor += 3
		groups[kind] = append(groups[kind], current)
		for cursor < end && data[cursor] != 0xff {
			delta := uint32(data[cursor])
			cursor++
			if delta == 0 {
				if cursor+3 > end {
					return groups, cursor - 1, malformed(cursor-1, "truncated extended fixup delta")
				}
				delta = uint32(data[cursor]) | uint32(data[cursor+1])<<8 | uint32(data[cursor+2])<<16
				cursor += 3
				if delta == 0 {
					return groups, cursor - 3, malformed(cursor-3, "zero fixup delta")
				}
			}
			if current > 0xffffff-delta {
				return groups, cursor, malformed(cursor, "fixup offset exceeds 24 bits")
			}
			current += delta
			groups[kind] = append(groups[kind], current)
		}
		if cursor < end {
			cursor++
		}
	}
	for cursor < end && data[cursor] == 0xff {
		cursor++
	}
	return groups, cursor, nil
}

func word(data []byte, offset int) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}

func malformed(offset int, reason string) error {
	return &FormatError{Offset: offset, Reason: reason}
}
