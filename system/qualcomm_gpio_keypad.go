package system

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

var ErrQualcommGPIOKeypad = errors.New("invalid Qualcomm GPIO keypad operation")

// QualcommGPIOKeypadProfile describes a conventional active-low keypad
// matrix. Columns are digital-input bit numbers. Each row is active while the
// profiled bit is set in the most recent firmware write to its GPIO output
// register. Keys provide stable host-facing names without changing the
// electrical matrix model.
type QualcommGPIOKeypadProfile struct {
	Columns []uint8
	Rows    []QualcommGPIOKeypadRowProfile
	Keys    []QualcommGPIOKeyProfile
}

type QualcommGPIOOutputBank uint8

const (
	QualcommGPIOOutputInterrupt QualcommGPIOOutputBank = iota
	QualcommGPIOOutputSecondaryClock
)

type QualcommGPIOKeypadRowProfile struct {
	OutputBank   QualcommGPIOOutputBank
	OutputOffset uint32
	OutputMask   uint32
}

type QualcommGPIOKeyProfile struct {
	ID     string
	Row    uint8
	Column uint8
}

// QualcommGPIOWriteObserver receives successful writes in a GPIO-bearing MMIO
// aperture. It never reads or writes the bus, so observing a firmware
// row-selection write cannot re-enter MMIO dispatch.
type QualcommGPIOWriteObserver interface {
	ObserveGPIOWrite(offset, value uint32)
}

// QualcommGPIOKeypad turns host key state and guest-selected output rows into
// the active-low input value sampled by the primary-clock GPIO register.
type QualcommGPIOKeypad struct {
	profile      QualcommGPIOKeypadProfile
	fingerprint  [sha256.Size]byte
	selectors    []qualcommGPIOKeypadSelector
	outputValues map[qualcommGPIOKeypadSelector]uint32
	pressed      []bool
	keysByID     map[string]int
}

type qualcommGPIOKeypadSelector struct {
	bank   QualcommGPIOOutputBank
	offset uint32
}

type qualcommGPIOKeypadBankObserver struct {
	keypad *QualcommGPIOKeypad
	bank   QualcommGPIOOutputBank
}

func (o qualcommGPIOKeypadBankObserver) ObserveGPIOWrite(offset, value uint32) {
	o.keypad.ObserveGPIOBankWrite(o.bank, offset, value)
}

func NewQualcommGPIOKeypad(profile QualcommGPIOKeypadProfile) (*QualcommGPIOKeypad, error) {
	if err := profile.validate(); err != nil {
		return nil, err
	}
	profile = cloneQualcommGPIOKeypadProfile(profile)
	keypad := &QualcommGPIOKeypad{
		profile:     profile,
		fingerprint: fingerprintQualcommGPIOKeypadProfile(profile),
		pressed:     make([]bool, len(profile.Rows)*len(profile.Columns)),
		keysByID:    make(map[string]int, len(profile.Keys)),
	}
	selectorSet := make(map[qualcommGPIOKeypadSelector]struct{}, len(profile.Rows))
	for _, row := range profile.Rows {
		selectorSet[qualcommGPIOKeypadSelector{bank: row.OutputBank, offset: row.OutputOffset}] = struct{}{}
	}
	for selector := range selectorSet {
		keypad.selectors = append(keypad.selectors, selector)
	}
	sort.Slice(keypad.selectors, func(left, right int) bool {
		if keypad.selectors[left].bank != keypad.selectors[right].bank {
			return keypad.selectors[left].bank < keypad.selectors[right].bank
		}
		return keypad.selectors[left].offset < keypad.selectors[right].offset
	})
	for _, key := range profile.Keys {
		keypad.keysByID[key.ID] = keypad.matrixIndex(key.Row, key.Column)
	}
	_ = keypad.Reset()
	return keypad, nil
}

func (p QualcommGPIOKeypadProfile) validate() error {
	if len(p.Columns) == 0 || len(p.Columns) > 32 || len(p.Rows) == 0 || len(p.Rows) > 256 {
		return fmt.Errorf("%w: empty or oversized matrix", ErrQualcommGPIOKeypad)
	}
	columns := make(map[uint8]struct{}, len(p.Columns))
	for _, line := range p.Columns {
		if line >= 32 {
			return fmt.Errorf("%w: input line %d", ErrQualcommGPIOKeypad, line)
		}
		if _, duplicate := columns[line]; duplicate {
			return fmt.Errorf("%w: duplicate input line %d", ErrQualcommGPIOKeypad, line)
		}
		columns[line] = struct{}{}
	}
	rows := make(map[[3]uint32]struct{}, len(p.Rows))
	for _, row := range p.Rows {
		key := [3]uint32{uint32(row.OutputBank), row.OutputOffset, row.OutputMask}
		validOutput := false
		switch row.OutputBank {
		case QualcommGPIOOutputInterrupt:
			validOutput = qualcommInterruptControllerSupportsWrite(row.OutputOffset)
		case QualcommGPIOOutputSecondaryClock:
			validOutput = row.OutputOffset%4 == 0 &&
				row.OutputOffset < QualcommSecondaryClockWindowSize &&
				row.OutputOffset != qualcommSecondaryClockDisabledStatusOffset
		}
		if !validOutput || row.OutputMask == 0 {
			return fmt.Errorf("%w: row selector at 0x%x mask 0x%x", ErrQualcommGPIOKeypad, row.OutputOffset, row.OutputMask)
		}
		if _, duplicate := rows[key]; duplicate {
			return fmt.Errorf("%w: duplicate row selector at 0x%x mask 0x%x", ErrQualcommGPIOKeypad, row.OutputOffset, row.OutputMask)
		}
		rows[key] = struct{}{}
	}
	ids := make(map[string]struct{}, len(p.Keys))
	coordinates := make(map[[2]uint8]struct{}, len(p.Keys))
	for _, key := range p.Keys {
		coordinate := [2]uint8{key.Row, key.Column}
		if !validProfileID(key.ID) || int(key.Row) >= len(p.Rows) || int(key.Column) >= len(p.Columns) {
			return fmt.Errorf("%w: key %q at row %d column %d", ErrQualcommGPIOKeypad, key.ID, key.Row, key.Column)
		}
		if _, duplicate := ids[key.ID]; duplicate {
			return fmt.Errorf("%w: duplicate key ID %q", ErrQualcommGPIOKeypad, key.ID)
		}
		if _, duplicate := coordinates[coordinate]; duplicate {
			return fmt.Errorf("%w: duplicate key coordinate row %d column %d", ErrQualcommGPIOKeypad, key.Row, key.Column)
		}
		ids[key.ID] = struct{}{}
		coordinates[coordinate] = struct{}{}
	}
	return nil
}

func cloneQualcommGPIOKeypadProfile(profile QualcommGPIOKeypadProfile) QualcommGPIOKeypadProfile {
	profile.Columns = append([]uint8(nil), profile.Columns...)
	profile.Rows = append([]QualcommGPIOKeypadRowProfile(nil), profile.Rows...)
	profile.Keys = append([]QualcommGPIOKeyProfile(nil), profile.Keys...)
	return profile
}

func fingerprintQualcommGPIOKeypadProfile(profile QualcommGPIOKeypadProfile) [sha256.Size]byte {
	var encoded bytes.Buffer
	_ = binary.Write(&encoded, binary.LittleEndian, uint32(len(profile.Columns)))
	_, _ = encoded.Write(profile.Columns)
	_ = binary.Write(&encoded, binary.LittleEndian, uint32(len(profile.Rows)))
	for _, row := range profile.Rows {
		_ = encoded.WriteByte(byte(row.OutputBank))
		_ = binary.Write(&encoded, binary.LittleEndian, row.OutputOffset)
		_ = binary.Write(&encoded, binary.LittleEndian, row.OutputMask)
	}
	// Keys are host-facing aliases for matrix coordinates. Adding a discovered
	// name does not alter the electrical device or its serialized pressed bits.
	return sha256.Sum256(encoded.Bytes())
}

func (d *QualcommGPIOKeypad) Reset() error {
	clear(d.pressed)
	d.outputValues = make(map[qualcommGPIOKeypadSelector]uint32, len(d.selectors))
	for _, selector := range d.selectors {
		d.outputValues[selector] = 0
	}
	return nil
}

func (d *QualcommGPIOKeypad) ObserveGPIOWrite(offset, value uint32) {
	d.ObserveGPIOBankWrite(QualcommGPIOOutputInterrupt, offset, value)
}

func (d *QualcommGPIOKeypad) ObserveGPIOBankWrite(
	bank QualcommGPIOOutputBank,
	offset, value uint32,
) {
	selector := qualcommGPIOKeypadSelector{bank: bank, offset: offset}
	if _, observed := d.outputValues[selector]; observed {
		d.outputValues[selector] = value
	}
}

// InputStatus applies active-low matrix columns to the other physical inputs
// already supplied by the primary-clock GPIO bank.
func (d *QualcommGPIOKeypad) InputStatus(base uint32) uint32 {
	status := base
	columnCount := len(d.profile.Columns)
	for rowIndex, row := range d.profile.Rows {
		selector := qualcommGPIOKeypadSelector{bank: row.OutputBank, offset: row.OutputOffset}
		if d.outputValues[selector]&row.OutputMask == 0 {
			continue
		}
		for columnIndex, line := range d.profile.Columns {
			if d.pressed[rowIndex*columnCount+columnIndex] {
				status &^= uint32(1) << line
			}
		}
	}
	return status
}

func (d *QualcommGPIOKeypad) SetKey(id string, pressed bool) error {
	index, ok := d.keysByID[id]
	if !ok {
		return fmt.Errorf("key %q: %w", id, ErrQualcommGPIOKeypad)
	}
	d.pressed[index] = pressed
	return nil
}

// SetMatrixKey is the low-level equivalent used while identifying an unknown
// handset's key table. Product frontends should use stable profiled key IDs.
func (d *QualcommGPIOKeypad) SetMatrixKey(row, column uint8, pressed bool) error {
	if int(row) >= len(d.profile.Rows) || int(column) >= len(d.profile.Columns) {
		return fmt.Errorf("row %d column %d: %w", row, column, ErrQualcommGPIOKeypad)
	}
	d.pressed[d.matrixIndex(row, column)] = pressed
	return nil
}

func (d *QualcommGPIOKeypad) matrixIndex(row, column uint8) int {
	return int(row)*len(d.profile.Columns) + int(column)
}

func (d *QualcommGPIOKeypad) inputMask() uint32 {
	var mask uint32
	for _, line := range d.profile.Columns {
		mask |= uint32(1) << line
	}
	return mask
}

func (d *QualcommGPIOKeypad) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("QGKP")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_, _ = output.Write(d.fingerprint[:])
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.selectors)))
	for _, selector := range d.selectors {
		_ = output.WriteByte(byte(selector.bank))
		_ = binary.Write(&output, binary.LittleEndian, selector.offset)
		_ = binary.Write(&output, binary.LittleEndian, d.outputValues[selector])
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.pressed)))
	for _, pressed := range d.pressed {
		if pressed {
			_ = output.WriteByte(1)
		} else {
			_ = output.WriteByte(0)
		}
	}
	return output.Bytes(), nil
}

func (d *QualcommGPIOKeypad) LoadState(state []byte) error {
	return d.loadState(state, false)
}

// LoadStateSubset accepts a diagnostic state made with the legacy v1
// fingerprint that included host key aliases. A mismatched fingerprint is
// safe only when no key was held across the snapshot boundary; selector and
// matrix dimensions must still match exactly.
func (d *QualcommGPIOKeypad) LoadStateSubset(state []byte) error {
	return d.loadState(state, true)
}

func (d *QualcommGPIOKeypad) loadState(state []byte, allowReleasedLegacyProfile bool) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version uint32
	var fingerprint [sha256.Size]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QGKP" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 {
		return ErrInvalidState
	}
	if _, err := io.ReadFull(reader, fingerprint[:]); err != nil {
		return ErrInvalidState
	}
	fingerprintMatches := fingerprint == d.fingerprint
	if !fingerprintMatches && !allowReleasedLegacyProfile {
		return ErrInvalidState
	}
	var selectorCount uint32
	if binary.Read(reader, binary.LittleEndian, &selectorCount) != nil ||
		selectorCount != uint32(len(d.selectors)) {
		return ErrInvalidState
	}
	values := make(map[qualcommGPIOKeypadSelector]uint32, selectorCount)
	for index := uint32(0); index < selectorCount; index++ {
		bank, bankErr := reader.ReadByte()
		var offset, value uint32
		if bankErr != nil || binary.Read(reader, binary.LittleEndian, &offset) != nil ||
			binary.Read(reader, binary.LittleEndian, &value) != nil ||
			int(index) >= len(d.selectors) || QualcommGPIOOutputBank(bank) != d.selectors[index].bank ||
			offset != d.selectors[index].offset {
			return ErrInvalidState
		}
		values[d.selectors[index]] = value
	}
	var pressedCount uint32
	if binary.Read(reader, binary.LittleEndian, &pressedCount) != nil ||
		pressedCount != uint32(len(d.pressed)) || reader.Len() != int(pressedCount) {
		return ErrInvalidState
	}
	pressed := make([]bool, pressedCount)
	for index := range pressed {
		value, err := reader.ReadByte()
		if err != nil || value > 1 {
			return ErrInvalidState
		}
		pressed[index] = value != 0
	}
	if !fingerprintMatches {
		for _, held := range pressed {
			if held {
				return ErrInvalidState
			}
		}
	}
	d.outputValues = values
	d.pressed = pressed
	return nil
}
