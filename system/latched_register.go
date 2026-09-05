package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrLatchedRegisterMMIO = errors.New("unsupported latched register access")

// LatchedRegister is a single profile-declared MMIO register whose value is
// persistent. A profile may attach exact command-triggered interrupt pulses;
// adjacent addresses, other widths, and undeclared side effects remain faults.
type LatchedRegister struct {
	width      Width
	resetValue uint32
	value      uint32
	pulseRules []latchedRegisterWritePulse
}

type latchedRegisterWritePulse struct {
	mask    uint32
	value   uint32
	sources []uint8
	pulser  qualcommInterruptSourcePulser
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
	previous := d.value
	d.value = value
	for _, rule := range d.pulseRules {
		if value&rule.mask != rule.value {
			continue
		}
		for _, source := range rule.sources {
			if err := rule.pulser.PulseSource(source); err != nil {
				d.value = previous
				return fmt.Errorf("pulse latched-register interrupt source %d: %w", source, err)
			}
		}
	}
	return nil
}

// AttachWritePulse profiles a bounded command side effect without broadening
// the register's address or access-width contract.
func (d *LatchedRegister) AttachWritePulse(
	mask uint32,
	value uint32,
	sources []uint8,
	pulser qualcommInterruptSourcePulser,
) error {
	if mask == 0 || value&^mask != 0 || len(sources) == 0 || pulser == nil ||
		d.width < Width32 && mask >= uint32(1)<<(uint32(d.width)*8) {
		return fmt.Errorf("invalid latched-register write pulse")
	}
	seen := make(map[uint8]struct{}, len(sources))
	for _, source := range sources {
		if source >= 64 {
			return fmt.Errorf("invalid latched-register interrupt source %d", source)
		}
		if _, duplicate := seen[source]; duplicate {
			return fmt.Errorf("duplicate latched-register interrupt source %d", source)
		}
		seen[source] = struct{}{}
	}
	for _, rule := range d.pulseRules {
		if rule.mask == mask && rule.value == value {
			return fmt.Errorf("duplicate latched-register write pulse")
		}
	}
	d.pulseRules = append(d.pulseRules, latchedRegisterWritePulse{
		mask: mask, value: value, sources: append([]uint8(nil), sources...), pulser: pulser,
	})
	return nil
}

func (d *LatchedRegister) SaveState() ([]byte, error) {
	if len(d.pulseRules) == 0 {
		state := make([]byte, 17)
		copy(state, "LREG")
		binary.LittleEndian.PutUint32(state[4:8], 1)
		state[8] = byte(d.width)
		binary.LittleEndian.PutUint32(state[9:13], d.resetValue)
		binary.LittleEndian.PutUint32(state[13:17], d.value)
		return state, nil
	}
	stateSize := 21
	for _, rule := range d.pulseRules {
		stateSize += 12 + len(rule.sources)
	}
	state := make([]byte, stateSize)
	copy(state, "LREG")
	binary.LittleEndian.PutUint32(state[4:8], 2)
	state[8] = byte(d.width)
	binary.LittleEndian.PutUint32(state[9:13], d.resetValue)
	binary.LittleEndian.PutUint32(state[13:17], uint32(len(d.pulseRules)))
	offset := 17
	for _, rule := range d.pulseRules {
		binary.LittleEndian.PutUint32(state[offset:offset+4], rule.mask)
		binary.LittleEndian.PutUint32(state[offset+4:offset+8], rule.value)
		binary.LittleEndian.PutUint32(state[offset+8:offset+12], uint32(len(rule.sources)))
		offset += 12
		copy(state[offset:offset+len(rule.sources)], rule.sources)
		offset += len(rule.sources)
	}
	binary.LittleEndian.PutUint32(state[offset:offset+4], d.value)
	return state, nil
}

func (d *LatchedRegister) LoadState(state []byte) error {
	if len(state) < 8 || string(state[:4]) != "LREG" {
		return ErrInvalidState
	}
	var value uint32
	switch binary.LittleEndian.Uint32(state[4:8]) {
	case 1:
		if len(d.pulseRules) != 0 || len(state) != 17 || Width(state[8]) != d.width ||
			binary.LittleEndian.Uint32(state[9:13]) != d.resetValue {
			return ErrInvalidState
		}
		value = binary.LittleEndian.Uint32(state[13:17])
	case 2:
		if len(state) < 21 || len(d.pulseRules) == 0 || Width(state[8]) != d.width ||
			binary.LittleEndian.Uint32(state[9:13]) != d.resetValue ||
			binary.LittleEndian.Uint32(state[13:17]) != uint32(len(d.pulseRules)) {
			return ErrInvalidState
		}
		offset := 17
		for _, rule := range d.pulseRules {
			if offset+12 > len(state) || binary.LittleEndian.Uint32(state[offset:offset+4]) != rule.mask ||
				binary.LittleEndian.Uint32(state[offset+4:offset+8]) != rule.value {
				return ErrInvalidState
			}
			count := binary.LittleEndian.Uint32(state[offset+8 : offset+12])
			offset += 12
			if count != uint32(len(rule.sources)) || uint64(offset)+uint64(count)+4 > uint64(len(state)) {
				return ErrInvalidState
			}
			for index, source := range rule.sources {
				if state[offset+index] != source {
					return ErrInvalidState
				}
			}
			offset += int(count)
		}
		if offset+4 != len(state) {
			return ErrInvalidState
		}
		value = binary.LittleEndian.Uint32(state[offset : offset+4])
	default:
		return ErrInvalidState
	}
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
