package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

const (
	QualcommPrimaryClockWindowSize = 0x1000
	qualcommPrimaryClockModeOffset = 0x0588
)

var qualcommPrimaryClockWritableOffsets = [...]uint32{0x0574, 0x0578, 0x057c, 0x0580}

var ErrQualcommPrimaryClockMMIO = errors.New("unsupported Qualcomm primary-clock register")

type QualcommPrimaryClockConfig struct {
	Status          uint32
	WritableOffsets []uint32
}

// QualcommPrimaryClockControl exposes the bounded read-only status used
// by early OEM firmware. The stable value is a board fact; unrelated clock
// registers remain explicit faults until their behavior is exercised.
type QualcommPrimaryClockControl struct {
	status          uint32
	writableOffsets []uint32
	registers       map[uint32]uint32
}

func NewQualcommPrimaryClockControl(config QualcommPrimaryClockConfig) (*QualcommPrimaryClockControl, error) {
	if config.Status&^uint32(0xf) != 0 {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock configuration")
	}
	if err := validateQualcommPrimaryClockWritableOffsets(config.WritableOffsets); err != nil {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock writable offsets: %w", err)
	}
	device := &QualcommPrimaryClockControl{
		status:          config.Status,
		writableOffsets: mergedQualcommPrimaryClockWritableOffsets(config.WritableOffsets),
	}
	_ = device.Reset()
	return device, nil
}

func (d *QualcommPrimaryClockControl) Reset() error {
	d.registers = make(map[uint32]uint32, len(d.writableOffsets))
	for _, offset := range d.writableOffsets {
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
	var output bytes.Buffer
	output.WriteString("QPCC")
	_ = binary.Write(&output, binary.LittleEndian, uint32(3))
	_ = binary.Write(&output, binary.LittleEndian, d.status)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.writableOffsets)))
	for _, offset := range d.writableOffsets {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = binary.Write(&output, binary.LittleEndian, d.registers[offset])
	}
	return output.Bytes(), nil
}

func (d *QualcommPrimaryClockControl) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, status, count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QPCC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 3 ||
		binary.Read(reader, binary.LittleEndian, &status) != nil || status != d.status ||
		binary.Read(reader, binary.LittleEndian, &count) != nil ||
		count != uint32(len(d.writableOffsets)) || reader.Len() != int(count)*8 {
		return ErrInvalidState
	}
	registers := make(map[uint32]uint32, count)
	for index := uint32(0); index < count; index++ {
		var offset, value uint32
		if binary.Read(reader, binary.LittleEndian, &offset) != nil ||
			binary.Read(reader, binary.LittleEndian, &value) != nil {
			return ErrInvalidState
		}
		if _, allowed := d.registers[offset]; !allowed {
			return ErrInvalidState
		}
		if _, duplicate := registers[offset]; duplicate {
			return ErrInvalidState
		}
		registers[offset] = value
	}
	d.registers = registers
	return nil
}

func validateQualcommPrimaryClockWritableOffsets(offsets []uint32) error {
	seen := make(map[uint32]struct{}, len(qualcommPrimaryClockWritableOffsets)+len(offsets))
	for _, offset := range qualcommPrimaryClockWritableOffsets {
		seen[offset] = struct{}{}
	}
	for _, offset := range offsets {
		if offset%4 != 0 || offset >= QualcommPrimaryClockWindowSize ||
			offset == qualcommPrimaryClockModeOffset {
			return fmt.Errorf("offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[offset]; duplicate {
			return fmt.Errorf("duplicate offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		seen[offset] = struct{}{}
	}
	return nil
}

func mergedQualcommPrimaryClockWritableOffsets(extra []uint32) []uint32 {
	offsets := make([]uint32, 0, len(qualcommPrimaryClockWritableOffsets)+len(extra))
	offsets = append(offsets, qualcommPrimaryClockWritableOffsets[:]...)
	offsets = append(offsets, extra...)
	sort.Slice(offsets, func(left, right int) bool { return offsets[left] < offsets[right] })
	return offsets
}

var (
	_ Device         = (*QualcommPrimaryClockControl)(nil)
	_ StatefulDevice = (*QualcommPrimaryClockControl)(nil)
)
