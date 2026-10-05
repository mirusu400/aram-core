package system

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var ErrMixedWidthLatchedRegisterMMIO = errors.New("unsupported mixed-width latched register access")

// MixedWidthLatchedRegister is a single word whose profile explicitly allows
// more than one access width. Narrow writes update only the low addressed
// bytes unless aligned subword offsets are explicitly enabled.
type MixedWidthLatchedRegister struct {
	widths              []Width
	widthMask           uint8
	allowSubwordOffsets bool
	resetValue          uint32
	value               uint32
}

func NewMixedWidthLatchedRegister(
	widths []Width,
	resetValue uint32,
) (*MixedWidthLatchedRegister, error) {
	return newMixedWidthLatchedRegister(widths, resetValue, false)
}

func NewMixedWidthLatchedRegisterWithSubwordOffsets(
	widths []Width,
	resetValue uint32,
) (*MixedWidthLatchedRegister, error) {
	return newMixedWidthLatchedRegister(widths, resetValue, true)
}

func newMixedWidthLatchedRegister(
	widths []Width,
	resetValue uint32,
	allowSubwordOffsets bool,
) (*MixedWidthLatchedRegister, error) {
	if len(widths) < 2 {
		return nil, fmt.Errorf("create mixed-width latched register: %w", ErrInvalidRegion)
	}
	normalized := append([]Width(nil), widths...)
	sort.Slice(normalized, func(left, right int) bool { return normalized[left] < normalized[right] })
	var widthMask uint8
	for index, width := range normalized {
		if width != Width8 && width != Width16 && width != Width32 ||
			index > 0 && width == normalized[index-1] {
			return nil, fmt.Errorf("create mixed-width latched register: %w", ErrInvalidRegion)
		}
		widthMask |= 1 << uint8(width)
	}
	maximum := normalized[len(normalized)-1]
	if maximum < Width32 && resetValue >= uint32(1)<<(uint32(maximum)*8) {
		return nil, fmt.Errorf("create mixed-width latched register: %w", ErrInvalidRegion)
	}
	device := &MixedWidthLatchedRegister{
		widths: normalized, widthMask: widthMask,
		allowSubwordOffsets: allowSubwordOffsets,
		resetValue:          resetValue,
	}
	_ = device.Reset()
	return device, nil
}

func (d *MixedWidthLatchedRegister) Reset() error {
	d.value = d.resetValue
	return nil
}

func (d *MixedWidthLatchedRegister) supports(width Width) bool {
	return (width == Width8 || width == Width16 || width == Width32) &&
		d.widthMask&(1<<uint8(width)) != 0
}

func (d *MixedWidthLatchedRegister) validAccess(offset uint32, width Width) bool {
	if !d.supports(width) || offset%uint32(width) != 0 {
		return false
	}
	if !d.allowSubwordOffsets {
		return offset == 0
	}
	maximum := d.widths[len(d.widths)-1]
	return uint64(offset)+uint64(width) <= uint64(maximum)
}

func (d *MixedWidthLatchedRegister) Read(offset uint32, width Width) (uint32, error) {
	if !d.validAccess(offset, width) {
		return 0, fmt.Errorf(
			"%w: read%d at 0x%x",
			ErrMixedWidthLatchedRegisterMMIO,
			width*8,
			offset,
		)
	}
	if width == Width32 {
		return d.value, nil
	}
	mask := uint32(1)<<(uint32(width)*8) - 1
	return d.value >> (offset * 8) & mask, nil
}

func (d *MixedWidthLatchedRegister) Write(offset uint32, width Width, value uint32) error {
	if !d.validAccess(offset, width) ||
		width < Width32 && value >= uint32(1)<<(uint32(width)*8) {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrMixedWidthLatchedRegisterMMIO,
			width*8,
			value,
			offset,
		)
	}
	if width == Width32 {
		d.value = value
		return nil
	}
	mask := uint32(1)<<(uint32(width)*8) - 1
	shift := offset * 8
	d.value = d.value&^(mask<<shift) | value<<shift
	return nil
}

func (d *MixedWidthLatchedRegister) SaveState() ([]byte, error) {
	state := make([]byte, 20)
	copy(state, "MWLR")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	state[8] = d.widthMask
	if d.allowSubwordOffsets {
		state[9] = 1
	}
	binary.LittleEndian.PutUint32(state[12:16], d.resetValue)
	binary.LittleEndian.PutUint32(state[16:20], d.value)
	return state, nil
}

func (d *MixedWidthLatchedRegister) LoadState(state []byte) error {
	if len(state) != 20 || string(state[:4]) != "MWLR" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || state[8] != d.widthMask ||
		state[9] != boolByte(d.allowSubwordOffsets) || state[10] != 0 || state[11] != 0 ||
		binary.LittleEndian.Uint32(state[12:16]) != d.resetValue {
		return ErrInvalidState
	}
	value := binary.LittleEndian.Uint32(state[16:20])
	maximum := d.widths[len(d.widths)-1]
	if maximum < Width32 && value >= uint32(1)<<(uint32(maximum)*8) {
		return ErrInvalidState
	}
	d.value = value
	return nil
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}

var (
	_ Device         = (*MixedWidthLatchedRegister)(nil)
	_ StatefulDevice = (*MixedWidthLatchedRegister)(nil)
)
