package system

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	QualcommNANDWindowSize            = 0x1000
	qualcommNANDDataSize              = 0x0200
	qualcommNANDAddressOffset         = 0x0300
	qualcommNANDCommandOffset         = 0x0304
	qualcommNANDStatusOffset          = 0x0308
	qualcommNANDCommandValidityOffset = 0x031c
	qualcommNANDReadIDOffset          = 0x0320
	qualcommNANDReadDataOffset        = 0x0324
	qualcommNANDDeviceConfig0Offset   = 0x0328
	qualcommNANDDeviceConfig1Offset   = 0x0330
	qualcommNANDCommandRead           = 1
	qualcommNANDStatusError           = 0x80000000
)

type QualcommNANDConfig struct {
	PageSize        uint32
	DeviceConfig0   uint32
	DeviceConfig1   uint32
	CommandValidity uint32
	ReadID          uint32
	Ready           *StatusSignal
}

// Qualcomm2K8BitNANDConfig describes the standard four-codeword, 2 KiB,
// eight-bit NAND configuration consumed by the early Qualcomm controller.
func Qualcomm2K8BitNANDConfig(readID uint32, ready *StatusSignal) QualcommNANDConfig {
	return QualcommNANDConfig{
		PageSize:        0x0800,
		DeviceConfig0:   0xe8d408c0,
		DeviceConfig1:   0x0004745c,
		CommandValidity: 0x0000001d,
		ReadID:          readID,
		Ready:           ready,
	}
}

var (
	ErrInvalidQualcommNAND = errors.New("invalid Qualcomm NAND geometry")
	ErrQualcommNANDMMIO    = errors.New("unsupported Qualcomm NAND register")
)

// QualcommNAND models the early-boot 512-byte data aperture and the address,
// command, and status registers exercised by the SCH-W830 QCSBL. A 2 KiB page
// is transferred as four repeated read commands to the same page address.
type QualcommNAND struct {
	storage                ReadOnlyStorage
	pageSize               uint32
	initialDeviceConfig0   uint32
	initialDeviceConfig1   uint32
	initialCommandValidity uint32
	readID                 uint32
	ready                  *StatusSignal
	deviceConfig0          uint32
	deviceConfig1          uint32
	commandValidity        uint32
	readData               uint32
	address                uint32
	nextChunk              uint32
	status                 uint32
	data                   [qualcommNANDDataSize]byte
}

func NewQualcommNAND(storage ReadOnlyStorage, config QualcommNANDConfig) (*QualcommNAND, error) {
	if storage == nil || storage.Size() <= 0 ||
		config.PageSize < qualcommNANDDataSize || config.PageSize > 16<<10 ||
		config.PageSize%qualcommNANDDataSize != 0 ||
		uint64(storage.Size())%uint64(config.PageSize) != 0 ||
		config.DeviceConfig0 == 0 || config.DeviceConfig1 == 0 ||
		config.CommandValidity == 0 || config.CommandValidity&^uint32(0x7f) != 0 ||
		config.ReadID == 0 || config.ReadID&^uint32(0xffff) != 0 || config.Ready == nil {
		return nil, ErrInvalidQualcommNAND
	}
	device := &QualcommNAND{
		storage: storage, pageSize: config.PageSize,
		initialDeviceConfig0:   config.DeviceConfig0,
		initialDeviceConfig1:   config.DeviceConfig1,
		initialCommandValidity: config.CommandValidity,
		readID:                 config.ReadID,
		ready:                  config.Ready,
	}
	if err := device.Reset(); err != nil {
		return nil, err
	}
	return device, nil
}

func (n *QualcommNAND) Reset() error {
	n.address = 0
	n.nextChunk = 0
	n.status = 0
	n.deviceConfig0 = n.initialDeviceConfig0
	n.deviceConfig1 = n.initialDeviceConfig1
	n.commandValidity = n.initialCommandValidity
	n.readData = 0xffff
	for index := range n.data {
		n.data[index] = 0xff
	}
	return nil
}

func (n *QualcommNAND) Read(offset uint32, width Width) (uint32, error) {
	if offset < qualcommNANDDataSize {
		if uint64(offset)+uint64(width) > qualcommNANDDataSize {
			return 0, ErrQualcommNANDMMIO
		}
		return valueOf(n.data[offset : offset+uint32(width)]), nil
	}
	if offset == qualcommNANDStatusOffset && width == Width32 {
		return n.status, nil
	}
	if offset == qualcommNANDAddressOffset && width == Width32 {
		return n.address, nil
	}
	if width == Width32 {
		switch offset {
		case qualcommNANDCommandValidityOffset:
			return n.commandValidity, nil
		case qualcommNANDReadIDOffset:
			return n.readID, nil
		case qualcommNANDReadDataOffset:
			return n.readData, nil
		case qualcommNANDDeviceConfig0Offset:
			return n.deviceConfig0, nil
		case qualcommNANDDeviceConfig1Offset:
			return n.deviceConfig1, nil
		}
	}
	return 0, fmt.Errorf("%w: read%d at 0x%x", ErrQualcommNANDMMIO, width*8, offset)
}

func (n *QualcommNAND) Write(offset uint32, width Width, value uint32) error {
	if width != Width32 {
		return fmt.Errorf("%w: write%d at 0x%x", ErrQualcommNANDMMIO, width*8, offset)
	}
	switch offset {
	case qualcommNANDAddressOffset:
		if value != n.address || n.nextChunk >= n.pageSize {
			n.nextChunk = 0
		}
		n.address = value
		return nil
	case qualcommNANDCommandOffset:
		if value == 0 {
			n.nextChunk = 0
			n.ready.Set(0)
			return nil
		}
		if value == 4 {
			n.nextChunk = 0
			n.ready.Set(1)
			return nil
		}
		if value == 5 || value == 6 || value == 7 {
			n.nextChunk = 0
			n.ready.Set(2)
			return nil
		}
		if value == 2 {
			n.readWord()
			if n.status == 0 {
				n.ready.Set(2)
			} else {
				n.ready.Set(0)
			}
			return nil
		}
		if value != qualcommNANDCommandRead {
			return fmt.Errorf("%w: command 0x%x", ErrQualcommNANDMMIO, value)
		}
		n.readChunk()
		if n.status == 0 {
			n.ready.Set(2)
		} else {
			n.ready.Set(0)
		}
		return nil
	case qualcommNANDCommandValidityOffset:
		if value&^uint32(0x7f) != 0 {
			return fmt.Errorf("%w: command-validity value 0x%x", ErrQualcommNANDMMIO, value)
		}
		n.commandValidity = value
		return nil
	case qualcommNANDDeviceConfig0Offset:
		n.deviceConfig0 = value
		return nil
	case qualcommNANDDeviceConfig1Offset:
		n.deviceConfig1 = value
		return nil
	default:
		return fmt.Errorf("%w: write32 at 0x%x", ErrQualcommNANDMMIO, offset)
	}
}

func (n *QualcommNAND) SaveState() ([]byte, error) {
	output := make([]byte, 4+4+4*11+len(n.data))
	copy(output, "QNAN")
	binary.LittleEndian.PutUint32(output[4:8], 2)
	values := []uint32{
		n.initialDeviceConfig0, n.initialDeviceConfig1, n.initialCommandValidity, n.readID,
		n.address, n.nextChunk, n.status,
		n.deviceConfig0, n.deviceConfig1, n.commandValidity, n.readData,
	}
	for index, value := range values {
		binary.LittleEndian.PutUint32(output[8+index*4:], value)
	}
	copy(output[8+len(values)*4:], n.data[:])
	return output, nil
}

func (n *QualcommNAND) LoadState(state []byte) error {
	const scalarCount = 11
	dataOffset := 8 + scalarCount*4
	if len(state) != dataOffset+len(n.data) || string(state[:4]) != "QNAN" ||
		binary.LittleEndian.Uint32(state[4:8]) != 2 {
		return ErrInvalidState
	}
	values := make([]uint32, scalarCount)
	for index := range values {
		values[index] = binary.LittleEndian.Uint32(state[8+index*4:])
	}
	if values[0] != n.initialDeviceConfig0 || values[1] != n.initialDeviceConfig1 ||
		values[2] != n.initialCommandValidity || values[3] != n.readID {
		return ErrInvalidState
	}
	address, nextChunk, status := values[4], values[5], values[6]
	deviceConfig0, deviceConfig1, commandValidity, readData := values[7], values[8], values[9], values[10]
	if address%qualcommNANDDataSize != 0 || nextChunk > n.pageSize ||
		nextChunk%qualcommNANDDataSize != 0 || status&^uint32(qualcommNANDStatusError) != 0 ||
		commandValidity&^uint32(0x7f) != 0 {
		return ErrInvalidState
	}
	n.address = address
	n.nextChunk = nextChunk
	n.status = status
	n.deviceConfig0 = deviceConfig0
	n.deviceConfig1 = deviceConfig1
	n.commandValidity = commandValidity
	n.readData = readData
	copy(n.data[:], state[dataOffset:])
	return nil
}

func (n *QualcommNAND) readChunk() {
	page := uint64(n.address / qualcommNANDDataSize)
	offset := page*uint64(n.pageSize) + uint64(n.nextChunk)
	if offset > uint64(n.storage.Size()) || qualcommNANDDataSize > uint64(n.storage.Size())-offset {
		n.failRead()
		return
	}
	count, err := n.storage.ReadAt(n.data[:], int64(offset))
	if count != len(n.data) || (err != nil && !errors.Is(err, io.EOF)) {
		n.failRead()
		return
	}
	n.status = 0
	n.nextChunk += qualcommNANDDataSize
}

func (n *QualcommNAND) readWord() {
	var data [2]byte
	if uint64(n.address)+uint64(len(data)) > uint64(n.storage.Size()) {
		n.status = qualcommNANDStatusError
		n.readData = 0xffff
		return
	}
	count, err := n.storage.ReadAt(data[:], int64(n.address))
	if count != len(data) || err != nil && !errors.Is(err, io.EOF) {
		n.status = qualcommNANDStatusError
		n.readData = 0xffff
		return
	}
	n.status = 0
	n.readData = uint32(binary.LittleEndian.Uint16(data[:]))
}

func (n *QualcommNAND) failRead() {
	n.status = qualcommNANDStatusError
	n.nextChunk = 0
	for index := range n.data {
		n.data[index] = 0xff
	}
}

var (
	_ Device         = (*QualcommNAND)(nil)
	_ StatefulDevice = (*QualcommNAND)(nil)
)
