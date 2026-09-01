package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	QualcommSFlashWindowSize = 0x1000

	qualcommSFlashStatusOffset       = 0x001c
	qualcommSFlashStatusCommandReady = 1 << 5
	qualcommSFlashCommandOffset      = 0x0038
	qualcommSFlashExecuteOffset      = 0x003c
	qualcommSFlashMacro1Offset       = 0x0064
	qualcommSFlashGenP0Offset        = 0x0090
	qualcommSFlashBufferOffset       = 0x0100
	qualcommSFlashBufferSize         = 0x0200
	qualcommSFlashStateVersion       = 1
	qualcommSFlashCommandRegRead     = 2
	qualcommSFlashCommandRegWrite    = 3
	qualcommSFlashCommandIntLow      = 4
	qualcommSFlashCommandIntHigh     = 5
	qualcommSFlashCommandDataRead    = 6
	qualcommSFlashCommandDataWrite   = 7
)

var (
	ErrInvalidQualcommSFlash = errors.New("invalid Qualcomm SFlash controller")
	ErrQualcommSFlashMMIO    = errors.New("unsupported Qualcomm SFlash register")
)

var qualcommSFlashRegisterOffsets = []uint32{
	0x0000, 0x0004, 0x0008, 0x000c, 0x0010, 0x0014, 0x0018, 0x001c,
	0x0020, 0x0024, 0x0028, 0x0030, 0x0034, 0x0038, 0x003c, 0x0040,
	0x0044, 0x0050, 0x0054, 0x0058, 0x0060, 0x0064,
	0x0070, 0x0074, 0x0078, 0x007c, 0x0080, 0x0084, 0x0088,
	0x0090, 0x0094, 0x0098, 0x009c,
	0x00a0, 0x00a4, 0x00a8, 0x00ac, 0x00b0,
	0x00c0, 0x00c4, 0x00c8, 0x00cc, 0x00d0, 0x00d4, 0x00d8, 0x00dc,
	0x00e0, 0x00e4, 0x00e8, 0x00ec, 0x00f0, 0x00fc,
}

var qualcommSFlashAddressOffsets = []uint32{
	0x0004, 0x0008, 0x00c0, 0x00c4, 0x00c8, 0x00cc, 0x00e4,
}

var qualcommSFlashDataOffsets = []uint32{
	0x0090, 0x0094, 0x0098, 0x009c, 0x00d0, 0x00d4, 0x00d8,
}

// QualcommSFlashController models the MSM7K SFlash aperture used to reach a
// multiplexed OneNAND device. The controller executes synchronously, while
// retaining the architected register, status, and 512-byte transfer-buffer
// contract consumed by early boot firmware.
type QualcommSFlashController struct {
	target    *OneNAND
	registers map[uint32]uint32
	buffer    [qualcommSFlashBufferSize]byte
}

func NewQualcommSFlashController(target *OneNAND) (*QualcommSFlashController, error) {
	if target == nil {
		return nil, ErrInvalidQualcommSFlash
	}
	device := &QualcommSFlashController{target: target}
	if err := device.Reset(); err != nil {
		return nil, err
	}
	return device, nil
}

func (d *QualcommSFlashController) Reset() error {
	d.registers = make(map[uint32]uint32, len(qualcommSFlashRegisterOffsets))
	for _, offset := range qualcommSFlashRegisterOffsets {
		d.registers[offset] = 0
	}
	d.registers[qualcommSFlashStatusOffset] = qualcommSFlashStatusCommandReady
	clear(d.buffer[:])
	return d.target.Reset()
}

func (d *QualcommSFlashController) Read(offset uint32, width Width) (uint32, error) {
	if offset >= qualcommSFlashBufferOffset &&
		uint64(offset)+uint64(width) <= qualcommSFlashBufferOffset+qualcommSFlashBufferSize &&
		(width == Width8 || width == Width16 || width == Width32) && offset%uint32(width) == 0 {
		start := offset - qualcommSFlashBufferOffset
		return valueOf(d.buffer[start : start+uint32(width)]), nil
	}
	if width != Width32 || offset&3 != 0 {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommSFlashMMIO, width*8, offset)
	}
	if offset == qualcommSFlashExecuteOffset {
		return 0, nil
	}
	value, ok := d.registers[offset]
	if !ok {
		return 0, fmt.Errorf("%w: read32 at 0x%x", ErrQualcommSFlashMMIO, offset)
	}
	return value, nil
}

func (d *QualcommSFlashController) Write(offset uint32, width Width, value uint32) error {
	if offset >= qualcommSFlashBufferOffset &&
		uint64(offset)+uint64(width) <= qualcommSFlashBufferOffset+qualcommSFlashBufferSize &&
		(width == Width8 || width == Width16 || width == Width32) && offset%uint32(width) == 0 {
		start := offset - qualcommSFlashBufferOffset
		putValue(d.buffer[start:start+uint32(width)], value)
		return nil
	}
	if width != Width32 || offset&3 != 0 {
		return fmt.Errorf("%w: write%d value 0x%x at 0x%x", ErrQualcommSFlashMMIO, width*8, value, offset)
	}
	if _, ok := d.registers[offset]; !ok {
		return fmt.Errorf("%w: write32 value 0x%x at 0x%x", ErrQualcommSFlashMMIO, value, offset)
	}
	if offset == qualcommSFlashExecuteOffset {
		if value != 1 {
			return fmt.Errorf("%w: execute value 0x%x", ErrQualcommSFlashMMIO, value)
		}
		if err := d.execute(); err != nil {
			return err
		}
		d.registers[offset] = 0
		return nil
	}
	d.registers[offset] = value
	return nil
}

func (d *QualcommSFlashController) execute() error {
	command := d.registers[qualcommSFlashCommandOffset]
	count := command >> 20
	opcode := command & 0xf
	if count == 0 {
		if opcode == qualcommSFlashCommandIntLow || opcode == qualcommSFlashCommandIntHigh {
			return nil
		}
		return fmt.Errorf("%w: empty command 0x%x", ErrQualcommSFlashMMIO, command)
	}
	switch opcode {
	case qualcommSFlashCommandRegRead:
		return d.transferRegisters(count, false)
	case qualcommSFlashCommandRegWrite:
		return d.transferRegisters(count, true)
	case qualcommSFlashCommandIntLow, qualcommSFlashCommandIntHigh:
		// OneNAND operations complete synchronously, so the active-level wait
		// instruction observes an already-satisfied interrupt line.
		return nil
	case qualcommSFlashCommandDataRead:
		return d.transferBuffer(count, false)
	case qualcommSFlashCommandDataWrite:
		return d.transferBuffer(count, true)
	default:
		return fmt.Errorf("%w: command 0x%x", ErrQualcommSFlashMMIO, command)
	}
}

func (d *QualcommSFlashController) transferRegisters(count uint32, write bool) error {
	if count > uint32(len(qualcommSFlashAddressOffsets))*2 {
		return fmt.Errorf("%w: register transfer count %d", ErrQualcommSFlashMMIO, count)
	}
	for index := uint32(0); index < count; index++ {
		addressWord := d.registers[qualcommSFlashAddressOffsets[index/2]]
		address := uint16(addressWord >> (16 * (index & 1)))
		dataOffset := qualcommSFlashDataOffsets[index/2]
		shift := uint32(16 * (index & 1))
		if write {
			value := uint16(d.registers[dataOffset] >> shift)
			if err := d.target.Write(uint32(address)*2, Width16, uint32(value)); err != nil {
				return fmt.Errorf("execute Qualcomm SFlash register write 0x%x: %w", address, err)
			}
			continue
		}
		value, err := d.target.Read(uint32(address)*2, Width16)
		if err != nil {
			return fmt.Errorf("execute Qualcomm SFlash register read 0x%x: %w", address, err)
		}
		mask := uint32(0xffff) << shift
		d.registers[dataOffset] = d.registers[dataOffset]&^mask | (value&0xffff)<<shift
	}
	return nil
}

func (d *QualcommSFlashController) transferBuffer(count uint32, write bool) error {
	if count > qualcommSFlashBufferSize/2 {
		return fmt.Errorf("%w: data transfer count %d", ErrQualcommSFlashMMIO, count)
	}
	base := d.registers[qualcommSFlashMacro1Offset] * 2
	for index := uint32(0); index < count; index++ {
		offset := index * 2
		if write {
			value := binary.LittleEndian.Uint16(d.buffer[offset:])
			if err := d.target.Write(base+offset, Width16, uint32(value)); err != nil {
				return fmt.Errorf("execute Qualcomm SFlash data write 0x%x: %w", base+offset, err)
			}
			continue
		}
		value, err := d.target.Read(base+offset, Width16)
		if err != nil {
			return fmt.Errorf("execute Qualcomm SFlash data read 0x%x: %w", base+offset, err)
		}
		binary.LittleEndian.PutUint16(d.buffer[offset:], uint16(value))
	}
	return nil
}

func (d *QualcommSFlashController) SaveState() ([]byte, error) {
	target, err := d.target.SaveState()
	if err != nil {
		return nil, err
	}
	registerBytes := len(qualcommSFlashRegisterOffsets) * 4
	output := make([]byte, 16+registerBytes+len(d.buffer)+len(target))
	copy(output, "QSFL")
	binary.LittleEndian.PutUint32(output[4:8], qualcommSFlashStateVersion)
	binary.LittleEndian.PutUint32(output[8:12], uint32(len(target)))
	binary.LittleEndian.PutUint32(output[12:16], uint32(len(qualcommSFlashRegisterOffsets)))
	offset := 16
	for _, register := range qualcommSFlashRegisterOffsets {
		binary.LittleEndian.PutUint32(output[offset:], d.registers[register])
		offset += 4
	}
	copy(output[offset:], d.buffer[:])
	offset += len(d.buffer)
	copy(output[offset:], target)
	return output, nil
}

func (d *QualcommSFlashController) LoadState(state []byte) error {
	registerBytes := len(qualcommSFlashRegisterOffsets) * 4
	minimum := 16 + registerBytes + len(d.buffer)
	if len(state) < minimum || string(state[:4]) != "QSFL" ||
		binary.LittleEndian.Uint32(state[4:8]) != qualcommSFlashStateVersion ||
		binary.LittleEndian.Uint32(state[12:16]) != uint32(len(qualcommSFlashRegisterOffsets)) {
		return ErrInvalidState
	}
	targetSize := binary.LittleEndian.Uint32(state[8:12])
	if uint64(minimum)+uint64(targetSize) != uint64(len(state)) {
		return ErrInvalidState
	}
	oldTarget, err := d.target.SaveState()
	if err != nil {
		return err
	}
	if err := d.target.LoadState(state[minimum:]); err != nil {
		return err
	}
	registers := make(map[uint32]uint32, len(qualcommSFlashRegisterOffsets))
	offset := 16
	for _, register := range qualcommSFlashRegisterOffsets {
		registers[register] = binary.LittleEndian.Uint32(state[offset:])
		offset += 4
	}
	if registers[qualcommSFlashExecuteOffset] != 0 {
		_ = d.target.LoadState(oldTarget)
		return ErrInvalidState
	}
	d.registers = registers
	copy(d.buffer[:], state[offset:offset+len(d.buffer)])
	return nil
}

var _ StatefulDevice = (*QualcommSFlashController)(nil)
