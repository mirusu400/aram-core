package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

var ErrSparseWordRegistersMMIO = errors.New("unsupported sparse word register")

// SparseWordRegisters provides persistent storage for an explicitly listed
// set of aligned 32-bit registers. It is useful while a board's register
// layout is known but the side effects belong to a later hardware model.
type SparseWordRegisters struct {
	offsets   []uint32
	resets    map[uint32]uint32
	registers map[uint32]uint32
}

type SparseWordRegisterReset struct {
	Offset uint32
	Value  uint32
}

type SparseWordRegistersConfig struct {
	Offsets []uint32
	Resets  []SparseWordRegisterReset
}

func NewSparseWordRegisters(offsets []uint32) (*SparseWordRegisters, error) {
	return NewSparseWordRegistersWithConfig(SparseWordRegistersConfig{Offsets: offsets})
}

func NewSparseWordRegistersWithConfig(config SparseWordRegistersConfig) (*SparseWordRegisters, error) {
	if len(config.Offsets) == 0 {
		return nil, fmt.Errorf("sparse word register file has no offsets")
	}
	ordered := append([]uint32(nil), config.Offsets...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	allowed := make(map[uint32]struct{}, len(ordered))
	for index, offset := range ordered {
		if offset%4 != 0 || index > 0 && offset == ordered[index-1] {
			return nil, fmt.Errorf("invalid sparse word register offset 0x%x", offset)
		}
		allowed[offset] = struct{}{}
	}
	resets := make(map[uint32]uint32, len(config.Resets))
	for _, reset := range config.Resets {
		if _, ok := allowed[reset.Offset]; !ok {
			return nil, fmt.Errorf("sparse word register reset at unsupported offset 0x%x", reset.Offset)
		}
		if _, duplicate := resets[reset.Offset]; duplicate {
			return nil, fmt.Errorf("duplicate sparse word register reset at 0x%x", reset.Offset)
		}
		resets[reset.Offset] = reset.Value
	}
	device := &SparseWordRegisters{offsets: ordered, resets: resets}
	_ = device.Reset()
	return device, nil
}

func (d *SparseWordRegisters) Reset() error {
	d.registers = make(map[uint32]uint32, len(d.offsets))
	for _, offset := range d.offsets {
		d.registers[offset] = d.resets[offset]
	}
	return nil
}

func (d *SparseWordRegisters) Read(offset uint32, width Width) (uint32, error) {
	value, ok := d.registers[offset]
	if width != Width32 || !ok {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrSparseWordRegistersMMIO, width*8, offset)
	}
	return value, nil
}

func (d *SparseWordRegisters) Write(offset uint32, width Width, value uint32) error {
	if _, ok := d.registers[offset]; width != Width32 || !ok {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrSparseWordRegistersMMIO, width*8, value, offset,
		)
	}
	d.registers[offset] = value
	return nil
}

func (d *SparseWordRegisters) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("SWRF")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.offsets)))
	for _, offset := range d.offsets {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = binary.Write(&output, binary.LittleEndian, d.registers[offset])
	}
	return output.Bytes(), nil
}

func (d *SparseWordRegisters) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "SWRF" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 ||
		binary.Read(reader, binary.LittleEndian, &count) != nil || int(count) != len(d.offsets) {
		return ErrInvalidState
	}
	registers := make(map[uint32]uint32, len(d.offsets))
	for index, wantOffset := range d.offsets {
		var offset, value uint32
		if binary.Read(reader, binary.LittleEndian, &offset) != nil || offset != wantOffset ||
			binary.Read(reader, binary.LittleEndian, &value) != nil {
			return ErrInvalidState
		}
		registers[d.offsets[index]] = value
	}
	if reader.Len() != 0 {
		return ErrInvalidState
	}
	d.registers = registers
	return nil
}

var (
	_ Device         = (*SparseWordRegisters)(nil)
	_ StatefulDevice = (*SparseWordRegisters)(nil)
)
