package system

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	QualcommInterruptControllerWindowSize = uint32(0x100)

	qualcommInterruptClear0Offset     = uint32(0x00)
	qualcommInterruptClear1Offset     = uint32(0x04)
	qualcommGPIOInterruptClear0Offset = uint32(0x08)
	qualcommGPIOInterruptClear1Offset = uint32(0x0c)
	qualcommGPIOInterruptClear4Offset = uint32(0x10)
	qualcommIRQEnable0Offset          = uint32(0x14)
	qualcommIRQEnable1Offset          = uint32(0x18)
	qualcommFIQEnable0Offset          = uint32(0x1c)
	qualcommFIQEnable1Offset          = uint32(0x20)
	qualcommGPIOInterruptEnable0      = uint32(0x24)
	qualcommGPIOInterruptEnable1      = uint32(0x28)
	qualcommGPIOInterruptEnable4      = uint32(0x2c)
	qualcommInterruptPolarity0        = uint32(0x30)
	qualcommInterruptPolarity1        = uint32(0x34)
	qualcommInterruptPolarity2        = uint32(0x38)
	qualcommInterruptPolarity5        = uint32(0x3c)
	qualcommGPIOInterruptDetect0      = uint32(0x48)
	qualcommGPIOInterruptDetect1      = uint32(0x4c)
	qualcommGPIOInterruptDetect4      = uint32(0x50)
	qualcommInterruptStatus0Offset    = uint32(0x54)
	qualcommInterruptStatus1Offset    = uint32(0x58)
	qualcommGPIOInterruptStatus0      = uint32(0x5c)
	qualcommGPIOInterruptStatus1      = uint32(0x60)
	qualcommGPIOInterruptStatus4      = uint32(0x64)
)

var ErrQualcommInterruptControllerMMIO = errors.New("unsupported Qualcomm interrupt-controller register")

// QualcommGPIOInputRegister exposes an evidenced read-only GPIO input word in
// the legacy 0x100-byte control aperture. Some ARM9-era Qualcomm parts place
// their split GPIO banks at offsets that are reserved in the MSM6150/6550
// INTCTL layout, so these aliases remain board-profile data rather than
// permissive controller defaults.
type QualcommGPIOInputRegister struct {
	Offset uint32
	Value  uint32
}

// QualcommInterruptStatusAlias describes a board-specific read-only view of
// one interrupt status bank. Some companion generations expose a bank at an
// offset occupied by a configuration register in the baseline MSM layout.
type QualcommInterruptStatusAlias struct {
	Offset     uint32
	Bank       uint8
	ResetValue uint32
}

type QualcommInterruptControllerConfig struct {
	GPIOInputs    []QualcommGPIOInputRegister
	StatusAliases []QualcommInterruptStatusAlias
}

// InterruptLineSink is implemented by a CPU backend or a test wire. The
// controller drives level-sensitive ARM IRQ and FIQ inputs after applying its
// source enables.
type InterruptLineSink interface {
	SetInterruptLine(cpu.InterruptLine, bool) error
}

// QualcommInterruptController models the two 32-source status banks in the
// MSM6150/6550-family INTCTL layout. Status is sticky: a level source must be
// deasserted and then acknowledged through INT_CLEAR before its bit clears.
// GPIO detail registers are retained explicitly, while their group-source
// routing is left for the GPIO device that eventually drives source 62/63.
type QualcommInterruptController struct {
	sink              InterruptLineSink
	gpioWriteObserver QualcommGPIOWriteObserver
	gpioInputOffsets  []uint32
	gpioInputs        map[uint32]uint32
	statusAliases     []QualcommInterruptStatusAlias
	statusAliasBanks  map[uint32]uint8
	resetStatus       [2]uint32
	irqEnable         [2]uint32
	fiqEnable         [2]uint32
	status            [2]uint32
	level             [2]uint32
	gpioEnable        [3]uint32
	polarity          [4]uint32
	detect            [3]uint32
	gpioStatus        [3]uint32
}

func NewQualcommInterruptController(sink InterruptLineSink) *QualcommInterruptController {
	device, _ := NewQualcommInterruptControllerWithConfig(QualcommInterruptControllerConfig{}, sink)
	return device
}

func NewQualcommInterruptControllerWithConfig(
	config QualcommInterruptControllerConfig,
	sink InterruptLineSink,
) (*QualcommInterruptController, error) {
	if err := validateQualcommInterruptControllerConfig(config); err != nil {
		return nil, err
	}
	inputs := append([]QualcommGPIOInputRegister(nil), config.GPIOInputs...)
	sort.Slice(inputs, func(left, right int) bool { return inputs[left].Offset < inputs[right].Offset })
	aliases := append([]QualcommInterruptStatusAlias(nil), config.StatusAliases...)
	sort.Slice(aliases, func(left, right int) bool { return aliases[left].Offset < aliases[right].Offset })
	device := &QualcommInterruptController{
		sink:             sink,
		gpioInputOffsets: make([]uint32, 0, len(inputs)),
		gpioInputs:       make(map[uint32]uint32, len(inputs)),
		statusAliases:    aliases,
		statusAliasBanks: make(map[uint32]uint8, len(aliases)),
	}
	for _, input := range inputs {
		device.gpioInputOffsets = append(device.gpioInputOffsets, input.Offset)
		device.gpioInputs[input.Offset] = input.Value
	}
	for _, alias := range aliases {
		device.statusAliasBanks[alias.Offset] = alias.Bank
		device.resetStatus[alias.Bank] = alias.ResetValue
	}
	_ = device.Reset()
	return device, nil
}

func validateQualcommInterruptControllerConfig(config QualcommInterruptControllerConfig) error {
	if err := validateQualcommGPIOInputRegisters(config.GPIOInputs); err != nil {
		return err
	}
	seen := make(map[uint32]struct{}, len(config.GPIOInputs)+len(config.StatusAliases))
	for _, input := range config.GPIOInputs {
		seen[input.Offset] = struct{}{}
	}
	bankResets := make(map[uint8]uint32, 2)
	for _, alias := range config.StatusAliases {
		if alias.Offset%4 != 0 || alias.Offset >= QualcommInterruptControllerWindowSize ||
			alias.Offset == qualcommInterruptStatus0Offset || alias.Offset == qualcommInterruptStatus1Offset ||
			alias.Bank >= 2 {
			return fmt.Errorf("status alias offset 0x%x bank %d: %w", alias.Offset, alias.Bank, ErrInvalidRegion)
		}
		if _, duplicate := seen[alias.Offset]; duplicate {
			return fmt.Errorf("duplicate interrupt alias offset 0x%x: %w", alias.Offset, ErrInvalidRegion)
		}
		seen[alias.Offset] = struct{}{}
		if reset, configured := bankResets[alias.Bank]; configured && reset != alias.ResetValue {
			return fmt.Errorf("conflicting reset for interrupt bank %d: %w", alias.Bank, ErrInvalidRegion)
		}
		bankResets[alias.Bank] = alias.ResetValue
	}
	return nil
}

func validateQualcommGPIOInputRegisters(inputs []QualcommGPIOInputRegister) error {
	seen := make(map[uint32]struct{}, len(inputs))
	for _, input := range inputs {
		switch input.Offset {
		case 0x34, 0x38, 0x3c, 0x40, 0x44:
		default:
			return fmt.Errorf("GPIO input offset 0x%x: %w", input.Offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[input.Offset]; duplicate {
			return fmt.Errorf("duplicate GPIO input offset 0x%x: %w", input.Offset, ErrInvalidRegion)
		}
		seen[input.Offset] = struct{}{}
	}
	return nil
}

func (d *QualcommInterruptController) AttachGPIOWriteObserver(observer QualcommGPIOWriteObserver) error {
	if observer == nil || d.gpioWriteObserver != nil {
		return fmt.Errorf("attach Qualcomm GPIO write observer: %w", ErrQualcommInterruptControllerMMIO)
	}
	d.gpioWriteObserver = observer
	return nil
}

func (d *QualcommInterruptController) Reset() error {
	d.irqEnable = [2]uint32{}
	d.fiqEnable = [2]uint32{}
	d.status = d.resetStatus
	d.level = [2]uint32{}
	d.gpioEnable = [3]uint32{}
	d.polarity = [4]uint32{}
	d.detect = [3]uint32{}
	d.gpioStatus = [3]uint32{}
	return d.updateOutputs()
}

func (d *QualcommInterruptController) Read(offset uint32, width Width) (uint32, error) {
	if width != Width32 {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommInterruptControllerMMIO, width*8, offset)
	}
	if value, ok := d.gpioInputs[offset]; ok {
		return value, nil
	}
	if bank, ok := d.statusAliasBanks[offset]; ok {
		return d.status[bank], nil
	}
	switch offset {
	case qualcommIRQEnable0Offset:
		return d.irqEnable[0], nil
	case qualcommIRQEnable1Offset:
		return d.irqEnable[1], nil
	case qualcommFIQEnable0Offset:
		return d.fiqEnable[0], nil
	case qualcommFIQEnable1Offset:
		return d.fiqEnable[1], nil
	case qualcommGPIOInterruptEnable0:
		return d.gpioEnable[0], nil
	case qualcommGPIOInterruptEnable1:
		return d.gpioEnable[1], nil
	case qualcommGPIOInterruptEnable4:
		return d.gpioEnable[2], nil
	case qualcommInterruptPolarity0:
		return d.polarity[0], nil
	case qualcommInterruptPolarity1:
		return d.polarity[1], nil
	case qualcommInterruptPolarity2:
		return d.polarity[2], nil
	case qualcommInterruptPolarity5:
		return d.polarity[3], nil
	case qualcommGPIOInterruptDetect0:
		return d.detect[0], nil
	case qualcommGPIOInterruptDetect1:
		return d.detect[1], nil
	case qualcommGPIOInterruptDetect4:
		return d.detect[2], nil
	case qualcommInterruptStatus0Offset:
		return d.status[0], nil
	case qualcommInterruptStatus1Offset:
		return d.status[1], nil
	case qualcommGPIOInterruptStatus0:
		return d.gpioStatus[0], nil
	case qualcommGPIOInterruptStatus1:
		return d.gpioStatus[1], nil
	case qualcommGPIOInterruptStatus4:
		return d.gpioStatus[2], nil
	default:
		return 0, fmt.Errorf("%w: read32 at 0x%x", ErrQualcommInterruptControllerMMIO, offset)
	}
}

func (d *QualcommInterruptController) Write(offset uint32, width Width, value uint32) error {
	if width != Width32 {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrQualcommInterruptControllerMMIO, width*8, value, offset,
		)
	}
	if _, readOnly := d.statusAliasBanks[offset]; readOnly {
		return fmt.Errorf(
			"%w: write32 value 0x%x at read-only status alias 0x%x",
			ErrQualcommInterruptControllerMMIO, value, offset,
		)
	}
	switch offset {
	case qualcommInterruptClear0Offset:
		d.status[0] &^= value &^ d.level[0]
	case qualcommInterruptClear1Offset:
		d.status[1] &^= value &^ d.level[1]
	case qualcommGPIOInterruptClear0Offset:
		d.gpioStatus[0] &^= value
	case qualcommGPIOInterruptClear1Offset:
		d.gpioStatus[1] &^= value
	case qualcommGPIOInterruptClear4Offset:
		d.gpioStatus[2] &^= value
	case qualcommIRQEnable0Offset:
		d.irqEnable[0] = value
	case qualcommIRQEnable1Offset:
		d.irqEnable[1] = value
	case qualcommFIQEnable0Offset:
		d.fiqEnable[0] = value
	case qualcommFIQEnable1Offset:
		d.fiqEnable[1] = value
	case qualcommGPIOInterruptEnable0:
		d.gpioEnable[0] = value
	case qualcommGPIOInterruptEnable1:
		d.gpioEnable[1] = value
	case qualcommGPIOInterruptEnable4:
		d.gpioEnable[2] = value
	case qualcommInterruptPolarity0:
		d.polarity[0] = value
	case qualcommInterruptPolarity1:
		d.polarity[1] = value
	case qualcommInterruptPolarity2:
		d.polarity[2] = value
	case qualcommInterruptPolarity5:
		d.polarity[3] = value
	case qualcommGPIOInterruptDetect0:
		d.detect[0] = value
	case qualcommGPIOInterruptDetect1:
		d.detect[1] = value
	case qualcommGPIOInterruptDetect4:
		d.detect[2] = value
	default:
		return fmt.Errorf(
			"%w: write32 value 0x%x at 0x%x",
			ErrQualcommInterruptControllerMMIO, value, offset,
		)
	}
	if d.gpioWriteObserver != nil {
		d.gpioWriteObserver.ObserveGPIOWrite(offset, value)
	}
	return d.updateOutputs()
}

func qualcommInterruptControllerSupportsWrite(offset uint32) bool {
	switch offset {
	case qualcommInterruptClear0Offset,
		qualcommInterruptClear1Offset,
		qualcommGPIOInterruptClear0Offset,
		qualcommGPIOInterruptClear1Offset,
		qualcommGPIOInterruptClear4Offset,
		qualcommIRQEnable0Offset,
		qualcommIRQEnable1Offset,
		qualcommFIQEnable0Offset,
		qualcommFIQEnable1Offset,
		qualcommGPIOInterruptEnable0,
		qualcommGPIOInterruptEnable1,
		qualcommGPIOInterruptEnable4,
		qualcommInterruptPolarity0,
		qualcommInterruptPolarity1,
		qualcommInterruptPolarity2,
		qualcommInterruptPolarity5,
		qualcommGPIOInterruptDetect0,
		qualcommGPIOInterruptDetect1,
		qualcommGPIOInterruptDetect4:
		return true
	default:
		return false
	}
}

func (d *QualcommInterruptController) SetSource(source uint8, asserted bool) error {
	if source >= 64 {
		return fmt.Errorf("invalid Qualcomm interrupt source %d", source)
	}
	bank := source / 32
	mask := uint32(1) << (source % 32)
	if asserted {
		d.level[bank] |= mask
		d.status[bank] |= mask
	} else {
		d.level[bank] &^= mask
	}
	return d.updateOutputs()
}

func (d *QualcommInterruptController) PulseSource(source uint8) error {
	if source >= 64 {
		return fmt.Errorf("invalid Qualcomm interrupt source %d", source)
	}
	bank := source / 32
	d.status[bank] |= uint32(1) << (source % 32)
	return d.updateOutputs()
}

func (d *QualcommInterruptController) updateOutputs() error {
	if d.sink == nil {
		return nil
	}
	irq := d.status[0]&d.irqEnable[0] != 0 || d.status[1]&d.irqEnable[1] != 0
	fiq := d.status[0]&d.fiqEnable[0] != 0 || d.status[1]&d.fiqEnable[1] != 0
	if err := d.sink.SetInterruptLine(cpu.InterruptIRQ, irq); err != nil {
		return fmt.Errorf("drive Qualcomm IRQ output: %w", err)
	}
	if err := d.sink.SetInterruptLine(cpu.InterruptFIQ, fiq); err != nil {
		return fmt.Errorf("drive Qualcomm FIQ output: %w", err)
	}
	return nil
}

func (d *QualcommInterruptController) SaveState() ([]byte, error) {
	const words = 2 + 2 + 2 + 2 + 3 + 4 + 3 + 3
	version := uint32(1)
	headerSize := 8
	if len(d.statusAliases) != 0 {
		version = 3
		headerSize = 16 + len(d.gpioInputOffsets)*8 + len(d.statusAliases)*12
	} else if len(d.gpioInputOffsets) != 0 {
		version = 2
		headerSize = 12 + len(d.gpioInputOffsets)*8
	}
	state := make([]byte, headerSize+words*4)
	copy(state, "QINT")
	binary.LittleEndian.PutUint32(state[4:8], version)
	offset := 8
	if version >= 2 {
		binary.LittleEndian.PutUint32(state[8:12], uint32(len(d.gpioInputOffsets)))
		offset = 12
		for _, inputOffset := range d.gpioInputOffsets {
			binary.LittleEndian.PutUint32(state[offset:offset+4], inputOffset)
			binary.LittleEndian.PutUint32(state[offset+4:offset+8], d.gpioInputs[inputOffset])
			offset += 8
		}
	}
	if version == 3 {
		binary.LittleEndian.PutUint32(state[offset:offset+4], uint32(len(d.statusAliases)))
		offset += 4
		for _, alias := range d.statusAliases {
			binary.LittleEndian.PutUint32(state[offset:offset+4], alias.Offset)
			binary.LittleEndian.PutUint32(state[offset+4:offset+8], uint32(alias.Bank))
			binary.LittleEndian.PutUint32(state[offset+8:offset+12], alias.ResetValue)
			offset += 12
		}
	}
	put := func(values []uint32) {
		for _, value := range values {
			binary.LittleEndian.PutUint32(state[offset:offset+4], value)
			offset += 4
		}
	}
	put(d.irqEnable[:])
	put(d.fiqEnable[:])
	put(d.status[:])
	put(d.level[:])
	put(d.gpioEnable[:])
	put(d.polarity[:])
	put(d.detect[:])
	put(d.gpioStatus[:])
	return state, nil
}

func (d *QualcommInterruptController) LoadState(state []byte) error {
	const words = 2 + 2 + 2 + 2 + 3 + 4 + 3 + 3
	if len(state) < 8 || string(state[:4]) != "QINT" {
		return ErrInvalidState
	}
	offset := 8
	switch binary.LittleEndian.Uint32(state[4:8]) {
	case 1:
		if len(d.gpioInputOffsets) != 0 || len(d.statusAliases) != 0 || len(state) != 8+words*4 {
			return ErrInvalidState
		}
	case 2:
		if len(d.statusAliases) != 0 || len(state) < 12 {
			return ErrInvalidState
		}
		inputCount := binary.LittleEndian.Uint32(state[8:12])
		if inputCount != uint32(len(d.gpioInputOffsets)) ||
			uint64(len(state)) != 12+uint64(inputCount)*8+words*4 {
			return ErrInvalidState
		}
		offset = 12
		for _, inputOffset := range d.gpioInputOffsets {
			if binary.LittleEndian.Uint32(state[offset:offset+4]) != inputOffset ||
				binary.LittleEndian.Uint32(state[offset+4:offset+8]) != d.gpioInputs[inputOffset] {
				return ErrInvalidState
			}
			offset += 8
		}
	case 3:
		if len(state) < 16 {
			return ErrInvalidState
		}
		inputCount := binary.LittleEndian.Uint32(state[8:12])
		inputEnd := uint64(12) + uint64(inputCount)*8
		if inputCount != uint32(len(d.gpioInputOffsets)) || inputEnd+4 > uint64(len(state)) {
			return ErrInvalidState
		}
		offset = 12
		for _, inputOffset := range d.gpioInputOffsets {
			if binary.LittleEndian.Uint32(state[offset:offset+4]) != inputOffset ||
				binary.LittleEndian.Uint32(state[offset+4:offset+8]) != d.gpioInputs[inputOffset] {
				return ErrInvalidState
			}
			offset += 8
		}
		aliasCount := binary.LittleEndian.Uint32(state[offset : offset+4])
		offset += 4
		expectedSize := uint64(offset) + uint64(aliasCount)*12 + words*4
		if aliasCount != uint32(len(d.statusAliases)) || expectedSize != uint64(len(state)) {
			return ErrInvalidState
		}
		for _, alias := range d.statusAliases {
			if binary.LittleEndian.Uint32(state[offset:offset+4]) != alias.Offset ||
				binary.LittleEndian.Uint32(state[offset+4:offset+8]) != uint32(alias.Bank) ||
				binary.LittleEndian.Uint32(state[offset+8:offset+12]) != alias.ResetValue {
				return ErrInvalidState
			}
			offset += 12
		}
	default:
		return ErrInvalidState
	}
	read := func(values []uint32) {
		for index := range values {
			values[index] = binary.LittleEndian.Uint32(state[offset : offset+4])
			offset += 4
		}
	}
	var irqEnable, fiqEnable, status, level [2]uint32
	var gpioEnable, detect, gpioStatus [3]uint32
	var polarity [4]uint32
	read(irqEnable[:])
	read(fiqEnable[:])
	read(status[:])
	read(level[:])
	read(gpioEnable[:])
	read(polarity[:])
	read(detect[:])
	read(gpioStatus[:])
	if status[0]&level[0] != level[0] || status[1]&level[1] != level[1] {
		return ErrInvalidState
	}
	previous := *d
	d.irqEnable, d.fiqEnable, d.status, d.level = irqEnable, fiqEnable, status, level
	d.gpioEnable, d.polarity, d.detect, d.gpioStatus = gpioEnable, polarity, detect, gpioStatus
	if err := d.updateOutputs(); err != nil {
		*d = previous
		_ = d.updateOutputs()
		return err
	}
	return nil
}

var (
	_ Device         = (*QualcommInterruptController)(nil)
	_ StatefulDevice = (*QualcommInterruptController)(nil)
)
