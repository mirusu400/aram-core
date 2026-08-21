package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	QualcommLegacyTopWindowSize    = 0x1000
	qualcommLegacyTopVersionOffset = 0x0f00
	qualcommLegacyTopIDOffset      = 0x0f04
)

var ErrQualcommLegacyTopMMIO = errors.New("unsupported Qualcomm legacy top-page register")

type QualcommLegacyTopConfig struct {
	Version        uint32
	Identification uint32
}

// QualcommLegacyTopPage models the single read-only identification register
// selected by the legacy MSM chip-family path. Its address is at the top of
// the 32-bit physical space; all other top-page accesses remain faults.
type QualcommLegacyTopPage struct {
	version        uint32
	identification uint32
}

func NewQualcommLegacyTopPage(config QualcommLegacyTopConfig) *QualcommLegacyTopPage {
	return &QualcommLegacyTopPage{
		version: config.Version, identification: config.Identification,
	}
}

func (d *QualcommLegacyTopPage) Reset() error {
	return nil
}

func (d *QualcommLegacyTopPage) Read(offset uint32, width Width) (uint32, error) {
	if width == Width32 {
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
	return fmt.Errorf(
		"%w: write%d value 0x%x at 0x%x",
		ErrQualcommLegacyTopMMIO, width*8, value, offset,
	)
}

func (d *QualcommLegacyTopPage) SaveState() ([]byte, error) {
	state := make([]byte, 16)
	copy(state, "QLTP")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	binary.LittleEndian.PutUint32(state[8:12], d.version)
	binary.LittleEndian.PutUint32(state[12:16], d.identification)
	return state, nil
}

func (d *QualcommLegacyTopPage) LoadState(state []byte) error {
	if len(state) != 16 || string(state[:4]) != "QLTP" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 ||
		binary.LittleEndian.Uint32(state[8:12]) != d.version ||
		binary.LittleEndian.Uint32(state[12:16]) != d.identification {
		return ErrInvalidState
	}
	return nil
}

var (
	_ Device         = (*QualcommLegacyTopPage)(nil)
	_ StatefulDevice = (*QualcommLegacyTopPage)(nil)
)
