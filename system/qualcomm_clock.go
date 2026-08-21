package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	QualcommPrimaryClockWindowSize = 0x1000
	qualcommPrimaryClockModeOffset = 0x0588
)

var qualcommPrimaryClockWritableOffsets = [...]uint32{0x0574, 0x0578, 0x057c, 0x0580}

var ErrQualcommPrimaryClockMMIO = errors.New("unsupported Qualcomm primary-clock register")

type QualcommPrimaryClockConfig struct {
	Status uint32
}

// QualcommPrimaryClockControl exposes the bounded read-only status used
// by early OEM firmware. The stable value is a board fact; unrelated clock
// registers remain explicit faults until their behavior is exercised.
type QualcommPrimaryClockControl struct {
	status    uint32
	registers map[uint32]uint32
}

func NewQualcommPrimaryClockControl(config QualcommPrimaryClockConfig) (*QualcommPrimaryClockControl, error) {
	if config.Status&^uint32(0xf) != 0 {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock configuration")
	}
	device := &QualcommPrimaryClockControl{status: config.Status}
	_ = device.Reset()
	return device, nil
}

func (d *QualcommPrimaryClockControl) Reset() error {
	d.registers = make(map[uint32]uint32, len(qualcommPrimaryClockWritableOffsets))
	for _, offset := range qualcommPrimaryClockWritableOffsets {
		d.registers[offset] = 0
	}
	return nil
}

func (d *QualcommPrimaryClockControl) Read(offset uint32, width Width) (uint32, error) {
	if offset == qualcommPrimaryClockModeOffset && width == Width32 {
		return d.status, nil
	}
	if value, ok := d.registers[offset]; ok && width == Width32 {
		return value, nil
	}
	return 0, fmt.Errorf(
		"%w: read%d at 0x%x",
		ErrQualcommPrimaryClockMMIO, width*8, offset,
	)
}

func (d *QualcommPrimaryClockControl) Write(offset uint32, width Width, value uint32) error {
	if _, ok := d.registers[offset]; ok && width == Width32 {
		d.registers[offset] = value
		return nil
	}
	return fmt.Errorf(
		"%w: write%d value 0x%x at 0x%x",
		ErrQualcommPrimaryClockMMIO, width*8, value, offset,
	)
}

func (d *QualcommPrimaryClockControl) SaveState() ([]byte, error) {
	state := make([]byte, 12+len(qualcommPrimaryClockWritableOffsets)*4)
	copy(state, "QPCC")
	binary.LittleEndian.PutUint32(state[4:8], 2)
	binary.LittleEndian.PutUint32(state[8:12], d.status)
	for index, offset := range qualcommPrimaryClockWritableOffsets {
		binary.LittleEndian.PutUint32(state[12+index*4:], d.registers[offset])
	}
	return state, nil
}

func (d *QualcommPrimaryClockControl) LoadState(state []byte) error {
	if len(state) != 12+len(qualcommPrimaryClockWritableOffsets)*4 || string(state[:4]) != "QPCC" ||
		binary.LittleEndian.Uint32(state[4:8]) != 2 ||
		binary.LittleEndian.Uint32(state[8:12]) != d.status {
		return ErrInvalidState
	}
	registers := make(map[uint32]uint32, len(qualcommPrimaryClockWritableOffsets))
	for index, offset := range qualcommPrimaryClockWritableOffsets {
		registers[offset] = binary.LittleEndian.Uint32(state[12+index*4:])
	}
	d.registers = registers
	return nil
}

var (
	_ Device         = (*QualcommPrimaryClockControl)(nil)
	_ StatefulDevice = (*QualcommPrimaryClockControl)(nil)
)
