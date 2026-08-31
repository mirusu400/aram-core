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
	Columns          []uint8
	Rows             []QualcommGPIOKeypadRowProfile
	Keys             []QualcommGPIOKeyProfile
	InterruptGroups  []QualcommGPIOInterruptGroupProfile
	ColumnInterrupts []QualcommGPIOKeypadColumnInterruptProfile
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

// QualcommGPIOInterruptGroupProfile describes one bank in the MSM6xxx GPIO
// interrupt block. Clear is write-one-to-clear, the three configuration words
// are guest programmed, and Status is the raw pending word read by the shared
// group ISR. Several groups may feed the same aggregate interrupt source.
type QualcommGPIOInterruptGroupProfile struct {
	ClearOffset           uint32
	EnableOffset          uint32
	DetectOffset          uint32
	PolarityOffset        uint32
	StatusOffset          uint32
	InterruptSource       uint8
	UseVectoredController bool
}

// QualcommGPIOKeypadColumnInterruptProfile maps one electrical matrix column
// to the GPIO bit that wakes the firmware's scanner. Row selection still
// determines the value returned by InputStatus; the interrupt is generated
// when the shared column line first becomes active.
type QualcommGPIOKeypadColumnInterruptProfile struct {
	Column uint8
	Group  uint8
	Mask   uint32
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
	profile          QualcommGPIOKeypadProfile
	fingerprint      [sha256.Size]byte
	selectors        []qualcommGPIOKeypadSelector
	outputValues     map[qualcommGPIOKeypadSelector]uint32
	pressed          []bool
	keysByID         map[string]int
	interruptGroups  []qualcommGPIOInterruptGroupState
	columnInterrupts map[uint8]QualcommGPIOKeypadColumnInterruptProfile
	interruptDrivers []qualcommGPIOInterruptSourceDriver
}

type qualcommGPIOInterruptSourceDriver interface {
	PulseSource(uint8) error
}

type qualcommGPIOInterruptGroupState struct {
	enable   uint32
	detect   uint32
	polarity uint32
	status   uint32
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
		profile:          profile,
		fingerprint:      fingerprintQualcommGPIOKeypadProfile(profile),
		pressed:          make([]bool, len(profile.Rows)*len(profile.Columns)),
		keysByID:         make(map[string]int, len(profile.Keys)),
		interruptGroups:  make([]qualcommGPIOInterruptGroupState, len(profile.InterruptGroups)),
		columnInterrupts: make(map[uint8]QualcommGPIOKeypadColumnInterruptProfile, len(profile.ColumnInterrupts)),
		interruptDrivers: make([]qualcommGPIOInterruptSourceDriver, len(profile.InterruptGroups)),
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
	for _, interrupt := range profile.ColumnInterrupts {
		keypad.columnInterrupts[interrupt.Column] = interrupt
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
	if len(p.InterruptGroups) > 256 || len(p.ColumnInterrupts) > len(p.Columns) ||
		(len(p.InterruptGroups) == 0) != (len(p.ColumnInterrupts) == 0) {
		return fmt.Errorf("%w: incomplete GPIO interrupt routing", ErrQualcommGPIOKeypad)
	}
	interruptOffsets := make(map[uint32]struct{}, len(p.InterruptGroups)*5)
	for index, group := range p.InterruptGroups {
		for _, offset := range []uint32{
			group.ClearOffset,
			group.EnableOffset,
			group.DetectOffset,
			group.PolarityOffset,
			group.StatusOffset,
		} {
			if offset%4 != 0 || offset >= QualcommPrimaryClockWindowSize {
				return fmt.Errorf("%w: interrupt group %d offset 0x%x", ErrQualcommGPIOKeypad, index, offset)
			}
			if _, duplicate := interruptOffsets[offset]; duplicate {
				return fmt.Errorf("%w: duplicate interrupt offset 0x%x", ErrQualcommGPIOKeypad, offset)
			}
			interruptOffsets[offset] = struct{}{}
		}
	}
	interruptColumns := make(map[uint8]struct{}, len(p.ColumnInterrupts))
	interruptLines := make(map[[2]uint32]struct{}, len(p.ColumnInterrupts))
	for _, interrupt := range p.ColumnInterrupts {
		line := [2]uint32{uint32(interrupt.Group), interrupt.Mask}
		if int(interrupt.Column) >= len(p.Columns) || int(interrupt.Group) >= len(p.InterruptGroups) ||
			interrupt.Mask == 0 || interrupt.Mask&(interrupt.Mask-1) != 0 {
			return fmt.Errorf(
				"%w: column %d interrupt group %d mask 0x%x",
				ErrQualcommGPIOKeypad, interrupt.Column, interrupt.Group, interrupt.Mask,
			)
		}
		if _, duplicate := interruptColumns[interrupt.Column]; duplicate {
			return fmt.Errorf("%w: duplicate interrupt column %d", ErrQualcommGPIOKeypad, interrupt.Column)
		}
		if _, duplicate := interruptLines[line]; duplicate {
			return fmt.Errorf(
				"%w: duplicate interrupt group %d mask 0x%x",
				ErrQualcommGPIOKeypad, interrupt.Group, interrupt.Mask,
			)
		}
		interruptColumns[interrupt.Column] = struct{}{}
		interruptLines[line] = struct{}{}
	}
	return nil
}

func cloneQualcommGPIOKeypadProfile(profile QualcommGPIOKeypadProfile) QualcommGPIOKeypadProfile {
	profile.Columns = append([]uint8(nil), profile.Columns...)
	profile.Rows = append([]QualcommGPIOKeypadRowProfile(nil), profile.Rows...)
	profile.Keys = append([]QualcommGPIOKeyProfile(nil), profile.Keys...)
	profile.InterruptGroups = append(
		[]QualcommGPIOInterruptGroupProfile(nil), profile.InterruptGroups...,
	)
	profile.ColumnInterrupts = append(
		[]QualcommGPIOKeypadColumnInterruptProfile(nil), profile.ColumnInterrupts...,
	)
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
	_ = binary.Write(&encoded, binary.LittleEndian, uint32(len(profile.InterruptGroups)))
	for _, group := range profile.InterruptGroups {
		for _, offset := range []uint32{
			group.ClearOffset,
			group.EnableOffset,
			group.DetectOffset,
			group.PolarityOffset,
			group.StatusOffset,
		} {
			_ = binary.Write(&encoded, binary.LittleEndian, offset)
		}
		_ = encoded.WriteByte(group.InterruptSource)
		if group.UseVectoredController {
			_ = encoded.WriteByte(1)
		} else {
			_ = encoded.WriteByte(0)
		}
	}
	_ = binary.Write(&encoded, binary.LittleEndian, uint32(len(profile.ColumnInterrupts)))
	for _, interrupt := range profile.ColumnInterrupts {
		_ = encoded.WriteByte(interrupt.Column)
		_ = encoded.WriteByte(interrupt.Group)
		_ = binary.Write(&encoded, binary.LittleEndian, interrupt.Mask)
	}
	// Keys are host-facing aliases for matrix coordinates. Adding a discovered
	// name does not alter the electrical device or its serialized pressed bits.
	return sha256.Sum256(encoded.Bytes())
}

func (d *QualcommGPIOKeypad) Reset() error {
	clear(d.pressed)
	clear(d.interruptGroups)
	d.outputValues = make(map[qualcommGPIOKeypadSelector]uint32, len(d.selectors))
	for _, selector := range d.selectors {
		d.outputValues[selector] = 0
	}
	return nil
}

// AttachInterruptControllers connects the GPIO group outputs selected by the
// board profile. The keypad owns only GPIO pending/configuration state; the
// interrupt controllers retain their normal acknowledgement semantics.
func (d *QualcommGPIOKeypad) AttachInterruptControllers(
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
) error {
	if len(d.profile.InterruptGroups) == 0 {
		return nil
	}
	for _, driver := range d.interruptDrivers {
		if driver != nil {
			return fmt.Errorf("attach Qualcomm GPIO keypad interrupts: %w", ErrQualcommGPIOKeypad)
		}
	}
	drivers := make([]qualcommGPIOInterruptSourceDriver, len(d.profile.InterruptGroups))
	for index, group := range d.profile.InterruptGroups {
		if group.UseVectoredController {
			if vectoredInterruptController == nil {
				return fmt.Errorf("attach Qualcomm GPIO keypad VIC group %d: %w", index, ErrQualcommGPIOKeypad)
			}
			drivers[index] = vectoredInterruptController
		} else {
			if interruptController == nil {
				return fmt.Errorf("attach Qualcomm GPIO keypad INTCTL group %d: %w", index, ErrQualcommGPIOKeypad)
			}
			drivers[index] = interruptController
		}
	}
	d.interruptDrivers = drivers
	return nil
}

func (d *QualcommGPIOKeypad) readPrimaryGPIORegister(offset uint32) (uint32, bool) {
	for index, group := range d.profile.InterruptGroups {
		state := d.interruptGroups[index]
		switch offset {
		case group.ClearOffset:
			return 0, true
		case group.EnableOffset:
			return state.enable, true
		case group.DetectOffset:
			return state.detect, true
		case group.PolarityOffset:
			return state.polarity, true
		case group.StatusOffset:
			return state.status, true
		}
	}
	return 0, false
}

func (d *QualcommGPIOKeypad) writePrimaryGPIORegister(offset, value uint32) (bool, error) {
	for index, group := range d.profile.InterruptGroups {
		state := &d.interruptGroups[index]
		switch offset {
		case group.ClearOffset:
			state.status &^= value
			return true, nil
		case group.EnableOffset:
			wasPending := state.status&state.enable != 0
			newlyEnabled := value &^ state.enable
			state.enable = value
			d.latchEnabledActiveLevelColumns(index, newlyEnabled)
			if !wasPending && state.status&state.enable != 0 {
				return true, d.signalInterruptGroup(index)
			}
			return true, nil
		case group.DetectOffset:
			state.detect = value
			return true, nil
		case group.PolarityOffset:
			state.polarity = value
			return true, nil
		}
	}
	return false, nil
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
	columnCount := len(d.profile.Columns)
	return d.setMatrixKey(index/columnCount, index%columnCount, pressed)
}

// SetMatrixKey is the low-level equivalent used while identifying an unknown
// handset's key table. Product frontends should use stable profiled key IDs.
func (d *QualcommGPIOKeypad) SetMatrixKey(row, column uint8, pressed bool) error {
	if int(row) >= len(d.profile.Rows) || int(column) >= len(d.profile.Columns) {
		return fmt.Errorf("row %d column %d: %w", row, column, ErrQualcommGPIOKeypad)
	}
	return d.setMatrixKey(int(row), int(column), pressed)
}

func (d *QualcommGPIOKeypad) setMatrixKey(row, column int, pressed bool) error {
	index := row*len(d.profile.Columns) + column
	if d.pressed[index] == pressed {
		return nil
	}
	wasActive := d.columnActive(column)
	d.pressed[index] = pressed
	nowActive := d.columnActive(column)
	if wasActive == nowActive {
		return nil
	}
	interrupt, routed := d.columnInterrupts[uint8(column)]
	if !routed {
		return nil
	}
	groupState := &d.interruptGroups[interrupt.Group]
	lineHigh := !nowActive
	activeHigh := groupState.polarity&interrupt.Mask != 0
	if lineHigh != activeHigh {
		return nil
	}
	// DA05 programs these keypad lines as active-low. Latch the active
	// transition exactly once; the guest's shared group ISR clears Status via
	// ClearOffset after dispatching the registered GPIO callback.
	groupState.status |= interrupt.Mask
	return d.signalInterruptGroup(int(interrupt.Group))
}

func (d *QualcommGPIOKeypad) columnActive(column int) bool {
	columnCount := len(d.profile.Columns)
	for row := range d.profile.Rows {
		if d.pressed[row*columnCount+column] {
			return true
		}
	}
	return false
}

// latchEnabledActiveLevelColumns models the GPIO block's level detector at
// the point firmware unmasks a line. W770 deliberately masks the keypad GPIOs
// while its debounce timer scans the matrix, then unmasks them again. A held
// special key must therefore create one fresh aggregate interrupt on that
// disabled-to-enabled transition; re-latching immediately on W1C would spin
// inside the shared GPIO ISR before the timer can run.
func (d *QualcommGPIOKeypad) latchEnabledActiveLevelColumns(groupIndex int, enabled uint32) {
	groupState := &d.interruptGroups[groupIndex]
	for column, interrupt := range d.columnInterrupts {
		if int(interrupt.Group) != groupIndex || enabled&interrupt.Mask == 0 ||
			groupState.detect&interrupt.Mask != 0 {
			continue
		}
		lineHigh := !d.columnActive(int(column))
		activeHigh := groupState.polarity&interrupt.Mask != 0
		if lineHigh == activeHigh {
			groupState.status |= interrupt.Mask
		}
	}
}

func (d *QualcommGPIOKeypad) signalInterruptGroup(groupIndex int) error {
	group := d.profile.InterruptGroups[groupIndex]
	driver := d.interruptDrivers[groupIndex]
	if driver == nil {
		if d.interruptGroups[groupIndex].status&d.interruptGroups[groupIndex].enable == 0 {
			return nil
		}
		return fmt.Errorf("signal Qualcomm GPIO keypad group %d: %w", groupIndex, ErrQualcommGPIOKeypad)
	}
	if d.interruptGroups[groupIndex].status&d.interruptGroups[groupIndex].enable == 0 {
		return nil
	}
	if err := driver.PulseSource(group.InterruptSource); err != nil {
		return fmt.Errorf(
			"pulse Qualcomm GPIO keypad interrupt source %d: %w",
			group.InterruptSource, err,
		)
	}
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
	version := uint32(1)
	if len(d.interruptGroups) != 0 {
		version = 2
	}
	var output bytes.Buffer
	output.WriteString("QGKP")
	_ = binary.Write(&output, binary.LittleEndian, version)
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
	if version == 2 {
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.interruptGroups)))
		for _, group := range d.interruptGroups {
			_ = binary.Write(&output, binary.LittleEndian, group.enable)
			_ = binary.Write(&output, binary.LittleEndian, group.detect)
			_ = binary.Write(&output, binary.LittleEndian, group.polarity)
			_ = binary.Write(&output, binary.LittleEndian, group.status)
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
		binary.Read(reader, binary.LittleEndian, &version) != nil ||
		version != 1 && version != 2 ||
		version == 1 && len(d.interruptGroups) != 0 ||
		version == 2 && len(d.interruptGroups) == 0 {
		return ErrInvalidState
	}
	if _, err := io.ReadFull(reader, fingerprint[:]); err != nil {
		return ErrInvalidState
	}
	fingerprintMatches := fingerprint == d.fingerprint
	if !fingerprintMatches && (!allowReleasedLegacyProfile || version != 1) {
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
		pressedCount != uint32(len(d.pressed)) {
		return ErrInvalidState
	}
	remaining := int(pressedCount)
	if version == 2 {
		remaining += 4 + len(d.interruptGroups)*16
	}
	if reader.Len() != remaining {
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
	interruptGroups := make([]qualcommGPIOInterruptGroupState, len(d.interruptGroups))
	if version == 2 {
		var groupCount uint32
		if binary.Read(reader, binary.LittleEndian, &groupCount) != nil ||
			groupCount != uint32(len(interruptGroups)) {
			return ErrInvalidState
		}
		statusMasks := make([]uint32, len(interruptGroups))
		for _, interrupt := range d.profile.ColumnInterrupts {
			statusMasks[interrupt.Group] |= interrupt.Mask
		}
		for index := range interruptGroups {
			group := &interruptGroups[index]
			if binary.Read(reader, binary.LittleEndian, &group.enable) != nil ||
				binary.Read(reader, binary.LittleEndian, &group.detect) != nil ||
				binary.Read(reader, binary.LittleEndian, &group.polarity) != nil ||
				binary.Read(reader, binary.LittleEndian, &group.status) != nil ||
				group.status&^statusMasks[index] != 0 {
				return ErrInvalidState
			}
		}
	}
	if reader.Len() != 0 {
		return ErrInvalidState
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
	d.interruptGroups = interruptGroups
	return nil
}
