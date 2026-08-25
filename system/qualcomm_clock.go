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
	Status          uint32
	InputMask       uint32
	WritableOffsets []uint32
}

// QualcommPrimaryClockControl models the bounded primary control window used
// by early OEM firmware. It includes the four raw digital input lines at
// offset 0x588 and only the writable registers evidenced for a board profile;
// unrelated registers remain explicit faults.
type QualcommPrimaryClockControl struct {
	inputMask       uint32
	resetStatus     uint32
	status          uint32
	writableOffsets []uint32
	registers       map[uint32]uint32
	keypad          *QualcommGPIOKeypad
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
	device := &QualcommPrimaryClockControl{
		inputMask:       inputMask,
		resetStatus:     config.Status,
		status:          config.Status,
		writableOffsets: mergedQualcommPrimaryClockWritableOffsets(config.WritableOffsets),
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
		return d.keypad.Reset()
	}
	return nil
}

func (d *QualcommPrimaryClockControl) Read(offset uint32, width Width) (uint32, error) {
	if offset == qualcommPrimaryGPIOInputOffset && width == Width32 {
		return d.InputStatus(), nil
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
	if keypad == nil || d.keypad != nil || keypad.inputMask()&^d.inputMask != 0 {
		return fmt.Errorf("attach Qualcomm GPIO keypad: %w", ErrQualcommPrimaryClockMMIO)
	}
	d.keypad = keypad
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
	version := uint32(5)
	var keypadState []byte
	if d.keypad != nil {
		var err error
		keypadState, err = d.keypad.SaveState()
		if err != nil {
			return nil, err
		}
		version = 6
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
	} else if version == 5 || version == 6 {
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
	if version == 6 {
		minimumRemaining += 4
	}
	if reader.Len() < minimumRemaining || version != 6 && reader.Len() != minimumRemaining {
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
	} else if d.keypad != nil {
		if !allowInputExpansion {
			return ErrInvalidState
		}
		if err := d.keypad.Reset(); err != nil {
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
