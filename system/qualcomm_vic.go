package system

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	// QualcommVectoredInterruptControllerBaseOffset is the compact VIC window
	// observed inside the MSM6260-family CHIP register page.
	QualcommVectoredInterruptControllerBaseOffset = uint32(0x0400)
	QualcommVectoredInterruptControllerWindowSize = uint32(0x0200)

	qualcommVICAcknowledge0Offset = uint32(0x00)
	qualcommVICAcknowledge1Offset = uint32(0x04)
	qualcommVICEnable0Offset      = uint32(0x30)
	qualcommVICEnable1Offset      = uint32(0x34)
	qualcommVICStatus0Offset      = uint32(0x74)
	qualcommVICStatus1Offset      = uint32(0x78)
	qualcommVICVectorReadOffset   = uint32(0x9c)
	qualcommVICPendingReadOffset  = uint32(0xa0)
	qualcommVICVectorWriteOffset  = uint32(0xa4)
	qualcommVICInServiceOffset    = uint32(0xa8)
	qualcommVICNoPendingVector    = uint32(0x3f)
	qualcommVICNoInServiceVector  = uint32(0xff)
)

var ErrQualcommVectoredInterruptControllerMMIO = errors.New(
	"unsupported Qualcomm vectored interrupt-controller register",
)

// QualcommVectoredInterruptConfig describes the source packing used by one
// compact Qualcomm VIC instance. SCH-W830 firmware exposes 49 sources as a
// 25-bit first bank followed by a 24-bit second bank; keeping that split in
// profile data avoids baking a handset-specific interrupt map into the core.
type QualcommVectoredInterruptConfig struct {
	SourceCount        uint8
	Bank0Sources       uint8
	ReverseSourceOrder bool
	VectorOffset       uint8
}

func (c QualcommVectoredInterruptConfig) validate() error {
	if c.SourceCount == 0 || c.SourceCount > 64 || c.Bank0Sources == 0 ||
		c.Bank0Sources >= c.SourceCount || c.Bank0Sources > 32 ||
		c.SourceCount-c.Bank0Sources > 32 ||
		uint16(c.VectorOffset)+uint16(c.SourceCount)-1 >= uint16(qualcommVICNoPendingVector) {
		return fmt.Errorf("invalid Qualcomm vectored interrupt configuration")
	}
	return nil
}

// QualcommVectoredInterruptController models the compact two-bank VIC used by
// the SCH-W830 firmware. Sources are sticky until acknowledged; an asserted
// level source remains pending after acknowledgement. The lowest numbered
// enabled pending source wins until priority registers are evidenced.
type QualcommVectoredInterruptController struct {
	config         QualcommVectoredInterruptConfig
	sink           InterruptLineSink
	enable         [2]uint32
	status         [2]uint32
	level          [2]uint32
	inService      uint8
	inServiceValid bool
}

func NewQualcommVectoredInterruptController(
	config QualcommVectoredInterruptConfig,
	sink InterruptLineSink,
) (*QualcommVectoredInterruptController, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	device := &QualcommVectoredInterruptController{config: config, sink: sink}
	if err := device.Reset(); err != nil {
		return nil, err
	}
	return device, nil
}

func (d *QualcommVectoredInterruptController) Reset() error {
	d.enable = [2]uint32{}
	d.status = [2]uint32{}
	d.level = [2]uint32{}
	d.inService = 0
	d.inServiceValid = false
	return d.updateOutput()
}

func (d *QualcommVectoredInterruptController) SourceCount() uint8 {
	return d.config.SourceCount
}

func (d *QualcommVectoredInterruptController) Handles(offset uint32) bool {
	switch offset {
	case qualcommVICAcknowledge0Offset, qualcommVICAcknowledge1Offset,
		qualcommVICEnable0Offset, qualcommVICEnable1Offset,
		qualcommVICStatus0Offset, qualcommVICStatus1Offset,
		qualcommVICVectorReadOffset, qualcommVICPendingReadOffset,
		qualcommVICVectorWriteOffset, qualcommVICInServiceOffset:
		return true
	default:
		return false
	}
}

func (d *QualcommVectoredInterruptController) Read(offset uint32, width Width) (uint32, error) {
	if width != Width32 {
		return 0, fmt.Errorf(
			"%w: read%d at 0x%x",
			ErrQualcommVectoredInterruptControllerMMIO,
			width*8,
			offset,
		)
	}
	switch offset {
	case qualcommVICEnable0Offset:
		return d.enable[0], nil
	case qualcommVICEnable1Offset:
		return d.enable[1], nil
	case qualcommVICStatus0Offset:
		return d.status[0], nil
	case qualcommVICStatus1Offset:
		return d.status[1], nil
	case qualcommVICVectorReadOffset:
		return d.claimPendingSource()
	case qualcommVICPendingReadOffset:
		return d.claimPendingSource()
	case qualcommVICInServiceOffset:
		if !d.inServiceValid {
			return qualcommVICNoInServiceVector, nil
		}
		return uint32(d.vectorForSource(d.inService)), nil
	default:
		return 0, fmt.Errorf(
			"%w: read32 at 0x%x",
			ErrQualcommVectoredInterruptControllerMMIO,
			offset,
		)
	}
}

func (d *QualcommVectoredInterruptController) Write(offset uint32, width Width, value uint32) error {
	if width != Width32 {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrQualcommVectoredInterruptControllerMMIO,
			width*8,
			value,
			offset,
		)
	}
	switch offset {
	case qualcommVICAcknowledge0Offset:
		d.status[0] &^= value &^ d.level[0]
		d.completeAcknowledgedSource(0, value)
	case qualcommVICAcknowledge1Offset:
		d.status[1] &^= value &^ d.level[1]
		d.completeAcknowledgedSource(1, value)
	case qualcommVICEnable0Offset:
		d.enable[0] = value & d.validMask(0)
	case qualcommVICEnable1Offset:
		d.enable[1] = value & d.validMask(1)
	case qualcommVICVectorWriteOffset:
		d.inService = 0
		d.inServiceValid = false
	case qualcommVICStatus0Offset, qualcommVICStatus1Offset,
		qualcommVICVectorReadOffset, qualcommVICPendingReadOffset,
		qualcommVICInServiceOffset:
		return fmt.Errorf(
			"%w: write32 value 0x%x at read-only offset 0x%x",
			ErrQualcommVectoredInterruptControllerMMIO,
			value,
			offset,
		)
	default:
		return fmt.Errorf(
			"%w: write32 value 0x%x at 0x%x",
			ErrQualcommVectoredInterruptControllerMMIO,
			value,
			offset,
		)
	}
	return d.updateOutput()
}

func (d *QualcommVectoredInterruptController) SetSource(source uint8, asserted bool) error {
	bank, mask, err := d.sourceMask(source)
	if err != nil {
		return err
	}
	if asserted {
		d.level[bank] |= mask
		d.status[bank] |= mask
	} else {
		d.level[bank] &^= mask
	}
	return d.updateOutput()
}

func (d *QualcommVectoredInterruptController) PulseSource(source uint8) error {
	bank, mask, err := d.sourceMask(source)
	if err != nil {
		return err
	}
	d.status[bank] |= mask
	return d.updateOutput()
}

func (d *QualcommVectoredInterruptController) PendingStatusBanks() [2]uint32 {
	return d.status
}

func (d *QualcommVectoredInterruptController) pendingSource() (uint8, bool) {
	for bank := uint8(0); bank < 2; bank++ {
		pending := d.status[bank] & d.enable[bank]
		if pending == 0 {
			continue
		}
		packed := uint8(bits.TrailingZeros32(pending))
		if bank == 1 {
			packed += d.config.Bank0Sources
		}
		return d.logicalSource(packed), true
	}
	return 0, false
}

func (d *QualcommVectoredInterruptController) claimPendingSource() (uint32, error) {
	source, pending := d.pendingSource()
	if !pending {
		return qualcommVICNoPendingVector, nil
	}
	previousSource, previousValid := d.inService, d.inServiceValid
	d.inService = source
	d.inServiceValid = true
	if err := d.updateOutput(); err != nil {
		d.inService, d.inServiceValid = previousSource, previousValid
		_ = d.updateOutput()
		return 0, err
	}
	return uint32(d.vectorForSource(source)), nil
}

func (d *QualcommVectoredInterruptController) completeAcknowledgedSource(bank uint8, value uint32) {
	if !d.inServiceValid {
		return
	}
	sourceBank, mask, err := d.sourceMask(d.inService)
	if err == nil && sourceBank == bank && value&mask != 0 {
		d.inService = 0
		d.inServiceValid = false
	}
}

func (d *QualcommVectoredInterruptController) sourceMask(source uint8) (uint8, uint32, error) {
	if source >= d.config.SourceCount {
		return 0, 0, fmt.Errorf("invalid Qualcomm vectored interrupt source %d", source)
	}
	packed := d.packedSource(source)
	if packed < d.config.Bank0Sources {
		return 0, uint32(1) << packed, nil
	}
	return 1, uint32(1) << (packed - d.config.Bank0Sources), nil
}

func (d *QualcommVectoredInterruptController) packedSource(source uint8) uint8 {
	if d.config.ReverseSourceOrder {
		return d.config.SourceCount - 1 - source
	}
	return source
}

func (d *QualcommVectoredInterruptController) logicalSource(packed uint8) uint8 {
	if d.config.ReverseSourceOrder {
		return d.config.SourceCount - 1 - packed
	}
	return packed
}

func (d *QualcommVectoredInterruptController) vectorForSource(source uint8) uint8 {
	return d.packedSource(source) + d.config.VectorOffset
}

func (d *QualcommVectoredInterruptController) validMask(bank uint8) uint32 {
	count := d.config.Bank0Sources
	if bank == 1 {
		count = d.config.SourceCount - d.config.Bank0Sources
	}
	if count == 32 {
		return ^uint32(0)
	}
	return uint32(1)<<count - 1
}

func (d *QualcommVectoredInterruptController) updateOutput() error {
	if d.sink == nil {
		return nil
	}
	asserted := !d.inServiceValid &&
		(d.status[0]&d.enable[0] != 0 || d.status[1]&d.enable[1] != 0)
	if err := d.sink.SetInterruptLine(cpu.InterruptIRQ, asserted); err != nil {
		return fmt.Errorf("drive Qualcomm vectored IRQ output: %w", err)
	}
	return nil
}

func (d *QualcommVectoredInterruptController) SaveState() ([]byte, error) {
	state := make([]byte, 40)
	copy(state, "QVIC")
	binary.LittleEndian.PutUint32(state[4:8], 2)
	state[8] = d.config.SourceCount
	state[9] = d.config.Bank0Sources
	if d.config.ReverseSourceOrder {
		state[10] = 1
	}
	state[11] = d.config.VectorOffset
	state[12] = d.inService
	if d.inServiceValid {
		state[13] = 1
	}
	offset := 16
	for _, banks := range [][2]uint32{d.enable, d.status, d.level} {
		for _, value := range banks {
			binary.LittleEndian.PutUint32(state[offset:offset+4], value)
			offset += 4
		}
	}
	return state, nil
}

func (d *QualcommVectoredInterruptController) LoadState(state []byte) error {
	reverse := uint8(0)
	if d.config.ReverseSourceOrder {
		reverse = 1
	}
	if len(state) != 40 || string(state[:4]) != "QVIC" ||
		binary.LittleEndian.Uint32(state[4:8]) != 2 ||
		state[8] != d.config.SourceCount || state[9] != d.config.Bank0Sources ||
		state[10] != reverse || state[11] != d.config.VectorOffset ||
		state[13] > 1 || state[13] == 1 && state[12] >= d.config.SourceCount ||
		state[14] != 0 || state[15] != 0 {
		return ErrInvalidState
	}
	offset := 16
	var enable, status, level [2]uint32
	for _, banks := range []*[2]uint32{&enable, &status, &level} {
		for index := range banks {
			banks[index] = binary.LittleEndian.Uint32(state[offset : offset+4])
			offset += 4
		}
	}
	if enable[0]&^d.validMask(0) != 0 || enable[1]&^d.validMask(1) != 0 ||
		status[0]&^d.validMask(0) != 0 || status[1]&^d.validMask(1) != 0 ||
		level[0]&^d.validMask(0) != 0 || level[1]&^d.validMask(1) != 0 ||
		status[0]&level[0] != level[0] || status[1]&level[1] != level[1] {
		return ErrInvalidState
	}
	previous := *d
	d.enable, d.status, d.level = enable, status, level
	d.inService = state[12]
	d.inServiceValid = state[13] == 1
	if err := d.updateOutput(); err != nil {
		*d = previous
		_ = d.updateOutput()
		return err
	}
	return nil
}

var _ StatefulDevice = (*QualcommVectoredInterruptController)(nil)
