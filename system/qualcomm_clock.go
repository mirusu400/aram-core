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
	qualcommPrimaryGPIOInputOffset = 0x0588
	qualcommPrimaryGPIOInputMask   = 0x0000000f
)

var qualcommPrimaryClockWritableOffsets = [...]uint32{0x0574, 0x0578, 0x057c, 0x0580}

var ErrQualcommPrimaryClockMMIO = errors.New("unsupported Qualcomm primary-clock register")

type QualcommPrimaryClockConfig struct {
	// Status is the reset value of the four raw digital input lines exposed
	// at offset 0x588. A set bit is a high line. InputMask defaults to the
	// original four-line compatibility aperture when omitted; board profiles
	// can expose additional evidenced lines such as SCH-W830's power-key input.
	Status              uint32
	InputMask           uint32
	WritableOffsets     []uint32
	ReadOnlyRegisters   []QualcommPrimaryClockReadOnlyRegister
	InterruptRegisters  []QualcommPrimaryClockInterruptRegister
	InterruptController *QualcommInterruptController
}

// QualcommPrimaryClockReadOnlyRegister is a board-wired status word in the
// primary clock/GPIO aperture. Profiles opt into exact offsets without making
// them writable or widening the controller for unrelated handsets.
type QualcommPrimaryClockReadOnlyRegister struct {
	Offset uint32
	Value  uint32
}

// QualcommPrimaryClockInterruptBit maps one controller source onto the bit
// position exposed by a companion status/acknowledge register pair.
type QualcommPrimaryClockInterruptBit struct {
	Bit    uint8
	Source uint8
}

// QualcommPrimaryClockInterruptRegister describes a board-specific remapped
// view of legacy interrupt sources inside the primary clock/control aperture.
type QualcommPrimaryClockInterruptRegister struct {
	StatusOffset uint32
	ClearOffset  uint32
	Bits         []QualcommPrimaryClockInterruptBit
}

// QualcommPrimaryClockControl models the bounded primary control window used
// by early OEM firmware. It includes the four raw digital input lines at
// offset 0x588 and only the writable registers evidenced for a board profile;
// unrelated registers remain explicit faults.
type QualcommPrimaryClockControl struct {
	inputMask                uint32
	resetStatus              uint32
	status                   uint32
	writableOffsets          []uint32
	registers                map[uint32]uint32
	readOnlyRegisters        map[uint32]uint32
	interruptStatusRegisters map[uint32][]QualcommPrimaryClockInterruptBit
	interruptClearRegisters  map[uint32][]QualcommPrimaryClockInterruptBit
	interruptController      *QualcommInterruptController
	keypad                   *QualcommGPIOKeypad
	touchscreen              *QualcommTSC2007
}

func NewQualcommPrimaryClockControl(config QualcommPrimaryClockConfig) (*QualcommPrimaryClockControl, error) {
	inputMask := config.InputMask
	if inputMask == 0 {
		inputMask = qualcommPrimaryGPIOInputMask
	}
	if config.Status&^inputMask != 0 {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock configuration")
	}
	if err := validateQualcommPrimaryClockWritableOffsets(config.WritableOffsets); err != nil {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock writable offsets: %w", err)
	}
	if err := validateQualcommPrimaryClockReadOnlyRegisters(
		config.WritableOffsets,
		config.ReadOnlyRegisters,
	); err != nil {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock read-only registers: %w", err)
	}
	if len(config.InterruptRegisters) != 0 && config.InterruptController == nil {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock interrupt registers: missing interrupt controller")
	}
	if err := validateQualcommPrimaryClockInterruptRegisters(
		config.WritableOffsets,
		config.ReadOnlyRegisters,
		config.InterruptRegisters,
	); err != nil {
		return nil, fmt.Errorf("invalid Qualcomm primary-clock interrupt registers: %w", err)
	}
	readOnlyRegisters := make(map[uint32]uint32, len(config.ReadOnlyRegisters))
	for _, register := range config.ReadOnlyRegisters {
		readOnlyRegisters[register.Offset] = register.Value
	}
	interruptStatusRegisters := make(map[uint32][]QualcommPrimaryClockInterruptBit, len(config.InterruptRegisters))
	interruptClearRegisters := make(map[uint32][]QualcommPrimaryClockInterruptBit, len(config.InterruptRegisters))
	for _, register := range config.InterruptRegisters {
		interruptStatusRegisters[register.StatusOffset] = append(
			[]QualcommPrimaryClockInterruptBit(nil), register.Bits...,
		)
		interruptClearRegisters[register.ClearOffset] = append(
			interruptClearRegisters[register.ClearOffset], register.Bits...,
		)
	}
	device := &QualcommPrimaryClockControl{
		inputMask:                inputMask,
		resetStatus:              config.Status,
		status:                   config.Status,
		writableOffsets:          mergedQualcommPrimaryClockWritableOffsets(config.WritableOffsets),
		readOnlyRegisters:        readOnlyRegisters,
		interruptStatusRegisters: interruptStatusRegisters,
		interruptClearRegisters:  interruptClearRegisters,
		interruptController:      config.InterruptController,
	}
	_ = device.Reset()
	return device, nil
}

func (d *QualcommPrimaryClockControl) Reset() error {
	d.status = d.resetStatus
	d.registers = make(map[uint32]uint32, len(d.writableOffsets))
	for _, offset := range d.writableOffsets {
		d.registers[offset] = 0
	}
	if d.keypad != nil {
		if err := d.keypad.Reset(); err != nil {
			return err
		}
	}
	if d.touchscreen != nil {
		return d.touchscreen.Reset()
	}
	return nil
}

func (d *QualcommPrimaryClockControl) Read(offset uint32, width Width) (uint32, error) {
	if offset == qualcommPrimaryGPIOInputOffset && width == Width32 {
		return d.InputStatus(), nil
	}
	if d.keypad != nil && width == Width32 {
		if value, handled := d.keypad.readPrimaryGPIORegister(offset); handled {
			return value, nil
		}
	}
	if d.touchscreen != nil && width == Width32 {
		if value, handled := d.touchscreen.readPrimaryGPIORegister(offset); handled {
			return value, nil
		}
	}
	if bits, ok := d.interruptStatusRegisters[offset]; ok && width == Width32 {
		var value uint32
		for _, bit := range bits {
			if d.interruptController.sourcePending(bit.Source) {
				value |= uint32(1) << bit.Bit
			}
		}
		return value, nil
	}
	if value, ok := d.readOnlyRegisters[offset]; ok && width == Width32 {
		return value, nil
	}
	if value, ok := d.registers[offset]; ok && width == Width32 {
		return value, nil
	}
	return 0, fmt.Errorf(
		"%w: read%d at 0x%x",
		ErrQualcommPrimaryClockMMIO, width*8, offset,
	)
}

// InputStatus returns the raw high/low state of the profiled digital inputs,
// including any active-low columns currently driven by an attached keypad.
func (d *QualcommPrimaryClockControl) InputStatus() uint32 {
	if d.keypad != nil {
		return d.keypad.InputStatus(d.status)
	}
	return d.status
}

// AttachGPIOKeypad connects a profile-created matrix to this input bank. The
// keypad columns must all be exposed by the board's input mask.
func (d *QualcommPrimaryClockControl) AttachGPIOKeypad(keypad *QualcommGPIOKeypad) error {
	if keypad == nil || d.keypad != nil || d.touchscreen != nil || keypad.inputMask()&^d.inputMask != 0 {
		return fmt.Errorf("attach Qualcomm GPIO keypad: %w", ErrQualcommPrimaryClockMMIO)
	}
	d.keypad = keypad
	return nil
}

// AttachTouchscreen connects a board's pen-detect GPIO group to this aperture.
func (d *QualcommPrimaryClockControl) AttachTouchscreen(touchscreen *QualcommTSC2007) error {
	if touchscreen == nil || d.touchscreen != nil || d.keypad != nil {
		return fmt.Errorf("attach Qualcomm touchscreen: %w", ErrQualcommPrimaryClockMMIO)
	}
	d.touchscreen = touchscreen
	return nil
}

// SetInputStatus replaces all four raw digital input lines. Frontends should
// normally use SetInputLine so unrelated input lines retain their state.
func (d *QualcommPrimaryClockControl) SetInputStatus(value uint32) error {
	if value&^d.inputMask != 0 {
		return fmt.Errorf("input status 0x%x: %w", value, ErrQualcommPrimaryClockMMIO)
	}
	d.status = value
	return nil
}

// SetInputLine drives one raw digital input line high or low.
func (d *QualcommPrimaryClockControl) SetInputLine(line uint8, high bool) error {
	if line >= 32 || d.inputMask&(uint32(1)<<line) == 0 {
		return fmt.Errorf("input line %d: %w", line, ErrQualcommPrimaryClockMMIO)
	}
	mask := uint32(1) << line
	if high {
		d.status |= mask
	} else {
		d.status &^= mask
	}
	return nil
}

func (d *QualcommPrimaryClockControl) Write(offset uint32, width Width, value uint32) error {
	if d.keypad != nil && width == Width32 {
		if handled, err := d.keypad.writePrimaryGPIORegister(offset, value); handled {
			return err
		}
	}
	if d.touchscreen != nil && width == Width32 {
		if handled, err := d.touchscreen.writePrimaryGPIORegister(offset, value); handled {
			return err
		}
	}
	if _, ok := d.registers[offset]; ok && width == Width32 {
		d.registers[offset] = value
		for _, bit := range d.interruptClearRegisters[offset] {
			if value&(uint32(1)<<bit.Bit) != 0 {
				if err := d.interruptController.acknowledgeSource(bit.Source); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return fmt.Errorf(
		"%w: write%d value 0x%x at 0x%x",
		ErrQualcommPrimaryClockMMIO, width*8, value, offset,
	)
}

func (d *QualcommPrimaryClockControl) SaveState() ([]byte, error) {
	version := uint32(5)
	var keypadState []byte
	var touchscreenState []byte
	if d.keypad != nil {
		var err error
		keypadState, err = d.keypad.SaveState()
		if err != nil {
			return nil, err
		}
		version = 6
	}
	if d.touchscreen != nil {
		var err error
		touchscreenState, err = d.touchscreen.SaveState()
		if err != nil {
			return nil, err
		}
		version = 7
	}
	var output bytes.Buffer
	output.WriteString("QPCC")
	_ = binary.Write(&output, binary.LittleEndian, version)
	_ = binary.Write(&output, binary.LittleEndian, d.inputMask)
	_ = binary.Write(&output, binary.LittleEndian, d.resetStatus)
	_ = binary.Write(&output, binary.LittleEndian, d.status)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.writableOffsets)))
	for _, offset := range d.writableOffsets {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = binary.Write(&output, binary.LittleEndian, d.registers[offset])
	}
	if version == 6 {
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(keypadState)))
		_, _ = output.Write(keypadState)
	} else if version == 7 {
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(touchscreenState)))
		_, _ = output.Write(touchscreenState)
	}
	return output.Bytes(), nil
}

func (d *QualcommPrimaryClockControl) LoadState(state []byte) error {
	return d.loadState(state, false)
}

// LoadStateSubset migrates v4 four-input checkpoints into a wider profiled
// input bank. Newly exposed inputs take their current reset state instead of
// being invented as low by the older checkpoint format.
func (d *QualcommPrimaryClockControl) LoadStateSubset(state []byte) error {
	return d.loadState(state, true)
}

func (d *QualcommPrimaryClockControl) loadState(state []byte, allowInputExpansion bool) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, inputMask, resetStatus, status, count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QPCC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil {
		return ErrInvalidState
	}
	if version == 4 {
		inputMask = qualcommPrimaryGPIOInputMask
		if !allowInputExpansion && d.inputMask != inputMask {
			return ErrInvalidState
		}
	} else if version == 5 || version == 6 || version == 7 {
		if binary.Read(reader, binary.LittleEndian, &inputMask) != nil || inputMask != d.inputMask {
			return ErrInvalidState
		}
	} else {
		return ErrInvalidState
	}
	if binary.Read(reader, binary.LittleEndian, &resetStatus) != nil ||
		resetStatus != d.resetStatus&inputMask ||
		binary.Read(reader, binary.LittleEndian, &status) != nil ||
		status&^inputMask != 0 ||
		binary.Read(reader, binary.LittleEndian, &count) != nil || count != uint32(len(d.writableOffsets)) {
		return ErrInvalidState
	}
	minimumRemaining := int(count) * 8
	if version == 6 || version == 7 {
		minimumRemaining += 4
	}
	if reader.Len() < minimumRemaining || version != 6 && version != 7 && reader.Len() != minimumRemaining {
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
	if version == 6 {
		var keypadStateLength uint32
		if binary.Read(reader, binary.LittleEndian, &keypadStateLength) != nil ||
			uint64(keypadStateLength) > uint64(reader.Len()) || reader.Len() != int(keypadStateLength) ||
			d.keypad == nil {
			return ErrInvalidState
		}
		keypadState := make([]byte, keypadStateLength)
		if _, err := io.ReadFull(reader, keypadState); err != nil || reader.Len() != 0 {
			return ErrInvalidState
		}
		var keypadErr error
		if allowInputExpansion {
			keypadErr = d.keypad.LoadStateSubset(keypadState)
		} else {
			keypadErr = d.keypad.LoadState(keypadState)
		}
		if keypadErr != nil {
			return keypadErr
		}
	} else if version == 7 {
		var touchscreenStateLength uint32
		if binary.Read(reader, binary.LittleEndian, &touchscreenStateLength) != nil ||
			uint64(touchscreenStateLength) > uint64(reader.Len()) ||
			reader.Len() != int(touchscreenStateLength) || d.touchscreen == nil {
			return ErrInvalidState
		}
		touchscreenState := make([]byte, touchscreenStateLength)
		if _, err := io.ReadFull(reader, touchscreenState); err != nil || reader.Len() != 0 {
			return ErrInvalidState
		}
		if err := d.touchscreen.LoadState(touchscreenState); err != nil {
			return err
		}
	} else if d.keypad != nil {
		if !allowInputExpansion {
			return ErrInvalidState
		}
		if err := d.keypad.Reset(); err != nil {
			return err
		}
	} else if d.touchscreen != nil {
		if !allowInputExpansion {
			return ErrInvalidState
		}
		if err := d.touchscreen.Reset(); err != nil {
			return err
		}
	}
	d.status = status | d.resetStatus&^inputMask
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
			offset == qualcommPrimaryGPIOInputOffset {
			return fmt.Errorf("offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[offset]; duplicate {
			return fmt.Errorf("duplicate offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		seen[offset] = struct{}{}
	}
	return nil
}

func validateQualcommPrimaryClockReadOnlyRegisters(
	writableOffsets []uint32,
	registers []QualcommPrimaryClockReadOnlyRegister,
) error {
	seen := make(map[uint32]struct{},
		len(qualcommPrimaryClockWritableOffsets)+len(writableOffsets)+len(registers)+1)
	seen[qualcommPrimaryGPIOInputOffset] = struct{}{}
	for _, offset := range mergedQualcommPrimaryClockWritableOffsets(writableOffsets) {
		seen[offset] = struct{}{}
	}
	for _, register := range registers {
		if register.Offset%4 != 0 || register.Offset >= QualcommPrimaryClockWindowSize {
			return fmt.Errorf("offset 0x%x: %w", register.Offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[register.Offset]; duplicate {
			return fmt.Errorf("duplicate offset 0x%x: %w", register.Offset, ErrInvalidRegion)
		}
		seen[register.Offset] = struct{}{}
	}
	return nil
}

func validateQualcommPrimaryClockInterruptRegisters(
	writableOffsets []uint32,
	readOnlyRegisters []QualcommPrimaryClockReadOnlyRegister,
	registers []QualcommPrimaryClockInterruptRegister,
) error {
	writable := make(map[uint32]struct{}, len(qualcommPrimaryClockWritableOffsets)+len(writableOffsets))
	for _, offset := range mergedQualcommPrimaryClockWritableOffsets(writableOffsets) {
		writable[offset] = struct{}{}
	}
	statusOffsets := make(map[uint32]struct{}, len(readOnlyRegisters)+len(registers)+1)
	statusOffsets[qualcommPrimaryGPIOInputOffset] = struct{}{}
	for _, register := range readOnlyRegisters {
		statusOffsets[register.Offset] = struct{}{}
	}
	for _, register := range registers {
		if register.StatusOffset%4 != 0 || register.StatusOffset >= QualcommPrimaryClockWindowSize ||
			register.StatusOffset == register.ClearOffset || len(register.Bits) == 0 {
			return ErrInvalidRegion
		}
		if _, duplicate := statusOffsets[register.StatusOffset]; duplicate {
			return fmt.Errorf("duplicate status offset 0x%x: %w", register.StatusOffset, ErrInvalidRegion)
		}
		if _, overlap := writable[register.StatusOffset]; overlap {
			return fmt.Errorf("writable status offset 0x%x: %w", register.StatusOffset, ErrInvalidRegion)
		}
		if _, allowed := writable[register.ClearOffset]; !allowed {
			return fmt.Errorf("unwritable clear offset 0x%x: %w", register.ClearOffset, ErrInvalidRegion)
		}
		statusOffsets[register.StatusOffset] = struct{}{}
		seenBits := make(map[uint8]struct{}, len(register.Bits))
		seenSources := make(map[uint8]struct{}, len(register.Bits))
		for _, bit := range register.Bits {
			if bit.Bit >= 32 || bit.Source >= 64 {
				return ErrInvalidRegion
			}
			if _, duplicate := seenBits[bit.Bit]; duplicate {
				return fmt.Errorf("duplicate status bit %d: %w", bit.Bit, ErrInvalidRegion)
			}
			if _, duplicate := seenSources[bit.Source]; duplicate {
				return fmt.Errorf("duplicate interrupt source %d: %w", bit.Source, ErrInvalidRegion)
			}
			seenBits[bit.Bit] = struct{}{}
			seenSources[bit.Source] = struct{}{}
		}
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
	_ Device               = (*QualcommPrimaryClockControl)(nil)
	_ StatefulDevice       = (*QualcommPrimaryClockControl)(nil)
	_ SubsetStatefulDevice = (*QualcommPrimaryClockControl)(nil)
)
