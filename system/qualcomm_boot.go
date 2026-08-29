package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"sort"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	QualcommBootControlWindowSize    = 0x10000
	QualcommSecondaryClockWindowSize = 0x1000
	qualcommPBLMagic                 = 0xa1b2c3d4
	qualcommPBLServiceEnd            = 0x015d
	qualcommPBLFlashTypeNAND2K       = 6
	qualcommLegacyPBLFeatureEnd      = 0x0131
	qualcommPBLFeatureDataHeaderSize = 0x2c
)

var (
	ErrQualcommBootControlMMIO    = errors.New("unsupported Qualcomm boot-control register")
	ErrQualcommSecondaryClockMMIO = errors.New("unsupported Qualcomm secondary-clock register")
)

type QualcommNANDPBLConfig struct {
	Entry                    uint32
	TableAddress             uint32
	LegacyFeatureDataAddress uint32
	PageSize                 uint32
	EraseBlockSize           uint32
	FlashSize                uint64
	BadBlockLimit            uint32
}

type QualcommBootControlConfig struct {
	HardwareRevision            uint32
	NANDInterfaceMode           uint32
	EBIMemoryConfiguration      uint32
	ClockModeStatus             uint32
	WritableOffsets             []uint32
	HalfwordOffsets             []uint32
	MixedWidthOffsets           []uint32
	ReadOnlyRegisters           []QualcommBootReadOnlyRegister
	RegisterResets              []QualcommBootRegisterReset
	CompletionEvents            []QualcommCompletionEventConfig
	LegacyUARTControllers       []uint32
	SBIControllers              []uint32
	SBICompletionStatus         uint32
	NANDReady                   *StatusSignal
	InterruptController         *QualcommInterruptController
	VectoredInterruptController *QualcommVectoredInterruptController
	TimeTickClock               *QualcommTimeTickClockConfig
}

// QualcommBootReadOnlyRegister describes a profile-specific word register
// whose fixed reset, strap, or idle-status value is evidenced but whose writes
// are not.
type QualcommBootReadOnlyRegister struct {
	Offset uint32
	Value  uint32
}

// QualcommBootRegisterReset gives a profiled writable word register its
// hardware reset value. WritableOffsets still defines whether the register is
// present; keeping reset values separate lets related Qualcomm parts reuse the
// same sparse register-bank implementation without treating zero as a
// universal power-on value.
type QualcommBootRegisterReset struct {
	Offset uint32
	Value  uint32
}

// QualcommCompletionEventConfig describes an evidenced command/status/ack
// handshake within the shared control window. This keeps device-specific
// register locations in the board profile while the deterministic completion
// and interrupt behavior remains reusable.
type QualcommCompletionEventConfig struct {
	StartOffset           uint32
	StartMask             uint32
	StatusOffset          uint32
	StatusMask            uint32
	AcknowledgeOffset     uint32
	AcknowledgeWidth      Width
	AcknowledgeMask       uint32
	InterruptSource       uint8
	UseVectoredController bool
}

// QualcommCompletionHandler supplies a deferred side effect for a profiled
// command/completion register pair. QueueCompletion runs during the MMIO write
// and must not access the physical bus; Advance runs after a CPU runner slice.
type QualcommCompletionHandler interface {
	QueueCompletion(registerValue func(offset uint32) (uint32, bool)) error
	Advance(retiredInstructions uint64) error
	Reset() error
}

// QualcommTimeTickClockConfig relates deterministic instruction retirement to
// the free-running sleep-clock timetick. InterruptSource is platform/profile
// data: Qualcomm family members do not necessarily route TIMETICK_INT
// identically.
type QualcommTimeTickClockConfig struct {
	InstructionsPerSecond uint64
	TimeTickHz            uint64
	InterruptSource       uint8
	UseVectoredController bool
}

// NewQualcommNANDPBLHandoff builds the bounded PBL service data consumed by
// the early QCSBL. The missing mask-ROM remains an explicit HLE boundary.
func NewQualcommNANDPBLHandoff(config QualcommNANDPBLConfig) (BootHandoff, error) {
	if config.Entry&3 != 0 || config.TableAddress&3 != 0 || config.PageSize != 0x800 ||
		config.EraseBlockSize == 0 || config.EraseBlockSize%config.PageSize != 0 ||
		config.FlashSize == 0 || config.FlashSize%uint64(config.EraseBlockSize) != 0 ||
		config.FlashSize/uint64(config.EraseBlockSize) > uint64(^uint32(0)) ||
		config.BadBlockLimit == 0 ||
		(config.LegacyFeatureDataAddress != 0 && (config.LegacyFeatureDataAddress&3 != 0 ||
			uint64(config.LegacyFeatureDataAddress)+qualcommPBLFeatureDataHeaderSize+6*8 > 1<<32)) {
		return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
	}
	entries := [][2]uint32{
		{0x012f, config.EraseBlockSize / config.PageSize},
		{0x0130, uint32(config.FlashSize / uint64(config.EraseBlockSize))},
		{0x0132, config.PageSize},
		{0x0133, config.BadBlockLimit},
		{0x0141, qualcommPBLFlashTypeNAND2K},
		{qualcommPBLServiceEnd, 0},
	}
	table := make([]byte, qualcommPBLFeatureDataHeaderSize+len(entries)*8)
	for index, entry := range entries {
		binary.LittleEndian.PutUint32(table[qualcommPBLFeatureDataHeaderSize+index*8:], entry[0])
		binary.LittleEndian.PutUint32(table[qualcommPBLFeatureDataHeaderSize+index*8+4:], entry[1])
	}
	handoff := BootHandoff{
		ID:    "qualcomm.pbl-hle.nand2k-v1",
		Entry: config.Entry,
		Mode:  cpu.ModeARM,
		Registers: []RegisterSeed{
			{Register: cpu.RegisterR7, Value: qualcommPBLMagic},
			{Register: cpu.RegisterR8, Value: config.TableAddress},
		},
		Memory: []MemorySeed{{Address: config.TableAddress, Bytes: table}},
	}
	if config.LegacyFeatureDataAddress != 0 {
		// Earlier QCSBLs consume the same NAND facts through boot_feature_cfg
		// IDs in a fixed PBL-owned structure. Keep that compatibility ABI an
		// explicit handoff seed instead of treating high IRAM as magic RAM.
		legacyEntries := [][2]uint32{
			{0x0108, config.EraseBlockSize / config.PageSize},
			{0x0109, uint32(config.FlashSize / uint64(config.EraseBlockSize))},
			{0x010b, config.PageSize},
			{0x010c, config.BadBlockLimit},
			{0x0115, qualcommPBLFlashTypeNAND2K},
			{qualcommLegacyPBLFeatureEnd, 0},
		}
		legacy := make([]byte, qualcommPBLFeatureDataHeaderSize+len(legacyEntries)*8)
		for index, entry := range legacyEntries {
			binary.LittleEndian.PutUint32(legacy[qualcommPBLFeatureDataHeaderSize+index*8:], entry[0])
			binary.LittleEndian.PutUint32(legacy[qualcommPBLFeatureDataHeaderSize+index*8+4:], entry[1])
		}
		handoff.Memory = append(handoff.Memory, MemorySeed{
			Address: config.LegacyFeatureDataAddress,
			Bytes:   legacy,
		})
	}
	if err := handoff.Validate(); err != nil {
		return BootHandoff{}, err
	}
	return handoff, nil
}

var qualcommBootWritableOffsets = append(append([]uint32{
	0x0000, 0x0004, 0x0010, 0x0014,
	0x0028, 0x002c, 0x0030, 0x0034, 0x0038, 0x003c, 0x0040, 0x0044,
	0x004c, 0x0050, 0x0054, 0x0058, 0x005c, 0x0060,
	0x0068, 0x006c, 0x0070, 0x0074, 0x0078, 0x007c,
	0x0084, 0x0088, 0x008c, 0x0090, 0x0094, 0x0098,
	0x00a4, 0x00a8, 0x00ac, 0x00b0, 0x00b4, 0x00b8,
	0x00c4, 0x00c8, 0x00cc, 0x00d0, 0x00d4, 0x00d8, 0x00dc, 0x00e0,
	0x00e4, 0x00e8, 0x00ec, 0x00f0, 0x00f4, 0x00f8, 0x00fc,
	0x0100, 0x0114, 0x0124, 0x0128,
	0x0104, 0x0108, 0x0118, 0x013c,
	0x0200, 0x021c, 0x0220, 0x0228,
	0x0244, 0x024c, 0x0260, 0x0280, 0x0290, 0x0294,
	0x0330,
	0x0380, 0x0384, 0x0388, 0x03ac,
	0x0400, 0x0404, 0x0408, 0x040c, 0x0410, 0x0414, 0x0418, 0x041c, 0x0420, 0x0424,
	0x0430, 0x0434, 0x0438, 0x043c, 0x0440, 0x0444, 0x0448, 0x044c, 0x0450, 0x0454,
	0x0458, 0x045c, 0x0460, 0x0464, 0x0468, 0x046c, 0x0470,
	0x0aa0,
	0x0a00, 0x0a04, 0x0a48, 0x0aa8, 0x0aac, 0x0ab0, 0x0ab4,
	0x0ab8,
	0x0abc,
	0x0ac0, 0x0ac4,
	0x0ac8, 0x0acc, 0x0ad0, 0x0ad4, 0x0ad8, 0x0adc,
	0x0ae0, 0x5300, 0x5320, 0x5324, 0x5328, 0x532c, 0x5344, 0x54c4,
}, qualcommInterruptConfigWritableOffsets...), qualcommMPMCWritableOffsets...)

func validateQualcommBootControlWritableOffsets(offsets []uint32) error {
	seen := make(map[uint32]struct{}, len(qualcommBootWritableOffsets)+len(offsets))
	for _, offset := range qualcommBootWritableOffsets {
		seen[offset] = struct{}{}
	}
	for _, offset := range offsets {
		if offset%4 != 0 || offset >= QualcommBootControlWindowSize ||
			(offset >= 0x0900 && offset < 0x0900+QualcommInterruptControllerWindowSize) ||
			isQualcommBootControlSpecialOffset(offset) {
			return fmt.Errorf("offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[offset]; duplicate {
			return fmt.Errorf("duplicate offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		seen[offset] = struct{}{}
	}
	return nil
}

const (
	qualcommBootSBICommandOffset  = 0x08
	qualcommBootSBIResultOffset   = 0x10
	qualcommBootSBIStatusOffset   = 0x14
	qualcommBootSBICompleteStatus = 0x01
	qualcommBootSBIControllerSize = 0x18
)

var qualcommBootSBIRegisterOffsets = [...]uint32{0x00, 0x04, 0x08, 0x10, 0x14}

func validateQualcommBootControlConfigurationOffsets(
	writableOffsets []uint32,
	halfwordOffsets []uint32,
	mixedWidthOffsets []uint32,
	readOnlyRegisters []QualcommBootReadOnlyRegister,
	registerResets []QualcommBootRegisterReset,
	completionEvents []QualcommCompletionEventConfig,
	legacyUARTControllers []uint32,
	sbiControllers []uint32,
	sbiCompletionStatus uint32,
) error {
	if err := validateQualcommBootControlWritableOffsets(writableOffsets); err != nil {
		return err
	}
	seen := make(map[uint32]struct{}, len(qualcommBootWritableOffsets)+len(writableOffsets)+
		len(sbiControllers)*len(qualcommBootSBIRegisterOffsets))
	wordWritable := make(map[uint32]struct{}, len(qualcommBootWritableOffsets)+len(writableOffsets))
	for _, offset := range qualcommBootWritableOffsets {
		seen[offset] = struct{}{}
		wordWritable[offset] = struct{}{}
	}
	for _, offset := range writableOffsets {
		seen[offset] = struct{}{}
		wordWritable[offset] = struct{}{}
	}
	mixed := make(map[uint32]struct{}, len(mixedWidthOffsets))
	for _, offset := range mixedWidthOffsets {
		if offset%4 != 0 || isQualcommBootControlSpecialOffset(offset) {
			return fmt.Errorf("mixed-width offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, writable := seen[offset]; !writable {
			return fmt.Errorf("mixed-width offset 0x%x is not writable: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := mixed[offset]; duplicate {
			return fmt.Errorf("duplicate mixed-width offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		mixed[offset] = struct{}{}
	}
	for _, register := range readOnlyRegisters {
		offset := register.Offset
		if offset%4 != 0 || offset >= QualcommBootControlWindowSize ||
			(offset >= 0x0900 && offset < 0x0900+QualcommInterruptControllerWindowSize) ||
			isQualcommBootControlSpecialOffset(offset) {
			return fmt.Errorf("read-only offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[offset]; duplicate {
			return fmt.Errorf("duplicate read-only offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		seen[offset] = struct{}{}
	}
	resetOffsets := make(map[uint32]struct{}, len(registerResets))
	for _, reset := range registerResets {
		if _, writable := wordWritable[reset.Offset]; !writable {
			return fmt.Errorf(
				"reset value for non-writable word offset 0x%x: %w",
				reset.Offset,
				ErrInvalidRegion,
			)
		}
		if _, duplicate := resetOffsets[reset.Offset]; duplicate {
			return fmt.Errorf("duplicate reset value at offset 0x%x: %w", reset.Offset, ErrInvalidRegion)
		}
		resetOffsets[reset.Offset] = struct{}{}
	}
	bases := make(map[uint32]struct{}, len(sbiControllers))
	for _, base := range sbiControllers {
		if base%4 != 0 || uint64(base)+qualcommBootSBIControllerSize > QualcommBootControlWindowSize ||
			(base >= 0x0900 && base < 0x0900+QualcommInterruptControllerWindowSize) {
			return fmt.Errorf("SBI controller 0x%x: %w", base, ErrInvalidRegion)
		}
		if _, duplicate := bases[base]; duplicate {
			return fmt.Errorf("duplicate SBI controller 0x%x: %w", base, ErrInvalidRegion)
		}
		bases[base] = struct{}{}
		for _, relative := range qualcommBootSBIRegisterOffsets {
			offset := base + relative
			if isQualcommBootControlSpecialOffset(offset) {
				return fmt.Errorf("SBI controller register 0x%x: %w", offset, ErrInvalidRegion)
			}
			if _, duplicate := seen[offset]; duplicate {
				return fmt.Errorf("duplicate SBI controller register 0x%x: %w", offset, ErrInvalidRegion)
			}
			seen[offset] = struct{}{}
		}
	}
	if len(sbiControllers) == 0 {
		if sbiCompletionStatus != 0 {
			return fmt.Errorf("SBI completion status without controllers: %w", ErrInvalidRegion)
		}
	} else {
		if sbiCompletionStatus == 0 || sbiCompletionStatus%4 != 0 ||
			sbiCompletionStatus >= QualcommBootControlWindowSize ||
			(sbiCompletionStatus >= 0x0900 &&
				sbiCompletionStatus < 0x0900+QualcommInterruptControllerWindowSize) ||
			isQualcommBootControlSpecialOffset(sbiCompletionStatus) {
			return fmt.Errorf("SBI completion status 0x%x: %w", sbiCompletionStatus, ErrInvalidRegion)
		}
		if _, duplicate := seen[sbiCompletionStatus]; duplicate {
			return fmt.Errorf("duplicate SBI completion status 0x%x: %w", sbiCompletionStatus, ErrInvalidRegion)
		}
		seen[sbiCompletionStatus] = struct{}{}
	}
	halfwords := make(map[uint32]struct{}, len(halfwordOffsets))
	for _, offset := range halfwordOffsets {
		if offset%2 != 0 || uint64(offset)+uint64(Width16) > QualcommBootControlWindowSize ||
			(offset < 0x0900+QualcommInterruptControllerWindowSize &&
				offset+uint32(Width16) > 0x0900) ||
			isQualcommBootControlSpecialOffset(offset) {
			return fmt.Errorf("halfword offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := halfwords[offset]; duplicate {
			return fmt.Errorf("duplicate halfword offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		wordOffset := offset &^ uint32(3)
		if _, overlap := seen[wordOffset]; overlap {
			return fmt.Errorf("overlapping halfword offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		halfwords[offset] = struct{}{}
	}
	completionStarts := make(map[uint32]struct{}, len(completionEvents))
	for _, event := range completionEvents {
		if _, duplicate := completionStarts[event.StartOffset]; duplicate {
			return fmt.Errorf("duplicate completion start 0x%x: %w", event.StartOffset, ErrInvalidRegion)
		}
		completionStarts[event.StartOffset] = struct{}{}
	}
	completionStatuses := make(map[uint32]struct{}, len(completionEvents))
	for _, event := range completionEvents {
		if event.StartOffset%4 != 0 || event.StatusOffset%4 != 0 ||
			event.StartMask == 0 || event.StatusMask == 0 || event.AcknowledgeMask == 0 ||
			(event.AcknowledgeWidth != Width16 && event.AcknowledgeWidth != Width32) ||
			event.AcknowledgeOffset%uint32(event.AcknowledgeWidth) != 0 ||
			event.StatusOffset >= QualcommBootControlWindowSize ||
			(event.StatusOffset >= 0x0900 &&
				event.StatusOffset < 0x0900+QualcommInterruptControllerWindowSize) ||
			isQualcommBootControlSpecialOffset(event.StartOffset) ||
			isQualcommBootControlSpecialOffset(event.StatusOffset) ||
			isQualcommBootControlSpecialOffset(event.AcknowledgeOffset) {
			return fmt.Errorf("invalid completion event at status 0x%x: %w", event.StatusOffset, ErrInvalidRegion)
		}
		if _, writable := wordWritable[event.StartOffset]; !writable {
			return fmt.Errorf("completion start 0x%x is not writable: %w", event.StartOffset, ErrInvalidRegion)
		}
		if _, duplicate := seen[event.StatusOffset]; duplicate {
			return fmt.Errorf("completion status 0x%x overlaps a register: %w", event.StatusOffset, ErrInvalidRegion)
		}
		if _, duplicate := completionStatuses[event.StatusOffset]; duplicate {
			return fmt.Errorf("duplicate completion status 0x%x: %w", event.StatusOffset, ErrInvalidRegion)
		}
		completionStatuses[event.StatusOffset] = struct{}{}
		if _, startsCompletion := completionStarts[event.AcknowledgeOffset]; startsCompletion {
			return fmt.Errorf(
				"completion acknowledge 0x%x overlaps a start register: %w",
				event.AcknowledgeOffset,
				ErrInvalidRegion,
			)
		}
		for halfwordOffset := range halfwords {
			if halfwordOffset&^uint32(3) == event.StatusOffset {
				return fmt.Errorf(
					"completion status 0x%x overlaps halfword register 0x%x: %w",
					event.StatusOffset,
					halfwordOffset,
					ErrInvalidRegion,
				)
			}
		}
		switch event.AcknowledgeWidth {
		case Width16:
			if _, configured := halfwords[event.AcknowledgeOffset]; !configured {
				return fmt.Errorf(
					"completion acknowledge 0x%x is not a halfword register: %w",
					event.AcknowledgeOffset,
					ErrInvalidRegion,
				)
			}
			if event.AcknowledgeMask > 0xffff {
				return fmt.Errorf("completion acknowledge mask exceeds halfword: %w", ErrInvalidRegion)
			}
		case Width32:
			if _, configured := wordWritable[event.AcknowledgeOffset]; !configured {
				return fmt.Errorf(
					"completion acknowledge 0x%x is not a word register: %w",
					event.AcknowledgeOffset,
					ErrInvalidRegion,
				)
			}
		}
	}
	uartBases := make(map[uint32]struct{}, len(legacyUARTControllers))
	for _, base := range legacyUARTControllers {
		if base%4 != 0 || uint64(base)+uint64(qualcommLegacyUARTWindowSize) > QualcommBootControlWindowSize ||
			(base < 0x0900+QualcommInterruptControllerWindowSize &&
				base+qualcommLegacyUARTWindowSize > 0x0900) {
			return fmt.Errorf("legacy UART controller 0x%x: %w", base, ErrInvalidRegion)
		}
		if _, duplicate := uartBases[base]; duplicate {
			return fmt.Errorf("duplicate legacy UART controller 0x%x: %w", base, ErrInvalidRegion)
		}
		for sbiBase := range bases {
			if uint64(base) < uint64(sbiBase)+qualcommBootSBIControllerSize &&
				uint64(sbiBase) < uint64(base)+uint64(qualcommLegacyUARTWindowSize) {
				return fmt.Errorf("legacy UART controller 0x%x overlaps SBI controller: %w", base, ErrInvalidRegion)
			}
		}
		for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
			if _, configured := halfwords[base+relative]; !configured {
				return fmt.Errorf(
					"legacy UART controller 0x%x lacks halfword register 0x%x: %w",
					base,
					base+relative,
					ErrInvalidRegion,
				)
			}
		}
		uartBases[base] = struct{}{}
	}
	return nil
}

func isQualcommBootControlSpecialOffset(offset uint32) bool {
	switch offset {
	case 0x0274, 0x0400, 0x0404, 0x0430, 0x0434, 0x0474, 0x0478,
		0x0488, 0x049c, 0x04a0, 0x04a4, 0x04a8,
		0x0a40, 0x1004,
		0x5408, 0x540c, 0x54c0, 0x551c:
		return true
	default:
		return false
	}
}

func mergedQualcommBootControlWritableOffsets(
	extra, halfwords []uint32,
	readOnlyRegisters []QualcommBootReadOnlyRegister,
	completionEvents []QualcommCompletionEventConfig,
	sbiControllers []uint32,
	sbiCompletionStatus uint32,
) []uint32 {
	offsets := make(
		[]uint32,
		0,
		len(qualcommBootWritableOffsets)+len(extra)+len(halfwords)+len(readOnlyRegisters)+len(completionEvents)+
			len(sbiControllers)*len(qualcommBootSBIRegisterOffsets),
	)
	offsets = append(offsets, qualcommBootWritableOffsets...)
	offsets = append(offsets, extra...)
	offsets = append(offsets, halfwords...)
	for _, register := range readOnlyRegisters {
		offsets = append(offsets, register.Offset)
	}
	for _, event := range completionEvents {
		offsets = append(offsets, event.StatusOffset)
	}
	for _, base := range sbiControllers {
		for _, relative := range qualcommBootSBIRegisterOffsets {
			offsets = append(offsets, base+relative)
		}
	}
	if sbiCompletionStatus != 0 {
		offsets = append(offsets, sbiCompletionStatus)
	}
	sort.Slice(offsets, func(left, right int) bool { return offsets[left] < offsets[right] })
	return offsets
}

// The firmware's IRQ configuration routine maps interrupt IDs 0..48 in
// reverse order onto this word table.
var qualcommInterruptConfigWritableOffsets = func() []uint32 {
	offsets := make([]uint32, 0, 49)
	for offset := uint32(0x04b0); offset <= 0x0570; offset += 4 {
		offsets = append(offsets, offset)
	}
	return offsets
}()

// These offsets are the ARM PL172-compatible MPMC block at CHIP_BASE+0x1000.
// Keeping the documented register set explicit permits real timing/configuration
// sequences while accesses to reserved gaps continue to fault.
var qualcommMPMCWritableOffsets = []uint32{
	0x1000, 0x1008,
	0x1020, 0x1024, 0x1028, 0x1030, 0x1034, 0x1038, 0x103c,
	0x1040, 0x1044, 0x1048, 0x104c, 0x1050, 0x1054, 0x1058,
	0x1080,
	0x1100, 0x1104, 0x1120, 0x1124, 0x1140, 0x1144, 0x1160, 0x1164,
	0x1200, 0x1204, 0x1208, 0x120c, 0x1210, 0x1214, 0x1218,
	0x1220, 0x1224, 0x1228, 0x122c, 0x1230, 0x1234, 0x1238,
	0x1240, 0x1244, 0x1248, 0x124c, 0x1250, 0x1254, 0x1258,
	0x1260, 0x1264, 0x1268, 0x126c, 0x1270, 0x1274, 0x1278,
}

var qualcommSecondaryClockOffsets = []uint32{0x0400, 0x0404, 0x0408, 0x0430, 0x0434}

const qualcommSecondaryClockDisabledStatusOffset = 0x0440

// QualcommSecondaryClockReadOnlyRegister describes one board-wired input in
// the secondary GPIO/clock aperture. These inputs share the register page with
// the output latches above but must not silently become writable storage.
type QualcommSecondaryClockReadOnlyRegister struct {
	Offset uint32
	Value  uint32
}

type QualcommSecondaryClockConfig struct {
	WritableOffsets   []uint32
	ReadOnlyRegisters []QualcommSecondaryClockReadOnlyRegister
}

func validateQualcommSecondaryClockWritableOffsets(offsets []uint32) error {
	seen := make(map[uint32]struct{}, len(qualcommSecondaryClockOffsets)+len(offsets))
	for _, offset := range qualcommSecondaryClockOffsets {
		seen[offset] = struct{}{}
	}
	for _, offset := range offsets {
		if offset%4 != 0 || offset >= QualcommSecondaryClockWindowSize ||
			offset == qualcommSecondaryClockDisabledStatusOffset {
			return fmt.Errorf("secondary-clock offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[offset]; duplicate {
			return fmt.Errorf("duplicate secondary-clock offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		seen[offset] = struct{}{}
	}
	return nil
}

func validateQualcommSecondaryClockConfig(config QualcommSecondaryClockConfig) error {
	if err := validateQualcommSecondaryClockWritableOffsets(config.WritableOffsets); err != nil {
		return err
	}
	seen := make(map[uint32]struct{},
		len(qualcommSecondaryClockOffsets)+len(config.WritableOffsets)+1+len(config.ReadOnlyRegisters))
	for _, offset := range qualcommSecondaryClockOffsets {
		seen[offset] = struct{}{}
	}
	for _, offset := range config.WritableOffsets {
		seen[offset] = struct{}{}
	}
	seen[qualcommSecondaryClockDisabledStatusOffset] = struct{}{}
	for _, register := range config.ReadOnlyRegisters {
		if register.Offset%4 != 0 || register.Offset >= QualcommSecondaryClockWindowSize {
			return fmt.Errorf("secondary-clock read-only offset 0x%x: %w", register.Offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[register.Offset]; duplicate {
			return fmt.Errorf("duplicate secondary-clock offset 0x%x: %w", register.Offset, ErrInvalidRegion)
		}
		seen[register.Offset] = struct{}{}
	}
	return nil
}

// QualcommBootControl is an explicit compatibility bank for the currently
// evidenced system-control, MPMC, IRQ-configuration, and timetick registers.
// Registers with understood side effects are modeled separately; every
// unknown access fails.
type QualcommBootControl struct {
	hardwareRevision            uint32
	nandInterfaceMode           uint32
	ebiMemoryConfiguration      uint32
	clockModeStatus             uint32
	nandReady                   *StatusSignal
	interruptController         *QualcommInterruptController
	vectoredInterruptController *QualcommVectoredInterruptController
	writableOffsets             []uint32
	halfwordOffsets             map[uint32]struct{}
	mixedWidthOffsets           map[uint32]struct{}
	readOnlyRegisters           map[uint32]uint32
	registerResets              []QualcommBootRegisterReset
	completionEvents            []QualcommCompletionEventConfig
	completionHandlers          map[uint32]QualcommCompletionHandler
	orderedCompletionHandlers   []QualcommCompletionHandler
	legacyUARTControllers       map[uint32]struct{}
	sbiControllers              map[uint32]struct{}
	sbiCompletionStatus         uint32
	registers                   map[uint32]uint32
	watchdogServices            uint64
	timeTick                    uint32
	timeTickReadPhase           uint8
	timeTickClocked             bool
	timeTickInstructionRate     uint64
	timeTickHz                  uint64
	timeTickInterruptSource     uint8
	timeTickUseVectored         bool
	timeTickPhase               uint64
	timeTickMatchReady          bool
	timeTickMatchConfigured     bool
}

func NewQualcommBootControl(config QualcommBootControlConfig) (*QualcommBootControl, error) {
	if config.HardwareRevision>>28 == 0 ||
		config.NANDInterfaceMode != 2 && config.NANDInterfaceMode != 4 ||
		config.EBIMemoryConfiguration&0x5f80 != 0x5680 &&
			config.EBIMemoryConfiguration&0x5f80 != 0x5880 ||
		config.ClockModeStatus&^uint32(0x11) != 0 || config.NANDReady == nil {
		return nil, fmt.Errorf("invalid Qualcomm boot-control configuration")
	}
	if err := validateQualcommBootControlConfigurationOffsets(
		config.WritableOffsets,
		config.HalfwordOffsets,
		config.MixedWidthOffsets,
		config.ReadOnlyRegisters,
		config.RegisterResets,
		config.CompletionEvents,
		config.LegacyUARTControllers,
		config.SBIControllers,
		config.SBICompletionStatus,
	); err != nil {
		return nil, fmt.Errorf("invalid Qualcomm boot-control register profile: %w", err)
	}
	if clock := config.TimeTickClock; clock != nil {
		const maximumClockHz = uint64(1) << 48
		if clock.InstructionsPerSecond == 0 || clock.TimeTickHz == 0 ||
			clock.InstructionsPerSecond > maximumClockHz ||
			clock.TimeTickHz > clock.InstructionsPerSecond ||
			clock.InterruptSource >= 64 {
			return nil, fmt.Errorf("invalid Qualcomm timetick clock configuration")
		}
		if clock.UseVectoredController &&
			(config.VectoredInterruptController == nil ||
				clock.InterruptSource >= config.VectoredInterruptController.SourceCount()) {
			return nil, fmt.Errorf("Qualcomm timetick interrupt source exceeds vectored controller")
		}
	}
	for _, event := range config.CompletionEvents {
		if event.UseVectoredController {
			if config.VectoredInterruptController == nil ||
				event.InterruptSource >= config.VectoredInterruptController.SourceCount() {
				return nil, fmt.Errorf(
					"Qualcomm completion interrupt source %d exceeds vectored controller",
					event.InterruptSource,
				)
			}
		} else if event.InterruptSource >= 64 {
			return nil, fmt.Errorf("invalid Qualcomm completion interrupt source %d", event.InterruptSource)
		}
	}
	completionEvents := append([]QualcommCompletionEventConfig(nil), config.CompletionEvents...)
	sort.Slice(completionEvents, func(left, right int) bool {
		return completionEvents[left].StartOffset < completionEvents[right].StartOffset
	})
	registerResets := append([]QualcommBootRegisterReset(nil), config.RegisterResets...)
	sort.Slice(registerResets, func(left, right int) bool {
		return registerResets[left].Offset < registerResets[right].Offset
	})
	device := &QualcommBootControl{
		hardwareRevision:            config.HardwareRevision,
		nandInterfaceMode:           config.NANDInterfaceMode,
		ebiMemoryConfiguration:      config.EBIMemoryConfiguration,
		clockModeStatus:             config.ClockModeStatus,
		nandReady:                   config.NANDReady,
		interruptController:         config.InterruptController,
		vectoredInterruptController: config.VectoredInterruptController,
		writableOffsets: mergedQualcommBootControlWritableOffsets(
			config.WritableOffsets,
			config.HalfwordOffsets,
			config.ReadOnlyRegisters,
			config.CompletionEvents,
			config.SBIControllers,
			config.SBICompletionStatus,
		),
		halfwordOffsets:       make(map[uint32]struct{}, len(config.HalfwordOffsets)),
		mixedWidthOffsets:     make(map[uint32]struct{}, len(config.MixedWidthOffsets)),
		readOnlyRegisters:     make(map[uint32]uint32, len(config.ReadOnlyRegisters)),
		registerResets:        registerResets,
		completionEvents:      completionEvents,
		completionHandlers:    make(map[uint32]QualcommCompletionHandler),
		legacyUARTControllers: make(map[uint32]struct{}, len(config.LegacyUARTControllers)),
		sbiControllers:        make(map[uint32]struct{}, len(config.SBIControllers)),
		sbiCompletionStatus:   config.SBICompletionStatus,
	}
	for _, offset := range config.HalfwordOffsets {
		device.halfwordOffsets[offset] = struct{}{}
	}
	for _, offset := range config.MixedWidthOffsets {
		device.mixedWidthOffsets[offset] = struct{}{}
	}
	for _, base := range config.LegacyUARTControllers {
		device.legacyUARTControllers[base] = struct{}{}
	}
	for _, register := range config.ReadOnlyRegisters {
		device.readOnlyRegisters[register.Offset] = register.Value
	}
	for _, base := range config.SBIControllers {
		device.sbiControllers[base] = struct{}{}
	}
	if clock := config.TimeTickClock; clock != nil {
		device.timeTickClocked = true
		device.timeTickInstructionRate = clock.InstructionsPerSecond
		device.timeTickHz = clock.TimeTickHz
		device.timeTickInterruptSource = clock.InterruptSource
		device.timeTickUseVectored = clock.UseVectoredController
	}
	if device.interruptController == nil {
		device.interruptController = NewQualcommInterruptController(nil)
	}
	if err := device.Reset(); err != nil {
		return nil, err
	}
	return device, nil
}

func (d *QualcommBootControl) Reset() error {
	d.registers = make(map[uint32]uint32, len(d.writableOffsets))
	for _, offset := range d.writableOffsets {
		d.registers[offset] = 0
	}
	for _, reset := range d.registerResets {
		d.registers[reset.Offset] = reset.Value
	}
	for offset, value := range d.readOnlyRegisters {
		d.registers[offset] = value
	}
	d.registers[0x0380] = d.nandInterfaceMode
	d.registers[0x1000] = 1
	d.registers[0x1020] = 2
	for _, offset := range []uint32{0x1030, 0x1034, 0x1038, 0x103c, 0x1040, 0x1044} {
		d.registers[offset] = 0xf
	}
	for _, offset := range []uint32{0x1048, 0x104c, 0x1050, 0x1054, 0x1058} {
		d.registers[offset] = 0x1f
	}
	d.registers[0x1100] = d.ebiMemoryConfiguration
	d.nandReady.Set(0)
	d.watchdogServices = 0
	d.timeTick = 0
	d.timeTickReadPhase = 0
	d.timeTickPhase = 0
	d.timeTickMatchReady = true
	d.timeTickMatchConfigured = false
	if err := d.interruptController.Reset(); err != nil {
		return err
	}
	if d.vectoredInterruptController != nil {
		if err := d.vectoredInterruptController.Reset(); err != nil {
			return err
		}
	}
	for _, handler := range d.orderedCompletionHandlers {
		if err := handler.Reset(); err != nil {
			return fmt.Errorf("reset Qualcomm completion handler: %w", err)
		}
	}
	return nil
}

// AttachCompletionHandler connects an evidenced completion event to a device
// side effect without expanding the boot-control MMIO aperture.
func (d *QualcommBootControl) AttachCompletionHandler(
	startOffset uint32,
	handler QualcommCompletionHandler,
) error {
	if handler == nil {
		return fmt.Errorf("nil Qualcomm completion handler")
	}
	profiled := false
	for _, event := range d.completionEvents {
		if event.StartOffset == startOffset {
			profiled = true
			break
		}
	}
	if !profiled {
		return fmt.Errorf("completion start 0x%x is not profiled: %w", startOffset, ErrInvalidRegion)
	}
	if _, duplicate := d.completionHandlers[startOffset]; duplicate {
		return fmt.Errorf("completion start 0x%x already has a handler: %w", startOffset, ErrInvalidRegion)
	}
	if err := handler.Reset(); err != nil {
		return fmt.Errorf("reset Qualcomm completion handler: %w", err)
	}
	d.completionHandlers[startOffset] = handler
	d.orderedCompletionHandlers = append(d.orderedCompletionHandlers, handler)
	return nil
}

func (d *QualcommBootControl) Read(offset uint32, width Width) (uint32, error) {
	if offset >= 0x0900 && offset < 0x0900+QualcommInterruptControllerWindowSize {
		return d.interruptController.Read(offset-0x0900, width)
	}
	if d.vectoredInterruptController != nil &&
		offset >= QualcommVectoredInterruptControllerBaseOffset &&
		offset < QualcommVectoredInterruptControllerBaseOffset+
			QualcommVectoredInterruptControllerWindowSize {
		relative := offset - QualcommVectoredInterruptControllerBaseOffset
		if d.vectoredInterruptController.Handles(relative) {
			return d.vectoredInterruptController.Read(relative, width)
		}
	}
	if value, handled, err := d.readLegacyUART(offset, width); handled {
		return value, err
	}
	if _, ok := d.halfwordOffsets[offset]; ok {
		if width != Width16 {
			return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
		}
		return d.registers[offset], nil
	}
	if _, ok := d.mixedWidthOffsets[offset]; ok && width == Width16 {
		return d.registers[offset] & 0xffff, nil
	}
	if width != Width32 {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
	}
	if d.sbiCompletionStatus != 0 && offset == d.sbiCompletionStatus {
		value := d.registers[offset]
		d.registers[offset] = 0
		return value, nil
	}
	switch offset {
	case 0x0a40:
		return d.hardwareRevision, nil
	case 0x1004:
		return 0, nil
	case 0x0274:
		return d.clockModeStatus, nil
	case 0x0488:
		return d.nandReady.Value(), nil
	case 0x5408:
		value := d.timeTick
		if !d.timeTickClocked {
			d.timeTickReadPhase ^= 1
			if d.timeTickReadPhase == 0 {
				d.timeTick++
			}
		}
		return value, nil
	case 0x54c0:
		if d.timeTickMatchReady {
			return 1, nil
		}
		return 0, nil
	case 0x551c:
		return 0, nil
	default:
		if value, ok := d.registers[offset]; ok {
			return value, nil
		}
		return 0, fmt.Errorf("%w: read32 at 0x%x", ErrQualcommBootControlMMIO, offset)
	}
}

func (d *QualcommBootControl) Write(offset uint32, width Width, value uint32) error {
	if offset >= 0x0900 && offset < 0x0900+QualcommInterruptControllerWindowSize {
		return d.interruptController.Write(offset-0x0900, width, value)
	}
	if d.vectoredInterruptController != nil &&
		offset >= QualcommVectoredInterruptControllerBaseOffset &&
		offset < QualcommVectoredInterruptControllerBaseOffset+
			QualcommVectoredInterruptControllerWindowSize {
		relative := offset - QualcommVectoredInterruptControllerBaseOffset
		if d.vectoredInterruptController.Handles(relative) {
			return d.vectoredInterruptController.Write(relative, width, value)
		}
	}
	for _, event := range d.completionEvents {
		if offset == event.StatusOffset {
			return fmt.Errorf(
				"%w: write%d value 0x%x at read-only completion status 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				value,
				offset,
			)
		}
	}
	acknowledge := false
	for _, event := range d.completionEvents {
		if offset != event.AcknowledgeOffset {
			continue
		}
		acknowledge = true
		if width != event.AcknowledgeWidth || width == Width16 && value > 0xffff {
			return fmt.Errorf(
				"%w: write%d value 0x%x at completion acknowledge 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				value,
				offset,
			)
		}
	}
	if acknowledge {
		d.registers[offset] = value
		for _, event := range d.completionEvents {
			if offset == event.AcknowledgeOffset && value&event.AcknowledgeMask != 0 {
				d.registers[event.StatusOffset] &^= event.StatusMask
			}
		}
		return nil
	}
	if handled, err := d.writeLegacyUART(offset, width, value); handled {
		return err
	}
	if _, ok := d.halfwordOffsets[offset]; ok {
		if width != Width16 || value > 0xffff {
			return fmt.Errorf(
				"%w: write%d value 0x%x at 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				value,
				offset,
			)
		}
		d.registers[offset] = value
		return nil
	}
	if _, ok := d.mixedWidthOffsets[offset]; ok && width == Width16 {
		if value > 0xffff {
			return fmt.Errorf(
				"%w: write%d value 0x%x at 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				value,
				offset,
			)
		}
		d.registers[offset] = d.registers[offset]&0xffff0000 | value
		return nil
	}
	if offset == 0x540c {
		if width != Width8 && width != Width32 || value != 1 {
			return fmt.Errorf("%w: watchdog service value 0x%x", ErrQualcommBootControlMMIO, value)
		}
		d.watchdogServices++
		return nil
	}
	if width != Width32 {
		return fmt.Errorf("%w: write%d at 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
	}
	if _, readOnly := d.readOnlyRegisters[offset]; readOnly {
		return fmt.Errorf("%w: write32 at read-only offset 0x%x", ErrQualcommBootControlMMIO, offset)
	}
	if _, ok := d.registers[offset]; !ok {
		return fmt.Errorf("%w: write32 at 0x%x", ErrQualcommBootControlMMIO, offset)
	}
	previousValue := d.registers[offset]
	d.registers[offset] = value
	for _, event := range d.completionEvents {
		if offset != event.StartOffset || value&event.StartMask == 0 {
			continue
		}
		if handler := d.completionHandlers[event.StartOffset]; handler != nil {
			if err := handler.QueueCompletion(func(registerOffset uint32) (uint32, bool) {
				registerValue, ok := d.registers[registerOffset]
				return registerValue, ok
			}); err != nil {
				d.registers[offset] = previousValue
				return fmt.Errorf("queue Qualcomm completion handler: %w", err)
			}
		}
		previousStatus := d.registers[event.StatusOffset]
		d.registers[event.StatusOffset] |= event.StatusMask
		var err error
		if event.UseVectoredController {
			err = d.vectoredInterruptController.PulseSource(event.InterruptSource)
		} else {
			err = d.interruptController.PulseSource(event.InterruptSource)
		}
		if err != nil {
			d.registers[offset] = previousValue
			d.registers[event.StatusOffset] = previousStatus
			return fmt.Errorf("signal Qualcomm completion interrupt: %w", err)
		}
		break
	}
	for base := range d.sbiControllers {
		if offset == base+qualcommBootSBICommandOffset {
			d.registers[base+qualcommBootSBIResultOffset] = 0
			d.registers[d.sbiCompletionStatus] = qualcommBootSBICompleteStatus
			return nil
		}
	}
	if offset == 0x54c4 {
		d.timeTickMatchConfigured = true
		// Firmware rewrites the match value on every synchronization-poll
		// iteration. Delaying this register-ready bit until the next coarse CPU
		// runner slice therefore makes each retry reset its own delay forever.
		// The write latch is guest-visible immediately; only match expiry and
		// interrupt delivery advance with the configured sleep clock.
		d.timeTickMatchReady = true
	} else if offset == 0x0380 && value&8 != 0 {
		d.nandReady.Set(d.nandReady.Value() | 2)
	} else if offset == 0x0414 {
		d.nandReady.Clear(value & 3)
	}
	return nil
}

// Advance implements ClockedDevice. Clocked profiles derive timetick progress
// only from retired guest instructions; compatibility profiles retain the older
// stable-pair read behavior until their clock and interrupt route are known.
func (d *QualcommBootControl) Advance(retiredInstructions uint64) error {
	if err := d.advanceTimeTick(retiredInstructions); err != nil {
		return err
	}
	for _, handler := range d.orderedCompletionHandlers {
		if err := handler.Advance(retiredInstructions); err != nil {
			return fmt.Errorf("advance Qualcomm completion handler: %w", err)
		}
	}
	return nil
}

func (d *QualcommBootControl) advanceTimeTick(retiredInstructions uint64) error {
	if !d.timeTickClocked || retiredInstructions == 0 {
		return nil
	}
	d.timeTickMatchReady = true
	high, low := bits.Mul64(retiredInstructions, d.timeTickHz)
	low, carry := bits.Add64(low, d.timeTickPhase, 0)
	high, carry = bits.Add64(high, 0, carry)
	if carry != 0 {
		return fmt.Errorf("Qualcomm timetick advance overflow")
	}
	quotientHigh, remainder := bits.Div64(0, high, d.timeTickInstructionRate)
	quotientLow, remainder := bits.Div64(remainder, low, d.timeTickInstructionRate)
	d.timeTickPhase = remainder
	if quotientHigh == 0 && quotientLow == 0 {
		return nil
	}
	previous := d.timeTick
	d.timeTick += uint32(quotientLow)
	if !d.timeTickMatchConfigured {
		return nil
	}
	distance := uint64(uint32(d.registers[0x54c4] - previous))
	if distance == 0 {
		distance = uint64(1) << 32
	}
	if quotientHigh != 0 || quotientLow >= uint64(1)<<32 || quotientLow >= distance {
		if d.timeTickUseVectored {
			return d.vectoredInterruptController.PulseSource(d.timeTickInterruptSource)
		}
		return d.interruptController.PulseSource(d.timeTickInterruptSource)
	}
	return nil
}

func (d *QualcommBootControl) WatchdogServices() uint64 {
	return d.watchdogServices
}

func (d *QualcommBootControl) registerAccessWidths(offset uint32) uint8 {
	if _, ok := d.halfwordOffsets[offset]; ok {
		return uint8(Width16)
	}
	if _, ok := d.mixedWidthOffsets[offset]; ok {
		return uint8(Width16 | Width32)
	}
	return uint8(Width32)
}

func (d *QualcommBootControl) SaveState() ([]byte, error) {
	interruptState, err := d.interruptController.SaveState()
	if err != nil {
		return nil, err
	}
	var vectoredInterruptState []byte
	if d.vectoredInterruptController != nil {
		vectoredInterruptState, err = d.vectoredInterruptController.SaveState()
		if err != nil {
			return nil, err
		}
	}
	offsets := d.writableOffsets
	var output bytes.Buffer
	output.WriteString("QBTC")
	_ = binary.Write(&output, binary.LittleEndian, uint32(17))
	_ = binary.Write(&output, binary.LittleEndian, d.hardwareRevision)
	_ = binary.Write(&output, binary.LittleEndian, d.nandInterfaceMode)
	_ = binary.Write(&output, binary.LittleEndian, d.ebiMemoryConfiguration)
	_ = binary.Write(&output, binary.LittleEndian, d.clockModeStatus)
	ready := uint8(0)
	ready = uint8(d.nandReady.Value())
	_ = output.WriteByte(ready)
	_ = binary.Write(&output, binary.LittleEndian, d.watchdogServices)
	_ = binary.Write(&output, binary.LittleEndian, d.timeTick)
	_ = output.WriteByte(d.timeTickReadPhase)
	clocked := uint8(0)
	if d.timeTickClocked {
		clocked = 1
	}
	_ = output.WriteByte(clocked)
	_ = binary.Write(&output, binary.LittleEndian, d.timeTickInstructionRate)
	_ = binary.Write(&output, binary.LittleEndian, d.timeTickHz)
	_ = output.WriteByte(d.timeTickInterruptSource)
	useVectored := uint8(0)
	if d.timeTickUseVectored {
		useVectored = 1
	}
	_ = output.WriteByte(useVectored)
	_ = binary.Write(&output, binary.LittleEndian, d.timeTickPhase)
	matchReady := uint8(0)
	if d.timeTickMatchReady {
		matchReady = 1
	}
	_ = output.WriteByte(matchReady)
	matchConfigured := uint8(0)
	if d.timeTickMatchConfigured {
		matchConfigured = 1
	}
	_ = output.WriteByte(matchConfigured)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.completionEvents)))
	for _, event := range d.completionEvents {
		_ = binary.Write(&output, binary.LittleEndian, event.StartOffset)
		_ = binary.Write(&output, binary.LittleEndian, event.StartMask)
		_ = binary.Write(&output, binary.LittleEndian, event.StatusOffset)
		_ = binary.Write(&output, binary.LittleEndian, event.StatusMask)
		_ = binary.Write(&output, binary.LittleEndian, event.AcknowledgeOffset)
		_ = output.WriteByte(byte(event.AcknowledgeWidth))
		_ = binary.Write(&output, binary.LittleEndian, event.AcknowledgeMask)
		_ = output.WriteByte(event.InterruptSource)
		vectored := uint8(0)
		if event.UseVectoredController {
			vectored = 1
		}
		_ = output.WriteByte(vectored)
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.registerResets)))
	for _, reset := range d.registerResets {
		_ = binary.Write(&output, binary.LittleEndian, reset.Offset)
		_ = binary.Write(&output, binary.LittleEndian, reset.Value)
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(offsets)))
	for _, offset := range offsets {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = output.WriteByte(d.registerAccessWidths(offset))
		_ = binary.Write(&output, binary.LittleEndian, d.registers[offset])
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(interruptState)))
	output.Write(interruptState)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(vectoredInterruptState)))
	output.Write(vectoredInterruptState)
	return output.Bytes(), nil
}

func (d *QualcommBootControl) LoadState(state []byte) error {
	return d.loadState(state, false)
}

// LoadStateSubset permits diagnostic snapshots made before a read-only status
// register or an explicitly reset writable register was added to the board
// profile. Missing registers take their configured reset values; unreset
// writable-register and all other profile changes remain incompatible.
func (d *QualcommBootControl) LoadStateSubset(state []byte) error {
	return d.loadState(state, true)
}

func (d *QualcommBootControl) loadState(state []byte, allowMissingProfileRegisters bool) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, revision, nandInterfaceMode, ebiMemoryConfiguration, clockModeStatus uint32
	var ready uint8
	var watchdog uint64
	var timeTick uint32
	var timeTickReadPhase uint8
	var clocked, interruptSource, useVectored, matchReady, matchConfigured uint8
	var instructionRate, timeTickHz, timeTickPhase uint64
	var completionCount, resetCount, count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QBTC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 17 ||
		binary.Read(reader, binary.LittleEndian, &revision) != nil || revision != d.hardwareRevision ||
		binary.Read(reader, binary.LittleEndian, &nandInterfaceMode) != nil ||
		nandInterfaceMode != d.nandInterfaceMode ||
		binary.Read(reader, binary.LittleEndian, &ebiMemoryConfiguration) != nil ||
		ebiMemoryConfiguration != d.ebiMemoryConfiguration ||
		binary.Read(reader, binary.LittleEndian, &clockModeStatus) != nil ||
		clockModeStatus != d.clockModeStatus ||
		binary.Read(reader, binary.LittleEndian, &ready) != nil || ready > 3 ||
		binary.Read(reader, binary.LittleEndian, &watchdog) != nil ||
		binary.Read(reader, binary.LittleEndian, &timeTick) != nil ||
		binary.Read(reader, binary.LittleEndian, &timeTickReadPhase) != nil || timeTickReadPhase > 1 ||
		binary.Read(reader, binary.LittleEndian, &clocked) != nil || clocked > 1 ||
		(clocked == 1) != d.timeTickClocked ||
		binary.Read(reader, binary.LittleEndian, &instructionRate) != nil ||
		instructionRate != d.timeTickInstructionRate ||
		binary.Read(reader, binary.LittleEndian, &timeTickHz) != nil || timeTickHz != d.timeTickHz ||
		binary.Read(reader, binary.LittleEndian, &interruptSource) != nil ||
		interruptSource != d.timeTickInterruptSource ||
		binary.Read(reader, binary.LittleEndian, &useVectored) != nil || useVectored > 1 ||
		(useVectored == 1) != d.timeTickUseVectored ||
		binary.Read(reader, binary.LittleEndian, &timeTickPhase) != nil ||
		(d.timeTickClocked && timeTickPhase >= d.timeTickInstructionRate) ||
		binary.Read(reader, binary.LittleEndian, &matchReady) != nil || matchReady > 1 ||
		binary.Read(reader, binary.LittleEndian, &matchConfigured) != nil || matchConfigured > 1 ||
		binary.Read(reader, binary.LittleEndian, &completionCount) != nil ||
		completionCount != uint32(len(d.completionEvents)) {
		return ErrInvalidState
	}
	for index := uint32(0); index < completionCount; index++ {
		var event QualcommCompletionEventConfig
		var acknowledgeWidth, vectored uint8
		if binary.Read(reader, binary.LittleEndian, &event.StartOffset) != nil ||
			binary.Read(reader, binary.LittleEndian, &event.StartMask) != nil ||
			binary.Read(reader, binary.LittleEndian, &event.StatusOffset) != nil ||
			binary.Read(reader, binary.LittleEndian, &event.StatusMask) != nil ||
			binary.Read(reader, binary.LittleEndian, &event.AcknowledgeOffset) != nil ||
			binary.Read(reader, binary.LittleEndian, &acknowledgeWidth) != nil ||
			binary.Read(reader, binary.LittleEndian, &event.AcknowledgeMask) != nil ||
			binary.Read(reader, binary.LittleEndian, &event.InterruptSource) != nil ||
			binary.Read(reader, binary.LittleEndian, &vectored) != nil || vectored > 1 {
			return ErrInvalidState
		}
		event.AcknowledgeWidth = Width(acknowledgeWidth)
		event.UseVectoredController = vectored == 1
		if event != d.completionEvents[index] {
			return ErrInvalidState
		}
	}
	if binary.Read(reader, binary.LittleEndian, &resetCount) != nil ||
		resetCount > uint32(len(d.registerResets)) ||
		(!allowMissingProfileRegisters && resetCount != uint32(len(d.registerResets))) {
		return ErrInvalidState
	}
	serializedResets := make(map[uint32]uint32, resetCount)
	configuredResets := make(map[uint32]uint32, len(d.registerResets))
	for _, reset := range d.registerResets {
		configuredResets[reset.Offset] = reset.Value
	}
	for index := uint32(0); index < resetCount; index++ {
		var reset QualcommBootRegisterReset
		if binary.Read(reader, binary.LittleEndian, &reset.Offset) != nil ||
			binary.Read(reader, binary.LittleEndian, &reset.Value) != nil {
			return ErrInvalidState
		}
		configured, present := configuredResets[reset.Offset]
		if !present || configured != reset.Value {
			return ErrInvalidState
		}
		if _, duplicate := serializedResets[reset.Offset]; duplicate {
			return ErrInvalidState
		}
		serializedResets[reset.Offset] = reset.Value
	}
	if binary.Read(reader, binary.LittleEndian, &count) != nil ||
		count > uint32(len(d.writableOffsets)) ||
		(!allowMissingProfileRegisters && count != uint32(len(d.writableOffsets))) ||
		reader.Len() < int(count)*9+4 {
		return ErrInvalidState
	}
	registers := make(map[uint32]uint32, count)
	for index := uint32(0); index < count; index++ {
		var offset, value uint32
		var width uint8
		if binary.Read(reader, binary.LittleEndian, &offset) != nil ||
			binary.Read(reader, binary.LittleEndian, &width) != nil ||
			binary.Read(reader, binary.LittleEndian, &value) != nil {
			return ErrInvalidState
		}
		if _, allowed := d.registers[offset]; !allowed {
			return ErrInvalidState
		}
		if width != d.registerAccessWidths(offset) || width == uint8(Width16) && value > 0xffff {
			return ErrInvalidState
		}
		if configured, readOnly := d.readOnlyRegisters[offset]; readOnly && value != configured {
			return ErrInvalidState
		}
		if _, duplicate := registers[offset]; duplicate {
			return ErrInvalidState
		}
		registers[offset] = value
	}
	for _, offset := range d.writableOffsets {
		if _, restored := registers[offset]; restored {
			continue
		}
		configured, readOnly := d.readOnlyRegisters[offset]
		if allowMissingProfileRegisters && readOnly {
			registers[offset] = configured
			continue
		}
		reset, resetConfigured := configuredResets[offset]
		_, resetExistedInState := serializedResets[offset]
		if !allowMissingProfileRegisters || !resetConfigured || resetExistedInState {
			return ErrInvalidState
		}
		registers[offset] = reset
	}
	var interruptStateLength uint32
	if binary.Read(reader, binary.LittleEndian, &interruptStateLength) != nil ||
		uint64(interruptStateLength)+4 > uint64(reader.Len()) {
		return ErrInvalidState
	}
	interruptState := make([]byte, interruptStateLength)
	if _, err := io.ReadFull(reader, interruptState); err != nil {
		return ErrInvalidState
	}
	var vectoredInterruptStateLength uint32
	if binary.Read(reader, binary.LittleEndian, &vectoredInterruptStateLength) != nil ||
		uint64(vectoredInterruptStateLength) != uint64(reader.Len()) ||
		(d.vectoredInterruptController == nil) != (vectoredInterruptStateLength == 0) {
		return ErrInvalidState
	}
	vectoredInterruptState := make([]byte, vectoredInterruptStateLength)
	if _, err := io.ReadFull(reader, vectoredInterruptState); err != nil || reader.Len() != 0 {
		return ErrInvalidState
	}
	if err := d.interruptController.LoadState(interruptState); err != nil {
		return err
	}
	if d.vectoredInterruptController != nil {
		if err := d.vectoredInterruptController.LoadState(vectoredInterruptState); err != nil {
			return err
		}
	}
	d.registers = registers
	d.nandReady.Set(uint32(ready))
	d.watchdogServices = watchdog
	d.timeTick = timeTick
	d.timeTickReadPhase = timeTickReadPhase
	d.timeTickPhase = timeTickPhase
	d.timeTickMatchReady = matchReady == 1
	d.timeTickMatchConfigured = matchConfigured == 1
	return nil
}

// QualcommSecondaryClockControl is the bounded second clock-register window
// exercised by OEMSBL. The selector/data and gate-mask registers are explicit
// stateful latches; accesses outside the evidenced set fail.
type QualcommSecondaryClockControl struct {
	offsets           []uint32
	registers         map[uint32]uint32
	readOnlyRegisters map[uint32]uint32
	gpioWriteObserver QualcommGPIOWriteObserver
}

func NewQualcommSecondaryClockControl() *QualcommSecondaryClockControl {
	device, _ := NewQualcommSecondaryClockControlWithWritableOffsets(nil)
	return device
}

// NewQualcommSecondaryClockControlWithWritableOffsets adds registers evidenced
// for one board without widening the default family contract.
func NewQualcommSecondaryClockControlWithWritableOffsets(
	extra []uint32,
) (*QualcommSecondaryClockControl, error) {
	return NewQualcommSecondaryClockControlWithConfig(QualcommSecondaryClockConfig{
		WritableOffsets: extra,
	})
}

// NewQualcommSecondaryClockControlWithConfig adds only the output latches and
// raw input words evidenced by a board profile.
func NewQualcommSecondaryClockControlWithConfig(
	config QualcommSecondaryClockConfig,
) (*QualcommSecondaryClockControl, error) {
	if err := validateQualcommSecondaryClockConfig(config); err != nil {
		return nil, err
	}
	offsets := append([]uint32(nil), qualcommSecondaryClockOffsets...)
	offsets = append(offsets, config.WritableOffsets...)
	sort.Slice(offsets, func(left, right int) bool { return offsets[left] < offsets[right] })
	readOnlyRegisters := make(map[uint32]uint32, len(config.ReadOnlyRegisters))
	for _, register := range config.ReadOnlyRegisters {
		readOnlyRegisters[register.Offset] = register.Value
	}
	device := &QualcommSecondaryClockControl{
		offsets:           offsets,
		readOnlyRegisters: readOnlyRegisters,
	}
	_ = device.Reset()
	return device, nil
}

func (d *QualcommSecondaryClockControl) Reset() error {
	d.registers = make(map[uint32]uint32, len(d.offsets))
	for _, offset := range d.offsets {
		d.registers[offset] = 0
	}
	return nil
}

func (d *QualcommSecondaryClockControl) AttachGPIOWriteObserver(observer QualcommGPIOWriteObserver) error {
	if observer == nil || d.gpioWriteObserver != nil {
		return fmt.Errorf("attach Qualcomm secondary-clock GPIO write observer: %w", ErrQualcommSecondaryClockMMIO)
	}
	d.gpioWriteObserver = observer
	return nil
}

func (d *QualcommSecondaryClockControl) Read(offset uint32, width Width) (uint32, error) {
	if width == Width32 {
		if offset == qualcommSecondaryClockDisabledStatusOffset {
			return 0x10, nil
		}
		if value, ok := d.readOnlyRegisters[offset]; ok {
			return value, nil
		}
		if value, ok := d.registers[offset]; ok {
			return value, nil
		}
	}
	return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommSecondaryClockMMIO, width*8, offset)
}

func (d *QualcommSecondaryClockControl) Write(offset uint32, width Width, value uint32) error {
	if width == Width32 {
		if _, ok := d.registers[offset]; ok {
			d.registers[offset] = value
			if d.gpioWriteObserver != nil {
				d.gpioWriteObserver.ObserveGPIOWrite(offset, value)
			}
			return nil
		}
	}
	return fmt.Errorf(
		"%w: write%d value 0x%x at 0x%x",
		ErrQualcommSecondaryClockMMIO, width*8, value, offset,
	)
}

func (d *QualcommSecondaryClockControl) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("QSCC")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.offsets)))
	for _, offset := range d.offsets {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = binary.Write(&output, binary.LittleEndian, d.registers[offset])
	}
	return output.Bytes(), nil
}

func (d *QualcommSecondaryClockControl) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QSCC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 ||
		binary.Read(reader, binary.LittleEndian, &count) != nil ||
		count != uint32(len(d.offsets)) || reader.Len() != int(count)*8 {
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
	d.registers = registers
	return nil
}

var (
	_ Device               = (*QualcommBootControl)(nil)
	_ StatefulDevice       = (*QualcommBootControl)(nil)
	_ SubsetStatefulDevice = (*QualcommBootControl)(nil)
	_ Device               = (*QualcommSecondaryClockControl)(nil)
	_ StatefulDevice       = (*QualcommSecondaryClockControl)(nil)
)
