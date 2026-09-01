package system

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
)

type MemoryKind string

const (
	MemoryRAM       MemoryKind = "ram"
	MemorySparseRAM MemoryKind = "sparse-ram"
)

type MemoryRegionProfile struct {
	ID      string
	Kind    MemoryKind
	Address uint32
	Size    uint32
}

type ReadOnlyRegisterProfile struct {
	ID      string
	Address uint32
	Width   Width
	Value   uint32
}

type LatchedRegisterProfile struct {
	ID         string
	Address    uint32
	Width      Width
	ResetValue uint32
}

type LatchedRegisterWindowProfile struct {
	ID      string
	Address uint32
	Size    uint32
	Width   Width
}

// SamsungMGPProfile locates Samsung's external ARM7/MGP control aperture and
// its ready flag inside a separately mapped shared-memory region.
type SamsungMGPProfile struct {
	ID                        string
	Address                   uint32
	Size                      uint32
	ReleaseOffset             uint32
	SharedMemoryID            string
	ReadyOffset               uint32
	ReadyValue                uint8
	ResponseDelayInstructions uint64
}

func (p SamsungMGPProfile) validate() error {
	if !validProfileID(p.ID) || !validProfileID(p.SharedMemoryID) ||
		p.Address%uint32(Width16) != 0 ||
		uint64(p.Address)+uint64(p.Size) > 1<<32 {
		return fmt.Errorf("invalid Samsung MGP profile %q", p.ID)
	}
	return SamsungMGPControlConfig{
		Size:                      p.Size,
		ReleaseOffset:             p.ReleaseOffset,
		ReadyValue:                p.ReadyValue,
		ResponseDelayInstructions: p.ResponseDelayInstructions,
	}.validate()
}

type HLEReturn string

const (
	HLEReturnLinkRegister HLEReturn = "link-register"

	// HLEContractQualcommPBLVerifiedLoaderState restores the success result
	// produced by an unavailable mask-ROM PBL after it authenticates the exact
	// QCSBL image selected by the host loader.
	HLEContractQualcommPBLVerifiedLoaderState = "qualcomm.pbl.verified-loader-state-v1"
	// HLEContractQualcommBootstrapVerifiedFirmware supplies the zero success
	// result of a bootstrap verifier whose per-handset manufacturing key is not
	// distributed in a firmware package. It is valid only for a build profile
	// that has already matched every package piece by SHA-256.
	HLEContractQualcommBootstrapVerifiedFirmware = "qualcomm.bootstrap.verified-firmware-v1"
	// HLEContractQualcommResidentBootCallback supplies the return boundary of
	// a boot-resident callback for which Qualcomm's progressive ELF carries no
	// packaged implementation. It is valid only for an exact, hash-matched
	// build whose call site ignores the result.
	HLEContractQualcommResidentBootCallback = "qualcomm.boot.resident-callback-v1"
)

type HLECallProfile struct {
	ID       string
	Contract string
	Address  uint32
	Mode     cpu.Mode
	Return   HLEReturn
}

type OneNANDProfile struct {
	Address        uint32
	ManufacturerID uint16
	DeviceID       uint16
	VersionID      uint16
	TechnologyID   uint16
	DieBlockOffset uint32
	Capacity       uint64
	FlexGeometry   *OneNANDFlexGeometry
}

// QualcommSFlashOneNANDProfile describes a OneNAND device reached through the
// MSM7K SFlash controller instead of a directly mapped external-bus aperture.
type QualcommSFlashOneNANDProfile struct {
	Address          uint32
	ManufacturerID   uint16
	DeviceID         uint16
	VersionID        uint16
	TechnologyID     uint16
	DieBlockOffset   uint32
	Capacity         uint64
	FlexGeometry     *OneNANDFlexGeometry
	SpareInitialData []FlashSeed
}

type ParallelPanelPortProfile struct {
	CommandAddress uint32
	DataAddress    uint32
}

// IndexedHalfwordRegisterPortProfile maps a pair of sparse 16-bit external-bus
// ports. Writing the command port selects a register; the data port then reads
// or writes the selected register. CommandReadValue is the board-observed
// status value returned by reads from the command port.
type IndexedHalfwordRegisterPortProfile struct {
	ID               string
	CommandAddress   uint32
	DataAddress      uint32
	CommandReadValue uint16
}

func (p IndexedHalfwordRegisterPortProfile) validate() error {
	if !validProfileID(p.ID) ||
		p.CommandAddress%uint32(Width16) != 0 || p.DataAddress%uint32(Width16) != 0 ||
		p.CommandAddress == p.DataAddress ||
		uint64(p.CommandAddress)+uint64(Width16) > 1<<32 ||
		uint64(p.DataAddress)+uint64(Width16) > 1<<32 {
		return ErrInvalidRegion
	}
	return nil
}

func (p ParallelPanelPortProfile) validate() error {
	if p.CommandAddress%uint32(Width16) != 0 || p.DataAddress%uint32(Width16) != 0 ||
		p.CommandAddress == p.DataAddress || uint64(p.CommandAddress)+uint64(Width16) > 1<<32 ||
		uint64(p.DataAddress)+uint64(Width16) > 1<<32 {
		return ErrInvalidRegion
	}
	return nil
}

func (p OneNANDProfile) validate() error {
	geometry, geometryErr := normalizeOneNANDGeometry(p.Capacity, p.FlexGeometry)
	if p.Address%OneNANDWindowSize != 0 ||
		uint64(p.Address)+OneNANDWindowSize > 1<<32 ||
		p.ManufacturerID == 0 || p.DeviceID == 0 ||
		geometryErr != nil ||
		p.DieBlockOffset != 0 && (p.DieBlockOffset&(p.DieBlockOffset-1) != 0 ||
			p.DieBlockOffset >= geometry.blockCount) {
		return ErrInvalidOneNAND
	}
	return nil
}

func (p QualcommSFlashOneNANDProfile) validate() error {
	geometry, geometryErr := normalizeOneNANDGeometry(p.Capacity, p.FlexGeometry)
	if p.Address%QualcommSFlashWindowSize != 0 ||
		uint64(p.Address)+QualcommSFlashWindowSize > 1<<32 ||
		p.ManufacturerID == 0 || p.DeviceID == 0 ||
		geometryErr != nil ||
		p.DieBlockOffset != 0 && (p.DieBlockOffset&(p.DieBlockOffset-1) != 0 ||
			p.DieBlockOffset >= geometry.blockCount) {
		return ErrInvalidOneNAND
	}
	sparePageSize := geometry.pageSize / oneNANDSectorSize * oneNANDSpareSectorSize
	spareCapacity := p.Capacity / uint64(geometry.pageSize) * uint64(sparePageSize)
	initialData := append([]FlashSeed(nil), p.SpareInitialData...)
	sort.Slice(initialData, func(left, right int) bool {
		return initialData[left].Offset < initialData[right].Offset
	})
	for index, seed := range initialData {
		if len(seed.Data) == 0 || seed.Offset >= spareCapacity ||
			uint64(len(seed.Data)) > spareCapacity-seed.Offset {
			return ErrInvalidNANDSpare
		}
		if index > 0 {
			previous := initialData[index-1]
			if previous.Offset+uint64(len(previous.Data)) > seed.Offset {
				return ErrInvalidNANDSpare
			}
		}
	}
	return nil
}

// QualcommPrimaryClockKeyProfile maps a host control to one raw digital input
// exposed by the Qualcomm primary-clock GPIO status register.
type QualcommPrimaryClockKeyProfile struct {
	ID        string
	InputLine uint8
	ActiveLow bool
}

func (p HLECallProfile) validate() error {
	trap := cpu.ExecutionTrap{Address: p.Address, Mode: p.Mode}
	if !validProfileID(p.ID) || !validProfileID(p.Contract) || !trap.Valid() ||
		p.Return != HLEReturnLinkRegister {
		return fmt.Errorf("invalid HLE call profile %q", p.ID)
	}
	return nil
}

type BoardProfile struct {
	ID                            string
	PlatformID                    string
	FirmwareBuildID               string
	NANDReadID                    uint32
	NANDSize                      uint64
	NANDFactoryBadBlocks          []uint32
	NANDReportsErasedECCCodewords bool
	NANDInitialData               []FlashSeed
	OneNAND                       *OneNANDProfile
	SFlashOneNAND                 *QualcommSFlashOneNANDProfile
	// PBLServiceTableAddress overrides the legacy MSM6xxx PBL-owned IRAM
	// address used for the r8 service table. Zero keeps the legacy default.
	PBLServiceTableAddress uint32
	// PBLServiceTableHeaderSize selects the first service-entry offset for the
	// profiled PBL ABI. Zero keeps the legacy 0x2c-byte header.
	PBLServiceTableHeaderSize uint32
	// PBLHeaderFeatureDataAddress supplies the separate r9 feature-slot table
	// used by newer PBL ABIs. Zero omits that table.
	PBLHeaderFeatureDataAddress uint32
	PBLHeaderFeatures           []QualcommPBLHeaderFeature
	PBLLegacyFeatureDataAddress uint32
	PBLSharedDataAddress        uint32
	PBLSharedDataSize           uint32
	BootClockModeStatus         uint32
	// BootControlAddress overrides the MSM6xxx default 0x80000000 physical
	// base for chip families which relocate the same bounded control device.
	BootControlAddress                        uint32
	PrimaryClockStatus                        uint32
	PrimaryClockInputMask                     uint32
	PrimaryClockKeys                          []QualcommPrimaryClockKeyProfile
	BootControlWritableOffsets                []uint32
	BootControlInterruptWindowWritableOffsets []uint32
	BootControlHalfwordOffsets                []uint32
	BootControlMixedWidthOffsets              []uint32
	BootControlReadOnlyRegisters              []QualcommBootReadOnlyRegister
	BootControlRegisterResets                 []QualcommBootRegisterReset
	BootControlCompletionEvents               []QualcommCompletionEventConfig
	BootControlLegacyUARTControllers          []uint32
	BootControlSBIControllers                 []uint32
	BootControlSBIReadResponses               []QualcommSBIReadResponse
	BootControlSBICompletionStatus            uint32
	BootControlWatchdogReadable               bool
	BootControlGPIOInputs                     []QualcommGPIOInputRegister
	PrimaryClockWritableOffsets               []uint32
	SecondaryClockWritableOffsets             []uint32
	SecondaryClockReadOnlyRegisters           []QualcommSecondaryClockReadOnlyRegister
	SparseBusRegisterOffsets                  []uint32
	SparseBusRegisterResets                   []SparseWordRegisterReset
	ClockRegimeSleepControllers               []uint32
	ClockRegimeCounters                       []QualcommClockRegimeCounterConfig
	ClockRegimeComparators                    []QualcommClockRegimeComparatorConfig
	VectoredInterrupt                         *QualcommVectoredInterruptConfig
	TimeTickClock                             *QualcommTimeTickClockConfig
	Keypad                                    *QualcommGPIOKeypadProfile
	Panel                                     DCSPanelConfig
	PanelPorts                                *ParallelPanelPortProfile
	IndexedHalfwordRegisterPorts              []IndexedHalfwordRegisterPortProfile
	MDP                                       *QualcommMDPProfile
	LegacyTopVersion                          uint32
	LegacyTopIdentification                   uint32
	LegacyTopWritableOffsets                  []uint32
	Memory                                    []MemoryRegionProfile
	ReadOnlyRegisters                         []ReadOnlyRegisterProfile
	LatchedRegisters                          []LatchedRegisterProfile
	LatchedRegisterWindows                    []LatchedRegisterWindowProfile
	SamsungMGP                                *SamsungMGPProfile
	ADSPMailbox                               *QualcommADSPMailboxProfile
	HLECalls                                  []HLECallProfile
}

func (p BoardProfile) Validate() error {
	if !validProfileID(p.ID) || !validProfileID(p.PlatformID) || !validProfileID(p.FirmwareBuildID) {
		return fmt.Errorf("system board profile identity is invalid")
	}
	if p.NANDSize != 0 && (p.NANDSize%0x800 != 0 || p.NANDSize > uint64(1<<63-1)) {
		return fmt.Errorf("board profile %q has invalid NAND size 0x%x", p.ID, p.NANDSize)
	}
	if _, err := validateQualcommLegacyTopWritableOffsets(p.LegacyTopWritableOffsets); err != nil {
		return fmt.Errorf("board profile %q legacy top page: %w", p.ID, err)
	}
	badBlocks := make(map[uint32]struct{}, len(p.NANDFactoryBadBlocks))
	for _, block := range p.NANDFactoryBadBlocks {
		if p.NANDSize == 0 || uint64(block) >= p.NANDSize/qualcomm2K8BitNANDEraseBlockSize {
			return fmt.Errorf("board profile %q has invalid NAND factory bad block 0x%x", p.ID, block)
		}
		if _, duplicate := badBlocks[block]; duplicate {
			return fmt.Errorf("board profile %q repeats NAND factory bad block 0x%x", p.ID, block)
		}
		badBlocks[block] = struct{}{}
	}
	initialData := append([]FlashSeed(nil), p.NANDInitialData...)
	sort.Slice(initialData, func(left, right int) bool {
		return initialData[left].Offset < initialData[right].Offset
	})
	for index, seed := range initialData {
		if p.NANDSize == 0 || len(seed.Data) == 0 || seed.Offset >= p.NANDSize ||
			uint64(len(seed.Data)) > p.NANDSize-seed.Offset {
			return fmt.Errorf("board profile %q has invalid NAND initial data at 0x%x", p.ID, seed.Offset)
		}
		if index > 0 {
			previous := initialData[index-1]
			if previous.Offset+uint64(len(previous.Data)) > seed.Offset {
				return fmt.Errorf("board profile %q has overlapping NAND initial data", p.ID)
			}
		}
	}
	if p.OneNAND != nil {
		if err := p.OneNAND.validate(); err != nil {
			return fmt.Errorf("board profile %q OneNAND: %w", p.ID, err)
		}
	}
	if p.SFlashOneNAND != nil {
		if p.OneNAND != nil {
			return fmt.Errorf("board profile %q configures two OneNAND interfaces: %w", p.ID, ErrInvalidOneNAND)
		}
		if err := p.SFlashOneNAND.validate(); err != nil {
			return fmt.Errorf("board profile %q Qualcomm SFlash OneNAND: %w", p.ID, err)
		}
	}
	primaryClockInputMask := p.PrimaryClockInputMask
	if primaryClockInputMask == 0 {
		primaryClockInputMask = qualcommPrimaryGPIOInputMask
	}
	if p.PrimaryClockStatus&^primaryClockInputMask != 0 {
		return fmt.Errorf("board profile %q has invalid primary clock status 0x%x", p.ID, p.PrimaryClockStatus)
	}
	primaryClockKeyIDs := make(map[string]struct{}, len(p.PrimaryClockKeys))
	primaryClockKeyLines := make(map[uint8]struct{}, len(p.PrimaryClockKeys))
	for _, key := range p.PrimaryClockKeys {
		if !validProfileID(key.ID) || key.InputLine >= 32 {
			return fmt.Errorf("board profile %q has invalid primary-clock key %q", p.ID, key.ID)
		}
		lineMask := uint32(1) << key.InputLine
		idleHigh := p.PrimaryClockStatus&lineMask != 0
		if lineMask&primaryClockInputMask == 0 || idleHigh != key.ActiveLow {
			return fmt.Errorf("board profile %q has invalid primary-clock key %q", p.ID, key.ID)
		}
		if _, duplicate := primaryClockKeyIDs[key.ID]; duplicate {
			return fmt.Errorf("board profile %q repeats primary-clock key ID %q", p.ID, key.ID)
		}
		if _, duplicate := primaryClockKeyLines[key.InputLine]; duplicate {
			return fmt.Errorf("board profile %q repeats primary-clock key input line %d", p.ID, key.InputLine)
		}
		primaryClockKeyIDs[key.ID] = struct{}{}
		primaryClockKeyLines[key.InputLine] = struct{}{}
	}
	if p.BootClockModeStatus&^uint32(0x11) != 0 {
		return fmt.Errorf("board profile %q has invalid boot clock mode status 0x%x", p.ID, p.BootClockModeStatus)
	}
	if p.BootControlAddress != 0 &&
		(p.BootControlAddress%QualcommBootControlWindowSize != 0 ||
			uint64(p.BootControlAddress)+QualcommBootControlWindowSize > 1<<32) {
		return fmt.Errorf("board profile %q has invalid boot-control address 0x%x", p.ID, p.BootControlAddress)
	}
	if err := validateQualcommBootControlConfigurationOffsets(
		p.BootControlWritableOffsets,
		p.BootControlInterruptWindowWritableOffsets,
		p.BootControlHalfwordOffsets,
		p.BootControlMixedWidthOffsets,
		p.BootControlReadOnlyRegisters,
		p.BootControlRegisterResets,
		p.BootControlCompletionEvents,
		p.BootControlLegacyUARTControllers,
		p.BootControlSBIControllers,
		p.BootControlSBIReadResponses,
		p.BootControlSBICompletionStatus,
	); err != nil {
		return fmt.Errorf("board profile %q boot-control register profile: %w", p.ID, err)
	}
	if err := validateQualcommGPIOInputRegisters(p.BootControlGPIOInputs); err != nil {
		return fmt.Errorf("board profile %q boot-control GPIO inputs: %w", p.ID, err)
	}
	if err := validateQualcommPrimaryClockWritableOffsets(p.PrimaryClockWritableOffsets); err != nil {
		return fmt.Errorf("board profile %q primary-clock writable offsets: %w", p.ID, err)
	}
	if err := validateQualcommSecondaryClockConfig(QualcommSecondaryClockConfig{
		WritableOffsets:   p.SecondaryClockWritableOffsets,
		ReadOnlyRegisters: p.SecondaryClockReadOnlyRegisters,
	}); err != nil {
		return fmt.Errorf("board profile %q secondary-clock registers: %w", p.ID, err)
	}
	if keypad := p.Keypad; keypad != nil {
		if err := keypad.validate(); err != nil {
			return fmt.Errorf("board profile %q keypad: %w", p.ID, err)
		}
		var keypadInputMask uint32
		for _, line := range keypad.Columns {
			keypadInputMask |= uint32(1) << line
		}
		if keypadInputMask&^primaryClockInputMask != 0 {
			return fmt.Errorf("board profile %q keypad columns exceed primary-clock inputs", p.ID)
		}
		for _, key := range keypad.Keys {
			if _, duplicate := primaryClockKeyIDs[key.ID]; duplicate {
				return fmt.Errorf("board profile %q repeats control ID %q", p.ID, key.ID)
			}
		}
		for line := range primaryClockKeyLines {
			if keypadInputMask&(uint32(1)<<line) != 0 {
				return fmt.Errorf("board profile %q shares primary-clock input line %d with its keypad", p.ID, line)
			}
		}
		secondaryOffsets := make(map[uint32]struct{}, len(qualcommSecondaryClockOffsets)+len(p.SecondaryClockWritableOffsets))
		for _, offset := range qualcommSecondaryClockOffsets {
			secondaryOffsets[offset] = struct{}{}
		}
		for _, offset := range p.SecondaryClockWritableOffsets {
			secondaryOffsets[offset] = struct{}{}
		}
		for _, row := range keypad.Rows {
			if row.OutputBank == QualcommGPIOOutputSecondaryClock {
				if _, writable := secondaryOffsets[row.OutputOffset]; !writable {
					return fmt.Errorf(
						"board profile %q keypad row uses unwritable secondary-clock offset 0x%x",
						p.ID, row.OutputOffset,
					)
				}
			}
		}
		primaryWritable := make(map[uint32]struct{}, len(qualcommPrimaryClockWritableOffsets)+len(p.PrimaryClockWritableOffsets))
		for _, offset := range mergedQualcommPrimaryClockWritableOffsets(p.PrimaryClockWritableOffsets) {
			primaryWritable[offset] = struct{}{}
		}
		for index, group := range keypad.InterruptGroups {
			for _, offset := range []uint32{
				group.ClearOffset,
				group.EnableOffset,
				group.DetectOffset,
				group.PolarityOffset,
			} {
				if _, writable := primaryWritable[offset]; !writable {
					return fmt.Errorf(
						"board profile %q keypad interrupt group %d uses unwritable primary-clock offset 0x%x",
						p.ID, index, offset,
					)
				}
			}
			if _, writable := primaryWritable[group.StatusOffset]; writable ||
				group.StatusOffset == qualcommPrimaryGPIOInputOffset {
				return fmt.Errorf(
					"board profile %q keypad interrupt group %d has invalid status offset 0x%x",
					p.ID, index, group.StatusOffset,
				)
			}
			if group.UseVectoredController {
				if p.VectoredInterrupt == nil || group.InterruptSource >= p.VectoredInterrupt.SourceCount {
					return fmt.Errorf(
						"board profile %q keypad interrupt group %d source %d exceeds vectored controller",
						p.ID, index, group.InterruptSource,
					)
				}
			} else if group.InterruptSource >= 64 {
				return fmt.Errorf(
					"board profile %q keypad interrupt group %d source %d exceeds legacy controller",
					p.ID, index, group.InterruptSource,
				)
			}
		}
	}
	if p.Panel.Width != 0 || p.Panel.Height != 0 {
		if _, err := validateDCSPanelConfig(p.Panel); err != nil {
			return fmt.Errorf("board profile %q panel: %w", p.ID, err)
		}
	}
	if p.PanelPorts != nil {
		if p.Panel.Width == 0 || p.Panel.Height == 0 {
			return fmt.Errorf("board profile %q sparse panel ports have no panel", p.ID)
		}
		if err := p.PanelPorts.validate(); err != nil {
			return fmt.Errorf("board profile %q sparse panel ports: %w", p.ID, err)
		}
	}
	indexedPortIDs := make(map[string]struct{}, len(p.IndexedHalfwordRegisterPorts))
	indexedPortAddresses := make(map[uint32]struct{}, len(p.IndexedHalfwordRegisterPorts)*2)
	for _, ports := range p.IndexedHalfwordRegisterPorts {
		if err := ports.validate(); err != nil {
			return fmt.Errorf("board profile %q indexed halfword ports %q: %w", p.ID, ports.ID, err)
		}
		if _, duplicate := indexedPortIDs[ports.ID]; duplicate {
			return fmt.Errorf("board profile %q repeats indexed halfword port ID %q", p.ID, ports.ID)
		}
		indexedPortIDs[ports.ID] = struct{}{}
		for _, address := range []uint32{ports.CommandAddress, ports.DataAddress} {
			if _, duplicate := indexedPortAddresses[address]; duplicate {
				return fmt.Errorf("board profile %q repeats indexed halfword port 0x%08x", p.ID, address)
			}
			indexedPortAddresses[address] = struct{}{}
		}
	}
	if mdp := p.MDP; mdp != nil {
		if err := mdp.validate(); err != nil {
			return fmt.Errorf("board profile %q MDP: %w", p.ID, err)
		}
		if p.Panel.Width == 0 || p.Panel.Height == 0 {
			return fmt.Errorf("board profile %q MDP has no panel", p.ID)
		}
		completionFound := false
		for _, event := range p.BootControlCompletionEvents {
			if event.StartOffset == mdp.CompletionStartOffset {
				completionFound = true
				break
			}
		}
		if !completionFound {
			return fmt.Errorf(
				"board profile %q MDP completion start 0x%x is not profiled",
				p.ID,
				mdp.CompletionStartOffset,
			)
		}
		scriptPointerFound := false
		for _, offset := range p.BootControlWritableOffsets {
			if offset == mdp.ScriptPointerOffset {
				scriptPointerFound = true
				break
			}
		}
		if !scriptPointerFound {
			return fmt.Errorf(
				"board profile %q MDP script-pointer register 0x%x is not writable",
				p.ID,
				mdp.ScriptPointerOffset,
			)
		}
		for _, offset := range p.BootControlHalfwordOffsets {
			if offset == mdp.ScriptPointerOffset {
				return fmt.Errorf(
					"board profile %q MDP script-pointer register 0x%x is not 32-bit",
					p.ID,
					mdp.ScriptPointerOffset,
				)
			}
		}
	}
	if p.VectoredInterrupt != nil {
		if err := p.VectoredInterrupt.validate(); err != nil {
			return fmt.Errorf("board profile %q vectored interrupt controller: %w", p.ID, err)
		}
	}
	if clock := p.TimeTickClock; clock != nil {
		const maximumClockHz = uint64(1) << 48
		if clock.InstructionsPerSecond == 0 || clock.TimeTickHz == 0 ||
			clock.InstructionsPerSecond > maximumClockHz ||
			clock.TimeTickHz > clock.InstructionsPerSecond ||
			clock.InterruptSource >= 64 {
			return fmt.Errorf("board profile %q has invalid timetick clock", p.ID)
		}
		if clock.UseVectoredController &&
			(p.VectoredInterrupt == nil || clock.InterruptSource >= p.VectoredInterrupt.SourceCount) {
			return fmt.Errorf(
				"board profile %q timetick interrupt source %d exceeds vectored controller",
				p.ID,
				clock.InterruptSource,
			)
		}
	}
	for _, event := range p.BootControlCompletionEvents {
		if event.UseVectoredController {
			if p.VectoredInterrupt == nil || event.InterruptSource >= p.VectoredInterrupt.SourceCount {
				return fmt.Errorf(
					"board profile %q completion interrupt source %d exceeds vectored controller",
					p.ID,
					event.InterruptSource,
				)
			}
		} else if event.InterruptSource >= 64 {
			return fmt.Errorf(
				"board profile %q has invalid completion interrupt source %d",
				p.ID,
				event.InterruptSource,
			)
		}
	}
	if err := validateQualcommClockRegimeConfig(QualcommClockRegimeConfig{
		SleepControllers: p.ClockRegimeSleepControllers,
		Counters:         p.ClockRegimeCounters,
		Comparators:      p.ClockRegimeComparators,
	}); err != nil {
		return fmt.Errorf("board profile %q clock-regime profile: %w", p.ID, err)
	}
	for _, comparator := range p.ClockRegimeComparators {
		if comparator.UseVectoredController &&
			(p.VectoredInterrupt == nil || comparator.InterruptSource >= p.VectoredInterrupt.SourceCount) {
			return fmt.Errorf(
				"board profile %q clock-regime comparator interrupt source %d exceeds vectored controller",
				p.ID,
				comparator.InterruptSource,
			)
		}
	}
	memory := append([]MemoryRegionProfile(nil), p.Memory...)
	for _, region := range memory {
		if !validProfileID(region.ID) ||
			(region.Kind != MemoryRAM && region.Kind != MemorySparseRAM) || region.Size == 0 ||
			uint64(region.Address)+uint64(region.Size) > 1<<32 {
			return fmt.Errorf("board profile %q has invalid memory region %q", p.ID, region.ID)
		}
	}
	sort.Slice(memory, func(i, j int) bool { return memory[i].Address < memory[j].Address })
	for index := 1; index < len(memory); index++ {
		previousEnd := uint64(memory[index-1].Address) + uint64(memory[index-1].Size)
		if previousEnd > uint64(memory[index].Address) {
			return fmt.Errorf(
				"board profile %q memory regions %q and %q overlap",
				p.ID,
				memory[index-1].ID,
				memory[index].ID,
			)
		}
	}
	var mgpSharedMemory *MemoryRegionProfile
	if mgp := p.SamsungMGP; mgp != nil {
		if err := mgp.validate(); err != nil {
			return fmt.Errorf("board profile %q: %w", p.ID, err)
		}
		mgpEnd := uint64(mgp.Address) + uint64(mgp.Size)
		for index := range memory {
			region := &memory[index]
			if region.ID == mgp.SharedMemoryID {
				if mgpSharedMemory != nil {
					return fmt.Errorf("board profile %q repeats MGP shared memory %q", p.ID, region.ID)
				}
				mgpSharedMemory = region
			}
			regionEnd := uint64(region.Address) + uint64(region.Size)
			if uint64(mgp.Address) < regionEnd && uint64(region.Address) < mgpEnd {
				return fmt.Errorf("board profile %q regions %q and %q overlap", p.ID, mgp.ID, region.ID)
			}
		}
		if mgpSharedMemory == nil || mgp.ID == mgp.SharedMemoryID ||
			mgp.ReadyOffset >= mgpSharedMemory.Size {
			return fmt.Errorf("board profile %q has invalid MGP shared-memory target", p.ID)
		}
	}
	registers := append([]ReadOnlyRegisterProfile(nil), p.ReadOnlyRegisters...)
	for _, register := range registers {
		if !validProfileID(register.ID) ||
			(register.Width != Width8 && register.Width != Width16 && register.Width != Width32) ||
			register.Address%uint32(register.Width) != 0 ||
			uint64(register.Address)+uint64(register.Width) > 1<<32 ||
			(register.Width < Width32 && register.Value >= uint32(1)<<(uint32(register.Width)*8)) {
			return fmt.Errorf("board profile %q has invalid read-only register %q", p.ID, register.ID)
		}
	}
	sort.Slice(registers, func(i, j int) bool { return registers[i].Address < registers[j].Address })
	for index := 1; index < len(registers); index++ {
		previousEnd := uint64(registers[index-1].Address) + uint64(registers[index-1].Width)
		if previousEnd > uint64(registers[index].Address) {
			return fmt.Errorf(
				"board profile %q read-only registers %q and %q overlap",
				p.ID,
				registers[index-1].ID,
				registers[index].ID,
			)
		}
	}
	latchedRegisters := append([]LatchedRegisterProfile(nil), p.LatchedRegisters...)
	for _, register := range latchedRegisters {
		if !validProfileID(register.ID) ||
			(register.Width != Width8 && register.Width != Width16 && register.Width != Width32) ||
			register.Address%uint32(register.Width) != 0 ||
			uint64(register.Address)+uint64(register.Width) > 1<<32 ||
			(register.Width < Width32 && register.ResetValue >= uint32(1)<<(uint32(register.Width)*8)) {
			return fmt.Errorf("board profile %q has invalid latched register %q", p.ID, register.ID)
		}
	}
	sort.Slice(latchedRegisters, func(i, j int) bool {
		return latchedRegisters[i].Address < latchedRegisters[j].Address
	})
	for index := 1; index < len(latchedRegisters); index++ {
		previousEnd := uint64(latchedRegisters[index-1].Address) + uint64(latchedRegisters[index-1].Width)
		if previousEnd > uint64(latchedRegisters[index].Address) {
			return fmt.Errorf(
				"board profile %q latched registers %q and %q overlap",
				p.ID,
				latchedRegisters[index-1].ID,
				latchedRegisters[index].ID,
			)
		}
	}
	for _, readOnly := range registers {
		readOnlyEnd := uint64(readOnly.Address) + uint64(readOnly.Width)
		for _, latched := range latchedRegisters {
			latchedEnd := uint64(latched.Address) + uint64(latched.Width)
			if uint64(readOnly.Address) < latchedEnd && uint64(latched.Address) < readOnlyEnd {
				return fmt.Errorf(
					"board profile %q registers %q and %q overlap",
					p.ID,
					readOnly.ID,
					latched.ID,
				)
			}
		}
	}
	latchedWindows := append([]LatchedRegisterWindowProfile(nil), p.LatchedRegisterWindows...)
	for _, window := range latchedWindows {
		if !validProfileID(window.ID) ||
			(window.Width != Width8 && window.Width != Width16 && window.Width != Width32) ||
			window.Size == 0 || window.Address%uint32(window.Width) != 0 ||
			window.Size%uint32(window.Width) != 0 ||
			uint64(window.Address)+uint64(window.Size) > 1<<32 {
			return fmt.Errorf("board profile %q has invalid latched register window %q", p.ID, window.ID)
		}
	}
	sort.Slice(latchedWindows, func(i, j int) bool {
		return latchedWindows[i].Address < latchedWindows[j].Address
	})
	for index := 1; index < len(latchedWindows); index++ {
		previousEnd := uint64(latchedWindows[index-1].Address) + uint64(latchedWindows[index-1].Size)
		if previousEnd > uint64(latchedWindows[index].Address) {
			return fmt.Errorf(
				"board profile %q latched register windows %q and %q overlap",
				p.ID,
				latchedWindows[index-1].ID,
				latchedWindows[index].ID,
			)
		}
	}
	for _, window := range latchedWindows {
		windowEnd := uint64(window.Address) + uint64(window.Size)
		for _, readOnly := range registers {
			readOnlyEnd := uint64(readOnly.Address) + uint64(readOnly.Width)
			if uint64(window.Address) < readOnlyEnd && uint64(readOnly.Address) < windowEnd {
				return fmt.Errorf(
					"board profile %q registers %q and %q overlap",
					p.ID,
					window.ID,
					readOnly.ID,
				)
			}
		}
		for _, latched := range latchedRegisters {
			latchedEnd := uint64(latched.Address) + uint64(latched.Width)
			if uint64(window.Address) < latchedEnd && uint64(latched.Address) < windowEnd {
				return fmt.Errorf(
					"board profile %q registers %q and %q overlap",
					p.ID,
					window.ID,
					latched.ID,
				)
			}
		}
	}
	if mgp := p.SamsungMGP; mgp != nil {
		mgpEnd := uint64(mgp.Address) + uint64(mgp.Size)
		for _, register := range registers {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if mgp.ID == register.ID ||
				uint64(mgp.Address) < registerEnd && uint64(register.Address) < mgpEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mgp.ID, register.ID)
			}
		}
		for _, register := range latchedRegisters {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if mgp.ID == register.ID ||
				uint64(mgp.Address) < registerEnd && uint64(register.Address) < mgpEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mgp.ID, register.ID)
			}
		}
		for _, window := range latchedWindows {
			windowEnd := uint64(window.Address) + uint64(window.Size)
			if mgp.ID == window.ID ||
				uint64(mgp.Address) < windowEnd && uint64(window.Address) < mgpEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mgp.ID, window.ID)
			}
		}
	}
	if mailbox := p.ADSPMailbox; mailbox != nil {
		if err := mailbox.validate(); err != nil {
			return fmt.Errorf("board profile %q: %w", p.ID, err)
		}
		mailboxEnd := uint64(mailbox.Address) + uint64(mailbox.Size)
		if mgp := p.SamsungMGP; mgp != nil {
			mgpEnd := uint64(mgp.Address) + uint64(mgp.Size)
			if mailbox.ID == mgp.ID ||
				uint64(mailbox.Address) < mgpEnd && uint64(mgp.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, mgp.ID)
			}
		}
		for _, region := range memory {
			regionEnd := uint64(region.Address) + uint64(region.Size)
			if uint64(mailbox.Address) < regionEnd && uint64(region.Address) < mailboxEnd {
				return fmt.Errorf(
					"board profile %q regions %q and %q overlap",
					p.ID,
					mailbox.ID,
					region.ID,
				)
			}
		}
		for _, register := range registers {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if uint64(mailbox.Address) < registerEnd && uint64(register.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, register.ID)
			}
		}
		for _, register := range latchedRegisters {
			registerEnd := uint64(register.Address) + uint64(register.Width)
			if uint64(mailbox.Address) < registerEnd && uint64(register.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, register.ID)
			}
		}
		for _, window := range latchedWindows {
			windowEnd := uint64(window.Address) + uint64(window.Size)
			if uint64(mailbox.Address) < windowEnd && uint64(window.Address) < mailboxEnd {
				return fmt.Errorf("board profile %q registers %q and %q overlap", p.ID, mailbox.ID, window.ID)
			}
		}
		if command := mailbox.HostCommand; command != nil {
			windowsByID := make(map[string]LatchedRegisterWindowProfile, len(latchedWindows))
			for _, window := range latchedWindows {
				windowsByID[window.ID] = window
			}
			selector, ok := windowsByID[command.SelectorWindowID]
			if !ok || command.SelectorWidth != selector.Width ||
				command.SelectorOffset%uint32(command.SelectorWidth) != 0 ||
				uint64(command.SelectorOffset)+uint64(command.SelectorWidth) > uint64(selector.Size) {
				return fmt.Errorf("board profile %q has invalid ADSP host-command selector", p.ID)
			}
			commands := make(map[uint32]struct{}, len(command.Rules))
			for _, rule := range command.Rules {
				if rule.Command == 0 ||
					command.SelectorWidth < Width32 &&
						rule.Command >= uint32(1)<<(uint32(command.SelectorWidth)*8) {
					return fmt.Errorf("board profile %q has invalid ADSP host command 0x%x", p.ID, rule.Command)
				}
				if _, duplicate := commands[rule.Command]; duplicate {
					return fmt.Errorf("board profile %q repeats ADSP host command 0x%x", p.ID, rule.Command)
				}
				commands[rule.Command] = struct{}{}
				for _, operation := range rule.Copies {
					source, sourceOK := windowsByID[operation.SourceWindowID]
					destination, destinationOK := windowsByID[operation.DestinationWindowID]
					if !sourceOK || !destinationOK || operation.Width != source.Width ||
						operation.Width != destination.Width ||
						operation.SourceOffset%uint32(operation.Width) != 0 ||
						operation.DestinationOffset%uint32(operation.Width) != 0 ||
						uint64(operation.SourceOffset)+uint64(operation.Width) > uint64(source.Size) ||
						uint64(operation.DestinationOffset)+uint64(operation.Width) > uint64(destination.Size) {
						return fmt.Errorf("board profile %q has invalid ADSP host-command memory copy", p.ID)
					}
				}
			}
		}
		windowsByID := make(map[string]LatchedRegisterWindowProfile, len(latchedWindows))
		for _, window := range latchedWindows {
			windowsByID[window.ID] = window
		}
		controlRules := make(map[[2]uint32]struct{}, len(mailbox.ControlRules))
		for _, rule := range mailbox.ControlRules {
			key := [2]uint32{rule.Offset, rule.Value}
			if rule.Offset%uint32(Width32) != 0 ||
				uint64(rule.Offset)+uint64(Width32) > uint64(mailbox.Size) {
				return fmt.Errorf("board profile %q has invalid ADSP control-rule offset", p.ID)
			}
			if _, duplicate := controlRules[key]; duplicate {
				return fmt.Errorf("board profile %q repeats ADSP control rule at 0x%x value 0x%x", p.ID, rule.Offset, rule.Value)
			}
			controlRules[key] = struct{}{}
			for _, operation := range rule.Writes {
				window, ok := windowsByID[operation.WindowID]
				if !ok || operation.Width != window.Width ||
					operation.Offset%uint32(operation.Width) != 0 ||
					uint64(operation.Offset)+uint64(operation.Width) > uint64(window.Size) ||
					operation.Width < Width32 && operation.Value >= uint32(1)<<(uint32(operation.Width)*8) {
					return fmt.Errorf("board profile %q has invalid ADSP control-rule memory write", p.ID)
				}
			}
			if interrupt := rule.Interrupt; interrupt != nil {
				if interrupt.UseVectoredController {
					if p.VectoredInterrupt == nil || interrupt.Source >= p.VectoredInterrupt.SourceCount {
						return fmt.Errorf(
							"board profile %q ADSP interrupt source %d exceeds vectored controller",
							p.ID,
							interrupt.Source,
						)
					}
				} else if interrupt.Source >= 64 {
					return fmt.Errorf(
						"board profile %q has invalid ADSP interrupt source %d",
						p.ID,
						interrupt.Source,
					)
				}
			}
		}
	}
	callIDs := make(map[string]struct{}, len(p.HLECalls))
	callTraps := make(map[cpu.ExecutionTrap]struct{}, len(p.HLECalls))
	for _, call := range p.HLECalls {
		if err := call.validate(); err != nil {
			return fmt.Errorf("board profile %q: %w", p.ID, err)
		}
		if _, duplicate := callIDs[call.ID]; duplicate {
			return fmt.Errorf("board profile %q repeats HLE call %q", p.ID, call.ID)
		}
		trap := cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode}
		if _, duplicate := callTraps[trap]; duplicate {
			return fmt.Errorf("board profile %q repeats HLE address 0x%08x", p.ID, call.Address)
		}
		callIDs[call.ID] = struct{}{}
		callTraps[trap] = struct{}{}
	}
	return nil
}

func (p BoardProfile) ApplyReadOnlyRegisters(bus *Bus) error {
	if bus == nil {
		return fmt.Errorf("apply board profile %q: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, spec := range p.ReadOnlyRegisters {
		register, err := NewReadOnlyRegister(spec.Width, spec.Value)
		if err != nil {
			return fmt.Errorf("apply board profile %q register %q: %w", p.ID, spec.ID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, uint32(spec.Width), register); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
	}
	return nil
}

func (p BoardProfile) ApplyLatchedRegisters(bus *Bus) error {
	return p.applyLatchedRegisters(bus, nil, nil)
}

// ApplyLatchedRegistersWithInterrupts wires register-window devices whose
// evidenced responses include interrupt pulses to the board's controllers.
// ApplyLatchedRegisters remains available for profiles and tests that only
// require passive register latches.
func (p BoardProfile) ApplyLatchedRegistersWithInterrupts(
	bus *Bus,
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
) error {
	return p.applyLatchedRegisters(bus, interruptController, vectoredInterruptController)
}

func (p BoardProfile) applyLatchedRegisters(
	bus *Bus,
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
) error {
	if bus == nil {
		return fmt.Errorf("apply board profile %q: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	windows := make(map[string]*LatchedRegisterWindow, len(p.LatchedRegisterWindows))
	for _, spec := range p.LatchedRegisterWindows {
		window, err := NewLatchedRegisterWindow(spec.Size, spec.Width)
		if err != nil {
			return fmt.Errorf("apply board profile %q register window %q: %w", p.ID, spec.ID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, spec.Size, window); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
		windows[spec.ID] = window
	}
	if spec := p.ADSPMailbox; spec != nil {
		mailbox, err := NewQualcommADSPMailbox(spec.Size, spec.WriteControlOffset)
		if err != nil {
			return fmt.Errorf("apply board profile %q ADSP mailbox %q: %w", p.ID, spec.ID, err)
		}
		if err := mailbox.configureHostCommand(spec.HostCommand, windows); err != nil {
			return fmt.Errorf("apply board profile %q ADSP mailbox %q: %w", p.ID, spec.ID, err)
		}
		if err := mailbox.configureControlRulesWithInterrupts(
			spec.ControlRules,
			windows,
			interruptController,
			vectoredInterruptController,
		); err != nil {
			return fmt.Errorf("apply board profile %q ADSP mailbox %q: %w", p.ID, spec.ID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, spec.Size, mailbox); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
	}
	for _, spec := range p.LatchedRegisters {
		register, err := NewLatchedRegister(spec.Width, spec.ResetValue)
		if err != nil {
			return fmt.Errorf("apply board profile %q register %q: %w", p.ID, spec.ID, err)
		}
		if err := bus.MapMMIO(spec.ID, spec.Address, uint32(spec.Width), register); err != nil {
			return fmt.Errorf("apply board profile %q: %w", p.ID, err)
		}
	}
	return nil
}

func (p BoardProfile) ApplyMemory(bus *Bus) error {
	if bus == nil {
		return fmt.Errorf("apply board profile %q: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	for _, region := range p.Memory {
		switch region.Kind {
		case MemoryRAM:
			if err := bus.MapRAM(region.ID, region.Address, region.Size); err != nil {
				return fmt.Errorf("apply board profile %q: %w", p.ID, err)
			}
		case MemorySparseRAM:
			if err := bus.MapSparseRAM(region.ID, region.Address, region.Size); err != nil {
				return fmt.Errorf("apply board profile %q: %w", p.ID, err)
			}
		}
	}
	return nil
}

// AttachSamsungMGP maps the profiled control aperture after its shared RAM is
// present. Profiles without this companion processor require no placeholder.
func (p BoardProfile) AttachSamsungMGP(bus *Bus) (*SamsungMGPControl, error) {
	if bus == nil {
		return nil, fmt.Errorf("attach board profile %q Samsung MGP: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.SamsungMGP == nil {
		return nil, nil
	}
	var shared MemoryRegionProfile
	for _, region := range p.Memory {
		if region.ID == p.SamsungMGP.SharedMemoryID {
			shared = region
			break
		}
	}
	readyAddress := shared.Address + p.SamsungMGP.ReadyOffset
	device, err := NewSamsungMGPControl(bus, SamsungMGPControlConfig{
		Size:                      p.SamsungMGP.Size,
		ReleaseOffset:             p.SamsungMGP.ReleaseOffset,
		ReadyAddress:              readyAddress,
		ReadyValue:                p.SamsungMGP.ReadyValue,
		ResponseDelayInstructions: p.SamsungMGP.ResponseDelayInstructions,
	})
	if err != nil {
		return nil, fmt.Errorf("attach board profile %q Samsung MGP: %w", p.ID, err)
	}
	if err := bus.MapMMIO(
		p.SamsungMGP.ID,
		p.SamsungMGP.Address,
		p.SamsungMGP.Size,
		device,
	); err != nil {
		return nil, fmt.Errorf("attach board profile %q Samsung MGP: %w", p.ID, err)
	}
	return device, nil
}

// AttachMDP wires the board-selected script engine to its boot-control
// completion event. Profiles without an MDP remain valid and require no
// display engine, which keeps board construction firmware-family driven.
func (p BoardProfile) AttachMDP(
	bus *Bus,
	panel *DCSPanelController,
	bootControl *QualcommBootControl,
) (*QualcommMDPScriptEngine, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.MDP == nil {
		return nil, nil
	}
	if bootControl == nil {
		return nil, fmt.Errorf("attach board profile %q MDP: nil boot control", p.ID)
	}
	engine, err := NewQualcommMDPScriptEngine(bus, panel, *p.MDP)
	if err != nil {
		return nil, fmt.Errorf("attach board profile %q MDP: %w", p.ID, err)
	}
	if err := bootControl.AttachCompletionHandler(p.MDP.CompletionStartOffset, engine); err != nil {
		return nil, fmt.Errorf("attach board profile %q MDP completion: %w", p.ID, err)
	}
	return engine, nil
}

// AttachKeypad creates the board-selected matrix and wires guest GPIO output
// writes to the primary-clock input bank sampled by firmware. Profiles without
// a keypad remain valid so non-phone boards need no placeholder device.
func (p BoardProfile) AttachKeypad(
	primaryClock *QualcommPrimaryClockControl,
	secondaryClock *QualcommSecondaryClockControl,
	interruptController *QualcommInterruptController,
) (*QualcommGPIOKeypad, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Keypad == nil {
		return nil, nil
	}
	if primaryClock == nil {
		return nil, fmt.Errorf("attach board profile %q keypad: nil GPIO device", p.ID)
	}
	usesInterrupt, usesSecondaryClock := false, false
	for _, row := range p.Keypad.Rows {
		switch row.OutputBank {
		case QualcommGPIOOutputInterrupt:
			usesInterrupt = true
		case QualcommGPIOOutputSecondaryClock:
			usesSecondaryClock = true
		}
	}
	if usesInterrupt && interruptController == nil || usesSecondaryClock && secondaryClock == nil {
		return nil, fmt.Errorf("attach board profile %q keypad: nil GPIO output device", p.ID)
	}
	if primaryClock.keypad != nil ||
		usesInterrupt && interruptController.gpioWriteObserver != nil ||
		usesSecondaryClock && secondaryClock.gpioWriteObserver != nil {
		return nil, fmt.Errorf("attach board profile %q keypad: GPIO device already attached", p.ID)
	}
	keypad, err := NewQualcommGPIOKeypad(*p.Keypad)
	if err != nil {
		return nil, fmt.Errorf("attach board profile %q keypad: %w", p.ID, err)
	}
	if err := primaryClock.AttachGPIOKeypad(keypad); err != nil {
		return nil, fmt.Errorf("attach board profile %q keypad inputs: %w", p.ID, err)
	}
	if usesInterrupt {
		observer := qualcommGPIOKeypadBankObserver{keypad: keypad, bank: QualcommGPIOOutputInterrupt}
		if err := interruptController.AttachGPIOWriteObserver(observer); err != nil {
			return nil, fmt.Errorf("attach board profile %q interrupt keypad outputs: %w", p.ID, err)
		}
	}
	if usesSecondaryClock {
		observer := qualcommGPIOKeypadBankObserver{keypad: keypad, bank: QualcommGPIOOutputSecondaryClock}
		if err := secondaryClock.AttachGPIOWriteObserver(observer); err != nil {
			return nil, fmt.Errorf("attach board profile %q secondary-clock keypad outputs: %w", p.ID, err)
		}
	}
	return keypad, nil
}

func SCHW830DL21BoardProfile() BoardProfile {
	return BoardProfile{
		ID:              "samsung.sch-w830",
		PlatformID:      "qualcomm.arm9-sch-family",
		FirmwareBuildID: "samsung.sch-w830.dl21",
		// SCH-W830's OEMSBL and AMSS XSR tables both support EC/BA for this
		// 256 MiB, 2 KiB-page device. Do not copy the EC/AA ID from the
		// reference SPH-W8300 runtime dump: that carrier variant has a
		// different AMSS NAND table. This controller generation exposes the
		// maker in READ_ID[15:8] and the device in READ_ID[7:0].
		NANDReadID: 0x0000ecba,
		NANDSize:   0x10000000,
		// The downloader set has no physical OOB stream. Its WBT normalizer
		// promotes the newest MIBIB generation into QCSBL's second usable boot
		// slot; inventing a factory-bad block here would incorrectly displace
		// every logical AMSS read by one erase block.
		NANDFactoryBadBlocks: nil,
		// Samsung's downloader leaves this little-endian 0xBEAFFEFF completion
		// marker at the first byte after the packaged NAND layout. DL21 consumes
		// it to select its one-shot native BML/STL/TFS4 factory provisioning path.
		NANDInitialData: []FlashSeed{{
			Offset: 0x097c0000,
			Data:   []byte{0xff, 0xfe, 0xaf, 0xbe},
		}},
		BootClockModeStatus: 0x00000001,
		// The multiplexed keypad inputs and the boot power-key/release input
		// are pulled high while idle. Firmware waits for bit 4 before leaving
		// its late hardware-initialization loop.
		PrimaryClockStatus:    0x0000001f,
		PrimaryClockInputMask: 0x0000001f,
		PrimaryClockKeys: []QualcommPrimaryClockKeyProfile{{
			// A short active-low pulse on the boot power-key input performs the
			// handset's red END action and returns the native UI to idle.
			ID: "end", InputLine: 4, ActiveLow: true,
		}},
		BootControlWritableOffsets: []uint32{
			0x0008,
			0x00bc, 0x00c0,
			// The runtime cache-maintenance helper pulses the adjacent memory-
			// controller command strobe high and then low. No completion value is
			// read from this latch; retaining it is sufficient for the profiled
			// MSM6550 control aperture.
			0x0204,
			0x058c, 0x0590, 0x059c,
			0x05a0, 0x05a4, 0x05b0, 0x05b4, 0x05b8,
			0x05c4, 0x05c8, 0x05cc, 0x05d8,
			0x0a34,
			0x0a54, 0x0a58,
			0x0b34,
			0x0c00, 0x0c04, 0x0c08, 0x0c0c, 0x0c2c, 0x0c38, 0x0c3c, 0x0c40,
			0x0e00, 0x0e04, 0x0e08, 0x0e10, 0x0e1c, 0x0e20, 0x0e38, 0x0e3c, 0x0e40,
			0x200c,
			0x2840,
			0x4100, 0x4104, 0x4108,
			// DL21's undocumented MMCC-side command path emits a command
			// sequence through +0x4110 and writes its word payload at +0x4120.
			0x4110, 0x4114, 0x4118, 0x411c, 0x4120,
			0x4128, 0x412c, 0x4130, 0x4134, 0x4138, 0x413c,
			0x423c,
			0x4600, 0x4604, 0x4614,
			0x533c,
		},
		BootControlSBIControllers: []uint32{0x5000, 0x5100, 0x5200},
		// DL21's battery monitor reads PMIC registers 0x54, 0x53, and 0x4f.
		// Bit 0 at 0x54 completes the conversion, 0xc1 at 0x4f preserves the
		// programmed 8-bit mode, and the maximum sample at 0x53 keeps the
		// handset's battery indicator at full charge.
		BootControlSBIReadResponses: []QualcommSBIReadResponse{
			{Controller: 0x5100, Address: 0x4f, Value: 0xc1},
			{Controller: 0x5100, Address: 0x53, Value: 0xff},
			{Controller: 0x5100, Address: 0x54, Value: 0x01},
		},
		BootControlSBICompletionStatus: 0x0494,
		BootControlHalfwordOffsets: []uint32{
			0x0e0c, 0x0e28,
			0x4000, 0x4004, 0x4008,
			0x4010, 0x4014, 0x4018, 0x401c,
			0x4020, 0x4024, 0x4028, 0x402c,
			0x4030, 0x4034, 0x4038,
			0x4200, 0x4204, 0x4208,
			0x4210, 0x4214, 0x4218, 0x421c,
			0x4220, 0x4224, 0x4228, 0x422c,
			0x4230, 0x4234, 0x4238,
		},
		BootControlMixedWidthOffsets: []uint32{0x0e20},
		// MSM6550 legacy UART1/2. Their write-side CSR/CR/IMR aliases are
		// configured as halfwords above; runtime status and FIFO traffic use
		// the read-side SR/MISR/ISR and byte-wide TF/RF aliases.
		BootControlLegacyUARTControllers: []uint32{0x4000, 0x4200},
		// MSM6xxx receive-front chains are separated by 0x200 bytes. The
		// original DL21 radio initializer reads, masks, and rewrites the second
		// chain's reset and control words at CHIP_BASE+0x0c00/+0x04; both chain
		// reset registers power on asserted while the control latch starts clear.
		BootControlRegisterResets: []QualcommBootRegisterReset{
			{Offset: 0x0204, Value: 0},
			{Offset: 0x0a00, Value: 1},
			{Offset: 0x0c00, Value: 1},
		},
		// A write to RXFRONT1+0x38 is followed by polling +0x34 with mask
		// 0x008007ff. Zero is the observed idle/completed state.
		BootControlReadOnlyRegisters: []QualcommBootReadOnlyRegister{
			// The runtime polls bits 3 and 5 while evaluating external
			// wake/input state. Neither is asserted in the deterministic
			// offline board state.
			{Offset: 0x048c, Value: 0},
			{Offset: 0x0c34, Value: 0},
			// The late e00-block consumer samples the low 12 bits of the
			// response/status word before deciding whether a wrapped interval
			// contains its request. No external response is pending offline.
			{Offset: 0x0e14, Value: 0},
		},
		BootControlCompletionEvents: []QualcommCompletionEventConfig{{
			StartOffset:           0x0e04,
			StartMask:             0x00000001,
			StatusOffset:          0x0e24,
			StatusMask:            0x00000002,
			AcknowledgeOffset:     0x0e28,
			AcknowledgeWidth:      Width16,
			AcknowledgeMask:       0x0000ffff,
			InterruptSource:       13,
			UseVectoredController: true,
		}},
		PrimaryClockWritableOffsets: []uint32{
			0x0594, 0x0598, 0x05a8, 0x05ac,
			0x05bc, 0x05c0, 0x05d0, 0x05d4,
		},
		SecondaryClockWritableOffsets: []uint32{0x040c},
		SparseBusRegisterOffsets:      samsungSCHSparseBusRegisterOffsets(),
		ClockRegimeSleepControllers:   []uint32{0x5200, 0x5244},
		ClockRegimeCounters: []QualcommClockRegimeCounterConfig{{
			// DL21 samples the low 18 bits at CHIP_BASE+0x6000 when
			// measuring short radio/clock intervals. MSM6xxx documentation
			// identifies this aperture as the CDMA chip-x8 RTC domain.
			Offset:                0x6000,
			InstructionsPerSecond: 60_000_000,
			CounterHz:             9_830_400,
			Bits:                  18,
		}},
		ClockRegimeComparators: []QualcommClockRegimeComparatorConfig{{
			// STMR timer 1 exposes a 0..149 phase in bits 15:8 of the shared
			// counter. Eight independent events compare that phase through the
			// +0x48c4 table and raise the firmware's source-46 ISR. The 150 Hz
			// phase is the chip-x8 RTC divided by 65,536.
			CounterOffset:         0x480c,
			CounterMask:           0x0000ff00,
			InstructionsPerSecond: 60_000_000,
			CounterHz:             150,
			CounterModulus:        150,
			MatchBaseOffset:       0x48c4,
			MatchStride:           4,
			MatchMask:             0x0000ff00,
			EnableOffset:          0x487c,
			StatusOffset:          0x4864,
			AcknowledgeOffset:     0x4870,
			EventMask:             0x000000ff,
			InterruptSource:       46,
			UseVectoredController: true,
		}},
		VectoredInterrupt: &QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25,
			ReverseSourceOrder: true,
		},
		TimeTickClock: &QualcommTimeTickClockConfig{
			// Match deltas of 326/327 ticks implement the firmware's 10 ms
			// scheduler quantum. Sixty million retired instructions per second
			// matches the existing KTF handset execution budget at 60 Hz.
			InstructionsPerSecond: 60_000_000,
			TimeTickHz:            32_768,
			InterruptSource:       21,
			UseVectoredController: true,
		},
		Keypad: &QualcommGPIOKeypadProfile{
			Columns: []uint8{0, 1, 2, 3},
			Rows: []QualcommGPIOKeypadRowProfile{
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000400},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000800},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00001000},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00002000},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00004000},
				{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000040},
				{OutputOffset: 0x10, OutputMask: 0x00200000},
			},
			Keys: []QualcommGPIOKeyProfile{
				// Native idle-screen probes identify the left/menu and right/memo
				// soft buttons. This also agrees with the handset manual's B(left)
				// and B(right) behavior.
				{ID: "soft-left", Row: 0, Column: 0},
				{ID: "soft-right", Row: 1, Column: 0},
				// Native DL21 screen probes identify the four ring directions on
				// row 4: from idle they open the four documented shortcuts, while
				// within a list they move the selection or change the selected
				// value. The firmware scans this row's columns in ascending order,
				// so up/down/left/right occupy columns 0..3; the earlier reversed
				// assignment rotated on-screen navigation relative to the pressed
				// direction (pressing right moved up, and so on).
				{ID: "up", Row: 4, Column: 0},
				{ID: "down", Row: 4, Column: 1},
				{ID: "left", Row: 4, Column: 2},
				{ID: "right", Row: 4, Column: 3},
				// NATE/OK enters the highlighted menu item. The C/back key returns
				// from a nested settings list to the grid, and from the grid home.
				{ID: "ok", Row: 5, Column: 0},
				{ID: "back", Row: 3, Column: 0},
				// Dialing a number and pressing this coordinate enters the native
				// call screen.
				{ID: "send", Row: 2, Column: 0},
				// DL21's raw codes 0x54 and 0x55 and its native sound overlay
				// identify the handset's two side volume buttons.
				{ID: "volume-up", Row: 6, Column: 0},
				{ID: "volume-down", Row: 6, Column: 1},
				{ID: "digit-1", Row: 3, Column: 1},
				{ID: "digit-2", Row: 3, Column: 2},
				{ID: "digit-3", Row: 3, Column: 3},
				{ID: "digit-4", Row: 2, Column: 1},
				{ID: "digit-5", Row: 2, Column: 2},
				{ID: "digit-6", Row: 2, Column: 3},
				{ID: "digit-7", Row: 1, Column: 1},
				{ID: "digit-8", Row: 1, Column: 2},
				{ID: "digit-9", Row: 1, Column: 3},
				{ID: "star", Row: 0, Column: 1},
				{ID: "digit-0", Row: 0, Column: 2},
				{ID: "pound", Row: 0, Column: 3},
			},
		},
		Panel: DCSPanelConfig{
			Width: 240, Height: 320, NativeAddressMode: 0x48,
		},
		MDP: &QualcommMDPProfile{
			CompletionStartOffset: 0x0e04,
			ScriptPointerOffset:   0x0e08,
			RGB565SourceFormat:    0x20,
		},
		LegacyTopVersion:        0x00000000,
		LegacyTopIdentification: 0x00000000,
		Memory: []MemoryRegionProfile{
			{
				ID:      "ebi-ram",
				Kind:    MemoryRAM,
				Address: 0x00000000,
				Size:    0x08000000,
			},
			{
				// MSM6550 exposes the application DSP address space as the
				// complete 0x70000000-0x77ffffff ARM window. The firmware
				// populates it incrementally, so page-backed storage avoids a
				// 128 MiB allocation plus an equally large reset copy.
				ID:      "adsp-address-space",
				Kind:    MemorySparseRAM,
				Address: 0x70000000,
				Size:    0x08000000,
			},
			{
				ID:      "pbl-iram",
				Kind:    MemoryRAM,
				Address: 0x78000000,
				Size:    0x00010000,
			},
			{
				ID:      "high-vector-iram",
				Kind:    MemoryRAM,
				Address: 0xffff0000,
				Size:    0x0000f000,
			},
		},
		ReadOnlyRegisters: []ReadOnlyRegisterProfile{
			{
				ID:      "external-platform-status",
				Address: 0x30010004,
				Width:   Width16,
				Value:   0x0000,
			},
			{
				ID:      "external-platform-selector",
				Address: 0x30030000,
				Width:   Width16,
				Value:   0x0300,
			},
		},
		LatchedRegisterWindows: []LatchedRegisterWindowProfile{
			// Late subsystem initialization copies tables into independently
			// decoded Qualcomm bus banks. Their side effects are not yet modeled;
			// retain only the observed apertures and access widths instead of
			// treating the surrounding address space as RAM.
			{ID: "external-16bit-bank-0", Address: 0x91000000, Size: 0x00010000, Width: Width16},
			{ID: "external-16bit-bank-1", Address: 0x91200000, Size: 0x00010000, Width: Width16},
			{ID: "external-32bit-bank-2", Address: 0x91400000, Size: 0x00010000, Width: Width32},
			{ID: "external-32bit-bank-4", Address: 0x91800000, Size: 0x00014000, Width: Width32},
		},
		ADSPMailbox: &QualcommADSPMailboxProfile{
			ID:                 "external-32bit-control",
			Address:            0x91c00000,
			Size:               0x00000100,
			WriteControlOffset: 0x00000008,
			ControlRules: []QualcommADSPControlRuleProfile{
				{
					// The ARM command queue writes HOST_INT=1 after publishing a
					// command in the shared buffer. The QDSP image acknowledges it
					// through event slot 0 before raising the registered host IRQ.
					Offset: 4, Value: 1, ResponseDelayInstructions: 1,
					Writes: []QualcommADSPMemoryWriteProfile{
						{
							// The QDSP command queue reads the first halfword back as
							// its completion status. Zero acknowledges success; leaving
							// the submitted command type here makes the host retry it.
							WindowID: "external-16bit-bank-1", Offset: 0x00004d1e,
							Width: Width16, Value: 0,
						},
						{
							WindowID: "external-16bit-bank-1", Offset: 0x000051a4,
							Width: Width16, Value: 1,
						},
					},
					Interrupt: &QualcommADSPInterruptProfile{
						Source: 33, UseVectoredController: true,
					},
				},
				{
					Offset: 0, Value: 2,
					Writes: []QualcommADSPMemoryWriteProfile{{
						WindowID: "external-16bit-bank-1", Offset: 0x00000b6c,
						Width: Width16, Value: 1,
					}},
				},
				{
					Offset: 0, Value: 3,
					Writes: []QualcommADSPMemoryWriteProfile{{
						WindowID: "external-16bit-bank-1", Offset: 0x00000b6c,
						Width: Width16, Value: 0,
					}},
				},
			},
			HostCommand: &QualcommADSPHostCommandProfile{
				SelectorWindowID: "external-16bit-bank-1",
				SelectorOffset:   0x00000bc8,
				SelectorWidth:    Width16,
				Rules: []QualcommADSPHostCommandRuleProfile{
					{
						Command: 1,
						Copies: []QualcommADSPMemoryCopyProfile{{
							SourceWindowID:      "external-32bit-bank-2",
							SourceOffset:        0x00000570,
							DestinationWindowID: "external-32bit-bank-2",
							DestinationOffset:   0x0000056c,
							Width:               Width32,
						}},
					},
					// Command 4 releases the temporary DSP lock after a module-set
					// transition. It has no payload; clearing the selector is the
					// complete DSP-side acknowledgement.
					{Command: 4},
				},
			},
		},
	}
}

// SCHW770DA05BoardProfile starts the earlier version-one MIBIB handset from
// its own board identity. DA05 has a 512 MiB EC/DC raw NAND containing the
// downloader image and a separate 384 MiB EC/5C OneNAND data device.
func SCHW770DA05BoardProfile() BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = "samsung.sch-w770"
	profile.FirmwareBuildID = "samsung.sch-w770.da05"
	profile.Memory = append([]MemoryRegionProfile(nil), profile.Memory...)
	for index := range profile.Memory {
		if profile.Memory[index].ID == "ebi-ram" {
			// DA05 places its late OEMSBL work area at 0x0bfff000 and
			// explicitly clears words immediately below 0x0c000000.
			profile.Memory[index].Size = 0x0c000000
			break
		}
	}
	profile.NANDSize = 0x20000000
	profile.NANDReadID = 0x0000ecdc
	// DA05 derives this location from the MIBIB packaged end (0x11200000)
	// and checks the downloader's little-endian 0xBEAFFEFF completion marker
	// before choosing its one-shot native BML/STL/TFS4 provisioning path. The
	// following words are the preload-table entry count and version. The
	// four-piece archive omits this downloader-generated footer, so model a
	// completed download with an empty preload table without inventing payload
	// records that are not present in the archive.
	profile.NANDInitialData = []FlashSeed{{
		Offset: 0x11200000,
		Data:   []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0},
	}}
	profile.OneNAND = &OneNANDProfile{
		Address: 0x40000000, ManufacturerID: 0x00ec, DeviceID: 0x005c,
		DieBlockOffset: 0x0800, Capacity: 0x18000000,
	}
	// DA05 predates the service-ID table used by later QCSBLs. Its ROM PBL
	// publishes equivalent NAND geometry through boot_feature_cfg at this
	// fixed high-IRAM structure address.
	profile.PBLLegacyFeatureDataAddress = 0xffff6044
	// The boot power-key input line that publishes W830's red END action is not
	// evidenced on DA05, whose scanner samples input bits 0..2 only. Drop the
	// inherited host control rather than exporting an unproven W770 key.
	profile.PrimaryClockKeys = nil
	// DA05's hsdevice_W770 key scanner selects three GPIO rows through bits
	// 10..12 of GPIO_OE_1 and samples input bits 0..2. The HOLD switch is the
	// otherwise-unmapped matrix position stored at scan-buffer index 6
	// (column 2, row 0); firmware handles that position separately to emit its
	// short- and long-hold events. Keep the other coordinates unnamed until
	// their handset-facing meanings are established independently.
	profile.Keypad = &QualcommGPIOKeypadProfile{
		Columns: []uint8{0, 1, 2},
		Rows: []QualcommGPIOKeypadRowProfile{
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000400},
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000800},
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00001000},
		},
		Keys: []QualcommGPIOKeyProfile{{ID: "hold", Row: 0, Column: 2}},
		// GPIOs 46, 62, and 63 are the three matrix columns. The shared GPIO
		// dispatcher services hardware groups 2 and 3 from VIC source 5, reads
		// their pending words at +0x05e4/+0x05e8, and acknowledges each bit via
		// +0x0594/+0x0598 after invoking the registered key callback.
		InterruptGroups: []QualcommGPIOInterruptGroupProfile{
			{
				ClearOffset: 0x0594, EnableOffset: 0x05a8,
				DetectOffset: 0x05bc, PolarityOffset: 0x05d0, StatusOffset: 0x05e4,
				InterruptSource: 5, UseVectoredController: true,
			},
			{
				ClearOffset: 0x0598, EnableOffset: 0x05ac,
				DetectOffset: 0x05c0, PolarityOffset: 0x05d4, StatusOffset: 0x05e8,
				InterruptSource: 5, UseVectoredController: true,
			},
		},
		ColumnInterrupts: []QualcommGPIOKeypadColumnInterruptProfile{
			{Column: 0, Group: 1, Mask: 1 << 7}, // GPIO 62
			{Column: 1, Group: 1, Mask: 1 << 8}, // GPIO 63
			{Column: 2, Group: 0, Mask: 1 << 7}, // GPIO 46 / HOLD column
		},
	}
	// DA05 selects the legacy high-page path and exposes paired getter/setter
	// routines for the boot scratch words at 0xffffff04 and 0xffffff08.
	// Both words reset clear and retain values written during the handoff.
	profile.LegacyTopWritableOffsets = []uint32{
		qualcommLegacyTopIDOffset,
		qualcommLegacyTopIDOffset + 4,
	}
	// DA05's portrait boot surface is scanned from the opposite mounted edge
	// to W830's 240x320 panel. Page-reverse plus BGR maps its native 0x88 mode
	// to the upright framebuffer orientation exposed to frontends.
	profile.Panel = DCSPanelConfig{Width: 240, Height: 400, NativeAddressMode: 0x88}
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20020000,
	}
	// OEMSBL streams a fixed 16-bit register table through the sparse
	// 0x30000000/0x30020000 command/data pair before it initializes the primary
	// panel. No status read or framebuffer transfer has been observed on this
	// auxiliary path, so retain only the two evidenced halfword latches.
	// DA05 also drives a byte-wide external command/data port at offsets 0 and 2.
	// Its low-level helper writes command values to the first byte and streams
	// payload bytes through the second; retaining those two latches is enough
	// to preserve the guest-visible bus contract while the attached peripheral
	// remains offline.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			ID:      "auxiliary-16bit-panel-command",
			Address: 0x30000000,
			Size:    uint32(Width16),
			Width:   Width16,
		},
		LatchedRegisterWindowProfile{
			ID:      "auxiliary-16bit-panel-data",
			Address: 0x30020000,
			Size:    uint32(Width16),
			Width:   Width16,
		},
		LatchedRegisterWindowProfile{
			ID:      "external-8bit-command-data",
			Address: 0x38000000,
			Size:    4,
			Width:   Width8,
		},
	)
	// DA05's GPIO helper resolves group 4 input through CHIP_BASE+0x0940.
	// That word is reserved in the older INTCTL layout inherited by W830, so
	// expose the W770 low-idle input only through this exact board contract.
	profile.BootControlGPIOInputs = []QualcommGPIOInputRegister{{Offset: 0x40, Value: 0}}
	// DA05 samples bit 10 of GPIO_IN_1 at this address when publishing the
	// initial slider state. No external transition is asserted at reset.
	profile.SecondaryClockReadOnlyRegisters = []QualcommSecondaryClockReadOnlyRegister{{
		Offset: 0x0444,
		Value:  0,
	}}
	// DA05 restores its saved chip configuration through +0x00a0 immediately
	// before the common +0x00a4 latch. AMSS samples and then replaces the clock
	// plan word at +0x010c, programs +0x0120 before the common +0x0124 control,
	// updates the paired clock-source controls at +0x0130/+0x0134, and writes
	// companion plan words at +0x0148/+0x0248. The clock-vote transition copies
	// its calibration pair into the common +0x0080/+0x0084 bank. AMSS peripheral
	// setup writes the +0x0a44 command word; its polling code treats bit 1 at
	// +0x0a4c as successful synchronous completion and reads a zero result from
	// +0x0a50. The QCSBL stack-exit path clears the adjacent watchdog/control
	// latches below.
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x0a4c, Value: 0x00000002},
		QualcommBootReadOnlyRegister{Offset: 0x0a50, Value: 0x00000000},
		// A periodic AMSS worker debounces bit 23 of this raw input word. The
		// deterministic board starts with that external signal deasserted.
		QualcommBootReadOnlyRegister{Offset: 0x05ec, Value: 0x00000000},
	)
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		0x0080,
		0x00a0,
		0x010c,
		0x0120,
		0x0130,
		0x0134,
		0x0148,
		0x0248,
		0x0a44,
		0x53a8,
		0x53e0,
	)
	return profile
}

// SCHW320DC18BoardProfile describes the shared early raw-downloader platform
// with SCH-W320's exact firmware and NAND-layout identity.
func SCHW320DC18BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w320", "samsung.sch-w320.dc18", 0x0a760000,
	)
	// DC18's OEMSBL samples bit 1 of the primary input word after its board
	// setup and takes a dedicated boot path only while that active-low line is
	// asserted. Expose the evidenced maintenance/download input without
	// assigning the unrelated END-key contract used by adjacent handsets.
	profile.PrimaryClockKeys = []QualcommPrimaryClockKeyProfile{{
		ID: "download", InputLine: 1, ActiveLow: true,
	}}
	// DC18's AMSS clears runtime arenas at 0x09800000 and 0x0a000000 through
	// its ordinary word-copy loop. Its identity-mapped MMU table covers the
	// complete second EBI RAM bank; leave adjacent builds on their evidenced
	// 128 MiB map and expose the additional 64 MiB only for SCH-W320.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "w320-ebi1-ram", Kind: MemorySparseRAM,
		Address: 0x08000000, Size: 0x04000000,
	})
	// DC18 programs the second UART controller with 32-bit STR operations,
	// while earlier boot stages retain their narrower accesses.
	promoteQualcommLegacyUARTToMixedWidth(&profile, 0x4200)
	// DC18's late GPIO setup resolves its fourth input group through
	// CHIP_BASE+0x0940. The word is reserved in this INTCTL generation and no
	// external line is asserted on the deterministic board at reset.
	profile.BootControlGPIOInputs = append(
		profile.BootControlGPIOInputs,
		QualcommGPIOInputRegister{Offset: 0x40, Value: 0},
	)
	// Unlike the adjacent raw builds, DC18 rechecks a signed loader-state
	// record that the missing mask-ROM PBL normally leaves behind after QCSBL
	// authentication. The package registry has already selected the exact
	// QCSBL by SHA-256, so restore only that PBL ABI result at the original
	// helper boundary; no guest instruction or firmware byte is patched.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID: "w320-pbl-verified-loader-state", Contract: HLEContractQualcommPBLVerifiedLoaderState,
		Address: 0x0010214e, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// DC18's ARM veneer at 0x00e00fb0 calls 0x001138c8, inside the
	// progressive ELF's entirely zero-filled 0x00100000 program segment. The
	// handset's preceding boot environment supplies that resident callback;
	// its sole AMSS call site continues unconditionally and ignores the result.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w320-resident-boot-callback",
		Contract: HLEContractQualcommResidentBootCallback,
		Address:  0x001138c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	// DC18 uploads its ARM7/MGP image into the same 32 KiB companion-memory
	// aperture as W340. Its image header publishes the polled ready byte at
	// +0x29e0.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
		Address: 0x90108000, Size: 0x00008000,
	})
	profile.SamsungMGP = &SamsungMGPProfile{
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0x40,
		ReleaseOffset: 0x0c, SharedMemoryID: "samsung-mgp-code-ram",
		ReadyOffset: 0x29e0, ReadyValue: 1, ResponseDelayInstructions: 1,
	}
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			ID: "samsung-mgp-interface-registers", Address: 0x9011f140,
			Size: 0x10, Width: Width16,
		},
	)
	profile.Panel.Protocol = ParallelPanelProtocolIndexedRGB565Window454647
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20000080,
	}
	return profile
}

// SCHW340DC18BoardProfile describes the shared early raw-downloader platform
// with SCH-W340's exact firmware and NAND-layout identity.
func SCHW340DC18BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w340", "samsung.sch-w340.dc18", 0x08800000,
	)
	// W340 decodes the LCD D/C line at address bit 7: OEMSBL writes 16-bit
	// register indexes at chip-select +0 and their values at +0x80.
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20000080,
	}
	profile.Panel.Protocol = ParallelPanelProtocolIndexedRGB565Window454647
	// The MGP loader copies its ARM7 image into the 32 KiB code/shared-RAM
	// aperture and exchanges boot pointers in the image header.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
		Address: 0x90108000, Size: 0x00008000,
	})
	// DC18 asserts +0x0c while uploading the companion image, clears it to
	// release the ARM7, and waits for the image's shared ready byte at +0x29e0.
	profile.SamsungMGP = &SamsungMGPProfile{
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0x40,
		ReleaseOffset: 0x0c, SharedMemoryID: "samsung-mgp-code-ram",
		ReadyOffset: 0x29e0, ReadyValue: 1, ResponseDelayInstructions: 1,
	}
	// The host-side MGP service initialises a second halfword interface at
	// +0x00/+0x0c before it publishes its command descriptor. No autonomous
	// response has been observed in this bounded register subset.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			ID: "samsung-mgp-interface-registers", Address: 0x9011f140,
			Size: 0x10, Width: Width16,
		},
	)
	return profile
}

// SCHW350CK06BoardProfile describes the shared early raw-downloader platform
// with SCH-W350's exact firmware and NAND-layout identity.
func SCHW350CK06BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w350", "samsung.sch-w350.ck06", 0x08f80000,
	)
	// CK06's AMSS clears the second UART configuration words with 32-bit STR
	// operations. OEMSBL still shares the same controller with narrower
	// accesses, so expose the evidenced mixed-width aperture.
	promoteQualcommLegacyUARTToMixedWidth(&profile, 0x4200)
	// Late AMSS hardware setup writes its 0x00100203 configuration word to
	// CHIP_BASE +0x039c. The value is not polled as a completion signal, so a
	// board-specific latch is sufficient.
	profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, 0x039c)
	// CK06 masks the five raw primary inputs during startup and enters its
	// on-screen UCDMA download mode when the idle 0x1f value becomes 0x1b.
	// Expose that evidenced active-low boot input without assigning it the
	// unrelated END/power-key meaning used by adjacent handsets.
	profile.PrimaryClockKeys = []QualcommPrimaryClockKeyProfile{{
		ID: "download", InputLine: 2, ActiveLow: true,
	}}
	profile.NANDReportsErasedECCCodewords = false
	// The downloader deliberately omits the handset-specific 0x019a0000
	// manufacturing block. Its bootstrap state byte is nevertheless present
	// on a device and accepts only zero (unprovisioned) or one (provisioned).
	// Seed the privacy-safe unprovisioned state and let CK06 create the rest.
	// Claiming the provisioned value without the handset's complete signed
	// manufacturing record fails its bootstrap validation.
	profile.NANDInitialData = append(profile.NANDInitialData, FlashSeed{
		Offset: 0x019a0004,
		Data:   []byte{0},
	})
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-bootstrap-verified-firmware",
		Contract: HLEContractQualcommBootstrapVerifiedFirmware,
		Address:  0x00113d30, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
	})
	// CK06 imports one ARM callback from 0x001478c8, inside its zero-filled
	// BOOT/NOTUSED program segment. The handset's preceding boot environment
	// supplies that resident ABI; the downloader package intentionally does
	// not. Its sole call site consumes no return value, so preserve registers
	// and return through LR without patching guest bytes.
	profile.HLECalls = append(profile.HLECalls, HLECallProfile{
		ID:       "w350-resident-boot-callback",
		Contract: HLEContractQualcommResidentBootCallback,
		Address:  0x001478c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	})
	// CK06's OEMSBL samples bit 2 at secondary input +0x440 before it
	// releases the startup timer path. The adjacent W830 board instead wires
	// its evidenced idle input to bit 4, so keep this value board-specific.
	profile.SecondaryClockReadOnlyRegisters = append(
		profile.SecondaryClockReadOnlyRegisters,
		QualcommSecondaryClockReadOnlyRegister{
			Offset: qualcommSecondaryClockDisabledStatusOffset,
			Value:  0x00000004,
		},
	)
	// The same startup routine reads back the watchdog service latch after
	// writing one and treats bit 0 as completion.
	profile.BootControlWatchdogReadable = true
	// CK06's late GPIO helper resolves its fourth input group through
	// CHIP_BASE+0x0940. That word is reserved in the older INTCTL layout, just
	// as it is on DA05, and no external line is asserted on the deterministic
	// board at reset.
	profile.BootControlGPIOInputs = append(
		profile.BootControlGPIOInputs,
		QualcommGPIOInputRegister{Offset: 0x40, Value: 0},
	)
	// CK06 drives its 16-bit LCD command FIFO at external chip-select
	// 0x38000000 and streams RGB565 payload words through the adjacent +4
	// data aperture.
	profile.PanelPorts = &ParallelPanelPortProfile{
		CommandAddress: 0x38000000,
		DataAddress:    0x38000004,
	}
	profile.Panel.Protocol = ParallelPanelProtocolPackedRGB565Window424A
	// Late AMSS uses address bit 18 to select the value port of a separate
	// indexed external device. It writes controller values such as
	// register 0x21 = 0x2010, reads selected registers back, and polls the
	// command port's clear ready status. These are not coordinates or pixels
	// for the packed LCD FIFO above.
	profile.IndexedHalfwordRegisterPorts = []IndexedHalfwordRegisterPortProfile{{
		ID:             "w350-indexed-external-registers",
		CommandAddress: 0x20000000,
		DataAddress:    0x20040000,
	}}
	// The handset also initializes a small secondary display through a
	// write-only 16-bit command/data pair at +0/+4. It has no observed status
	// or framebuffer feedback into the boot chain, so retain only the bounded
	// output aperture instead of aliasing it to the primary 240x320 surface.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			ID: "w350-secondary-panel-output", Address: 0x40000000,
			Size: 6, Width: Width16,
		},
	)
	return profile
}

// SCHW410CL10BoardProfile describes the shared early raw-downloader platform
// with SCH-W410's exact firmware and NAND-layout identity.
func SCHW410CL10BoardProfile() BoardProfile {
	profile := samsungRawDownloadBoardProfile(
		"samsung.sch-w410", "samsung.sch-w410.cl10", 0x09100000,
	)
	profile.PrimaryClockKeys = []QualcommPrimaryClockKeyProfile{{
		ID: "end", InputLine: 4, ActiveLow: true,
	}}
	return profile
}

func samsungRawDownloadBoardProfile(id, firmwareBuildID string, packagedEnd uint64) BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = id
	profile.PlatformID = "qualcomm.arm9-sch-raw-v1"
	profile.FirmwareBuildID = firmwareBuildID
	// These MSM6280 handsets use Toshiba 98/CA NAND: a 2 KiB-page,
	// 64-page/block, 256 MiB device accepted by both the boot chain and AMSS's
	// NFA device table.
	profile.NANDReadID = 0x000098ca
	profile.NANDSize = 0x10000000
	// The MSM6280 ECC path completes an erased-codeword transfer with its
	// operation-error bit set. AMSS then sets NAND config bit 0 and rereads the
	// 528-byte raw codeword to distinguish erased data from an actual failure.
	profile.NANDReportsErasedECCCodewords = true
	profile.Panel = DCSPanelConfig{
		Width: 240, Height: 320, Protocol: ParallelPanelProtocolIndexedRGB565,
	}
	profile.NANDInitialData = []FlashSeed{{
		Offset: packagedEnd,
		Data:   []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0},
	}}
	profile.OneNAND = nil
	// The raw QCSBL generation predates the service-ID table used by the
	// wrapped W830 downloads and consumes the ROM feature structure directly.
	profile.PBLLegacyFeatureDataAddress = 0xffff6044
	profile.BootControlSBIReadResponses = nil
	// The shared raw QCSBL clears this watchdog/control latch before entering
	// its NAND geometry path.
	profile.BootControlWritableOffsets = append(
		profile.BootControlWritableOffsets,
		// The raw AMSS clock-vote dispatcher updates both halves of the
		// CHIP_BASE +0x08/+0x0c control pair. W830 already supplies +0x08;
		// the raw platform additionally exercises the adjacent word.
		0x000c,
		// AMSS samples and updates the active clock-plan word during late
		// hardware initialisation, then publishes the companion plans at +0x148
		// and +0x248. Its clock-vote transition copies the calibration pair into
		// +0x80 and the inherited +0x84 latch.
		0x0080,
		0x00a0,
		0x010c,
		0x0120,
		0x0130,
		0x0134,
		0x0148,
		0x0248,
		0x0a44,
		0x0d60,
		0x0d70,
		0x2000,
		0x2008,
		0x2010,
		0x2028,
		// AMSS initialises this peripheral control group as one bounded
		// sequence, including the adjacent +0x34/+0x38 control pair.
		0x2040,
		0x2044,
		0x2048,
		0x204c,
		0x2064,
		0x2068,
		0x206c,
		0x2074,
		0x2078,
		0x207c,
		0x2804,
		0x2808,
		0x2810,
		// The same initialiser clears the companion interrupt/status mask at
		// +0x284c before publishing the peripheral table below.
		0x284c,
		0x53a8,
		// PBL publishes its cold-boot stack transition through this scratch
		// latch before switching exception modes.
		0x5410,
	)
	// AMSS builds 32 four-word peripheral descriptors in the bounded
	// 0x2400..0x25ff hardware table.
	for offset := uint32(0x2400); offset < 0x2600; offset += 4 {
		profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, offset)
	}
	// The companion 16-entry status table has an eight-byte stride and clears
	// the second word of each entry.
	for offset := uint32(0x2984); offset < 0x2a00; offset += 8 {
		profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, offset)
	}
	// The retained PBL ROM samples bit 4 of this reset-status word before it
	// decides between cold and warm boot. A newly constructed handset is cold.
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		QualcommBootReadOnlyRegister{Offset: 0x5314, Value: 0},
		// PBL tests bit 0 before programming the adjacent cold-start clock
		// sequence. The uninitialised clock domain reports clear after reset.
		QualcommBootReadOnlyRegister{Offset: 0x5418, Value: 0},
		// AMSS treats a zero optional peripheral source pointer as absent before
		// initialising the adjacent 0x2400 control block.
		QualcommBootReadOnlyRegister{Offset: 0x2824, Value: 0},
		// The raw OEMSBL samples bit 0 of the +0x0d64 hardware-status word
		// before selecting its optional warm/peripheral startup path. No such
		// external state is asserted on a deterministic cold boot.
		QualcommBootReadOnlyRegister{Offset: 0x0d64, Value: 0},
		// The synchronous peripheral command issued through +0x0a44 completes
		// with bit 1 set in its status word.
		QualcommBootReadOnlyRegister{Offset: 0x0a4c, Value: 0x00000002},
	)
	profile.Keypad = nil
	profile.PrimaryClockKeys = nil
	profile.LegacyTopWritableOffsets = []uint32{
		qualcommLegacyTopIDOffset,
		qualcommLegacyTopIDOffset + 4,
	}
	// The W350 OEMSBL feature enumerator samples the two halfwords preceding
	// the inherited +4 platform-status register. No optional external feature
	// line is asserted in the deterministic offline board state.
	profile.ReadOnlyRegisters = append(
		profile.ReadOnlyRegisters,
		ReadOnlyRegisterProfile{
			ID: "external-platform-feature-low", Address: 0x30010000,
			Width: Width16, Value: 0,
		},
		ReadOnlyRegisterProfile{
			ID: "external-platform-feature-high", Address: 0x30010002,
			Width: Width16, Value: 0,
		},
	)
	// After OEMSBL creates the NAND bad-block table and restarts, QCSBL
	// programs the external-memory timing bank at +0x20..+0x74 before loading
	// AMSS. These are retained control latches, not general-purpose RAM.
	profile.LatchedRegisterWindows = append(
		profile.LatchedRegisterWindows,
		LatchedRegisterWindowProfile{
			// AMSS's external-bus helper publishes byte-wide command and data
			// values through offsets 0 and 2 of this chip-select aperture. Leaving
			// either address unmapped turns the valid write into a data abort.
			ID: "external-8bit-command-data", Address: 0x30000000,
			Size: 4, Width: Width8,
		},
		LatchedRegisterWindowProfile{
			ID: "qcsbl-external-memory-control", Address: 0xa0000000,
			Size: 0x00000078, Width: Width32,
		},
	)
	profile.SparseBusRegisterOffsets = append(profile.SparseBusRegisterOffsets, 0x0000, 0x0040)
	profile.SparseBusRegisterResets = []SparseWordRegisterReset{{
		Offset: 0x0040, Value: 0x80000000,
	}}
	// The raw handset samples bit 10 of this GPIO input word while publishing
	// its initial slider state. No external transition is asserted at reset.
	profile.SecondaryClockReadOnlyRegisters = append(
		profile.SecondaryClockReadOnlyRegisters,
		QualcommSecondaryClockReadOnlyRegister{Offset: 0x0444, Value: 0},
	)
	return profile
}

func promoteQualcommLegacyUARTToMixedWidth(profile *BoardProfile, base uint32) {
	wordOffsets := make(map[uint32]struct{}, len(qualcommLegacyUARTHalfwordRegisterOffsets))
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets[base+relative] = struct{}{}
	}
	halfwordOffsets := make([]uint32, 0, len(profile.BootControlHalfwordOffsets))
	for _, offset := range profile.BootControlHalfwordOffsets {
		if _, promote := wordOffsets[offset]; promote {
			profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, offset)
			profile.BootControlMixedWidthOffsets = append(profile.BootControlMixedWidthOffsets, offset)
			continue
		}
		halfwordOffsets = append(halfwordOffsets, offset)
	}
	profile.BootControlHalfwordOffsets = halfwordOffsets
}

func samsungSCHSparseBusRegisterOffsets() []uint32 {
	offsets := make([]uint32, 0, 128)
	for _, span := range [][2]uint32{{0x240, 0x27c}, {0x280, 0x29c}, {0x2c0, 0x2dc}} {
		for offset := span[0]; offset <= span[1]; offset += 4 {
			offsets = append(offsets, offset)
		}
	}
	offsets = append(offsets,
		0x3a0, 0x3a4, 0x3a8, 0x3ac, 0x3b0, 0x3b4, 0x3b8, 0x3bc,
		0x3c0, 0x3c4, 0x3c8, 0x3cc, 0x3d0,
		0x3e0, 0x3e4, 0x3e8, 0x3ec, 0x3f0,
	)
	for column := uint32(0); column <= 0x200; column += 0x40 {
		offsets = append(offsets, column+0x10)
	}
	for column := uint32(0x400); column <= 0x600; column += 0x40 {
		for _, lane := range []uint32{0, 4, 8, 0x0c, 0x14} {
			offsets = append(offsets, column+lane)
		}
	}
	for column := uint32(0xc00); column <= 0xe00; column += 0x40 {
		offsets = append(offsets, column+0x18, column+0x1c)
	}
	return offsets
}

// SCHW860DA06BoardProfile is the adjacent SCH-family board contract currently
// evidenced by DA06's original QCSBL/OEMSBL. It deliberately clears the W830
// keypad map: sharing a Qualcomm platform does not prove identical handset
// matrix wiring.
func SCHW860DA06BoardProfile() BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = "samsung.sch-w860"
	profile.FirmwareBuildID = "samsung.sch-w860.da06"
	// DA06's MIBIB layout ends at 0x0a1c0000, but OEMSBL selects the EC/BA
	// 256 MiB NAND geometry. Partition extent and physical device capacity
	// are separate contracts.
	profile.NANDSize = 0x10000000
	// DA06's storage helper subtracts 0x06400000 from its 0x0fbc0000 preload-
	// table address, so the table begins at physical NAND 0x097c0000. That is
	// the same packaged-end offset at which W830 expects a different downloader
	// completion marker; inheriting the W830 bytes makes DA06 interpret the
	// following erased word as a 0xffffffff entry count. The four-piece W860
	// archive has no previous-user-media backup, so its board baseline carries
	// an empty table header (zero entry count and version) instead.
	profile.NANDInitialData = []FlashSeed{{
		Offset: 0x097c0000,
		Data:   []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}}
	profile.BootControlSBIReadResponses = nil
	profile.LegacyTopWritableOffsets = []uint32{
		qualcommLegacyTopIDOffset,
		qualcommLegacyTopIDOffset + 4,
	}
	profile.Keypad = nil
	profile.PrimaryClockKeys = nil
	return profile
}

// SCHW850CF11BoardProfile starts the modem ARM9 of the MSM7600 dual-processor
// handset. The application processor remains outside this machine boundary;
// the profiled boot path is the OEMSBL maintenance/download environment.
func SCHW850CF11BoardProfile() BoardProfile {
	profile := SCHW830DL21BoardProfile()
	profile.ID = "samsung.sch-w850"
	profile.PlatformID = "qualcomm.msm7600-modem-arm9"
	profile.FirmwareBuildID = "samsung.sch-w850.cf11"
	profile.NANDSize = 0x20000000
	// The downloader package leaves the TFS4 control area erased. A newly
	// provisioned handset carries an LPCH control marker there so QCSBL can
	// derive the usable/reserved-block split before it loads OEMSBL. The marker
	// is generated metadata; no firmware or user filesystem bytes are embedded.
	profile.NANDInitialData = []FlashSeed{
		{
			Offset: 0x18e80000,
			Data:   newQualcommFlexOneNANDControlHeader("UPCH"),
		},
		{
			// A control header is active only when its following payload page is
			// programmed. Zero denotes the generated empty bad-block map.
			Offset: 0x18e81000,
			Data:   []byte{0x00},
		},
		{
			// Page 60's second 512-byte chunk is the FBA enumeration table. One
			// unflagged record scans 0x400 blocks starting at FBA 0. An erased
			// count retries past FBA 0x3ff, while an empty table suppresses the
			// partition index construction entirely.
			Offset: 0x18ebc20c,
			Data: []byte{
				0x01, 0x00, 0x00, 0x00, // record count
				0x00, 0x00, 0x00, 0x00, // record tag and flags
				0x00, 0x00, 0x00, 0x04, // start FBA and block count
			},
		},
		{
			// The terminal page of the control group is an all-zero sentinel.
			// QCSBL locates it through the programmed OOB marker and requires
			// the complete main page to compare equal to zero.
			Offset: 0x18ebf000,
			Data:   make([]byte, 0x1000),
		},
		{
			Offset: 0x18f00000,
			Data:   newQualcommFlexOneNANDControlHeader("LPCH"),
		},
		{
			Offset: 0x18f01000,
			Data:   []byte{0x00},
		},
		{
			Offset: 0x18f3c20c,
			Data: []byte{
				0x01, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x04,
			},
		},
		{
			Offset: 0x18f3f000,
			Data:   make([]byte, 0x1000),
		},
	}
	// MSM7600 enables an identity mapping for its terminal high-IRAM section,
	// while the inherited 0x78001000 MSM6xxx service-table address is absent
	// from QCSBL's first-level page table. Keep the table adjacent to, but
	// disjoint from, the PBL shared record in that mapped section.
	profile.PBLServiceTableAddress = 0xffffe100
	profile.PBLServiceTableHeaderSize = 0x30
	profile.PBLHeaderFeatureDataAddress = 0xffffe0a0
	profile.PBLHeaderFeatures = []QualcommPBLHeaderFeature{
		{Selector: qualcommPBLHeaderFlashBlockCount, Value: 0x0400},
		{Selector: qualcommPBLHeaderSLCBlockCount, Value: 0x0010},
		{Selector: qualcommPBLHeaderBadBlockLimit, Value: 0x0014},
	}
	// QCSBL preserves a 0x68-byte record supplied by the missing PBL. Its
	// entry stub receives r11 as the exclusive end of this bounded IRAM data.
	profile.PBLSharedDataAddress = 0xffffe000
	profile.PBLSharedDataSize = 0x68
	profile.OneNAND = nil
	profile.SFlashOneNAND = &QualcommSFlashOneNANDProfile{
		Address:        0xa0a00000,
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		TechnologyID:   1,
		Capacity:       0x20000000,
		FlexGeometry: &OneNANDFlexGeometry{
			PageSize: 0x1000, BlockCount: 0x400, SLCBoundary: 0x0f,
			SLCBlockSize: 0x40000, MLCBlockSize: 0x80000,
		},
		SpareInitialData: []FlashSeed{
			{
				// Raw FBA 0x325 page 0 (UPCH).
				Offset: 0x00c74000,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				// The programmed control payload on page 1 carries the same
				// active-page marker while preserving the erased bad-block word.
				Offset: 0x00c74080,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				// Page 63 pairs the programmed OOB marker with the all-zero main
				// sentinel seeded above.
				Offset: 0x00c75f80,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				// Raw FBA 0x326 page 0 (LPCH). The first word is the erased
				// bad-block marker; 0xa5a5 identifies an active control page.
				Offset: 0x00c78000,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				Offset: 0x00c78080,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
			{
				Offset: 0x00c79f80,
				Data:   []byte{0xff, 0xff, 0xa5, 0xa5},
			},
		},
	}
	profile.BootControlAddress = 0xb8000000
	profile.Keypad = nil
	profile.PrimaryClockKeys = nil
	profile.BootControlSBIReadResponses = nil
	// MSM7600 retains this clock/interrupt control word as ordinary writable
	// state during QCSBL startup.
	profile.BootControlWritableOffsets = append(profile.BootControlWritableOffsets, 0x0840)
	// At legacy INTCTL +0x04, MSM7600 exposes a readable control latch rather
	// than the MSM6xxx write-only INT_CLEAR_1 register. QCSBL performs an
	// explicit read/modify/write sequence, so override only this profiled word.
	profile.BootControlInterruptWindowWritableOffsets = append(
		profile.BootControlInterruptWindowWritableOffsets,
		0x0904,
	)
	profile.BootControlGPIOInputs = append(
		profile.BootControlGPIOInputs,
		// MSM7600 reserves the legacy polarity-3 word as a fuse/status input.
		// Reset-zero selects the unprovisioned modem configuration fallback.
		QualcommGPIOInputRegister{Offset: 0x40, Value: 0},
		// MSM7600 places an additional input/status word at legacy INTCTL
		// +0x44. QCSBL tests bit 17 before consulting IRQ status bank 1; no
		// external source is asserted during deterministic cold boot.
		QualcommGPIOInputRegister{Offset: 0x44, Value: 0},
	)
	profile.BootControlReadOnlyRegisters = append(
		profile.BootControlReadOnlyRegisters,
		// The OEMSBL control sequence polls this completion word after
		// programming +0x200/+0x204. Reset-zero reports an idle controller.
		QualcommBootReadOnlyRegister{Offset: 0x0218, Value: 0},
	)
	// This QCSBL keeps its fatal-record header at 0xfffef000 and copies the
	// final 32-byte scatter record to 0xfffeffe0. Both fall inside the final,
	// bounded 4 KiB page of the MSM7600 modem IRAM bank.
	profile.Memory = append(profile.Memory, MemoryRegionProfile{
		ID: "msm7600-qcsbl-iram-page", Kind: MemoryRAM,
		Address: 0xfffef000, Size: 0x00001000,
	})
	// Retain the two bounded MSM7600 clock banks discovered during QCSBL
	// initialization; unrelated addresses remain unowned.
	profile.LatchedRegisterWindows = append(profile.LatchedRegisterWindows, LatchedRegisterWindowProfile{
		// The QCSBL clock bootstrap writes the +0x0c/+0x10/+0x14 control
		// triplet and +0x84 selector, then updates the base and +0x210 mode
		// words in the same compact register bank.
		ID: "msm7600-clock-bootstrap", Address: 0xa8600000,
		Size: 0x00000214, Width: Width32,
	}, LatchedRegisterWindowProfile{
		// QCSBL's static clock table builds four runtime banks from these exact
		// 1 KiB apertures before enabling the modem clock domains.
		ID: "msm7600-modem-clock-bank-0", Address: 0xa9400000,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		ID: "msm7600-modem-clock-bank-1", Address: 0xa9500400,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		ID: "msm7600-modem-clock-bank-2", Address: 0xa9600800,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		ID: "msm7600-modem-clock-bank-3", Address: 0xa9700c00,
		Size: 0x00000400, Width: Width32,
	}, LatchedRegisterWindowProfile{
		// The clock-control path updates base/+0x04/+0x0c and publishes its
		// completion/mode words at +0x834/+0x838 in this bank.
		ID: "msm7600-clock-control", Address: 0xb0007000,
		Size: 0x0000083c, Width: Width32,
	}, LatchedRegisterWindowProfile{
		// The application-side branches of the same clock table build aligned
		// addresses from +0x000 through the observed +0xf08 words.
		ID: "msm7600-app-clock-bank", Address: 0xb0400000,
		Size: 0x00001000, Width: Width32,
	})
	profile.LatchedRegisters = append(profile.LatchedRegisters, LatchedRegisterProfile{
		ID: "msm7600-delay-progress", Address: 0xb8200000,
		Width: Width32,
	}, LatchedRegisterProfile{
		// Revision 1 selects the PBL service-table ABI carried in r7/r8. A
		// reset-zero value selects the older link-time feature pointer instead,
		// which this MSM7600 QCSBL does not receive from its mask ROM handoff.
		ID: "msm7600-hardware-revision", Address: 0xa9000270,
		Width: Width32, ResetValue: 0x10000000,
	}, LatchedRegisterProfile{
		// The QCSBL clock initialiser publishes selector 9 through this single
		// MSM7600 control word before it starts the remaining clock sequence.
		ID: "msm7600-clock-selector", Address: 0xa8500004,
		Width: Width32,
	})
	return profile
}

func newQualcommFlexOneNANDControlHeader(magic string) []byte {
	// This is the first-generation header emitted by the Qualcomm flash
	// manager for an empty control map: no previous control blocks, generation
	// one for the selected half and globally, and the detected 1x1x0x400
	// Flex-OneNAND geometry. The halfword following the reserved-block count is
	// left erased, matching a freshly programmed header page.
	header := []byte{
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0xff, 0xff,
		0x01, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x00, 0x04, 0x00, 0x00,
		0x00, 0x04, 0x00, 0x00,
	}
	copy(header, magic)
	return header
}

func validProfileID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 255 && strings.IndexByte(value, 0) < 0
}
