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
	qualcommPBLHeaderFeatureFirst    = 0x0158
	qualcommPBLHeaderFeatureEnd      = 0x015d
	qualcommPBLHeaderFlashBlockCount = 0x0159
	qualcommPBLHeaderSLCBlockCount   = 0x015a
	qualcommPBLHeaderBadBlockLimit   = 0x015b
	qualcommPBLHeaderFeatureDataSize = (qualcommPBLHeaderFeatureEnd - qualcommPBLHeaderFeatureFirst) * 8
	qualcommPBLFlashTypeNAND         = 1
	qualcommPBLFlashTypeNAND2K       = 6
	qualcommLegacyPBLFeatureEnd      = 0x0131
	qualcommPBLFeatureDataHeaderSize = 0x2c
	qualcommPBLServiceEntryCount     = 6
)

var (
	ErrQualcommBootControlMMIO    = errors.New("unsupported Qualcomm boot-control register")
	ErrQualcommSecondaryClockMMIO = errors.New("unsupported Qualcomm secondary-clock register")
)

type QualcommNANDPBLConfig struct {
	Entry                    uint32
	StackPointer             uint32
	TableAddress             uint32
	ServiceTableHeaderSize   uint32
	HeaderFeatureDataAddress uint32
	HeaderFeatures           []QualcommPBLHeaderFeature
	FixedFeatureDataAddress  uint32
	FixedFeatureFirst        uint32
	FixedFeatureSlotCount    uint32
	FixedFeatures            []QualcommPBLFixedFeature
	LegacyFeatureDataAddress uint32
	SharedDataAddress        uint32
	SharedDataSize           uint32
	PageSize                 uint32
	EraseBlockSize           uint32
	FlashSize                uint64
	BadBlockLimit            uint32
}

// QualcommPBLHeaderFeature describes one fixed presence/value slot in the
// newer PBL table passed to QCSBL through r9.
type QualcommPBLHeaderFeature struct {
	Selector uint32
	Value    uint32
}

// QualcommPBLFixedFeature describes one present selector/value slot in a
// generation-specific fixed table. Selectors omitted from FixedFeatures stay
// absent while preserving their positional slot.
type QualcommPBLFixedFeature struct {
	Selector uint32
	Value    uint32
}

type QualcommBootControlConfig struct {
	HardwareRevision               uint32
	NANDInterfaceMode              uint32
	EBIMemoryConfiguration         uint32
	ClockModeStatus                uint32
	WritableOffsets                []uint32
	InterruptWindowWritableOffsets []uint32
	HalfwordOffsets                []uint32
	MixedWidthOffsets              []uint32
	ByteWritableOffsets            []uint32
	ReadOnlyRegisters              []QualcommBootReadOnlyRegister
	RegisterResets                 []QualcommBootRegisterReset
	CompletionEvents               []QualcommCompletionEventConfig
	LegacyUARTControllers          []uint32
	LegacyUARTReceiveData          []QualcommLegacyUARTReceiveData
	SBIControllers                 []uint32
	SBIReadResponses               []QualcommSBIReadResponse
	SBICompletionStatus            uint32
	SDCCControllers                []QualcommSDCCControllerConfig
	GroupedStatusResponses         []QualcommBootGroupedStatusResponse
	WatchdogServiceReadable        bool
	NANDReady                      *StatusSignal
	InterruptController            *QualcommInterruptController
	VectoredInterruptController    *QualcommVectoredInterruptController
	TimeTickClock                  *QualcommTimeTickClockConfig
}

// QualcommSDCCControllerConfig models one PL180-style removable-card
// controller. GroupStatusOffset and GroupMask optionally route its sticky
// status through the compact VIC's second-level interrupt hierarchy.
type QualcommSDCCControllerConfig struct {
	Base              uint32
	CardPresent       bool
	GroupStatusOffset uint32
	GroupMask         uint32
}

// QualcommBootGroupedStatusResponse connects a CHIP control bit and/or a live
// device signal to one raw child bit in the compact VIC's second-level status
// groups. A zero RequestMask describes a signal-only child. Some MSM6260-family
// blocks expose their local handshake status through that shared group window
// even when the corresponding first-level interrupt is disabled.
type QualcommBootGroupedStatusResponse struct {
	Offset            uint32
	RequestMask       uint32
	NANDReadyMask     uint32
	GroupStatusOffset uint32
	GroupMask         uint32
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

// QualcommSBIReadResponse gives a profiled SBI controller a deterministic
// byte response for one addressed peripheral register. Unprofiled reads keep
// the controller's existing zero-value compatibility behavior.
type QualcommSBIReadResponse struct {
	Controller uint32
	Address    uint8
	Value      uint8
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
	// PeriodicInterruptHz selects the legacy free-running TIME_TICK_INT
	// source used by firmware that does not program the sleep-clock match
	// register. Zero retains match-driven interrupt delivery.
	PeriodicInterruptHz   uint64
	InterruptSource       uint8
	UseVectoredController bool
}

// NewQualcommNANDPBLHandoff builds the bounded PBL service data consumed by
// the early QCSBL. The missing mask-ROM remains an explicit HLE boundary.
func NewQualcommNANDPBLHandoff(config QualcommNANDPBLConfig) (BootHandoff, error) {
	flashType := uint32(qualcommPBLFlashTypeNAND2K)
	handoffID := "qualcomm.pbl-hle.nand2k-v1"
	switch config.PageSize {
	case 0x200:
		flashType = qualcommPBLFlashTypeNAND
		handoffID = "qualcomm.pbl-hle.nand-v1"
	case 0x800:
	default:
		return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
	}
	serviceTableHeaderSize := config.ServiceTableHeaderSize
	if serviceTableHeaderSize == 0 {
		serviceTableHeaderSize = qualcommPBLFeatureDataHeaderSize
	}
	headerFeatureOffsets := make([]uint32, len(config.HeaderFeatures))
	seenHeaderFeatures := make(map[uint32]struct{}, len(config.HeaderFeatures))
	for index, feature := range config.HeaderFeatures {
		if feature.Selector < qualcommPBLHeaderFeatureFirst ||
			feature.Selector >= qualcommPBLHeaderFeatureEnd {
			return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
		}
		if _, duplicate := seenHeaderFeatures[feature.Selector]; duplicate {
			return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
		}
		seenHeaderFeatures[feature.Selector] = struct{}{}
		headerFeatureOffsets[index] = (feature.Selector - qualcommPBLHeaderFeatureFirst) * 8
	}
	fixedFeatureOffsets := make([]uint32, len(config.FixedFeatures))
	seenFixedFeatures := make(map[uint32]struct{}, len(config.FixedFeatures))
	fixedFeatureSize := uint64(config.FixedFeatureSlotCount) * 8
	if config.FixedFeatureDataAddress == 0 || config.FixedFeatureSlotCount == 0 {
		if config.FixedFeatureDataAddress != 0 || config.FixedFeatureFirst != 0 ||
			config.FixedFeatureSlotCount != 0 || len(config.FixedFeatures) != 0 {
			return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
		}
	} else {
		selectorEnd := uint64(config.FixedFeatureFirst) + uint64(config.FixedFeatureSlotCount)
		if config.FixedFeatureDataAddress&3 != 0 || selectorEnd > 1<<32 ||
			fixedFeatureSize > MaxHandoffSeedBytes ||
			uint64(config.FixedFeatureDataAddress)+fixedFeatureSize > 1<<32 {
			return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
		}
		for index, feature := range config.FixedFeatures {
			if feature.Selector < config.FixedFeatureFirst ||
				uint64(feature.Selector) >= selectorEnd {
				return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
			}
			if _, duplicate := seenFixedFeatures[feature.Selector]; duplicate {
				return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
			}
			seenFixedFeatures[feature.Selector] = struct{}{}
			fixedFeatureOffsets[index] = (feature.Selector - config.FixedFeatureFirst) * 8
		}
	}
	if config.Entry&3 != 0 || config.StackPointer&3 != 0 || config.TableAddress&3 != 0 ||
		serviceTableHeaderSize < qualcommPBLFeatureDataHeaderSize || serviceTableHeaderSize&3 != 0 ||
		uint64(config.TableAddress)+uint64(serviceTableHeaderSize)+qualcommPBLServiceEntryCount*8 > 1<<32 ||
		uint64(serviceTableHeaderSize)+qualcommPBLServiceEntryCount*8 > MaxHandoffSeedBytes ||
		(config.HeaderFeatureDataAddress == 0) != (len(config.HeaderFeatures) == 0) ||
		config.HeaderFeatureDataAddress&3 != 0 ||
		uint64(config.HeaderFeatureDataAddress)+qualcommPBLHeaderFeatureDataSize > 1<<32 ||
		config.EraseBlockSize == 0 || config.EraseBlockSize%config.PageSize != 0 ||
		config.FlashSize == 0 || config.FlashSize%uint64(config.EraseBlockSize) != 0 ||
		config.FlashSize/uint64(config.EraseBlockSize) > uint64(^uint32(0)) ||
		config.BadBlockLimit == 0 ||
		(config.LegacyFeatureDataAddress != 0 && (config.LegacyFeatureDataAddress&3 != 0 ||
			uint64(config.LegacyFeatureDataAddress)+qualcommPBLFeatureDataHeaderSize+6*8 > 1<<32)) ||
		(config.SharedDataAddress == 0) != (config.SharedDataSize == 0) ||
		config.SharedDataAddress&3 != 0 ||
		uint64(config.SharedDataAddress)+uint64(config.SharedDataSize) > 1<<32 ||
		config.SharedDataSize > MaxHandoffSeedBytes {
		return BootHandoff{}, fmt.Errorf("invalid Qualcomm NAND PBL geometry")
	}
	entries := [][2]uint32{
		{0x012f, config.EraseBlockSize / config.PageSize},
		{0x0130, uint32(config.FlashSize / uint64(config.EraseBlockSize))},
		{0x0132, config.PageSize},
		{0x0133, config.BadBlockLimit},
		{0x0141, flashType},
		{qualcommPBLServiceEnd, 0},
	}
	table := make([]byte, int(serviceTableHeaderSize)+len(entries)*8)
	for index, entry := range entries {
		offset := int(serviceTableHeaderSize) + index*8
		binary.LittleEndian.PutUint32(table[offset:], entry[0])
		binary.LittleEndian.PutUint32(table[offset+4:], entry[1])
	}
	handoff := BootHandoff{
		ID:    handoffID,
		Entry: config.Entry,
		Mode:  cpu.ModeARM,
		Registers: []RegisterSeed{
			{Register: cpu.RegisterR7, Value: qualcommPBLMagic},
			{Register: cpu.RegisterR8, Value: config.TableAddress},
		},
		Memory: []MemorySeed{{Address: config.TableAddress, Bytes: table}},
	}
	if config.HeaderFeatureDataAddress != 0 {
		// MSM7600's newer PBL ABI passes a second table in r9. QCSBL looks up
		// selectors 0x158..0x15c there as fixed presence/value pairs.
		headerFeatures := make([]byte, qualcommPBLHeaderFeatureDataSize)
		for index, feature := range config.HeaderFeatures {
			offset := headerFeatureOffsets[index]
			binary.LittleEndian.PutUint32(headerFeatures[offset:], 1)
			binary.LittleEndian.PutUint32(headerFeatures[offset+4:], feature.Value)
		}
		handoff.Registers = append(handoff.Registers, RegisterSeed{
			Register: cpu.RegisterR9,
			Value:    config.HeaderFeatureDataAddress,
		})
		handoff.Memory = append(handoff.Memory, MemorySeed{
			Address: config.HeaderFeatureDataAddress,
			Bytes:   headerFeatures,
		})
	}
	if config.FixedFeatureDataAddress != 0 {
		fixedFeatures := make([]byte, int(fixedFeatureSize))
		for index, feature := range config.FixedFeatures {
			offset := fixedFeatureOffsets[index]
			binary.LittleEndian.PutUint32(fixedFeatures[offset:], 1)
			binary.LittleEndian.PutUint32(fixedFeatures[offset+4:], feature.Value)
		}
		handoff.Memory = append(handoff.Memory, MemorySeed{
			Address: config.FixedFeatureDataAddress,
			Bytes:   fixedFeatures,
		})
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
			{0x0115, flashType},
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
	if config.SharedDataSize != 0 {
		// This PBL generation passes r11 as the exclusive end of a shared-data
		// record. QCSBL derives the start by subtracting its compile-time size,
		// then preserves the record for the flash and next-stage handoffs.
		handoff.Registers = append(handoff.Registers, RegisterSeed{
			Register: cpu.RegisterR11,
			Value:    config.SharedDataAddress + config.SharedDataSize,
		})
		handoff.Memory = append(handoff.Memory, MemorySeed{
			Address: config.SharedDataAddress,
			Bytes:   make([]byte, config.SharedDataSize),
		})
	}
	if config.StackPointer != 0 {
		// Some older QCSBL entrypoints are ordinary ARM functions and retain the
		// stack established by mask ROM. Keep that ABI input explicit and
		// optional; reset-style entrypoints continue to establish their own.
		handoff.Registers = append(handoff.Registers, RegisterSeed{
			Register: cpu.RegisterSP,
			Value:    config.StackPointer,
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
	// AMSS initialises this peripheral bank as one contiguous group. CK06
	// supplies the configuration word at +0xaa4 between the existing +0xaa0
	// and +0xaa8 latches and does not poll an independent completion signal.
	0x0aa0, 0x0aa4,
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

func isQualcommBootControlInterruptWindowOffset(offset uint32) bool {
	return offset >= 0x0900 && offset < 0x0900+QualcommInterruptControllerWindowSize
}

func validateQualcommBootControlInterruptWindowWritableOffsets(offsets []uint32) error {
	seen := make(map[uint32]struct{}, len(offsets))
	for _, offset := range offsets {
		if offset%4 != 0 || !isQualcommBootControlInterruptWindowOffset(offset) {
			return fmt.Errorf("interrupt-window writable offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := seen[offset]; duplicate {
			return fmt.Errorf(
				"duplicate interrupt-window writable offset 0x%x: %w",
				offset,
				ErrInvalidRegion,
			)
		}
		seen[offset] = struct{}{}
	}
	return nil
}

func validateQualcommBootGroupedStatusResponses(
	responses []QualcommBootGroupedStatusResponse,
	writableOffsets []uint32,
) error {
	writable := make(map[uint32]struct{}, len(qualcommBootWritableOffsets)+len(writableOffsets))
	for _, offset := range qualcommBootWritableOffsets {
		writable[offset] = struct{}{}
	}
	for _, offset := range writableOffsets {
		writable[offset] = struct{}{}
	}
	targetMasks := make(map[uint32]uint32, len(responses))
	for _, response := range responses {
		if _, ok := writable[response.Offset]; !ok ||
			response.RequestMask == 0 && response.NANDReadyMask == 0 ||
			response.NANDReadyMask&^uint32(3) != 0 ||
			response.GroupStatusOffset%4 != 0 ||
			response.GroupStatusOffset >= QualcommVectoredInterruptControllerWindowSize ||
			response.GroupMask == 0 {
			return fmt.Errorf("invalid Qualcomm grouped-status response at 0x%x: %w", response.Offset, ErrInvalidRegion)
		}
		if targetMasks[response.GroupStatusOffset]&response.GroupMask != 0 {
			return fmt.Errorf(
				"overlapping Qualcomm grouped-status response at 0x%x/0x%x: %w",
				response.GroupStatusOffset,
				response.GroupMask,
				ErrInvalidRegion,
			)
		}
		targetMasks[response.GroupStatusOffset] |= response.GroupMask
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
	interruptWindowWritableOffsets []uint32,
	halfwordOffsets []uint32,
	mixedWidthOffsets []uint32,
	byteWritableOffsets []uint32,
	readOnlyRegisters []QualcommBootReadOnlyRegister,
	registerResets []QualcommBootRegisterReset,
	completionEvents []QualcommCompletionEventConfig,
	legacyUARTControllers []uint32,
	legacyUARTReceiveData []QualcommLegacyUARTReceiveData,
	sbiControllers []uint32,
	sbiReadResponses []QualcommSBIReadResponse,
	sbiCompletionStatus uint32,
	sdccControllers []QualcommSDCCControllerConfig,
) error {
	if err := validateQualcommBootControlWritableOffsets(writableOffsets); err != nil {
		return err
	}
	if err := validateQualcommBootControlInterruptWindowWritableOffsets(
		interruptWindowWritableOffsets,
	); err != nil {
		return err
	}
	seen := make(map[uint32]struct{}, len(qualcommBootWritableOffsets)+len(writableOffsets)+
		len(interruptWindowWritableOffsets)+
		len(sbiControllers)*len(qualcommBootSBIRegisterOffsets))
	wordWritable := make(map[uint32]struct{}, len(qualcommBootWritableOffsets)+len(writableOffsets)+
		len(interruptWindowWritableOffsets))
	for _, offset := range qualcommBootWritableOffsets {
		seen[offset] = struct{}{}
		wordWritable[offset] = struct{}{}
	}
	for _, offset := range writableOffsets {
		seen[offset] = struct{}{}
		wordWritable[offset] = struct{}{}
	}
	for _, offset := range interruptWindowWritableOffsets {
		seen[offset] = struct{}{}
		wordWritable[offset] = struct{}{}
	}
	mixed := make(map[uint32]struct{}, len(mixedWidthOffsets))
	for _, offset := range mixedWidthOffsets {
		if offset%4 != 0 || isQualcommBootControlInterruptWindowOffset(offset) ||
			isQualcommBootControlSpecialOffset(offset) {
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
	byteWritable := make(map[uint32]struct{}, len(byteWritableOffsets))
	for _, offset := range byteWritableOffsets {
		if offset%4 != 0 || isQualcommBootControlInterruptWindowOffset(offset) ||
			isQualcommBootControlSpecialOffset(offset) {
			return fmt.Errorf("byte-writable offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		if _, writable := wordWritable[offset]; !writable {
			return fmt.Errorf("byte-writable offset 0x%x is not word-writable: %w", offset, ErrInvalidRegion)
		}
		if _, duplicate := byteWritable[offset]; duplicate {
			return fmt.Errorf("duplicate byte-writable offset 0x%x: %w", offset, ErrInvalidRegion)
		}
		byteWritable[offset] = struct{}{}
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
	sdccBases := make(map[uint32]struct{}, len(sdccControllers))
	for _, controller := range sdccControllers {
		base := controller.Base
		if base%4 != 0 || uint64(base)+0x3c > QualcommBootControlWindowSize ||
			isQualcommBootControlInterruptWindowOffset(base+0x34) ||
			isQualcommBootControlSpecialOffset(base+0x34) ||
			(controller.GroupStatusOffset == 0) != (controller.GroupMask == 0) {
			return fmt.Errorf("SDCC absent-card controller 0x%x: %w", base, ErrInvalidRegion)
		}
		if _, duplicate := sdccBases[base]; duplicate {
			return fmt.Errorf("duplicate SDCC absent-card controller 0x%x: %w", base, ErrInvalidRegion)
		}
		sdccBases[base] = struct{}{}
		for _, offset := range []uint32{base + 0x0c, base + 0x38} {
			if _, writable := wordWritable[offset]; !writable {
				return fmt.Errorf(
					"SDCC absent-card register 0x%x is not writable: %w",
					offset,
					ErrInvalidRegion,
				)
			}
		}
		for _, offset := range []uint32{base + 0x14, base + 0x18, base + 0x1c, base + 0x20, base + 0x34} {
			if _, duplicate := seen[offset]; duplicate {
				return fmt.Errorf("duplicate SDCC read-only register 0x%x: %w", offset, ErrInvalidRegion)
			}
			seen[offset] = struct{}{}
		}
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
	sbiResponses := make(map[qualcommSBIReadKey]struct{}, len(sbiReadResponses))
	for _, response := range sbiReadResponses {
		if _, configured := bases[response.Controller]; !configured {
			return fmt.Errorf(
				"SBI response controller 0x%x is not configured: %w",
				response.Controller,
				ErrInvalidRegion,
			)
		}
		key := qualcommSBIReadKey{controller: response.Controller, address: response.Address}
		if _, duplicate := sbiResponses[key]; duplicate {
			return fmt.Errorf(
				"duplicate SBI response for controller 0x%x address 0x%x: %w",
				response.Controller,
				response.Address,
				ErrInvalidRegion,
			)
		}
		sbiResponses[key] = struct{}{}
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
			isQualcommBootControlInterruptWindowOffset(event.StartOffset) ||
			isQualcommBootControlInterruptWindowOffset(event.StatusOffset) ||
			isQualcommBootControlInterruptWindowOffset(event.AcknowledgeOffset) ||
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
			offset := base + relative
			_, halfwordConfigured := halfwords[offset]
			_, mixedWidthConfigured := mixed[offset]
			if !halfwordConfigured && !mixedWidthConfigured {
				return fmt.Errorf(
					"legacy UART controller 0x%x lacks halfword or mixed-width register 0x%x: %w",
					base,
					offset,
					ErrInvalidRegion,
				)
			}
		}
		uartBases[base] = struct{}{}
	}
	uartReceiveControllers := make(map[uint32]struct{}, len(legacyUARTReceiveData))
	for _, receive := range legacyUARTReceiveData {
		if _, configured := uartBases[receive.Controller]; !configured ||
			len(receive.Data) == 0 || receive.InterruptSource >= 64 ||
			(receive.ReceiveFIFOOffset != 0 &&
				receive.ReceiveFIFOOffset != qualcommLegacyUARTFIFOOffset &&
				receive.ReceiveFIFOOffset != qualcommLegacyUARTMISROffset) ||
			receive.T0Card && (!receive.EchoTransmit ||
				receive.TransmitFrameBytes != 0 || len(receive.TransmitResponse) != 0) ||
			!receive.T0Card &&
				(receive.TransmitFrameBytes == 0) != (len(receive.TransmitResponse) == 0) ||
			receive.TransmitFrameBytes > 4096 || len(receive.TransmitResponse) > 4096 {
			return fmt.Errorf(
				"legacy UART receive data for unconfigured or empty controller 0x%x: %w",
				receive.Controller,
				ErrInvalidRegion,
			)
		}
		if _, duplicate := uartReceiveControllers[receive.Controller]; duplicate {
			return fmt.Errorf(
				"duplicate legacy UART receive data for controller 0x%x: %w",
				receive.Controller,
				ErrInvalidRegion,
			)
		}
		uartReceiveControllers[receive.Controller] = struct{}{}
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
	extra, interruptWindowExtra, halfwords []uint32,
	readOnlyRegisters []QualcommBootReadOnlyRegister,
	completionEvents []QualcommCompletionEventConfig,
	sbiControllers []uint32,
	sbiCompletionStatus uint32,
	sdccControllers []QualcommSDCCControllerConfig,
) []uint32 {
	offsets := make(
		[]uint32,
		0,
		len(qualcommBootWritableOffsets)+len(extra)+len(interruptWindowExtra)+len(halfwords)+
			len(readOnlyRegisters)+len(completionEvents)+len(sdccControllers)*5+
			len(sbiControllers)*len(qualcommBootSBIRegisterOffsets),
	)
	offsets = append(offsets, qualcommBootWritableOffsets...)
	offsets = append(offsets, extra...)
	offsets = append(offsets, interruptWindowExtra...)
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
	for _, controller := range sdccControllers {
		for _, relative := range []uint32{0x14, 0x18, 0x1c, 0x20, 0x34} {
			offsets = append(offsets, controller.Base+relative)
		}
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
		len(qualcommSecondaryClockOffsets)+len(config.WritableOffsets)+len(config.ReadOnlyRegisters))
	for _, offset := range qualcommSecondaryClockOffsets {
		seen[offset] = struct{}{}
	}
	for _, offset := range config.WritableOffsets {
		seen[offset] = struct{}{}
	}
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
	hardwareRevision               uint32
	nandInterfaceMode              uint32
	ebiMemoryConfiguration         uint32
	clockModeStatus                uint32
	nandReady                      *StatusSignal
	interruptController            *QualcommInterruptController
	vectoredInterruptController    *QualcommVectoredInterruptController
	writableOffsets                []uint32
	interruptWindowWritableOffsets map[uint32]struct{}
	halfwordOffsets                map[uint32]struct{}
	mixedWidthOffsets              map[uint32]struct{}
	byteWritableOffsets            map[uint32]struct{}
	readOnlyRegisters              map[uint32]uint32
	registerResets                 []QualcommBootRegisterReset
	completionEvents               []QualcommCompletionEventConfig
	completionHandlers             map[uint32]QualcommCompletionHandler
	orderedCompletionHandlers      []QualcommCompletionHandler
	legacyUARTControllers          map[uint32]struct{}
	legacyUARTReceiveData          []QualcommLegacyUARTReceiveData
	legacyUARTReceiveQueues        map[uint32][]byte
	legacyUARTReceiveIRQPending    map[uint32]bool
	legacyUARTActivationIRQPending map[uint32]bool
	legacyUARTReceiveArmed         map[uint32]bool
	legacyUARTReceivePublished     map[uint32]bool
	legacyUARTReceiveDelays        map[uint32]uint64
	legacyUARTTransmitCounts       map[uint32]uint32
	legacyUARTT0TransmitBuffers    map[uint32][]byte
	legacyUARTT0PendingResponses   map[uint32][]byte
	legacyUARTT0SelectedFiles      map[uint32]uint16
	groupedStatusResponses         []QualcommBootGroupedStatusResponse
	groupedStatusSignalArmed       []bool
	sbiControllers                 map[uint32]struct{}
	sbiReadResponses               []QualcommSBIReadResponse
	sbiReadResponseValues          map[qualcommSBIReadKey]uint8
	sbiCompletionStatus            uint32
	sdccControllers                map[uint32]QualcommSDCCControllerConfig
	watchdogServiceReadable        bool
	registers                      map[uint32]uint32
	watchdogServices               uint64
	timeTick                       uint32
	timeTickReadPhase              uint8
	timeTickClocked                bool
	timeTickInstructionRate        uint64
	timeTickHz                     uint64
	timeTickInterruptSource        uint8
	timeTickUseVectored            bool
	timeTickPhase                  uint64
	timeTickPeriodicHz             uint64
	timeTickPeriodicPhase          uint64
	timeTickMatchReady             bool
	timeTickMatchConfigured        bool
}

type qualcommSBIReadKey struct {
	controller uint32
	address    uint8
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
		config.InterruptWindowWritableOffsets,
		config.HalfwordOffsets,
		config.MixedWidthOffsets,
		config.ByteWritableOffsets,
		config.ReadOnlyRegisters,
		config.RegisterResets,
		config.CompletionEvents,
		config.LegacyUARTControllers,
		config.LegacyUARTReceiveData,
		config.SBIControllers,
		config.SBIReadResponses,
		config.SBICompletionStatus,
		config.SDCCControllers,
	); err != nil {
		return nil, fmt.Errorf("invalid Qualcomm boot-control register profile: %w", err)
	}
	if err := validateQualcommBootGroupedStatusResponses(
		config.GroupedStatusResponses,
		config.WritableOffsets,
	); err != nil {
		return nil, fmt.Errorf("invalid Qualcomm boot-control grouped-status profile: %w", err)
	}
	for _, response := range config.GroupedStatusResponses {
		if config.VectoredInterruptController == nil {
			return nil, fmt.Errorf("Qualcomm grouped-status response has no vectored controller")
		}
		index, status, ok := config.VectoredInterruptController.groupForOffset(response.GroupStatusOffset)
		if !ok || !status || response.GroupMask&^config.VectoredInterruptController.config.Groups[index].ValidMask != 0 {
			return nil, fmt.Errorf(
				"Qualcomm grouped-status response 0x%x/0x%x has no matching vectored group",
				response.GroupStatusOffset,
				response.GroupMask,
			)
		}
	}
	for _, controller := range config.SDCCControllers {
		if controller.GroupStatusOffset == 0 {
			continue
		}
		if config.VectoredInterruptController == nil {
			return nil, fmt.Errorf("Qualcomm SDCC absent-card interrupt has no vectored controller")
		}
		index, status, ok := config.VectoredInterruptController.groupForOffset(
			controller.GroupStatusOffset,
		)
		if !ok || !status ||
			controller.GroupMask&^config.VectoredInterruptController.config.Groups[index].ValidMask != 0 {
			return nil, fmt.Errorf(
				"Qualcomm SDCC absent-card interrupt 0x%x/0x%x has no matching vectored group",
				controller.GroupStatusOffset,
				controller.GroupMask,
			)
		}
	}
	if clock := config.TimeTickClock; clock != nil {
		const maximumClockHz = uint64(1) << 48
		if clock.InstructionsPerSecond == 0 || clock.TimeTickHz == 0 ||
			clock.InstructionsPerSecond > maximumClockHz ||
			clock.TimeTickHz > clock.InstructionsPerSecond ||
			clock.PeriodicInterruptHz > clock.InstructionsPerSecond ||
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
	for _, receive := range config.LegacyUARTReceiveData {
		grouped := receive.VectoredGroupStatusOffset != 0 || receive.VectoredGroupMask != 0
		if receive.UseVectoredController &&
			(!receive.PulseReceiveInterrupt || config.VectoredInterruptController == nil ||
				receive.InterruptSource >= config.VectoredInterruptController.SourceCount()) {
			return nil, fmt.Errorf(
				"Qualcomm legacy UART interrupt source %d exceeds vectored controller",
				receive.InterruptSource,
			)
		}
		if grouped {
			if !receive.UseVectoredController || !receive.PulseReceiveInterrupt ||
				config.VectoredInterruptController == nil {
				return nil, fmt.Errorf("invalid Qualcomm legacy UART grouped interrupt route")
			}
			index, status, ok := config.VectoredInterruptController.groupForOffset(
				receive.VectoredGroupStatusOffset,
			)
			if !ok || !status || receive.VectoredGroupMask == 0 ||
				receive.VectoredGroupMask&^config.VectoredInterruptController.config.Groups[index].ValidMask != 0 ||
				receive.InterruptSource != config.VectoredInterruptController.config.Groups[index].Source {
				return nil, fmt.Errorf("invalid Qualcomm legacy UART grouped interrupt source")
			}
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
	sbiReadResponses := append([]QualcommSBIReadResponse(nil), config.SBIReadResponses...)
	sort.Slice(sbiReadResponses, func(left, right int) bool {
		if sbiReadResponses[left].Controller != sbiReadResponses[right].Controller {
			return sbiReadResponses[left].Controller < sbiReadResponses[right].Controller
		}
		return sbiReadResponses[left].Address < sbiReadResponses[right].Address
	})
	groupedStatusResponses := append(
		[]QualcommBootGroupedStatusResponse(nil),
		config.GroupedStatusResponses...,
	)
	sort.Slice(groupedStatusResponses, func(left, right int) bool {
		if groupedStatusResponses[left].Offset != groupedStatusResponses[right].Offset {
			return groupedStatusResponses[left].Offset < groupedStatusResponses[right].Offset
		}
		if groupedStatusResponses[left].GroupStatusOffset != groupedStatusResponses[right].GroupStatusOffset {
			return groupedStatusResponses[left].GroupStatusOffset < groupedStatusResponses[right].GroupStatusOffset
		}
		return groupedStatusResponses[left].GroupMask < groupedStatusResponses[right].GroupMask
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
			config.InterruptWindowWritableOffsets,
			config.HalfwordOffsets,
			config.ReadOnlyRegisters,
			config.CompletionEvents,
			config.SBIControllers,
			config.SBICompletionStatus,
			config.SDCCControllers,
		),
		interruptWindowWritableOffsets: make(
			map[uint32]struct{},
			len(config.InterruptWindowWritableOffsets),
		),
		halfwordOffsets:         make(map[uint32]struct{}, len(config.HalfwordOffsets)),
		mixedWidthOffsets:       make(map[uint32]struct{}, len(config.MixedWidthOffsets)),
		byteWritableOffsets:     make(map[uint32]struct{}, len(config.ByteWritableOffsets)),
		readOnlyRegisters:       make(map[uint32]uint32, len(config.ReadOnlyRegisters)),
		registerResets:          registerResets,
		completionEvents:        completionEvents,
		completionHandlers:      make(map[uint32]QualcommCompletionHandler),
		legacyUARTControllers:   make(map[uint32]struct{}, len(config.LegacyUARTControllers)),
		legacyUARTReceiveQueues: make(map[uint32][]byte, len(config.LegacyUARTReceiveData)),
		legacyUARTReceiveIRQPending: make(
			map[uint32]bool,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTActivationIRQPending: make(
			map[uint32]bool,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTReceiveArmed: make(
			map[uint32]bool,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTReceivePublished: make(
			map[uint32]bool,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTReceiveDelays: make(
			map[uint32]uint64,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTTransmitCounts: make(
			map[uint32]uint32,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTT0TransmitBuffers: make(
			map[uint32][]byte,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTT0PendingResponses: make(
			map[uint32][]byte,
			len(config.LegacyUARTReceiveData),
		),
		legacyUARTT0SelectedFiles: make(
			map[uint32]uint16,
			len(config.LegacyUARTReceiveData),
		),
		groupedStatusResponses:   groupedStatusResponses,
		groupedStatusSignalArmed: make([]bool, len(groupedStatusResponses)),
		sbiControllers:           make(map[uint32]struct{}, len(config.SBIControllers)),
		sbiReadResponses:         sbiReadResponses,
		sbiReadResponseValues:    make(map[qualcommSBIReadKey]uint8, len(sbiReadResponses)),
		sbiCompletionStatus:      config.SBICompletionStatus,
		sdccControllers: make(
			map[uint32]QualcommSDCCControllerConfig,
			len(config.SDCCControllers),
		),
		watchdogServiceReadable: config.WatchdogServiceReadable,
	}
	for _, offset := range config.HalfwordOffsets {
		device.halfwordOffsets[offset] = struct{}{}
	}
	for _, offset := range config.InterruptWindowWritableOffsets {
		device.interruptWindowWritableOffsets[offset] = struct{}{}
	}
	for _, offset := range config.MixedWidthOffsets {
		device.mixedWidthOffsets[offset] = struct{}{}
	}
	for _, offset := range config.ByteWritableOffsets {
		device.byteWritableOffsets[offset] = struct{}{}
	}
	for _, base := range config.LegacyUARTControllers {
		device.legacyUARTControllers[base] = struct{}{}
	}
	device.legacyUARTReceiveData = make(
		[]QualcommLegacyUARTReceiveData,
		len(config.LegacyUARTReceiveData),
	)
	for index, receive := range config.LegacyUARTReceiveData {
		device.legacyUARTReceiveData[index] = QualcommLegacyUARTReceiveData{
			Controller:                receive.Controller,
			InterruptSource:           receive.InterruptSource,
			UseVectoredController:     receive.UseVectoredController,
			VectoredGroupStatusOffset: receive.VectoredGroupStatusOffset,
			VectoredGroupMask:         receive.VectoredGroupMask,
			DelayInstructions:         receive.DelayInstructions,
			ReceiveCommand:            receive.ReceiveCommand,
			ActivationCommand:         receive.ActivationCommand,
			ReceiveFIFOOffset:         receive.ReceiveFIFOOffset,
			PulseReceiveInterrupt:     receive.PulseReceiveInterrupt,
			EchoTransmit:              receive.EchoTransmit,
			T0Card:                    receive.T0Card,
			TransmitFrameBytes:        receive.TransmitFrameBytes,
			TransmitResponse:          append([]byte(nil), receive.TransmitResponse...),
			Data:                      append([]byte(nil), receive.Data...),
		}
	}
	sort.Slice(device.legacyUARTReceiveData, func(left, right int) bool {
		return device.legacyUARTReceiveData[left].Controller <
			device.legacyUARTReceiveData[right].Controller
	})
	for _, register := range config.ReadOnlyRegisters {
		device.readOnlyRegisters[register.Offset] = register.Value
	}
	for _, base := range config.SBIControllers {
		device.sbiControllers[base] = struct{}{}
	}
	for _, controller := range config.SDCCControllers {
		device.sdccControllers[controller.Base] = controller
	}
	for _, response := range sbiReadResponses {
		device.sbiReadResponseValues[qualcommSBIReadKey{
			controller: response.Controller,
			address:    response.Address,
		}] = response.Value
	}
	if clock := config.TimeTickClock; clock != nil {
		device.timeTickClocked = true
		device.timeTickInstructionRate = clock.InstructionsPerSecond
		device.timeTickHz = clock.TimeTickHz
		device.timeTickInterruptSource = clock.InterruptSource
		device.timeTickUseVectored = clock.UseVectoredController
		device.timeTickPeriodicHz = clock.PeriodicInterruptHz
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
	d.timeTickPeriodicPhase = 0
	d.timeTickMatchReady = true
	d.timeTickMatchConfigured = false
	for controller := range d.legacyUARTReceiveQueues {
		delete(d.legacyUARTReceiveQueues, controller)
	}
	for controller := range d.legacyUARTT0TransmitBuffers {
		delete(d.legacyUARTT0TransmitBuffers, controller)
	}
	for controller := range d.legacyUARTT0PendingResponses {
		delete(d.legacyUARTT0PendingResponses, controller)
	}
	for controller := range d.legacyUARTT0SelectedFiles {
		delete(d.legacyUARTT0SelectedFiles, controller)
	}
	for _, receive := range d.legacyUARTReceiveData {
		d.legacyUARTReceiveIRQPending[receive.Controller] = false
		d.legacyUARTActivationIRQPending[receive.Controller] = false
		d.legacyUARTReceiveArmed[receive.Controller] = false
		d.legacyUARTReceivePublished[receive.Controller] = false
		d.legacyUARTReceiveDelays[receive.Controller] = 0
		d.legacyUARTTransmitCounts[receive.Controller] = 0
	}
	if err := d.interruptController.Reset(); err != nil {
		return err
	}
	if d.vectoredInterruptController != nil {
		if err := d.vectoredInterruptController.Reset(); err != nil {
			return err
		}
	}
	if err := d.syncSDCCInterrupts(); err != nil {
		return err
	}
	for index, response := range d.groupedStatusResponses {
		d.groupedStatusSignalArmed[index] = response.RequestMask == 0 &&
			response.NANDReadyMask != 0
		asserted := response.RequestMask != 0 &&
			d.registers[response.Offset]&response.RequestMask != 0
		if err := d.vectoredInterruptController.SetGroupedSource(
			response.GroupStatusOffset,
			response.GroupMask,
			asserted,
		); err != nil {
			return fmt.Errorf("reset Qualcomm grouped-status response: %w", err)
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
	_, interruptWindowOverride := d.interruptWindowWritableOffsets[offset]
	if isQualcommBootControlInterruptWindowOffset(offset) && !interruptWindowOverride {
		return d.interruptController.Read(offset-0x0900, width)
	}
	if d.sbiCompletionStatus != 0 && offset == d.sbiCompletionStatus {
		if width != Width32 {
			return 0, fmt.Errorf("%w: read%d at SBI completion status 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
		}
		if d.vectoredInterruptController != nil &&
			offset >= QualcommVectoredInterruptControllerBaseOffset {
			relative := offset - QualcommVectoredInterruptControllerBaseOffset
			if d.vectoredInterruptController.Handles(relative) {
				value, err := d.vectoredInterruptController.Read(relative, width)
				if err != nil {
					return 0, err
				}
				if value&qualcommBootSBICompleteStatus != 0 {
					if err := d.vectoredInterruptController.SetGroupedSource(
						relative,
						qualcommBootSBICompleteStatus,
						false,
					); err != nil {
						return 0, fmt.Errorf("clear Qualcomm SBI completion source: %w", err)
					}
				}
				return value, nil
			}
		}
		value := d.registers[offset]
		d.registers[offset] = 0
		return value, nil
	}
	if d.vectoredInterruptController != nil &&
		offset >= QualcommVectoredInterruptControllerBaseOffset &&
		offset < QualcommVectoredInterruptControllerBaseOffset+
			QualcommVectoredInterruptControllerWindowSize {
		if err := d.syncGroupedStatusSignals(); err != nil {
			return 0, fmt.Errorf("sample Qualcomm grouped-status signal: %w", err)
		}
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
	if _, ok := d.byteWritableOffsets[offset]; ok && width == Width8 {
		return d.registers[offset] & 0xff, nil
	}
	if width != Width32 {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
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
	case 0x540c:
		if d.watchdogServiceReadable {
			if d.watchdogServices != 0 {
				return 1, nil
			}
			return 0, nil
		}
		return 0, fmt.Errorf("%w: read32 at 0x%x", ErrQualcommBootControlMMIO, offset)
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
	_, interruptWindowOverride := d.interruptWindowWritableOffsets[offset]
	if isQualcommBootControlInterruptWindowOffset(offset) && !interruptWindowOverride {
		return d.interruptController.Write(offset-0x0900, width, value)
	}
	if d.vectoredInterruptController != nil &&
		offset >= QualcommVectoredInterruptControllerBaseOffset &&
		offset < QualcommVectoredInterruptControllerBaseOffset+
			QualcommVectoredInterruptControllerWindowSize {
		relative := offset - QualcommVectoredInterruptControllerBaseOffset
		if d.vectoredInterruptController.Handles(relative) {
			if err := d.vectoredInterruptController.Write(relative, width, value); err != nil {
				return err
			}
			// On MSM6260-family NAND boards the second-level group control
			// write also acknowledges the corresponding raw device status.
			// The firmware writes the child mask and immediately verifies that
			// the paired group-status bit deasserted.
			for _, response := range d.groupedStatusResponses {
				if response.NANDReadyMask == 0 || value&response.GroupMask == 0 {
					continue
				}
				index, _, ok := d.vectoredInterruptController.groupForOffset(
					response.GroupStatusOffset,
				)
				if !ok || relative != d.vectoredInterruptController.config.Groups[index].EnableOffset {
					continue
				}
				d.nandReady.Clear(response.NANDReadyMask)
				if err := d.vectoredInterruptController.SetGroupedSource(
					response.GroupStatusOffset,
					response.GroupMask,
					false,
				); err != nil {
					return fmt.Errorf("acknowledge Qualcomm grouped-status response: %w", err)
				}
			}
			return nil
		}
	}
	for base, controller := range d.sdccControllers {
		switch offset {
		case base + 0x14, base + 0x18, base + 0x1c, base + 0x20, base + 0x34:
			return fmt.Errorf(
				"%w: write%d value 0x%x at read-only SDCC status 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				value,
				offset,
			)
		case base + 0x38:
			if width != Width32 {
				return fmt.Errorf("%w: write%d at SDCC clear 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
			}
			d.registers[offset] = value
			d.registers[base+0x34] &^= value
			if err := d.refreshSDCCInterrupt(controller); err != nil {
				return err
			}
			return nil
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
	if _, ok := d.byteWritableOffsets[offset]; ok && width == Width8 {
		if value > 0xff {
			return fmt.Errorf(
				"%w: write%d value 0x%x at 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				value,
				offset,
			)
		}
		d.registers[offset] = d.registers[offset]&0xffffff00 | value
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
	for base, controller := range d.sdccControllers {
		if offset == base+0x3c {
			if err := d.refreshSDCCInterrupt(controller); err != nil {
				d.registers[offset] = previousValue
				return err
			}
			break
		}
		if offset == base+0x0c && value&0x00000400 != 0 {
			if controller.CardPresent && value&0x3f != 5 {
				// The PL180 command register carries the command index in bits
				// 5:0 and the response-present selector in bit 6. Publish the
				// matching sticky completion bit and the small set of standard
				// SD responses needed by the native discovery sequence.
				status := uint32(0x00000080) // CMDSENT
				if value&0x40 != 0 {
					status = 0x00000040 // CMDRESPEND
				}
				d.registers[base+0x34] |= status
				d.setSDCCCommandResponse(base, value&0x3f)
			} else {
				// SDIO CMD5 times out on a memory-only card. With no card
				// inserted every enabled command completes this way.
				d.registers[base+0x34] |= 0x00000004 // CMDTIMEOUT
			}
			if err := d.refreshSDCCInterrupt(controller); err != nil {
				d.registers[offset] = previousValue
				d.registers[base+0x34] = 0
				return err
			}
			break
		}
	}
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
	for index, response := range d.groupedStatusResponses {
		if response.Offset != offset || response.RequestMask == 0 {
			continue
		}
		asserted := value&response.RequestMask != 0
		previousArmed := d.groupedStatusSignalArmed[index]
		// The raw-NAND probe first drives this grouped status through the
		// request bit itself. Only after that assert/deassert handshake does
		// the status become a live sample of the NAND-ready signal.
		d.groupedStatusSignalArmed[index] = !asserted && response.NANDReadyMask != 0
		if err := d.vectoredInterruptController.SetGroupedSource(
			response.GroupStatusOffset,
			response.GroupMask,
			asserted,
		); err != nil {
			d.registers[offset] = previousValue
			d.groupedStatusSignalArmed[index] = previousArmed
			_ = d.vectoredInterruptController.SetGroupedSource(
				response.GroupStatusOffset,
				response.GroupMask,
				previousValue&response.RequestMask != 0,
			)
			return fmt.Errorf("signal Qualcomm grouped-status response: %w", err)
		}
	}
	for base := range d.sbiControllers {
		if offset == base+qualcommBootSBICommandOffset {
			result := uint32(0)
			if value>>24 == 1 {
				result = uint32(d.sbiReadResponseValues[qualcommSBIReadKey{
					controller: base,
					address:    uint8(value >> 16),
				}])
			}
			d.registers[base+qualcommBootSBIResultOffset] = result
			if d.vectoredInterruptController != nil &&
				d.sbiCompletionStatus >= QualcommVectoredInterruptControllerBaseOffset {
				relative := d.sbiCompletionStatus - QualcommVectoredInterruptControllerBaseOffset
				if d.vectoredInterruptController.Handles(relative) {
					if err := d.vectoredInterruptController.SetGroupedSource(
						relative,
						qualcommBootSBICompleteStatus,
						true,
					); err != nil {
						d.registers[offset] = previousValue
						return fmt.Errorf("signal Qualcomm SBI completion source: %w", err)
					}
					return nil
				}
			}
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
	} else if offset == 0x0380 {
		if value&8 != 0 {
			d.nandReady.Set(d.nandReady.Value() | 2)
		} else {
			for _, response := range d.groupedStatusResponses {
				if response.Offset == offset && response.RequestMask&8 != 0 &&
					response.NANDReadyMask != 0 {
					d.nandReady.Clear(response.NANDReadyMask)
				}
			}
		}
	} else if offset == 0x0414 {
		d.nandReady.Clear(value & 3)
	}
	return nil
}

func (d *QualcommBootControl) syncGroupedStatusSignals() error {
	for index, response := range d.groupedStatusResponses {
		if response.NANDReadyMask == 0 || !d.groupedStatusSignalArmed[index] {
			continue
		}
		if err := d.vectoredInterruptController.SetGroupedSource(
			response.GroupStatusOffset,
			response.GroupMask,
			d.nandReady.Value()&response.NANDReadyMask != 0,
		); err != nil {
			return err
		}
	}
	return nil
}

func (d *QualcommBootControl) setSDCCCommandResponse(base, command uint32) {
	for _, relative := range []uint32{0x14, 0x18, 0x1c, 0x20} {
		d.registers[base+relative] = 0
	}
	switch command {
	case 2: // ALL_SEND_CID, deterministic 128-bit identity
		d.registers[base+0x14] = 0x12345678
		d.registers[base+0x18] = 0x30303030
		d.registers[base+0x1c] = 0x534d3030
		d.registers[base+0x20] = 0x1b000001
	case 3: // SEND_RELATIVE_ADDR
		d.registers[base+0x14] = 0x00010000
	case 8: // SEND_IF_COND
		d.registers[base+0x14] = 0x000001aa
	case 9: // SEND_CSD: SDHC, 4 GiB capacity, 512-byte blocks
		d.registers[base+0x14] = 0x00000000
		d.registers[base+0x18] = 0x0003ffff
		d.registers[base+0x1c] = 0x5b590000
		d.registers[base+0x20] = 0x400e0032
	case 13, 55: // card ready in transfer state; APP_CMD is accepted
		d.registers[base+0x14] = 0x00000920
	case 41: // SD_SEND_OP_COND: powered-up, high-capacity card
		d.registers[base+0x14] = 0xc0ff8000
	}
}

func (d *QualcommBootControl) refreshSDCCInterrupt(
	controller QualcommSDCCControllerConfig,
) error {
	if controller.GroupStatusOffset == 0 {
		return nil
	}
	return d.vectoredInterruptController.SetGroupedSource(
		controller.GroupStatusOffset,
		controller.GroupMask,
		d.registers[controller.Base+0x34]&d.registers[controller.Base+0x3c] != 0,
	)
}

func (d *QualcommBootControl) syncSDCCInterrupts() error {
	for _, controller := range d.sdccControllers {
		if err := d.refreshSDCCInterrupt(controller); err != nil {
			return fmt.Errorf("sync Qualcomm SDCC interrupt: %w", err)
		}
	}
	return nil
}

// Advance implements ClockedDevice. Clocked profiles derive timetick progress
// only from retired guest instructions; compatibility profiles retain the older
// stable-pair read behavior until their clock and interrupt route are known.
func (d *QualcommBootControl) Advance(retiredInstructions uint64) error {
	for _, receive := range d.legacyUARTReceiveData {
		remaining := d.legacyUARTReceiveDelays[receive.Controller]
		if remaining == 0 {
			continue
		}
		if retiredInstructions < remaining {
			d.legacyUARTReceiveDelays[receive.Controller] = remaining - retiredInstructions
			continue
		}
		d.legacyUARTReceiveDelays[receive.Controller] = 0
		d.legacyUARTReceiveQueues[receive.Controller] = append([]byte(nil), receive.Data...)
		d.legacyUARTReceiveIRQPending[receive.Controller] = len(receive.Data) != 0
		d.legacyUARTReceivePublished[receive.Controller] = true
		if err := d.refreshLegacyUARTInterrupt(receive.Controller); err != nil {
			return err
		}
	}
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
	pulseInterrupt := func() error {
		if d.timeTickUseVectored {
			return d.vectoredInterruptController.PulseSource(d.timeTickInterruptSource)
		}
		return d.interruptController.PulseSource(d.timeTickInterruptSource)
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
	periodicInterrupt := false
	if d.timeTickPeriodicHz != 0 {
		interruptHigh, interruptLow := bits.Mul64(retiredInstructions, d.timeTickPeriodicHz)
		interruptLow, carry = bits.Add64(interruptLow, d.timeTickPeriodicPhase, 0)
		interruptHigh, carry = bits.Add64(interruptHigh, 0, carry)
		if carry != 0 {
			return fmt.Errorf("Qualcomm periodic timetick advance overflow")
		}
		periodicHigh, periodicRemainder := bits.Div64(
			0, interruptHigh, d.timeTickInstructionRate,
		)
		periodicLow, periodicRemainder := bits.Div64(
			periodicRemainder, interruptLow, d.timeTickInstructionRate,
		)
		d.timeTickPeriodicPhase = periodicRemainder
		periodicInterrupt = periodicHigh != 0 || periodicLow != 0
	}
	if quotientHigh == 0 && quotientLow == 0 {
		if periodicInterrupt {
			return pulseInterrupt()
		}
		return nil
	}
	previous := d.timeTick
	d.timeTick += uint32(quotientLow)
	if !d.timeTickMatchConfigured {
		if periodicInterrupt {
			return pulseInterrupt()
		}
		return nil
	}
	distance := uint64(uint32(d.registers[0x54c4] - previous))
	if distance == 0 {
		distance = uint64(1) << 32
	}
	if quotientHigh != 0 || quotientLow >= uint64(1)<<32 || quotientLow >= distance {
		return pulseInterrupt()
	}
	if periodicInterrupt {
		return pulseInterrupt()
	}
	return nil
}

func (d *QualcommBootControl) WatchdogServices() uint64 {
	return d.watchdogServices
}

func (d *QualcommBootControl) registerAccessWidths(offset uint32) uint8 {
	widths := uint8(0)
	if _, ok := d.halfwordOffsets[offset]; ok {
		return uint8(Width16)
	}
	if _, ok := d.mixedWidthOffsets[offset]; ok {
		widths |= uint8(Width16)
	}
	if _, ok := d.byteWritableOffsets[offset]; ok {
		widths |= uint8(Width8)
	}
	return widths | uint8(Width32)
}

func (d *QualcommBootControl) SaveState() ([]byte, error) {
	if err := d.syncSDCCInterrupts(); err != nil {
		return nil, err
	}
	if err := d.syncGroupedStatusSignals(); err != nil {
		return nil, err
	}
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
	_ = binary.Write(&output, binary.LittleEndian, uint32(33))
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
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.sbiReadResponses)))
	for _, response := range d.sbiReadResponses {
		_ = binary.Write(&output, binary.LittleEndian, response.Controller)
		_ = output.WriteByte(response.Address)
		_ = output.WriteByte(response.Value)
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.legacyUARTReceiveData)))
	for _, receive := range d.legacyUARTReceiveData {
		_ = binary.Write(&output, binary.LittleEndian, receive.Controller)
		_ = output.WriteByte(receive.InterruptSource)
		useVectored := uint8(0)
		if receive.UseVectoredController {
			useVectored = 1
		}
		_ = output.WriteByte(useVectored)
		pulseInterrupt := uint8(0)
		if receive.PulseReceiveInterrupt {
			pulseInterrupt = 1
		}
		_ = output.WriteByte(pulseInterrupt)
		_ = binary.Write(&output, binary.LittleEndian, receive.ReceiveCommand)
		_ = binary.Write(&output, binary.LittleEndian, receive.ActivationCommand)
		_ = binary.Write(&output, binary.LittleEndian, receive.ReceiveFIFOOffset)
		_ = binary.Write(&output, binary.LittleEndian, receive.VectoredGroupStatusOffset)
		_ = binary.Write(&output, binary.LittleEndian, receive.VectoredGroupMask)
		echoTransmit := uint8(0)
		if receive.EchoTransmit {
			echoTransmit = 1
		}
		_ = output.WriteByte(echoTransmit)
		_ = binary.Write(&output, binary.LittleEndian, receive.TransmitFrameBytes)
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(receive.TransmitResponse)))
		output.Write(receive.TransmitResponse)
		_ = binary.Write(
			&output,
			binary.LittleEndian,
			d.legacyUARTTransmitCounts[receive.Controller],
		)
		t0Card := uint8(0)
		if receive.T0Card {
			t0Card = 1
		}
		_ = output.WriteByte(t0Card)
		t0Buffer := d.legacyUARTT0TransmitBuffers[receive.Controller]
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(t0Buffer)))
		output.Write(t0Buffer)
		_ = binary.Write(
			&output,
			binary.LittleEndian,
			d.legacyUARTT0SelectedFiles[receive.Controller],
		)
		pendingResponse := d.legacyUARTT0PendingResponses[receive.Controller]
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(pendingResponse)))
		output.Write(pendingResponse)
		_ = binary.Write(&output, binary.LittleEndian, receive.DelayInstructions)
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(receive.Data)))
		output.Write(receive.Data)
		published := uint8(0)
		if d.legacyUARTReceivePublished[receive.Controller] {
			published = 1
		}
		_ = output.WriteByte(published)
		_ = binary.Write(
			&output,
			binary.LittleEndian,
			d.legacyUARTReceiveDelays[receive.Controller],
		)
		queue := d.legacyUARTReceiveQueues[receive.Controller]
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(queue)))
		output.Write(queue)
		pending := uint8(0)
		if d.legacyUARTReceiveIRQPending[receive.Controller] {
			pending = 1
		}
		_ = output.WriteByte(pending)
		activationPending := uint8(0)
		if d.legacyUARTActivationIRQPending[receive.Controller] {
			activationPending = 1
		}
		_ = output.WriteByte(activationPending)
		receiveArmed := uint8(0)
		if d.legacyUARTReceiveArmed[receive.Controller] {
			receiveArmed = 1
		}
		_ = output.WriteByte(receiveArmed)
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.groupedStatusResponses)))
	for index, response := range d.groupedStatusResponses {
		_ = binary.Write(&output, binary.LittleEndian, response.Offset)
		_ = binary.Write(&output, binary.LittleEndian, response.RequestMask)
		_ = binary.Write(&output, binary.LittleEndian, response.NANDReadyMask)
		_ = binary.Write(&output, binary.LittleEndian, response.GroupStatusOffset)
		_ = binary.Write(&output, binary.LittleEndian, response.GroupMask)
		armed := uint8(0)
		if d.groupedStatusSignalArmed[index] {
			armed = 1
		}
		_ = output.WriteByte(armed)
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
	_ = binary.Write(&output, binary.LittleEndian, d.timeTickPeriodicHz)
	_ = binary.Write(&output, binary.LittleEndian, d.timeTickPeriodicPhase)
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
	var instructionRate, timeTickHz, timeTickPhase, periodicHz, periodicPhase uint64
	var completionCount, resetCount, sbiResponseCount, uartReceiveCount, groupedResponseCount, count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QBTC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil ||
		(version != 17 && version != 18 && version != 19 && version != 20 && version != 21 && version != 22 && version != 23 && version != 24 && version != 25 && version != 26 && version != 27 && version != 28 && version != 29 && version != 30 && version != 31 && version != 32 && version != 33) ||
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
	if version >= 18 {
		if binary.Read(reader, binary.LittleEndian, &sbiResponseCount) != nil ||
			sbiResponseCount != uint32(len(d.sbiReadResponses)) {
			return ErrInvalidState
		}
		for index := uint32(0); index < sbiResponseCount; index++ {
			var response QualcommSBIReadResponse
			if binary.Read(reader, binary.LittleEndian, &response.Controller) != nil ||
				binary.Read(reader, binary.LittleEndian, &response.Address) != nil ||
				binary.Read(reader, binary.LittleEndian, &response.Value) != nil ||
				response != d.sbiReadResponses[index] {
				return ErrInvalidState
			}
		}
	}
	legacyUARTReceiveQueues := make(map[uint32][]byte, len(d.legacyUARTReceiveData))
	legacyUARTReceiveIRQPending := make(map[uint32]bool, len(d.legacyUARTReceiveData))
	legacyUARTActivationIRQPending := make(map[uint32]bool, len(d.legacyUARTReceiveData))
	legacyUARTReceiveArmed := make(map[uint32]bool, len(d.legacyUARTReceiveData))
	legacyUARTReceivePublished := make(map[uint32]bool, len(d.legacyUARTReceiveData))
	legacyUARTReceiveDelays := make(map[uint32]uint64, len(d.legacyUARTReceiveData))
	legacyUARTTransmitCounts := make(map[uint32]uint32, len(d.legacyUARTReceiveData))
	legacyUARTT0TransmitBuffers := make(map[uint32][]byte, len(d.legacyUARTReceiveData))
	legacyUARTT0PendingResponses := make(map[uint32][]byte, len(d.legacyUARTReceiveData))
	legacyUARTT0SelectedFiles := make(map[uint32]uint16, len(d.legacyUARTReceiveData))
	groupedStatusSignalArmed := make([]bool, len(d.groupedStatusResponses))
	if version >= 20 {
		if binary.Read(reader, binary.LittleEndian, &uartReceiveCount) != nil ||
			uartReceiveCount != uint32(len(d.legacyUARTReceiveData)) {
			return ErrInvalidState
		}
		for index := uint32(0); index < uartReceiveCount; index++ {
			var controller, transmitFrameBytes, transmitResponseLength, transmitCount uint32
			var t0BufferLength, pendingResponseLength, initialLength, queueLength uint32
			var selectedFile uint16
			var configuredDelay, delay uint64
			var interruptSource, useVectored, pulseInterrupt, echoTransmit, t0Card, published, pending, activationPending, receiveArmed uint8
			var receiveCommand, activationCommand, receiveFIFOOffset, vectoredGroupStatusOffset, vectoredGroupMask uint32
			if binary.Read(reader, binary.LittleEndian, &controller) != nil ||
				binary.Read(reader, binary.LittleEndian, &interruptSource) != nil ||
				(version >= 28 && binary.Read(reader, binary.LittleEndian, &useVectored) != nil) ||
				useVectored > 1 ||
				(version >= 28 && (useVectored == 1) != d.legacyUARTReceiveData[index].UseVectoredController) ||
				(version < 28 && d.legacyUARTReceiveData[index].UseVectoredController) ||
				(version >= 28 && binary.Read(reader, binary.LittleEndian, &pulseInterrupt) != nil) ||
				pulseInterrupt > 1 ||
				(version >= 28 && (pulseInterrupt == 1) != d.legacyUARTReceiveData[index].PulseReceiveInterrupt) ||
				(version < 28 && d.legacyUARTReceiveData[index].PulseReceiveInterrupt) ||
				(version >= 28 && binary.Read(reader, binary.LittleEndian, &receiveCommand) != nil) ||
				(version >= 28 && receiveCommand != d.legacyUARTReceiveData[index].ReceiveCommand) ||
				(version < 28 && d.legacyUARTReceiveData[index].ReceiveCommand != 0) ||
				(version >= 31 && binary.Read(reader, binary.LittleEndian, &activationCommand) != nil) ||
				(version >= 31 && activationCommand != d.legacyUARTReceiveData[index].ActivationCommand) ||
				(version < 31 && d.legacyUARTReceiveData[index].ActivationCommand != 0) ||
				(version >= 33 && binary.Read(reader, binary.LittleEndian, &receiveFIFOOffset) != nil) ||
				(version >= 33 && receiveFIFOOffset != d.legacyUARTReceiveData[index].ReceiveFIFOOffset) ||
				(version < 33 && d.legacyUARTReceiveData[index].ReceiveFIFOOffset != 0) ||
				(version >= 29 && binary.Read(reader, binary.LittleEndian, &vectoredGroupStatusOffset) != nil) ||
				(version >= 29 && binary.Read(reader, binary.LittleEndian, &vectoredGroupMask) != nil) ||
				(version >= 29 && vectoredGroupStatusOffset != d.legacyUARTReceiveData[index].VectoredGroupStatusOffset) ||
				(version >= 29 && vectoredGroupMask != d.legacyUARTReceiveData[index].VectoredGroupMask) ||
				(version < 29 && (d.legacyUARTReceiveData[index].VectoredGroupStatusOffset != 0 ||
					d.legacyUARTReceiveData[index].VectoredGroupMask != 0)) ||
				(version >= 24 && binary.Read(reader, binary.LittleEndian, &echoTransmit) != nil) ||
				echoTransmit > 1 ||
				(version >= 24 && (echoTransmit == 1) != d.legacyUARTReceiveData[index].EchoTransmit) ||
				(version >= 25 && binary.Read(reader, binary.LittleEndian, &transmitFrameBytes) != nil) ||
				(version >= 25 && transmitFrameBytes != d.legacyUARTReceiveData[index].TransmitFrameBytes) ||
				(version >= 25 && binary.Read(reader, binary.LittleEndian, &transmitResponseLength) != nil) ||
				uint64(transmitResponseLength)+4 > uint64(reader.Len()) {
				return ErrInvalidState
			}
			transmitResponse := make([]byte, transmitResponseLength)
			if _, err := io.ReadFull(reader, transmitResponse); err != nil ||
				(version >= 25 && !bytes.Equal(transmitResponse, d.legacyUARTReceiveData[index].TransmitResponse)) ||
				(version >= 25 && binary.Read(reader, binary.LittleEndian, &transmitCount) != nil) ||
				transmitCount >= transmitFrameBytes && transmitCount != 0 {
				return ErrInvalidState
			}
			var t0Buffer []byte
			if version >= 26 {
				if binary.Read(reader, binary.LittleEndian, &t0Card) != nil || t0Card > 1 ||
					(t0Card == 1) != d.legacyUARTReceiveData[index].T0Card ||
					binary.Read(reader, binary.LittleEndian, &t0BufferLength) != nil ||
					uint64(t0BufferLength) > uint64(reader.Len()) {
					return ErrInvalidState
				}
				t0Buffer = make([]byte, t0BufferLength)
				if _, err := io.ReadFull(reader, t0Buffer); err != nil ||
					!validLegacyUARTT0TransmitBuffer(d.legacyUARTReceiveData[index], t0Buffer) {
					return ErrInvalidState
				}
			} else if d.legacyUARTReceiveData[index].T0Card {
				return ErrInvalidState
			}
			var pendingResponse []byte
			if version >= 27 {
				if binary.Read(reader, binary.LittleEndian, &selectedFile) != nil ||
					binary.Read(reader, binary.LittleEndian, &pendingResponseLength) != nil ||
					pendingResponseLength > 256 ||
					uint64(pendingResponseLength) > uint64(reader.Len()) ||
					(!d.legacyUARTReceiveData[index].T0Card &&
						(selectedFile != 0 || pendingResponseLength != 0)) {
					return ErrInvalidState
				}
				pendingResponse = make([]byte, pendingResponseLength)
				if _, err := io.ReadFull(reader, pendingResponse); err != nil {
					return ErrInvalidState
				}
			}
			if (version >= 22 && binary.Read(reader, binary.LittleEndian, &configuredDelay) != nil) ||
				(version >= 22 && configuredDelay != d.legacyUARTReceiveData[index].DelayInstructions) ||
				binary.Read(reader, binary.LittleEndian, &initialLength) != nil ||
				uint64(initialLength)+4 > uint64(reader.Len()) {
				return ErrInvalidState
			}
			initial := make([]byte, initialLength)
			if _, err := io.ReadFull(reader, initial); err != nil ||
				controller != d.legacyUARTReceiveData[index].Controller ||
				interruptSource != d.legacyUARTReceiveData[index].InterruptSource ||
				!bytes.Equal(initial, d.legacyUARTReceiveData[index].Data) ||
				(version >= 21 && binary.Read(reader, binary.LittleEndian, &published) != nil) ||
				published > 1 ||
				(version >= 22 && binary.Read(reader, binary.LittleEndian, &delay) != nil) ||
				delay > d.legacyUARTReceiveData[index].DelayInstructions ||
				binary.Read(reader, binary.LittleEndian, &queueLength) != nil ||
				uint64(queueLength) > uint64(reader.Len()) {
				return ErrInvalidState
			}
			queue := make([]byte, queueLength)
			if _, err := io.ReadFull(reader, queue); err != nil {
				return ErrInvalidState
			}
			if version >= 30 {
				if binary.Read(reader, binary.LittleEndian, &pending) != nil ||
					pending > 1 || pending == 1 && len(queue) == 0 {
					return ErrInvalidState
				}
			} else if len(queue) != 0 {
				pending = 1
			}
			if version >= 31 {
				if binary.Read(reader, binary.LittleEndian, &activationPending) != nil ||
					activationPending > 1 {
					return ErrInvalidState
				}
			}
			if version >= 32 {
				if binary.Read(reader, binary.LittleEndian, &receiveArmed) != nil ||
					receiveArmed > 1 {
					return ErrInvalidState
				}
			}
			legacyUARTReceiveQueues[controller] = queue
			legacyUARTReceiveIRQPending[controller] = pending == 1
			legacyUARTActivationIRQPending[controller] = activationPending == 1
			legacyUARTReceiveArmed[controller] = receiveArmed == 1
			if version == 20 {
				published = 1
			}
			if delay != 0 && (published != 0 || queueLength != 0) {
				return ErrInvalidState
			}
			legacyUARTReceivePublished[controller] = published == 1
			legacyUARTReceiveDelays[controller] = delay
			legacyUARTTransmitCounts[controller] = transmitCount
			if len(t0Buffer) != 0 {
				legacyUARTT0TransmitBuffers[controller] = t0Buffer
			}
			if len(pendingResponse) != 0 {
				legacyUARTT0PendingResponses[controller] = pendingResponse
			}
			if selectedFile != 0 {
				legacyUARTT0SelectedFiles[controller] = selectedFile
			}
		}
	} else if len(d.legacyUARTReceiveData) != 0 {
		return ErrInvalidState
	}
	if version >= 23 {
		if binary.Read(reader, binary.LittleEndian, &groupedResponseCount) != nil ||
			groupedResponseCount != uint32(len(d.groupedStatusResponses)) {
			return ErrInvalidState
		}
		for index := uint32(0); index < groupedResponseCount; index++ {
			var response QualcommBootGroupedStatusResponse
			var armed uint8
			if binary.Read(reader, binary.LittleEndian, &response.Offset) != nil ||
				binary.Read(reader, binary.LittleEndian, &response.RequestMask) != nil ||
				binary.Read(reader, binary.LittleEndian, &response.NANDReadyMask) != nil ||
				binary.Read(reader, binary.LittleEndian, &response.GroupStatusOffset) != nil ||
				binary.Read(reader, binary.LittleEndian, &response.GroupMask) != nil ||
				binary.Read(reader, binary.LittleEndian, &armed) != nil || armed > 1 ||
				(armed == 1 && response.NANDReadyMask == 0) ||
				response != d.groupedStatusResponses[index] {
				return ErrInvalidState
			}
			groupedStatusSignalArmed[index] = armed == 1
		}
	} else if len(d.groupedStatusResponses) != 0 {
		return ErrInvalidState
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
	periodicStateSize := uint64(0)
	if version >= 19 {
		periodicStateSize = 16
	}
	if binary.Read(reader, binary.LittleEndian, &vectoredInterruptStateLength) != nil ||
		uint64(vectoredInterruptStateLength)+periodicStateSize != uint64(reader.Len()) ||
		(d.vectoredInterruptController == nil) != (vectoredInterruptStateLength == 0) {
		return ErrInvalidState
	}
	vectoredInterruptState := make([]byte, vectoredInterruptStateLength)
	if _, err := io.ReadFull(reader, vectoredInterruptState); err != nil {
		return ErrInvalidState
	}
	if version >= 19 {
		if binary.Read(reader, binary.LittleEndian, &periodicHz) != nil ||
			periodicHz != d.timeTickPeriodicHz ||
			binary.Read(reader, binary.LittleEndian, &periodicPhase) != nil ||
			(d.timeTickClocked && periodicPhase >= d.timeTickInstructionRate) {
			return ErrInvalidState
		}
	} else if d.timeTickPeriodicHz != 0 {
		return ErrInvalidState
	}
	if reader.Len() != 0 {
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
	d.groupedStatusSignalArmed = groupedStatusSignalArmed
	if err := d.syncSDCCInterrupts(); err != nil {
		return err
	}
	if err := d.syncGroupedStatusSignals(); err != nil {
		return err
	}
	d.watchdogServices = watchdog
	d.timeTick = timeTick
	d.timeTickReadPhase = timeTickReadPhase
	d.timeTickPhase = timeTickPhase
	d.timeTickPeriodicPhase = periodicPhase
	d.timeTickMatchReady = matchReady == 1
	d.timeTickMatchConfigured = matchConfigured == 1
	d.legacyUARTReceiveQueues = legacyUARTReceiveQueues
	d.legacyUARTReceiveIRQPending = legacyUARTReceiveIRQPending
	d.legacyUARTActivationIRQPending = legacyUARTActivationIRQPending
	d.legacyUARTReceiveArmed = legacyUARTReceiveArmed
	d.legacyUARTReceivePublished = legacyUARTReceivePublished
	d.legacyUARTReceiveDelays = legacyUARTReceiveDelays
	d.legacyUARTTransmitCounts = legacyUARTTransmitCounts
	d.legacyUARTT0TransmitBuffers = legacyUARTT0TransmitBuffers
	d.legacyUARTT0PendingResponses = legacyUARTT0PendingResponses
	d.legacyUARTT0SelectedFiles = legacyUARTT0SelectedFiles
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
	gpioReadObserver  QualcommGPIOReadObserver
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
	readOnlyRegisters := make(map[uint32]uint32, len(config.ReadOnlyRegisters)+1)
	// Existing boards sample bit 4 of this raw input word. Profiles may
	// replace the value when another handset wires a different input line.
	readOnlyRegisters[qualcommSecondaryClockDisabledStatusOffset] = 0x10
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

func (d *QualcommSecondaryClockControl) AttachGPIOReadObserver(observer QualcommGPIOReadObserver) error {
	if observer == nil || d.gpioReadObserver != nil {
		return fmt.Errorf("attach Qualcomm secondary-clock GPIO read observer: %w", ErrQualcommSecondaryClockMMIO)
	}
	d.gpioReadObserver = observer
	return nil
}

func (d *QualcommSecondaryClockControl) Read(offset uint32, width Width) (uint32, error) {
	if width == Width32 {
		if value, ok := d.readOnlyRegisters[offset]; ok {
			if d.gpioReadObserver != nil {
				value = d.gpioReadObserver.ObserveGPIORead(offset, value)
			}
			return value, nil
		}
		if value, ok := d.registers[offset]; ok {
			if d.gpioReadObserver != nil {
				value = d.gpioReadObserver.ObserveGPIORead(offset, value)
			}
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
