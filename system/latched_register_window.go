package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrLatchedRegisterWindowMMIO = errors.New("unsupported latched register-window access")

// LatchedRegisterWindow models a profile-declared bank of equally sized MMIO
// registers. Values persist until reset, but no device-specific side effects
// are implied. Requiring the declared width and alignment keeps this narrower
// than a permissive RAM mapping while hardware behavior is still unknown.
type LatchedRegisterWindow struct {
	width Width
	data  []byte
}

func NewLatchedRegisterWindow(size uint32, width Width) (*LatchedRegisterWindow, error) {
	if width != Width8 && width != Width16 && width != Width32 ||
		size == 0 || size%uint32(width) != 0 ||
		uint64(size) > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("invalid latched register-window size/width")
	}
	return &LatchedRegisterWindow{width: width, data: make([]byte, int(size))}, nil
}

func (d *LatchedRegisterWindow) Reset() error {
	clear(d.data)
	return nil
}

func (d *LatchedRegisterWindow) validAccess(offset uint32, width Width) bool {
	return width == d.width && offset%uint32(d.width) == 0 &&
		uint64(offset)+uint64(width) <= uint64(len(d.data))
}

func (d *LatchedRegisterWindow) Read(offset uint32, width Width) (uint32, error) {
	if !d.validAccess(offset, width) {
		return 0, fmt.Errorf(
			"%w: read%d at 0x%x",
			ErrLatchedRegisterWindowMMIO,
			width*8,
			offset,
		)
	}
	start := int(offset)
	switch d.width {
	case Width8:
		return uint32(d.data[start]), nil
	case Width16:
		return uint32(binary.LittleEndian.Uint16(d.data[start : start+2])), nil
	default:
		return binary.LittleEndian.Uint32(d.data[start : start+4]), nil
	}
}

func (d *LatchedRegisterWindow) Write(offset uint32, width Width, value uint32) error {
	if !d.validAccess(offset, width) ||
		d.width < Width32 && value >= uint32(1)<<(uint32(d.width)*8) {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrLatchedRegisterWindowMMIO,
			width*8,
			value,
			offset,
		)
	}
	start := int(offset)
	switch d.width {
	case Width8:
		d.data[start] = byte(value)
	case Width16:
		binary.LittleEndian.PutUint16(d.data[start:start+2], uint16(value))
	default:
		binary.LittleEndian.PutUint32(d.data[start:start+4], value)
	}
	return nil
}

func (d *LatchedRegisterWindow) SaveState() ([]byte, error) {
	state := make([]byte, 16+len(d.data))
	copy(state, "LRWN")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	state[8] = byte(d.width)
	binary.LittleEndian.PutUint32(state[12:16], uint32(len(d.data)))
	copy(state[16:], d.data)
	return state, nil
}

func (d *LatchedRegisterWindow) LoadState(state []byte) error {
	if len(state) < 16 || string(state[:4]) != "LRWN" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || Width(state[8]) != d.width ||
		state[9] != 0 || state[10] != 0 || state[11] != 0 ||
		binary.LittleEndian.Uint32(state[12:16]) != uint32(len(d.data)) ||
		len(state) != 16+len(d.data) {
		return ErrInvalidState
	}
	copy(d.data, state[16:])
	return nil
}

var (
	_ Device         = (*LatchedRegisterWindow)(nil)
	_ StatefulDevice = (*LatchedRegisterWindow)(nil)
)
