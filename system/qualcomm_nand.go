package system

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	QualcommNANDWindowSize    = 0x1000
	qualcommNANDDataSize      = 0x0200
	qualcommNANDAddressOffset = 0x0300
	qualcommNANDCommandOffset = 0x0304
	qualcommNANDStatusOffset  = 0x0308
	qualcommNANDCommandRead   = 1
	qualcommNANDStatusError   = 0x80000000
)

var (
	ErrInvalidQualcommNAND = errors.New("invalid Qualcomm NAND geometry")
	ErrQualcommNANDMMIO    = errors.New("unsupported Qualcomm NAND register")
)

// QualcommNAND models the early-boot 512-byte data aperture and the address,
// command, and status registers exercised by the SCH-W830 QCSBL. A 2 KiB page
// is transferred as four repeated read commands to the same page address.
type QualcommNAND struct {
	storage   ReadOnlyStorage
	pageSize  uint32
	address   uint32
	nextChunk uint32
	status    uint32
	data      [qualcommNANDDataSize]byte
}

func NewQualcommNAND(storage ReadOnlyStorage, pageSize uint32) (*QualcommNAND, error) {
	if storage == nil || storage.Size() <= 0 || pageSize < qualcommNANDDataSize ||
		pageSize > 16<<10 || pageSize%qualcommNANDDataSize != 0 ||
		uint64(storage.Size())%uint64(pageSize) != 0 {
		return nil, ErrInvalidQualcommNAND
	}
	device := &QualcommNAND{storage: storage, pageSize: pageSize}
	if err := device.Reset(); err != nil {
		return nil, err
	}
	return device, nil
}

func (n *QualcommNAND) Reset() error {
	n.address = 0
	n.nextChunk = 0
	n.status = 0
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
		if value != qualcommNANDCommandRead {
			return fmt.Errorf("%w: command 0x%x", ErrQualcommNANDMMIO, value)
		}
		n.readChunk()
		return nil
	default:
		return fmt.Errorf("%w: write32 at 0x%x", ErrQualcommNANDMMIO, offset)
	}
}

func (n *QualcommNAND) SaveState() ([]byte, error) {
	output := make([]byte, 4+4+4+4+4+len(n.data))
	copy(output, "QNAN")
	binary.LittleEndian.PutUint32(output[4:8], 1)
	binary.LittleEndian.PutUint32(output[8:12], n.address)
	binary.LittleEndian.PutUint32(output[12:16], n.nextChunk)
	binary.LittleEndian.PutUint32(output[16:20], n.status)
	copy(output[20:], n.data[:])
	return output, nil
}

func (n *QualcommNAND) LoadState(state []byte) error {
	if len(state) != 4+4+4+4+4+len(n.data) || string(state[:4]) != "QNAN" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 {
		return ErrInvalidState
	}
	address := binary.LittleEndian.Uint32(state[8:12])
	nextChunk := binary.LittleEndian.Uint32(state[12:16])
	status := binary.LittleEndian.Uint32(state[16:20])
	if address%qualcommNANDDataSize != 0 || nextChunk > n.pageSize ||
		nextChunk%qualcommNANDDataSize != 0 || status&^uint32(qualcommNANDStatusError) != 0 {
		return ErrInvalidState
	}
	n.address = address
	n.nextChunk = nextChunk
	n.status = status
	copy(n.data[:], state[20:])
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
