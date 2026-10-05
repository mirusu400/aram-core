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
	qualcommVICMaximumGroups      = 6
)

var ErrQualcommVectoredInterruptControllerMMIO = errors.New(
	"unsupported Qualcomm vectored interrupt-controller register",
)

// QualcommVectoredInterruptConfig describes the source packing used by one
// compact Qualcomm VIC instance. SCH-W830 firmware exposes 49 sources as a
// 25-bit first bank followed by a 24-bit second bank; keeping that split in
// profile data avoids baking a handset-specific interrupt map into the core.
type QualcommVectoredInterruptGroupConfig struct {
	Source       uint8
	EnableOffset uint32
	StatusOffset uint32
	ValidMask    uint32
}

type QualcommVectoredInterruptConfig struct {
	SourceCount        uint8
	Bank0Sources       uint8
	ReverseSourceOrder bool
	VectorOffset       uint8
	// ResetEnabledSources restores first-level masks retained by an earlier
	// boot stage. Most boards reset both banks to zero and let firmware program
	// them explicitly.
	ResetEnabledSources [2]uint32
	GroupCount          uint8
	Groups              [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig
}

func (c QualcommVectoredInterruptConfig) validate() error {
	if c.SourceCount == 0 || c.SourceCount > 64 || c.Bank0Sources == 0 ||
		c.Bank0Sources >= c.SourceCount || c.Bank0Sources > 32 ||
		c.SourceCount-c.Bank0Sources > 32 ||
		uint16(c.VectorOffset)+uint16(c.SourceCount)-1 >= uint16(qualcommVICNoPendingVector) {
		return fmt.Errorf("invalid Qualcomm vectored interrupt configuration")
	}
	bankMask := func(count uint8) uint32 {
		if count == 32 {
			return ^uint32(0)
		}
		return uint32(1)<<count - 1
	}
	if c.ResetEnabledSources[0]&^bankMask(c.Bank0Sources) != 0 ||
		c.ResetEnabledSources[1]&^bankMask(c.SourceCount-c.Bank0Sources) != 0 {
		return fmt.Errorf("invalid Qualcomm vectored interrupt reset mask")
	}
	if c.GroupCount > uint8(len(c.Groups)) {
		return fmt.Errorf("invalid Qualcomm vectored interrupt group count %d", c.GroupCount)
	}
	seenSources := make(map[uint8]struct{}, c.GroupCount)
	seenOffsets := make(map[uint32]struct{}, c.GroupCount*2)
	for index := uint8(0); index < c.GroupCount; index++ {
		group := c.Groups[index]
		if group.Source >= c.SourceCount || group.ValidMask == 0 ||
			group.EnableOffset%4 != 0 || group.StatusOffset%4 != 0 ||
			group.EnableOffset >= QualcommVectoredInterruptControllerWindowSize ||
			group.StatusOffset >= QualcommVectoredInterruptControllerWindowSize ||
			isQualcommVICCoreOffset(group.EnableOffset) ||
			isQualcommVICCoreOffset(group.StatusOffset) ||
			group.EnableOffset == group.StatusOffset {
			return fmt.Errorf("invalid Qualcomm vectored interrupt group %d", index)
		}
		if _, duplicate := seenSources[group.Source]; duplicate {
			return fmt.Errorf("duplicate Qualcomm vectored interrupt group source %d", group.Source)
		}
		seenSources[group.Source] = struct{}{}
		for _, offset := range []uint32{group.EnableOffset, group.StatusOffset} {
			if _, duplicate := seenOffsets[offset]; duplicate {
				return fmt.Errorf("duplicate Qualcomm vectored interrupt group offset 0x%x", offset)
			}
			seenOffsets[offset] = struct{}{}
		}
	}
	return nil
}

func isQualcommVICCoreOffset(offset uint32) bool {
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
	groupEnable    [qualcommVICMaximumGroups]uint32
	groupLevel     [qualcommVICMaximumGroups]uint32
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
	d.enable = d.config.ResetEnabledSources
	d.status = [2]uint32{}
	d.level = [2]uint32{}
	d.groupEnable = [qualcommVICMaximumGroups]uint32{}
	d.groupLevel = [qualcommVICMaximumGroups]uint32{}
	d.inService = 0
	d.inServiceValid = false
	return d.updateOutput()
}

func (d *QualcommVectoredInterruptController) SourceCount() uint8 {
	return d.config.SourceCount
}

func (d *QualcommVectoredInterruptController) Handles(offset uint32) bool {
	if isQualcommVICCoreOffset(offset) {
		return true
	}
	_, _, ok := d.groupForOffset(offset)
	return ok
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
	if index, status, ok := d.groupForOffset(offset); ok {
		if status {
			return d.groupLevel[index], nil
		}
		return d.groupEnable[index], nil
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
		return d.readPendingSource()
	case qualcommVICVectorWriteOffset:
		// The AMSS diagnostics snapshot the completion port along with the
		// adjacent vector registers. Hardware reads this write-only latch as zero.
		return 0, nil
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
	if index, status, ok := d.groupForOffset(offset); ok {
		if status {
			return fmt.Errorf(
				"%w: write32 value 0x%x at read-only group status offset 0x%x",
				ErrQualcommVectoredInterruptControllerMMIO,
				value,
				offset,
			)
		}
		// The second-level enable apertures are ENSET registers, matching the
		// first-level VIC banks below.  Firmware enables children independently;
		// replacing the word here would make a later child silently disable every
		// one registered before it.
		d.groupEnable[index] |= value & d.config.Groups[index].ValidMask
		return d.refreshGroupSource(index)
	}
	switch offset {
	case qualcommVICAcknowledge0Offset:
		d.status[0] &^= value &^ d.level[0]
	case qualcommVICAcknowledge1Offset:
		d.status[1] &^= value &^ d.level[1]
	case qualcommVICEnable0Offset:
		// ENSET is a write-one-to-set register. In particular, the native
		// boot code writes zero while preserving masks installed by the PBL.
		d.enable[0] |= value & d.validMask(0)
	case qualcommVICEnable1Offset:
		d.enable[1] |= value & d.validMask(1)
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

// SetGroupedSource drives one second-level interrupt input. MSM6260-family
// firmware reads the group's raw status word before dispatching the child ISR,
// while the group's enabled aggregate is presented as an ordinary first-level
// VIC source.
func (d *QualcommVectoredInterruptController) SetGroupedSource(
	statusOffset uint32,
	mask uint32,
	asserted bool,
) error {
	index, status, ok := d.groupForOffset(statusOffset)
	if !ok || !status || mask == 0 || mask&^d.config.Groups[index].ValidMask != 0 {
		return fmt.Errorf("invalid Qualcomm vectored group source 0x%x/0x%x", statusOffset, mask)
	}
	if asserted {
		d.groupLevel[index] |= mask
	} else {
		d.groupLevel[index] &^= mask
	}
	return d.refreshGroupSource(index)
}

func (d *QualcommVectoredInterruptController) groupForOffset(
	offset uint32,
) (uint8, bool, bool) {
	for index := uint8(0); index < d.config.GroupCount; index++ {
		group := d.config.Groups[index]
		if offset == group.EnableOffset {
			return index, false, true
		}
		if offset == group.StatusOffset {
			return index, true, true
		}
	}
	return 0, false, false
}

func (d *QualcommVectoredInterruptController) refreshGroupSource(index uint8) error {
	if index >= d.config.GroupCount {
		return fmt.Errorf("invalid Qualcomm vectored interrupt group %d", index)
	}
	group := d.config.Groups[index]
	asserted := d.groupLevel[index]&d.groupEnable[index] != 0
	if err := d.SetSource(group.Source, asserted); err != nil {
		return err
	}
	if asserted {
		return nil
	}
	// A second-level group is a level input to the first-level VIC. Once every
	// enabled child is clear, its aggregate raw status is clear as well; keeping
	// the first-level latch set would immediately redispatch an empty group. The
	// MSM6280 dispatcher clears the child before returning rather than writing a
	// separate vector-completion word, so a deasserted claimed aggregate also
	// completes that in-service source.
	bank, mask, err := d.sourceMask(group.Source)
	if err != nil {
		return err
	}
	d.status[bank] &^= mask
	if d.inServiceValid && d.inService == group.Source {
		d.inService = 0
		d.inServiceValid = false
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
	// The compact controller does not queue another edge for the source whose
	// vector is currently in service. In particular, a periodic timer expiring
	// during its own handler is coalesced instead of recursively retriggering the
	// dispatcher before a lower-priority pending source can run.
	if d.inServiceValid && d.inService == source {
		return nil
	}
	d.status[bank] |= mask
	return d.updateOutput()
}

func (d *QualcommVectoredInterruptController) PendingStatusBanks() [2]uint32 {
	return d.status
}

// EnabledSourceBanks returns the guest-programmed first-level source masks.
// It is primarily useful when correlating a board peripheral with firmware
// interrupt-registration tables.
func (d *QualcommVectoredInterruptController) EnabledSourceBanks() [2]uint32 {
	return d.enable
}

// InServiceSource reports the source currently claimed by the guest.
func (d *QualcommVectoredInterruptController) InServiceSource() (uint8, bool) {
	return d.inService, d.inServiceValid
}

func (d *QualcommVectoredInterruptController) pendingSource() (uint8, bool) {
	for bank := uint8(0); bank < 2; bank++ {
		pending := d.status[bank] & d.enable[bank]
		// A source which re-latches while its handler is running is serviced by
		// a later architectural IRQ. It must not claim the pending-vector port
		// again inside the same dispatcher pass, or a high-priority periodic
		// source can starve every lower-priority source indefinitely.
		if d.inServiceValid {
			serviceBank, serviceMask, err := d.sourceMask(d.inService)
			if err == nil && serviceBank == bank {
				pending &^= serviceMask
			}
		}
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
		return d.completeEmptyService()
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

func (d *QualcommVectoredInterruptController) readPendingSource() (uint32, error) {
	source, pending := d.pendingSource()
	if !pending {
		return d.completeEmptyService()
	}
	// The pending port is a non-claiming look-ahead used while the native
	// dispatcher drains several sources inside one architectural IRQ.  The
	// current-vector port above owns the in-service latch; changing it here
	// makes the dispatcher's current-vector consistency check reject the next
	// source and leave the scheduler tick permanently in service.
	return uint32(d.vectorForSource(source)), nil
}

func (d *QualcommVectoredInterruptController) completeEmptyService() (uint32, error) {
	// Native dispatchers drain every pending vector inside one architectural
	// IRQ entry. A source ACK clears its sticky status but must not reassert the
	// CPU line between vector reads. Reading the idle sentinel is the completion
	// handshake once the dispatcher has emptied the controller.
	d.inService = 0
	d.inServiceValid = false
	if err := d.updateOutput(); err != nil {
		return 0, err
	}
	return qualcommVICNoPendingVector, nil
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
	state := make([]byte, 40+int(d.config.GroupCount)*8)
	copy(state, "QVIC")
	binary.LittleEndian.PutUint32(state[4:8], 3)
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
	state[14] = d.config.GroupCount
	offset := 16
	for _, banks := range [][2]uint32{d.enable, d.status, d.level} {
		for _, value := range banks {
			binary.LittleEndian.PutUint32(state[offset:offset+4], value)
			offset += 4
		}
	}
	for index := uint8(0); index < d.config.GroupCount; index++ {
		binary.LittleEndian.PutUint32(state[offset:offset+4], d.groupEnable[index])
		offset += 4
		binary.LittleEndian.PutUint32(state[offset:offset+4], d.groupLevel[index])
		offset += 4
	}
	return state, nil
}

func (d *QualcommVectoredInterruptController) LoadState(state []byte) error {
	reverse := uint8(0)
	if d.config.ReverseSourceOrder {
		reverse = 1
	}
	if len(state) < 40 || string(state[:4]) != "QVIC" ||
		binary.LittleEndian.Uint32(state[4:8]) != 3 ||
		len(state) != 40+int(d.config.GroupCount)*8 ||
		state[8] != d.config.SourceCount || state[9] != d.config.Bank0Sources ||
		state[10] != reverse || state[11] != d.config.VectorOffset ||
		state[13] > 1 || state[13] == 1 && state[12] >= d.config.SourceCount ||
		state[14] != d.config.GroupCount || state[15] != 0 {
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
	var groupEnable, groupLevel [qualcommVICMaximumGroups]uint32
	for index := uint8(0); index < d.config.GroupCount; index++ {
		groupEnable[index] = binary.LittleEndian.Uint32(state[offset : offset+4])
		offset += 4
		groupLevel[index] = binary.LittleEndian.Uint32(state[offset : offset+4])
		offset += 4
		validMask := d.config.Groups[index].ValidMask
		if groupEnable[index]&^validMask != 0 || groupLevel[index]&^validMask != 0 {
			return ErrInvalidState
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
	d.groupEnable, d.groupLevel = groupEnable, groupLevel
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
