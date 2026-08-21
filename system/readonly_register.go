package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrReadOnlyRegisterMMIO = errors.New("unsupported read-only register access")

// ReadOnlyRegister is a narrow, board-profile-supplied MMIO fact. It is useful
// for straps and external-bus identification pins whose value is fixed for a
// machine variant; unknown offsets, widths, and all writes remain faults.
type ReadOnlyRegister struct {
	width Width
	value uint32
}

func NewReadOnlyRegister(width Width, value uint32) (*ReadOnlyRegister, error) {
	if width != Width8 && width != Width16 && width != Width32 {
		return nil, fmt.Errorf("%w: invalid width %d", ErrReadOnlyRegisterMMIO, width)
	}
	if width < Width32 && value >= uint32(1)<<(uint32(width)*8) {
		return nil, fmt.Errorf("%w: value 0x%x exceeds width %d", ErrReadOnlyRegisterMMIO, value, width)
	}
	return &ReadOnlyRegister{width: width, value: value}, nil
}

func (r *ReadOnlyRegister) Reset() error {
	return nil
}

func (r *ReadOnlyRegister) Read(offset uint32, width Width) (uint32, error) {
	if offset != 0 || width != r.width {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrReadOnlyRegisterMMIO, width*8, offset)
	}
	return r.value, nil
}

func (r *ReadOnlyRegister) Write(offset uint32, width Width, value uint32) error {
	return fmt.Errorf("%w: write%d at 0x%x value 0x%x", ErrReadOnlyRegisterMMIO, width*8, offset, value)
}

func (r *ReadOnlyRegister) SaveState() ([]byte, error) {
	state := make([]byte, 16)
	copy(state, "RREG")
	binary.LittleEndian.PutUint32(state[4:], 2)
	binary.LittleEndian.PutUint32(state[8:], uint32(r.width))
	binary.LittleEndian.PutUint32(state[12:], r.value)
	return state, nil
}

func (r *ReadOnlyRegister) LoadState(state []byte) error {
	if len(state) != 16 || string(state[:4]) != "RREG" ||
		binary.LittleEndian.Uint32(state[4:]) != 2 ||
		Width(binary.LittleEndian.Uint32(state[8:])) != r.width ||
		binary.LittleEndian.Uint32(state[12:]) != r.value {
		return ErrInvalidState
	}
	return nil
}

var (
	_ Device         = (*ReadOnlyRegister)(nil)
	_ StatefulDevice = (*ReadOnlyRegister)(nil)
)
