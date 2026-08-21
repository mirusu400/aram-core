package system

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
)

type MemoryKind string

const (
	MemoryRAM MemoryKind = "ram"
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

func (p HLECallProfile) validate() error {
	trap := cpu.ExecutionTrap{Address: p.Address, Mode: p.Mode}
	if !validProfileID(p.ID) || !validProfileID(p.Contract) || !trap.Valid() ||
		p.Return != HLEReturnLinkRegister {
		return fmt.Errorf("invalid HLE call profile %q", p.ID)
	}
	return nil
}

type BoardProfile struct {
	ID                             string
	PlatformID                     string
	FirmwareBuildID                string
	NANDReadID                     uint32
	BootClockModeStatus            uint32
	PrimaryClockStatus             uint32
	BootControlWritableOffsets     []uint32
	BootControlHalfwordOffsets     []uint32
	BootControlReadOnlyRegisters   []QualcommBootReadOnlyRegister
	BootControlSBIControllers      []uint32
	BootControlSBICompletionStatus uint32
	PrimaryClockWritableOffsets    []uint32
	ClockRegimeSleepControllers    []uint32
	LegacyTopVersion               uint32
	LegacyTopIdentification        uint32
	Memory                         []MemoryRegionProfile
	ReadOnlyRegisters              []ReadOnlyRegisterProfile
	LatchedRegisters               []LatchedRegisterProfile
	HLECalls                       []HLECallProfile
}

func (p BoardProfile) Validate() error {
	if !validProfileID(p.ID) || !validProfileID(p.PlatformID) || !validProfileID(p.FirmwareBuildID) {
		return fmt.Errorf("system board profile identity is invalid")
	}
	if p.NANDReadID&^uint32(0xffff) != 0 {
		return fmt.Errorf("board profile %q has invalid NAND read ID 0x%x", p.ID, p.NANDReadID)
	}
	if p.PrimaryClockStatus&^uint32(0xf) != 0 {
		return fmt.Errorf("board profile %q has invalid primary clock status 0x%x", p.ID, p.PrimaryClockStatus)
	}
	if p.BootClockModeStatus&^uint32(0x11) != 0 {
		return fmt.Errorf("board profile %q has invalid boot clock mode status 0x%x", p.ID, p.BootClockModeStatus)
	}
	if err := validateQualcommBootControlConfigurationOffsets(
		p.BootControlWritableOffsets,
		p.BootControlHalfwordOffsets,
		p.BootControlReadOnlyRegisters,
		p.BootControlSBIControllers,
		p.BootControlSBICompletionStatus,
	); err != nil {
		return fmt.Errorf("board profile %q boot-control register profile: %w", p.ID, err)
	}
	if err := validateQualcommPrimaryClockWritableOffsets(p.PrimaryClockWritableOffsets); err != nil {
		return fmt.Errorf("board profile %q primary-clock writable offsets: %w", p.ID, err)
	}
	sleepControllers := make(map[uint32]struct{}, len(p.ClockRegimeSleepControllers))
	for _, offset := range p.ClockRegimeSleepControllers {
		if !validQualcommClockRegimeSleepControllerOffset(offset) {
			return fmt.Errorf(
				"board profile %q has invalid clock-regime sleep controller 0x%x",
				p.ID,
				offset,
			)
		}
		if _, duplicate := sleepControllers[offset]; duplicate {
			return fmt.Errorf(
				"board profile %q repeats clock-regime sleep controller 0x%x",
				p.ID,
				offset,
			)
		}
		sleepControllers[offset] = struct{}{}
	}
	memory := append([]MemoryRegionProfile(nil), p.Memory...)
	for _, region := range memory {
		if !validProfileID(region.ID) || region.Kind != MemoryRAM || region.Size == 0 ||
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
	if bus == nil {
		return fmt.Errorf("apply board profile %q: nil bus", p.ID)
	}
	if err := p.Validate(); err != nil {
		return err
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
		}
	}
	return nil
}

func SCHW830DL21BoardProfile() BoardProfile {
	return BoardProfile{
		ID:                  "samsung.sch-w830",
		PlatformID:          "qualcomm.arm9-sch-family",
		FirmwareBuildID:     "samsung.sch-w830.dl21",
		NANDReadID:          0x0000ecaa,
		BootClockModeStatus: 0x00000001,
		PrimaryClockStatus:  0x00000000,
		BootControlWritableOffsets: []uint32{
			0x0008,
			0x00bc, 0x00c0,
			0x058c, 0x0590, 0x059c,
			0x05a0, 0x05a4, 0x05b0, 0x05b4, 0x05b8,
			0x05c4, 0x05c8, 0x05cc, 0x05d8,
			0x0a34,
			0x0a54, 0x0a58,
			0x0b34,
			0x0c0c, 0x0c2c, 0x0c38, 0x0c3c, 0x0c40,
			0x200c,
			0x2840,
			0x4100, 0x4104, 0x4108,
			0x4110, 0x4114, 0x4118, 0x411c,
			0x4128, 0x412c, 0x4130, 0x4134, 0x4138, 0x413c,
			0x423c,
			0x4600, 0x4604, 0x4614,
			0x533c,
		},
		BootControlSBIControllers:      []uint32{0x5000, 0x5100, 0x5200},
		BootControlSBICompletionStatus: 0x0494,
		BootControlHalfwordOffsets: []uint32{
			0x4000, 0x4004, 0x4008,
			0x4010, 0x4014, 0x4018, 0x401c,
			0x4020, 0x4024, 0x4028, 0x402c,
			0x4030, 0x4034, 0x4038,
			0x4200, 0x4204, 0x4208,
			0x4210, 0x4214, 0x4218, 0x421c,
			0x4220, 0x4224, 0x4228, 0x422c,
			0x4230, 0x4234, 0x4238,
		},
		PrimaryClockWritableOffsets: []uint32{
			0x0594, 0x0598, 0x05a8, 0x05ac,
			0x05bc, 0x05c0, 0x05d0, 0x05d4,
		},
		ClockRegimeSleepControllers: []uint32{0x5200, 0x5244},
		LegacyTopVersion:            0x00000000,
		LegacyTopIdentification:     0x00000000,
		Memory: []MemoryRegionProfile{
			{
				ID:      "ebi-ram",
				Kind:    MemoryRAM,
				Address: 0x00000000,
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
		LatchedRegisters: []LatchedRegisterProfile{
			{ID: "ssbi-register-02188", Address: 0x91002188, Width: Width16},
			{ID: "ssbi-register-0218a", Address: 0x9100218a, Width: Width16},
			{ID: "ssbi-register-0218c", Address: 0x9100218c, Width: Width16},
			{ID: "ssbi-register-0552a", Address: 0x9100552a, Width: Width16},
		},
	}
}

func validProfileID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 255 && strings.IndexByte(value, 0) < 0
}
