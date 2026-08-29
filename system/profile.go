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

type HLEReturn string

const (
	HLEReturnLinkRegister HLEReturn = "link-register"
)

type HLECallProfile struct {
	ID       string
	Contract string
	Address  uint32
	Mode     cpu.Mode
	Return   HLEReturn
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
	ID                               string
	PlatformID                       string
	FirmwareBuildID                  string
	NANDReadID                       uint32
	NANDSize                         uint64
	NANDFactoryBadBlocks             []uint32
	NANDInitialData                  []FlashSeed
	BootClockModeStatus              uint32
	PrimaryClockStatus               uint32
	PrimaryClockInputMask            uint32
	PrimaryClockKeys                 []QualcommPrimaryClockKeyProfile
	BootControlWritableOffsets       []uint32
	BootControlHalfwordOffsets       []uint32
	BootControlMixedWidthOffsets     []uint32
	BootControlReadOnlyRegisters     []QualcommBootReadOnlyRegister
	BootControlRegisterResets        []QualcommBootRegisterReset
	BootControlCompletionEvents      []QualcommCompletionEventConfig
	BootControlLegacyUARTControllers []uint32
	BootControlSBIControllers        []uint32
	BootControlSBIReadResponses      []QualcommSBIReadResponse
	BootControlSBICompletionStatus   uint32
	PrimaryClockWritableOffsets      []uint32
	SecondaryClockWritableOffsets    []uint32
	ClockRegimeSleepControllers      []uint32
	ClockRegimeCounters              []QualcommClockRegimeCounterConfig
	ClockRegimeComparators           []QualcommClockRegimeComparatorConfig
	VectoredInterrupt                *QualcommVectoredInterruptConfig
	TimeTickClock                    *QualcommTimeTickClockConfig
	Keypad                           *QualcommGPIOKeypadProfile
	Panel                            DCSPanelConfig
	MDP                              *QualcommMDPProfile
	LegacyTopVersion                 uint32
	LegacyTopIdentification          uint32
	LegacyTopWritableOffsets         []uint32
	Memory                           []MemoryRegionProfile
	ReadOnlyRegisters                []ReadOnlyRegisterProfile
	LatchedRegisters                 []LatchedRegisterProfile
	LatchedRegisterWindows           []LatchedRegisterWindowProfile
	ADSPMailbox                      *QualcommADSPMailboxProfile
	HLECalls                         []HLECallProfile
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
	if err := validateQualcommBootControlConfigurationOffsets(
		p.BootControlWritableOffsets,
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
	if err := validateQualcommPrimaryClockWritableOffsets(p.PrimaryClockWritableOffsets); err != nil {
		return fmt.Errorf("board profile %q primary-clock writable offsets: %w", p.ID, err)
	}
	if err := validateQualcommSecondaryClockWritableOffsets(p.SecondaryClockWritableOffsets); err != nil {
		return fmt.Errorf("board profile %q secondary-clock writable offsets: %w", p.ID, err)
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
	}
	if p.Panel.Width != 0 || p.Panel.Height != 0 {
		if _, err := validateDCSPanelConfig(p.Panel); err != nil {
			return fmt.Errorf("board profile %q panel: %w", p.ID, err)
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
	if mailbox := p.ADSPMailbox; mailbox != nil {
		if err := mailbox.validate(); err != nil {
			return fmt.Errorf("board profile %q: %w", p.ID, err)
		}
		mailboxEnd := uint64(mailbox.Address) + uint64(mailbox.Size)
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

func validProfileID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 255 && strings.IndexByte(value, 0) < 0
}
