package gvm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// AddressRegion selects a region of an explicitly configured address space.
// The zero value is invalid. Regions do not encode native host pointers.
type AddressRegion uint8

const (
	AddressFile AddressRegion = iota + 1
	AddressRAM
)

// AddressSymbol binds a shared view with byte offsets relative to Region,
// never relative to the whole program. Empty and odd spans are permitted.
// The region also selects the opcode4d encoding base. Mismatched native
// descriptor-type/pointer-region pairs are deliberately not represented.
type AddressSymbol struct {
	Region AddressRegion
	Offset uint32
	Length uint32
}

// AddressSpace describes the complete file symbol-data range within program
// and the complete mutable symbol RAM range, excluding descriptors and media.
// The caller supplies all initialization and bindings, including any reserved
// symbols. Construction copies program, RAM and binding metadata.
type AddressSpace struct {
	FileStart  uint32
	FileLength uint32
	RAM        []byte
	Symbols    []AddressSymbol
}

type addressBinding struct {
	region AddressRegion
	offset uint32
}

type addressMemory struct {
	file     []byte
	ram      []byte
	bindings []addressBinding
}

// ErrInvalidAddress reports an unavailable address space or an unresolvable
// tagged word address. ReadWord errors do not alter execution or sticky faults.
var ErrInvalidAddress = errors.New("gvm: invalid word address")

// NewWithAddressSpace validates all spans using uint64 before allocating VM
// storage. It clones program once and RAM once, preserving all overlapping
// views within each region. File writes remain coherent with instruction fetch.
// This is an explicit host binding API, not image loading or game startup.
func NewWithAddressSpace(program []byte, entry uint32, space AddressSpace) (*VM, error) {
	if uint64(entry) >= uint64(len(program)) {
		return nil, fmt.Errorf("%w: entry offset %d", ErrInvalidTarget, entry)
	}
	fileEnd := uint64(space.FileStart) + uint64(space.FileLength)
	if fileEnd > uint64(len(program)) {
		return nil, fmt.Errorf("%w: file span", ErrInvalidSymbolRegion)
	}
	for i, s := range space.Symbols {
		var limit uint64
		switch s.Region {
		case AddressFile:
			limit = uint64(space.FileLength)
		case AddressRAM:
			limit = uint64(len(space.RAM))
		default:
			return nil, fmt.Errorf("%w: symbol %d region", ErrInvalidSymbolRegion, i)
		}
		if uint64(s.Offset)+uint64(s.Length) > limit {
			return nil, fmt.Errorf("%w: symbol %d span", ErrInvalidSymbolRegion, i)
		}
	}
	v := New(program)
	v.pc = int(entry)
	v.address = &addressMemory{file: v.code[int(space.FileStart):int(fileEnd):int(fileEnd)], ram: append([]byte(nil), space.RAM...), bindings: make([]addressBinding, len(space.Symbols))}
	v.symbols = make([][]byte, len(space.Symbols))
	for i, s := range space.Symbols {
		region := v.address.file
		if s.Region == AddressRAM {
			region = v.address.ram
		}
		start, end := int(s.Offset), int(uint64(s.Offset)+uint64(s.Length))
		v.symbols[i] = region[start:end:end]
		v.address.bindings[i] = addressBinding{region: s.Region, offset: s.Offset}
	}
	return v, nil
}

// ReadWord resolves a tagged word address to a scalar LE16 snapshot. Bit14
// selects file and only that bit is cleared before signed16 validation. The
// index must be within floor(region byte length/2), with a full two-byte span.
// Resolution uses the complete region, not the originating symbol's length.
// Truncation, odd-offset rounding and tag collisions are not corrected, and no
// host pointer or mutable view is returned. Old constructors have no space.
func (v *VM) ReadWord(address uint16) (uint16, error) {
	if v.address == nil {
		return 0, ErrInvalidAddress
	}
	region := v.address.ram
	index := address
	if index&0x4000 != 0 {
		region = v.address.file
		index &^= 0x4000
	}
	if int16(index) < 0 || uint64(index) >= uint64(len(region))>>1 {
		return 0, ErrInvalidAddress
	}
	offset := uint64(index) * 2
	if offset+2 > uint64(len(region)) {
		return 0, ErrInvalidAddress
	}
	return binary.LittleEndian.Uint16(region[int(offset):int(offset+2)]), nil
}
