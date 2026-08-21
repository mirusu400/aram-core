package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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
	NANDReady              *LevelSignal
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

var qualcommBootWritableOffsets = []uint32{
	0x0220, 0x0244, 0x024c, 0x0280, 0x0290, 0x0294,
	0x0330,
	0x0380, 0x0384, 0x0388, 0x03ac, 0x0414, 0x0900,
	0x0904, 0x0908, 0x090c, 0x0910, 0x0914, 0x0918,
	0x091c, 0x0920,
	0x0924, 0x0934, 0x0938, 0x093c, 0x0940, 0x0aa0,
	0x0a04, 0x0a48, 0x0aa8, 0x0aac, 0x0ab0, 0x0ab4,
	0x0ab8,
	0x0abc,
	0x0ac0, 0x0ac4,
	0x0ac8, 0x0acc, 0x0ad0, 0x0ad4, 0x0ad8, 0x0adc,
	0x0ae0, 0x5300, 0x5344,
}

var qualcommSecondaryClockOffsets = []uint32{0x0400, 0x0404, 0x0430, 0x0434}

const qualcommSecondaryClockDisabledStatusOffset = 0x0440

// QualcommBootControl is an explicit early-boot compatibility model. It
// exposes the hardware revision and latches only the clock/reset writes seen
// before the next platform boundary; every unknown access fails.
type QualcommBootControl struct {
	hardwareRevision       uint32
	nandInterfaceMode      uint32
	ebiMemoryConfiguration uint32
	clockModeStatus        uint32
	nandReady              *LevelSignal
	registers              map[uint32]uint32
	watchdogServices       uint64
}

func NewQualcommBootControl(config QualcommBootControlConfig) (*QualcommBootControl, error) {
	if config.HardwareRevision>>28 == 0 ||
		config.NANDInterfaceMode != 2 && config.NANDInterfaceMode != 4 ||
		config.EBIMemoryConfiguration&0x5f80 != 0x5680 &&
			config.EBIMemoryConfiguration&0x5f80 != 0x5880 ||
		config.ClockModeStatus > 1 || config.NANDReady == nil {
		return nil, fmt.Errorf("invalid Qualcomm boot-control configuration")
	}
	device := &QualcommBootControl{
		hardwareRevision:       config.HardwareRevision,
		nandInterfaceMode:      config.NANDInterfaceMode,
		ebiMemoryConfiguration: config.EBIMemoryConfiguration,
		clockModeStatus:        config.ClockModeStatus,
		nandReady:              config.NANDReady,
	}
	if err := device.Reset(); err != nil {
		return nil, err
	}
	return device, nil
}

func (d *QualcommBootControl) Reset() error {
	d.registers = make(map[uint32]uint32, len(qualcommBootWritableOffsets))
	for _, offset := range qualcommBootWritableOffsets {
		d.registers[offset] = 0
	}
	d.registers[0x0380] = d.nandInterfaceMode
	d.nandReady.Set(false)
	d.watchdogServices = 0
	return nil
}

func (d *QualcommBootControl) Read(offset uint32, width Width) (uint32, error) {
	if width != Width32 {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
	}
	switch offset {
	case 0x0a40:
		return d.hardwareRevision, nil
	case 0x1100:
		return d.ebiMemoryConfiguration, nil
	case 0x0274:
		return d.clockModeStatus, nil
	case 0x0488:
		if d.nandReady.Asserted() {
			return 2, nil
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
	if width != Width32 {
		return fmt.Errorf("%w: write%d at 0x%x", ErrQualcommBootControlMMIO, width*8, offset)
	}
	if offset == 0x540c {
		if value != 1 {
			return fmt.Errorf("%w: watchdog service value 0x%x", ErrQualcommBootControlMMIO, value)
		}
		d.watchdogServices++
		return nil
	}
	if _, ok := d.registers[offset]; !ok {
		return fmt.Errorf("%w: write32 at 0x%x", ErrQualcommBootControlMMIO, offset)
	}
	d.registers[offset] = value
	if offset == 0x0380 && value&8 != 0 {
		d.nandReady.Set(true)
	} else if offset == 0x0414 {
		d.nandReady.Set(false)
	}
	return nil
}

func (d *QualcommBootControl) WatchdogServices() uint64 {
	return d.watchdogServices
}

func (d *QualcommBootControl) SaveState() ([]byte, error) {
	offsets := append([]uint32(nil), qualcommBootWritableOffsets...)
	sort.Slice(offsets, func(left, right int) bool { return offsets[left] < offsets[right] })
	var output bytes.Buffer
	output.WriteString("QBTC")
	_ = binary.Write(&output, binary.LittleEndian, uint32(5))
	_ = binary.Write(&output, binary.LittleEndian, d.hardwareRevision)
	_ = binary.Write(&output, binary.LittleEndian, d.nandInterfaceMode)
	_ = binary.Write(&output, binary.LittleEndian, d.ebiMemoryConfiguration)
	_ = binary.Write(&output, binary.LittleEndian, d.clockModeStatus)
	ready := uint8(0)
	if d.nandReady.Asserted() {
		ready = 1
	}
	_ = output.WriteByte(ready)
	_ = binary.Write(&output, binary.LittleEndian, d.watchdogServices)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(offsets)))
	for _, offset := range offsets {
		_ = binary.Write(&output, binary.LittleEndian, offset)
		_ = binary.Write(&output, binary.LittleEndian, d.registers[offset])
	}
	return output.Bytes(), nil
}

func (d *QualcommBootControl) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, revision, nandInterfaceMode, ebiMemoryConfiguration, clockModeStatus uint32
	var ready uint8
	var watchdog uint64
	var count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QBTC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 5 ||
		binary.Read(reader, binary.LittleEndian, &revision) != nil || revision != d.hardwareRevision ||
		binary.Read(reader, binary.LittleEndian, &nandInterfaceMode) != nil ||
		nandInterfaceMode != d.nandInterfaceMode ||
		binary.Read(reader, binary.LittleEndian, &ebiMemoryConfiguration) != nil ||
		ebiMemoryConfiguration != d.ebiMemoryConfiguration ||
		binary.Read(reader, binary.LittleEndian, &clockModeStatus) != nil ||
		clockModeStatus != d.clockModeStatus ||
		binary.Read(reader, binary.LittleEndian, &ready) != nil || ready > 1 ||
		binary.Read(reader, binary.LittleEndian, &watchdog) != nil ||
		binary.Read(reader, binary.LittleEndian, &count) != nil || count != uint32(len(qualcommBootWritableOffsets)) ||
		reader.Len() != int(count)*8 {
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
	d.nandReady.Set(ready != 0)
	d.watchdogServices = watchdog
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
	return fmt.Errorf("%w: write%d at 0x%x", ErrQualcommSecondaryClockMMIO, width*8, offset)
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
