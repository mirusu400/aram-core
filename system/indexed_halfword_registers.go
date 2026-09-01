package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

var ErrIndexedHalfwordRegistersMMIO = errors.New("unsupported indexed halfword-register access")

// IndexedHalfwordRegisters models a register selector and a sparse bank of
// 16-bit values behind two external-bus ports. Register values have no implied
// side effects; this device records only behavior established at the bus
// boundary.
type IndexedHalfwordRegisters struct {
	commandReadValue uint16
	selected         uint16
	registers        map[uint16]uint16
}

// IndexedHalfwordRegisterPort exposes either the command/status or data half
// of an IndexedHalfwordRegisters device. The command port owns shared reset and
// serialization so mapping both aliases does not duplicate device state.
type IndexedHalfwordRegisterPort struct {
	registers *IndexedHalfwordRegisters
	data      bool
}

func NewIndexedHalfwordRegisters(commandReadValue uint16) *IndexedHalfwordRegisters {
	registers := &IndexedHalfwordRegisters{commandReadValue: commandReadValue}
	_ = registers.Reset()
	return registers
}

func NewIndexedHalfwordCommandPort(
	registers *IndexedHalfwordRegisters,
) (*IndexedHalfwordRegisterPort, error) {
	return newIndexedHalfwordRegisterPort(registers, false)
}

func NewIndexedHalfwordDataPort(
	registers *IndexedHalfwordRegisters,
) (*IndexedHalfwordRegisterPort, error) {
	return newIndexedHalfwordRegisterPort(registers, true)
}

func newIndexedHalfwordRegisterPort(
	registers *IndexedHalfwordRegisters,
	data bool,
) (*IndexedHalfwordRegisterPort, error) {
	if registers == nil {
		return nil, fmt.Errorf("indexed halfword-register port has no register bank")
	}
	return &IndexedHalfwordRegisterPort{registers: registers, data: data}, nil
}

func (d *IndexedHalfwordRegisters) Reset() error {
	d.selected = 0
	d.registers = make(map[uint16]uint16)
	return nil
}

func (d *IndexedHalfwordRegisters) SaveState() ([]byte, error) {
	indices := make([]int, 0, len(d.registers))
	for index := range d.registers {
		indices = append(indices, int(index))
	}
	sort.Ints(indices)

	var output bytes.Buffer
	output.WriteString("IHRG")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_ = binary.Write(&output, binary.LittleEndian, d.commandReadValue)
	_ = binary.Write(&output, binary.LittleEndian, d.selected)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(indices)))
	for _, rawIndex := range indices {
		index := uint16(rawIndex)
		_ = binary.Write(&output, binary.LittleEndian, index)
		_ = binary.Write(&output, binary.LittleEndian, d.registers[index])
	}
	return output.Bytes(), nil
}

func (d *IndexedHalfwordRegisters) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version uint32
	var commandReadValue, selected uint16
	var count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "IHRG" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 ||
		binary.Read(reader, binary.LittleEndian, &commandReadValue) != nil ||
		commandReadValue != d.commandReadValue ||
		binary.Read(reader, binary.LittleEndian, &selected) != nil ||
		binary.Read(reader, binary.LittleEndian, &count) != nil ||
		uint64(count)*4 != uint64(reader.Len()) {
		return ErrInvalidState
	}
	registers := make(map[uint16]uint16, count)
	var previous uint16
	for index := uint32(0); index < count; index++ {
		var register, value uint16
		if binary.Read(reader, binary.LittleEndian, &register) != nil ||
			binary.Read(reader, binary.LittleEndian, &value) != nil ||
			index != 0 && register <= previous {
			return ErrInvalidState
		}
		registers[register] = value
		previous = register
	}
	d.selected = selected
	d.registers = registers
	return nil
}

func (p *IndexedHalfwordRegisterPort) Reset() error {
	if p.data {
		return nil
	}
	return p.registers.Reset()
}

func (p *IndexedHalfwordRegisterPort) validAccess(offset uint32, width Width) bool {
	return offset == 0 && width == Width16
}

func (p *IndexedHalfwordRegisterPort) Read(offset uint32, width Width) (uint32, error) {
	if !p.validAccess(offset, width) {
		return 0, fmt.Errorf(
			"%w: read%d at sparse-port offset 0x%x",
			ErrIndexedHalfwordRegistersMMIO,
			width*8,
			offset,
		)
	}
	if !p.data {
		return uint32(p.registers.commandReadValue), nil
	}
	return uint32(p.registers.registers[p.registers.selected]), nil
}

func (p *IndexedHalfwordRegisterPort) Write(offset uint32, width Width, value uint32) error {
	if !p.validAccess(offset, width) || value > 0xffff {
		return fmt.Errorf(
			"%w: write%d value 0x%x at sparse-port offset 0x%x",
			ErrIndexedHalfwordRegistersMMIO,
			width*8,
			value,
			offset,
		)
	}
	if p.data {
		p.registers.registers[p.registers.selected] = uint16(value)
	} else {
		p.registers.selected = uint16(value)
	}
	return nil
}

func (p *IndexedHalfwordRegisterPort) SaveState() ([]byte, error) {
	header := make([]byte, 9)
	copy(header, "IHPT")
	binary.LittleEndian.PutUint32(header[4:8], 1)
	if p.data {
		header[8] = 1
		return header, nil
	}
	state, err := p.registers.SaveState()
	if err != nil {
		return nil, err
	}
	return append(header, state...), nil
}

func (p *IndexedHalfwordRegisterPort) LoadState(state []byte) error {
	wantData := byte(0)
	if p.data {
		wantData = 1
	}
	if len(state) < 9 || string(state[:4]) != "IHPT" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || state[8] != wantData {
		return ErrInvalidState
	}
	if p.data {
		if len(state) != 9 {
			return ErrInvalidState
		}
		return nil
	}
	return p.registers.LoadState(state[9:])
}

var _ StatefulDevice = (*IndexedHalfwordRegisterPort)(nil)
