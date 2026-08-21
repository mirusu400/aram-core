package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrLatchedRegisterMMIO = errors.New("unsupported latched register access")

// LatchedRegister is a single profile-declared MMIO register whose value is
// persistent but whose hardware side effects have not been evidenced. It
// keeps provisional compatibility state narrow: adjacent addresses and other
// access widths remain faults.
type LatchedRegister struct {
	width      Width
	resetValue uint32
	value      uint32
}

func NewLatchedRegister(width Width, resetValue uint32) (*LatchedRegister, error) {
	if width != Width8 && width != Width16 && width != Width32 ||
		width < Width32 && resetValue >= uint32(1)<<(uint32(width)*8) {
		return nil, fmt.Errorf("invalid latched register width/value")
	}
	device := &LatchedRegister{width: width, resetValue: resetValue}
	_ = device.Reset()
	return device, nil
}

func (d *LatchedRegister) Reset() error {
	d.value = d.resetValue
	return nil
}

func (d *LatchedRegister) Read(offset uint32, width Width) (uint32, error) {
	if offset != 0 || width != d.width {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrLatchedRegisterMMIO, width*8, offset)
	}
	return d.value, nil
}

func (d *LatchedRegister) Write(offset uint32, width Width, value uint32) error {
	if offset != 0 || width != d.width ||
		d.width < Width32 && value >= uint32(1)<<(uint32(d.width)*8) {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrLatchedRegisterMMIO,
			width*8,
			value,
			offset,
		)
	}
	d.value = value
	return nil
}

func (d *LatchedRegister) SaveState() ([]byte, error) {
	state := make([]byte, 17)
	copy(state, "LREG")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	state[8] = byte(d.width)
	binary.LittleEndian.PutUint32(state[9:13], d.resetValue)
	binary.LittleEndian.PutUint32(state[13:17], d.value)
	return state, nil
}

func (d *LatchedRegister) LoadState(state []byte) error {
	if len(state) != 17 || string(state[:4]) != "LREG" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || Width(state[8]) != d.width ||
		binary.LittleEndian.Uint32(state[9:13]) != d.resetValue {
		return ErrInvalidState
	}
	value := binary.LittleEndian.Uint32(state[13:17])
	if d.width < Width32 && value >= uint32(1)<<(uint32(d.width)*8) {
		return ErrInvalidState
	}
	d.value = value
	return nil
}

var (
	_ Device         = (*LatchedRegister)(nil)
	_ StatefulDevice = (*LatchedRegister)(nil)
)
