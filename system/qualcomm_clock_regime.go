package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// QualcommClockRegimeWindowSize covers the sparse clock-control apertures used
// below 0x90007000. Reserved gaps are kept unmapped by the device even though
// the bus owns the enclosing window.
const QualcommClockRegimeWindowSize = 0x7000

var ErrQualcommClockRegimeMMIO = errors.New("unsupported Qualcomm clock-regime register")

// QualcommClockRegime is the sparse word-addressed legacy clock-regime
// register file. Current firmware programs dividers, sources, and gate values
// through read/modify/write sequences, so words inside an evidenced aperture
// are persistent latches.
// Oscillator lock timing and derived device frequencies remain separate from
// this register-storage layer.
type QualcommClockRegime struct {
	registers [QualcommClockRegimeWindowSize / 4]uint32
}

func NewQualcommClockRegime() *QualcommClockRegime {
	return &QualcommClockRegime{}
}

var qualcommClockRegimeApertures = [...]struct {
	start uint32
	end   uint32
}{
	{0x0480, 0x0500},
	{0x0680, 0x0700},
	{0x1000, 0x1100},
	{0x1900, 0x1a00},
	{0x2000, 0x2100},
	{0x2400, 0x2600},
	{0x3080, 0x3100},
	{0x4900, 0x4a00},
	{0x4d00, 0x4e00},
	{0x5000, 0x6000},
	{0x6000, 0x6100},
}

func isQualcommClockRegimeOffset(offset uint32) bool {
	if offset%4 != 0 {
		return false
	}
	for _, aperture := range qualcommClockRegimeApertures {
		if offset >= aperture.start && offset < aperture.end {
			return true
		}
	}
	return false
}

func (d *QualcommClockRegime) Reset() error {
	d.registers = [QualcommClockRegimeWindowSize / 4]uint32{}
	return nil
}

func (d *QualcommClockRegime) Read(offset uint32, width Width) (uint32, error) {
	if width != Width32 || !isQualcommClockRegimeOffset(offset) {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommClockRegimeMMIO, width*8, offset)
	}
	return d.registers[offset/4], nil
}

func (d *QualcommClockRegime) Write(offset uint32, width Width, value uint32) error {
	if width != Width32 || !isQualcommClockRegimeOffset(offset) {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrQualcommClockRegimeMMIO, width*8, value, offset,
		)
	}
	d.registers[offset/4] = value
	return nil
}

func (d *QualcommClockRegime) SaveState() ([]byte, error) {
	state := make([]byte, 8+len(d.registers)*4)
	copy(state, "QCRG")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	for index, value := range d.registers {
		binary.LittleEndian.PutUint32(state[8+index*4:], value)
	}
	return state, nil
}

func (d *QualcommClockRegime) LoadState(state []byte) error {
	if len(state) != 8+len(d.registers)*4 || string(state[:4]) != "QCRG" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 {
		return ErrInvalidState
	}
	var registers [QualcommClockRegimeWindowSize / 4]uint32
	for index := range registers {
		registers[index] = binary.LittleEndian.Uint32(state[8+index*4:])
	}
	d.registers = registers
	return nil
}

var (
	_ Device         = (*QualcommClockRegime)(nil)
	_ StatefulDevice = (*QualcommClockRegime)(nil)
)
