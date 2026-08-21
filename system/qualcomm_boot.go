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
	QualcommBootControlWindowSize = 0x10000
	qualcommPBLMagic              = 0xa1b2c3d4
	qualcommPBLServiceEnd         = 0x015d
	qualcommPBLFlashTypeNAND2K    = 6
)

var ErrQualcommBootControlMMIO = errors.New("unsupported Qualcomm boot-control register")

type QualcommNANDPBLConfig struct {
	Entry          uint32
	TableAddress   uint32
	PageSize       uint32
	EraseBlockSize uint32
	FlashSize      uint64
	BadBlockLimit  uint32
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
	0x0244, 0x024c, 0x0280, 0x0290, 0x0294, 0x0330,
	0x0384, 0x0388, 0x03ac, 0x0920, 0x0924, 0x0aa0,
	0x0aa8, 0x5300,
}

// QualcommBootControl is an explicit early-boot compatibility model. It
// exposes the hardware revision and latches only the clock/reset writes seen
// before the next platform boundary; every unknown access fails.
type QualcommBootControl struct {
	hardwareRevision uint32
	registers        map[uint32]uint32
	watchdogServices uint64
}

func NewQualcommBootControl(hardwareRevision uint32) (*QualcommBootControl, error) {
	if hardwareRevision>>28 == 0 {
		return nil, fmt.Errorf("Qualcomm hardware revision has no major nibble")
	}
	device := &QualcommBootControl{hardwareRevision: hardwareRevision}
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
	case 0x0274, 0x551c:
		return 0, nil
	case 0x024c:
		return d.registers[offset], nil
	default:
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
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_ = binary.Write(&output, binary.LittleEndian, d.hardwareRevision)
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
	var version, revision uint32
	var watchdog uint64
	var count uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QBTC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 ||
		binary.Read(reader, binary.LittleEndian, &revision) != nil || revision != d.hardwareRevision ||
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
	d.watchdogServices = watchdog
	return nil
}

var (
	_ Device         = (*QualcommBootControl)(nil)
	_ StatefulDevice = (*QualcommBootControl)(nil)
)
