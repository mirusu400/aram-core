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
)

var (
	ErrQualcommBootControlMMIO    = errors.New("unsupported Qualcomm boot-control register")
	ErrQualcommSecondaryClockMMIO = errors.New("unsupported Qualcomm secondary-clock register")
)

type QualcommNANDPBLConfig struct {
	Entry          uint32
	TableAddress   uint32
	PageSize       uint32
	EraseBlockSize uint32
	FlashSize      uint64
	BadBlockLimit  uint32
}

type QualcommBootControlConfig struct {
	HardwareRevision       uint32
	NANDInterfaceMode      uint32
	EBIMemoryConfiguration uint32
	ClockModeStatus        uint32
	WritableOffsets        []uint32
	HalfwordOffsets        []uint32
	ReadOnlyRegisters      []QualcommBootReadOnlyRegister
	SBIControllers         []uint32
	SBICompletionStatus    uint32
	NANDReady              *StatusSignal
	InterruptController    *QualcommInterruptController
	TimeTickClock          *QualcommTimeTickClockConfig
}

// QualcommBootReadOnlyRegister describes a profile-specific word register
// whose fixed reset/strap value is evidenced but whose writes are not.
type QualcommBootReadOnlyRegister struct {
	Offset uint32
	Value  uint32
}

// QualcommTimeTickClockConfig relates deterministic instruction retirement to
// the free-running sleep-clock timetick. InterruptSource is platform/profile
// data: Qualcomm family members do not necessarily route TIMETICK_INT
// identically.
type QualcommTimeTickClockConfig struct {
	InstructionsPerSecond uint64
	TimeTickHz            uint64
	InterruptSource       uint8
}

// NewQualcommNANDPBLHandoff builds the bounded PBL service data consumed by
// the early QCSBL. The missing mask-ROM remains an explicit HLE boundary.
func NewQualcommNANDPBLHandoff(config QualcommNANDPBLConfig) (BootHandoff, error) {
	if config.Entry&3 != 0 || config.TableAddress&3 != 0 || config.PageSize != 0x800 ||
		config.EraseBlockSize == 0 || config.EraseBlockSize%config.PageSize != 0 ||
		config.FlashSize == 0 || config.FlashSize%uint64(config.EraseBlockSize) != 0 ||
		config.FlashSize/uint64(config.EraseBlockSize) > uint64(^uint32(0)) ||
		config.BadBlockLimit == 0 {
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
	table := make([]byte, 0x2c+len(entries)*8)
	for index, entry := range entries {
		binary.LittleEndian.PutUint32(table[0x2c+index*8:], entry[0])
		binary.LittleEndian.PutUint32(table[0x30+index*8:], entry[1])
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
	readOnlyRegisters []QualcommBootReadOnlyRegister,
	sbiControllers []uint32,
	sbiCompletionStatus uint32,
) error {
	if err := validateQualcommBootControlWritableOffsets(writableOffsets); err != nil {
		return err
	}
	seen := make(map[uint32]struct{}, len(qualcommBootWritableOffsets)+len(writableOffsets)+
		len(sbiControllers)*len(qualcommBootSBIRegisterOffsets))
	for _, offset := range qualcommBootWritableOffsets {
		seen[offset] = struct{}{}
	}
	for _, offset := range writableOffsets {
		seen[offset] = struct{}{}
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
	return nil
}

func isQualcommBootControlSpecialOffset(offset uint32) bool {
	switch offset {
	case 0x0274, 0x0488, 0x0a40, 0x1004,
		0x5408, 0x540c, 0x54c0, 0x551c:
		return true
	default:
		return false
	}
}

func mergedQualcommBootControlWritableOffsets(
	extra, halfwords []uint32,
	readOnlyRegisters []QualcommBootReadOnlyRegister,
	sbiControllers []uint32,
	sbiCompletionStatus uint32,
) []uint32 {
	offsets := make(
		[]uint32,
		0,
		len(qualcommBootWritableOffsets)+len(extra)+len(halfwords)+len(readOnlyRegisters)+
			len(sbiControllers)*len(qualcommBootSBIRegisterOffsets),
	)
	offsets = append(offsets, qualcommBootWritableOffsets...)
	offsets = append(offsets, extra...)
	offsets = append(offsets, halfwords...)
	for _, register := range readOnlyRegisters {
		offsets = append(offsets, register.Offset)
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

// QualcommBootControl is an explicit compatibility bank for the currently
// evidenced system-control, MPMC, IRQ-configuration, and timetick registers.
// Registers with understood side effects are modeled separately; every
// unknown access fails.
type QualcommBootControl struct {
	hardwareRevision        uint32
	nandInterfaceMode       uint32
	ebiMemoryConfiguration  uint32
	clockModeStatus         uint32
	nandReady               *StatusSignal
	interruptController     *QualcommInterruptController
	writableOffsets         []uint32
	halfwordOffsets         map[uint32]struct{}
	readOnlyRegisters       map[uint32]uint32
	sbiControllers          map[uint32]struct{}
	sbiCompletionStatus     uint32
	registers               map[uint32]uint32
	watchdogServices        uint64
	timeTick                uint32
	timeTickReadPhase       uint8
	timeTickClocked         bool
	timeTickInstructionRate uint64
	timeTickHz              uint64
	timeTickInterruptSource uint8
	timeTickPhase           uint64
	timeTickMatchReady      bool
	timeTickMatchConfigured bool
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
		config.ReadOnlyRegisters,
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
	}
	device := &QualcommBootControl{
		hardwareRevision:       config.HardwareRevision,
		nandInterfaceMode:      config.NANDInterfaceMode,
		ebiMemoryConfiguration: config.EBIMemoryConfiguration,
		clockModeStatus:        config.ClockModeStatus,
		nandReady:              config.NANDReady,
		interruptController:    config.InterruptController,
		writableOffsets: mergedQualcommBootControlWritableOffsets(
			config.WritableOffsets,
			config.HalfwordOffsets,
			config.ReadOnlyRegisters,
			config.SBIControllers,
			config.SBICompletionStatus,
		),
		halfwordOffsets:     make(map[uint32]struct{}, len(config.HalfwordOffsets)),
		readOnlyRegisters:   make(map[uint32]uint32, len(config.ReadOnlyRegisters)),
		sbiControllers:      make(map[uint32]struct{}, len(config.SBIControllers)),
		sbiCompletionStatus: config.SBICompletionStatus,
	}
	for _, offset := range config.HalfwordOffsets {
		device.halfwordOffsets[offset] = struct{}{}
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
	return d.interruptController.Reset()
}

func (d *QualcommBootControl) Read(offset uint32, width Width) (uint32, error) {
	if offset >= 0x0900 && offset < 0x0900+QualcommInterruptControllerWindowSize {
		return d.interruptController.Read(offset-0x0900, width)
	}
	if _, ok := d.halfwordOffsets[offset]; ok {
		if width != Width16 {
			return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
		}
		return d.registers[offset], nil
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
	d.registers[offset] = value
	for base := range d.sbiControllers {
		if offset == base+qualcommBootSBICommandOffset {
			d.registers[base+qualcommBootSBIResultOffset] = 0
			d.registers[d.sbiCompletionStatus] = qualcommBootSBICompleteStatus
			return nil
		}
	}
	if offset == 0x54c4 {
		d.timeTickMatchConfigured = true
		d.timeTickMatchReady = !d.timeTickClocked
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
		return d.interruptController.PulseSource(d.timeTickInterruptSource)
	}
	return nil
}

func (d *QualcommBootControl) WatchdogServices() uint64 {
	return d.watchdogServices
}

func (d *QualcommBootControl) registerWidth(offset uint32) Width {
	if _, ok := d.halfwordOffsets[offset]; ok {
		return Width16
	}
	return Width32
}

func (d *QualcommBootControl) SaveState() ([]byte, error) {
	interruptState, err := d.interruptController.SaveState()
	if err != nil {
		return nil, err
	}
	offsets := d.writableOffsets
	var output bytes.Buffer
	output.WriteString("QBTC")
	_ = binary.Write(&output, binary.LittleEndian, uint32(11))
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
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(offsets)))
	for _, offset := range offsets {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = output.WriteByte(byte(d.registerWidth(offset)))
		_ = binary.Write(&output, binary.LittleEndian, d.registers[offset])
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(interruptState)))
	output.Write(interruptState)
	return output.Bytes(), nil
}

func (d *QualcommBootControl) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, revision, nandInterfaceMode, ebiMemoryConfiguration, clockModeStatus uint32
	var ready uint8
	var watchdog uint64
	var timeTick uint32
	var timeTickReadPhase uint8
	var clocked, interruptSource, matchReady, matchConfigured uint8
	var instructionRate, timeTickHz, timeTickPhase uint64
	var count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QBTC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 11 ||
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
		binary.Read(reader, binary.LittleEndian, &timeTickPhase) != nil ||
		(d.timeTickClocked && timeTickPhase >= d.timeTickInstructionRate) ||
		binary.Read(reader, binary.LittleEndian, &matchReady) != nil || matchReady > 1 ||
		binary.Read(reader, binary.LittleEndian, &matchConfigured) != nil || matchConfigured > 1 ||
		binary.Read(reader, binary.LittleEndian, &count) != nil || count != uint32(len(d.writableOffsets)) ||
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
		if Width(width) != d.registerWidth(offset) || width == uint8(Width16) && value > 0xffff {
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
	var interruptStateLength uint32
	if binary.Read(reader, binary.LittleEndian, &interruptStateLength) != nil ||
		uint64(interruptStateLength) != uint64(reader.Len()) {
		return ErrInvalidState
	}
	interruptState := make([]byte, interruptStateLength)
	if _, err := io.ReadFull(reader, interruptState); err != nil || reader.Len() != 0 {
		return ErrInvalidState
	}
	if err := d.interruptController.LoadState(interruptState); err != nil {
		return err
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
	registers map[uint32]uint32
}

func NewQualcommSecondaryClockControl() *QualcommSecondaryClockControl {
	device := &QualcommSecondaryClockControl{}
	_ = device.Reset()
	return device
}

func (d *QualcommSecondaryClockControl) Reset() error {
	d.registers = make(map[uint32]uint32, len(qualcommSecondaryClockOffsets))
	for _, offset := range qualcommSecondaryClockOffsets {
		d.registers[offset] = 0
	}
	return nil
}

func (d *QualcommSecondaryClockControl) Read(offset uint32, width Width) (uint32, error) {
	if width == Width32 {
		if offset == qualcommSecondaryClockDisabledStatusOffset {
			return 0x10, nil
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
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(qualcommSecondaryClockOffsets)))
	for _, offset := range qualcommSecondaryClockOffsets {
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
		count != uint32(len(qualcommSecondaryClockOffsets)) || reader.Len() != int(count)*8 {
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
	_ Device         = (*QualcommBootControl)(nil)
	_ StatefulDevice = (*QualcommBootControl)(nil)
	_ Device         = (*QualcommSecondaryClockControl)(nil)
	_ StatefulDevice = (*QualcommSecondaryClockControl)(nil)
)
