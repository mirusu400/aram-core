package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
)

const (
	QualcommLegacyTopWindowSize    = 0x1000
	qualcommLegacyTopVersionOffset = 0x0f00
	qualcommLegacyTopIDOffset      = 0x0f04
)

var ErrQualcommLegacyTopMMIO = errors.New("unsupported Qualcomm legacy top-page register")

type QualcommLegacyTopConfig struct {
	Version         uint32
	Identification  uint32
	WritableOffsets []uint32
}

// QualcommLegacyTopPage models the single read-only identification register
// selected by the legacy MSM chip-family path. Its address is at the top of
// the 32-bit physical space; all other top-page accesses remain faults.
type QualcommLegacyTopPage struct {
	version        uint32
	identification uint32
	writable       []uint32
	values         map[uint32]uint32
}

func NewQualcommLegacyTopPage(config QualcommLegacyTopConfig) *QualcommLegacyTopPage {
	page, err := NewQualcommLegacyTopPageWithConfig(config)
	if err != nil {
		panic(err)
	}
	return page
}

func NewQualcommLegacyTopPageWithConfig(
	config QualcommLegacyTopConfig,
) (*QualcommLegacyTopPage, error) {
	writable, err := validateQualcommLegacyTopWritableOffsets(config.WritableOffsets)
	if err != nil {
		return nil, err
	}
	page := &QualcommLegacyTopPage{
		version: config.Version, identification: config.Identification,
		writable: writable, values: make(map[uint32]uint32, len(writable)),
	}
	_ = page.Reset()
	return page, nil
}

func validateQualcommLegacyTopWritableOffsets(offsets []uint32) ([]uint32, error) {
	writable := append([]uint32(nil), offsets...)
	slices.Sort(writable)
	for index, offset := range writable {
		if offset%4 != 0 || offset >= QualcommLegacyTopWindowSize ||
			index != 0 && writable[index-1] == offset {
			return nil, fmt.Errorf("%w: invalid writable offset 0x%x", ErrQualcommLegacyTopMMIO, offset)
		}
	}
	return writable, nil
}

func (d *QualcommLegacyTopPage) Reset() error {
	clear(d.values)
	for _, offset := range d.writable {
		switch offset {
		case qualcommLegacyTopVersionOffset:
			d.values[offset] = d.version
		case qualcommLegacyTopIDOffset:
			d.values[offset] = d.identification
		default:
			d.values[offset] = 0
		}
	}
	return nil
}

func (d *QualcommLegacyTopPage) Read(offset uint32, width Width) (uint32, error) {
	if width == Width32 {
		if value, ok := d.values[offset]; ok {
			return value, nil
		}
		switch offset {
		case qualcommLegacyTopVersionOffset:
			return d.version, nil
		case qualcommLegacyTopIDOffset:
			return d.identification, nil
		}
	}
	return 0, fmt.Errorf(
		"%w: read%d at 0x%x",
		ErrQualcommLegacyTopMMIO, width*8, offset,
	)
}

func (d *QualcommLegacyTopPage) Write(offset uint32, width Width, value uint32) error {
	if width == Width32 {
		if _, ok := d.values[offset]; ok {
			d.values[offset] = value
			return nil
		}
	}
	return fmt.Errorf(
		"%w: write%d value 0x%x at 0x%x",
		ErrQualcommLegacyTopMMIO, width*8, value, offset,
	)
}

func (d *QualcommLegacyTopPage) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("QLTP")
	_ = binary.Write(&output, binary.LittleEndian, uint32(2))
	_ = binary.Write(&output, binary.LittleEndian, d.version)
	_ = binary.Write(&output, binary.LittleEndian, d.identification)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.writable)))
	for _, offset := range d.writable {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = binary.Write(&output, binary.LittleEndian, d.values[offset])
	}
	return output.Bytes(), nil
}

func (d *QualcommLegacyTopPage) LoadState(state []byte) error {
	if len(state) == 16 && string(state[:4]) == "QLTP" &&
		binary.LittleEndian.Uint32(state[4:8]) == 1 {
		if len(d.writable) != 0 || binary.LittleEndian.Uint32(state[8:12]) != d.version ||
			binary.LittleEndian.Uint32(state[12:16]) != d.identification {
			return ErrInvalidState
		}
		return nil
	}
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, hardwareVersion, identification, count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QLTP" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 2 ||
		binary.Read(reader, binary.LittleEndian, &hardwareVersion) != nil || hardwareVersion != d.version ||
		binary.Read(reader, binary.LittleEndian, &identification) != nil || identification != d.identification ||
		binary.Read(reader, binary.LittleEndian, &count) != nil || count != uint32(len(d.writable)) ||
		reader.Len() != int(count)*8 {
		return ErrInvalidState
	}
	values := make(map[uint32]uint32, count)
	for index := range d.writable {
		var offset, value uint32
		if binary.Read(reader, binary.LittleEndian, &offset) != nil || offset != d.writable[index] ||
			binary.Read(reader, binary.LittleEndian, &value) != nil {
			return ErrInvalidState
		}
		values[offset] = value
	}
	if reader.Len() != 0 {
		return ErrInvalidState
	}
	d.values = values
	return nil
}

var (
	_ Device         = (*QualcommLegacyTopPage)(nil)
	_ StatefulDevice = (*QualcommLegacyTopPage)(nil)
)
