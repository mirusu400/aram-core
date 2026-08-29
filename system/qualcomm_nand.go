package system

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
)

const (
	QualcommNANDWindowSize            = 0x1000
	qualcommNANDCodewordDataSize      = 0x0200
	qualcommNANDCodewordSpareSize     = 0x0010
	qualcommNANDBufferSize            = 0x0210
	qualcomm2K8BitNANDEraseBlockSize  = 0x20000
	qualcommNANDAddressOffset         = 0x0300
	qualcommNANDCommandOffset         = 0x0304
	qualcommNANDStatusOffset          = 0x0308
	qualcommNANDCommandValidityOffset = 0x031c
	qualcommNANDReadIDOffset          = 0x0320
	qualcommNANDReadDataOffset        = 0x0324
	qualcommNANDDeviceConfig0Offset   = 0x0328
	qualcommNANDDeviceConfig1Offset   = 0x0330
	qualcommNANDCommandRead           = 1
	qualcommNANDCommandReadSpare      = 2
	qualcommNANDCommandProgram        = 3
	qualcommNANDCommandErase          = 4
	qualcommNANDCommandStatus         = 6
	qualcommNANDStatusDeviceReady     = 1 << 5
	qualcommNANDStatusProgramFailed   = 1 << 7
	qualcommNANDStatusReady           = 1 << 13
	qualcommNANDStatusWriteEnabled    = 1 << 14
	qualcommNANDStatusOperationError  = 1 << 3
	qualcommNANDStatusError           = qualcommNANDStatusOperationError
	qualcommNANDStatusMask            = qualcommNANDStatusDeviceReady |
		qualcommNANDStatusProgramFailed |
		qualcommNANDStatusReady |
		qualcommNANDStatusWriteEnabled |
		qualcommNANDStatusOperationError
	qualcommNANDPreviousStateVersion   = 3
	qualcommNANDFullBufferStateVersion = 4
	qualcommNANDStateVersion           = 5
	qualcommNANDLegacyStateVersion     = 2
)

type QualcommNANDConfig struct {
	PageSize         uint32
	EraseBlockSize   uint32
	Capacity         uint64
	SpareSize        uint32
	Spare            ReadOnlyStorage
	FactoryBadBlocks []uint32
	DeviceConfig0    uint32
	DeviceConfig1    uint32
	CommandValidity  uint32
	ReadID           uint32
	Ready            *StatusSignal
}

// NANDSpareStorage exposes page-aligned NAND spare/OOB media independently
// from a controller's MMIO data window. Controllers that address the same
// physical flash can share one implementation so guest-written metadata is
// persisted and reset together.
type NANDSpareStorage interface {
	SparePageSize() uint32
	ReadSparePage(destination []byte, page uint64) error
	ProgramSparePage(source []byte, page uint64) error
	EraseSpareBlock(block uint32) error
}

// Qualcomm2K8BitNANDConfig describes the standard four-codeword, 2 KiB,
// eight-bit NAND configuration consumed by the early Qualcomm controller.
func Qualcomm2K8BitNANDConfig(readID uint32, ready *StatusSignal) QualcommNANDConfig {
	return QualcommNANDConfig{
		PageSize:        0x0800,
		EraseBlockSize:  qualcomm2K8BitNANDEraseBlockSize,
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

// QualcommNAND models the early-controller 528-byte SRAM aperture: 512 bytes
// of codeword data followed by 16 bytes reserved for spare/ECC results. A 2 KiB
// page is transferred as four repeated read commands to the same page address.
type QualcommNAND struct {
	storage                ReadOnlyStorage
	spareStorage           ReadOnlyStorage
	pageSize               uint32
	capacity               uint64
	spareSize              uint32
	sparePageSize          uint32
	pagesPerEraseBlock     uint64
	factoryBadBlocks       map[uint32]struct{}
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
	data                   [qualcommNANDBufferSize]byte
	pageData               []byte
	spareData              []byte
	sparePages             map[uint64][]byte
	latchedPage            uint64
	pageLoaded             bool
}

type qualcommNANDWritableStorage interface {
	ProgramAt([]byte, int64) error
	EraseBlock(uint32) error
}

func NewQualcommNAND(storage ReadOnlyStorage, config QualcommNANDConfig) (*QualcommNAND, error) {
	if storage == nil || storage.Size() <= 0 ||
		config.PageSize < qualcommNANDCodewordDataSize || config.PageSize > 16<<10 ||
		config.PageSize%qualcommNANDCodewordDataSize != 0 ||
		config.EraseBlockSize < config.PageSize || config.EraseBlockSize%config.PageSize != 0 ||
		uint64(storage.Size())%uint64(config.PageSize) != 0 ||
		config.DeviceConfig0 == 0 || config.DeviceConfig1 == 0 ||
		config.CommandValidity == 0 || config.CommandValidity&^uint32(0x7f) != 0 ||
		config.ReadID == 0 || config.Ready == nil {
		return nil, ErrInvalidQualcommNAND
	}
	capacity := config.Capacity
	if capacity == 0 {
		capacity = uint64(storage.Size())
	}
	const maximumAddressablePages = (uint64(^uint32(0)) + 1) / qualcommNANDCodewordDataSize
	if capacity < uint64(storage.Size()) || capacity%uint64(config.PageSize) != 0 ||
		capacity/uint64(config.PageSize) > maximumAddressablePages {
		return nil, ErrInvalidQualcommNAND
	}
	pageCount := capacity / uint64(config.PageSize)
	if config.Spare == nil && config.SpareSize != 0 ||
		config.Spare != nil && (config.SpareSize < 2 || config.SpareSize > 4<<10 ||
			uint64(config.Spare.Size()) != pageCount*uint64(config.SpareSize)) {
		return nil, ErrInvalidQualcommNAND
	}
	sparePageSize := config.PageSize / qualcommNANDCodewordDataSize *
		qualcommNANDCodewordSpareSize
	if config.SpareSize > sparePageSize {
		sparePageSize = config.SpareSize
	}
	badBlocks := make(map[uint32]struct{}, len(config.FactoryBadBlocks))
	blockCount := capacity / uint64(config.EraseBlockSize)
	for _, block := range config.FactoryBadBlocks {
		if uint64(block) >= blockCount {
			return nil, ErrInvalidQualcommNAND
		}
		if _, duplicate := badBlocks[block]; duplicate {
			return nil, ErrInvalidQualcommNAND
		}
		badBlocks[block] = struct{}{}
	}
	device := &QualcommNAND{
		storage: storage, spareStorage: config.Spare,
		pageSize: config.PageSize, capacity: capacity, spareSize: config.SpareSize,
		sparePageSize:          sparePageSize,
		pagesPerEraseBlock:     uint64(config.EraseBlockSize / config.PageSize),
		factoryBadBlocks:       badBlocks,
		initialDeviceConfig0:   config.DeviceConfig0,
		initialDeviceConfig1:   config.DeviceConfig1,
		initialCommandValidity: config.CommandValidity,
		readID:                 config.ReadID,
		ready:                  config.Ready,
		pageData:               make([]byte, config.PageSize),
		spareData:              make([]byte, sparePageSize),
		sparePages:             make(map[uint64][]byte),
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
	n.pageLoaded = false
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
	if offset < qualcommNANDBufferSize {
		if uint64(offset)+uint64(width) > qualcommNANDBufferSize {
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
	if offset < qualcommNANDBufferSize {
		if uint64(offset)+uint64(width) > qualcommNANDBufferSize {
			return ErrQualcommNANDMMIO
		}
		putValue(n.data[offset:offset+uint32(width)], value)
		return nil
	}
	if width != Width32 {
		return fmt.Errorf("%w: write%d at 0x%x", ErrQualcommNANDMMIO, width*8, offset)
	}
	switch offset {
	case qualcommNANDAddressOffset:
		if value != n.address || n.nextChunk >= n.pageSize {
			n.nextChunk = 0
			n.pageLoaded = false
		}
		n.address = value
		return nil
	case qualcommNANDCommandOffset:
		if value == 0 {
			n.nextChunk = 0
			n.pageLoaded = false
			n.ready.Set(0)
			return nil
		}
		if value == qualcommNANDCommandErase {
			n.nextChunk = 0
			n.pageLoaded = false
			page := uint64(n.address / qualcommNANDCodewordDataSize)
			block := page / n.pagesPerEraseBlock
			writable, ok := n.storage.(qualcommNANDWritableStorage)
			if !ok || block >= n.capacity/uint64(n.pageSize)/n.pagesPerEraseBlock ||
				writable.EraseBlock(uint32(block)) != nil {
				n.status = qualcommNANDStatusOperationError
			} else {
				n.eraseSpareBlockUnchecked(block)
				n.status = 0
			}
			n.ready.Set(1)
			return nil
		}
		if value == qualcommNANDCommandProgram {
			// The early controller exposes distinct completion wires: block
			// erase completes on bit 0, while page transfers (including program)
			// complete on bit 1. Operation success remains available through a
			// following status-check command, so a failed program must still wake
			// the guest's completion poll.
			completed := n.programChunk()
			completion := uint32(2)
			// Each 512-byte codeword raises the controller-operation bit. The
			// NAND-ready bit is separate and becomes visible once the complete
			// page has been committed (or a failed operation has terminated).
			if !completed || n.nextChunk == n.pageSize {
				completion |= 1
			}
			n.ready.Set(completion)
			return nil
		}
		if value == qualcommNANDCommandStatus {
			// A clear write-protect result advertises a read-only NAND device.
			failed := n.status&(qualcommNANDStatusOperationError|
				qualcommNANDStatusProgramFailed) != 0
			n.status = qualcommNANDStatusDeviceReady | qualcommNANDStatusReady
			if _, ok := n.storage.(qualcommNANDWritableStorage); ok {
				n.status |= qualcommNANDStatusWriteEnabled
			}
			if failed {
				n.status |= qualcommNANDStatusProgramFailed
			}
			n.nextChunk = 0
			n.pageLoaded = false
			n.ready.Set(2)
			return nil
		}
		if value == 5 || value == 7 {
			n.status = 0
			n.nextChunk = 0
			n.pageLoaded = false
			n.ready.Set(2)
			return nil
		}
		if value == qualcommNANDCommandReadSpare {
			if n.readSpareWord() {
				n.ready.Set(2)
			} else {
				n.ready.Set(0)
			}
			return nil
		}
		if value != qualcommNANDCommandRead {
			return fmt.Errorf("%w: command 0x%x", ErrQualcommNANDMMIO, value)
		}
		if n.readChunk() {
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
	const scalarCount = 11
	dataOffset := 8 + scalarCount*4
	mediaOffset := dataOffset + len(n.data)
	pages := make([]uint64, 0, len(n.sparePages))
	for page := range n.sparePages {
		pages = append(pages, page)
	}
	slices.Sort(pages)
	recordSize := 8 + uint64(n.sparePageSize)
	outputSize := uint64(mediaOffset+4) + uint64(len(pages))*recordSize
	if outputSize > uint64(int(^uint(0)>>1)) {
		return nil, ErrInvalidState
	}
	output := make([]byte, int(outputSize))
	copy(output, "QNAN")
	binary.LittleEndian.PutUint32(output[4:8], qualcommNANDStateVersion)
	values := []uint32{
		n.initialDeviceConfig0, n.initialDeviceConfig1, n.initialCommandValidity, n.readID,
		n.address, n.nextChunk, n.status,
		n.deviceConfig0, n.deviceConfig1, n.commandValidity, n.readData,
	}
	for index, value := range values {
		binary.LittleEndian.PutUint32(output[8+index*4:], value)
	}
	copy(output[dataOffset:], n.data[:])
	binary.LittleEndian.PutUint32(output[mediaOffset:], uint32(len(pages)))
	offset := mediaOffset + 4
	for _, page := range pages {
		stored := n.sparePages[page]
		if len(stored) != int(n.sparePageSize) {
			return nil, ErrInvalidState
		}
		binary.LittleEndian.PutUint64(output[offset:], page)
		offset += 8
		copy(output[offset:], stored)
		offset += len(stored)
	}
	return output, nil
}

func (n *QualcommNAND) LoadState(state []byte) error {
	const scalarCount = 11
	dataOffset := 8 + scalarCount*4
	if len(state) < dataOffset || string(state[:4]) != "QNAN" {
		return ErrInvalidState
	}
	version := binary.LittleEndian.Uint32(state[4:8])
	dataSize := len(n.data)
	if version == qualcommNANDLegacyStateVersion {
		dataSize = qualcommNANDCodewordDataSize
	} else if version != qualcommNANDPreviousStateVersion &&
		version != qualcommNANDFullBufferStateVersion &&
		version != qualcommNANDStateVersion {
		return ErrInvalidState
	}
	mediaOffset := dataOffset + dataSize
	sparePages := make(map[uint64][]byte)
	if version != qualcommNANDStateVersion {
		if len(state) != mediaOffset {
			return ErrInvalidState
		}
	} else {
		if len(state) < mediaOffset+4 {
			return ErrInvalidState
		}
		recordCount := binary.LittleEndian.Uint32(state[mediaOffset:])
		recordSize := uint64(8 + n.sparePageSize)
		expectedSize := uint64(mediaOffset+4) + uint64(recordCount)*recordSize
		pageCount := n.capacity / uint64(n.pageSize)
		if expectedSize != uint64(len(state)) || uint64(recordCount) > pageCount {
			return ErrInvalidState
		}
		offset := mediaOffset + 4
		var previousPage uint64
		for index := uint32(0); index < recordCount; index++ {
			page := binary.LittleEndian.Uint64(state[offset:])
			offset += 8
			if page >= pageCount || index != 0 && page <= previousPage {
				return ErrInvalidState
			}
			sparePages[page] = append(
				[]byte(nil), state[offset:offset+int(n.sparePageSize)]...,
			)
			offset += int(n.sparePageSize)
			previousPage = page
		}
	}
	if len(state) < mediaOffset {
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
	if version <= qualcommNANDPreviousStateVersion && status == 0x80000000 {
		status = qualcommNANDStatusOperationError
	}
	deviceConfig0, deviceConfig1, commandValidity, readData := values[7], values[8], values[9], values[10]
	if address%qualcommNANDCodewordDataSize != 0 || nextChunk > n.pageSize ||
		nextChunk%qualcommNANDCodewordDataSize != 0 || status&^uint32(qualcommNANDStatusMask) != 0 ||
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
	n.pageLoaded = false
	n.sparePages = sparePages
	for index := range n.data {
		n.data[index] = 0xff
	}
	copy(n.data[:dataSize], state[dataOffset:])
	return nil
}

func (n *QualcommNAND) readChunk() bool {
	page := uint64(n.address / qualcommNANDCodewordDataSize)
	for index := range n.data {
		n.data[index] = 0xff
	}
	if !n.pageLoaded || n.latchedPage != page {
		offset := page * uint64(n.pageSize)
		if offset >= n.capacity {
			n.failRead()
			return false
		}
		if offset >= uint64(n.storage.Size()) {
			for index := range n.pageData {
				n.pageData[index] = 0xff
			}
		} else {
			count, err := n.storage.ReadAt(n.pageData, int64(offset))
			if count != len(n.pageData) || (err != nil && !errors.Is(err, io.EOF)) {
				n.failRead()
				return false
			}
		}
		n.latchedPage = page
		n.pageLoaded = true
	}
	copy(
		n.data[:qualcommNANDCodewordDataSize],
		n.pageData[n.nextChunk:n.nextChunk+qualcommNANDCodewordDataSize],
	)
	if !n.loadSparePage(page, n.spareData) {
		n.failRead()
		return false
	}
	spareOffset := n.nextChunk / qualcommNANDCodewordDataSize *
		qualcommNANDCodewordSpareSize
	copy(
		n.data[qualcommNANDCodewordDataSize:],
		n.spareData[spareOffset:spareOffset+qualcommNANDCodewordSpareSize],
	)
	// An erased codeword is still a successful transfer. Bad-block state comes
	// from the spare marker, not from all-0xff main data; early Qualcomm boot
	// code intentionally reads erased boundary pages while discovering images.
	n.status = 0
	n.nextChunk += qualcommNANDCodewordDataSize
	return true
}

func (n *QualcommNAND) programChunk() bool {
	page := uint64(n.address / qualcommNANDCodewordDataSize)
	pageCount := n.capacity / uint64(n.pageSize)
	writable, ok := n.storage.(qualcommNANDWritableStorage)
	if !ok || page >= pageCount || n.nextChunk > n.pageSize-qualcommNANDCodewordDataSize {
		n.status = qualcommNANDStatusOperationError
		n.nextChunk = 0
		n.pageLoaded = false
		return false
	}
	offset := page*uint64(n.pageSize) + uint64(n.nextChunk)
	if !n.loadSparePage(page, n.spareData) {
		n.status = qualcommNANDStatusOperationError
		n.nextChunk = 0
		n.pageLoaded = false
		return false
	}
	effective := n.pageData[:qualcommNANDCodewordDataSize]
	count, readErr := n.storage.ReadAt(effective, int64(offset))
	if count != len(effective) || readErr != nil && !errors.Is(readErr, io.EOF) {
		n.status = qualcommNANDStatusOperationError
		n.nextChunk = 0
		n.pageLoaded = false
		return false
	}
	// A one in the controller buffer inhibits programming; it cannot restore a
	// zero already present in the NAND cell. Pass the effective bitwise-AND
	// value to storage so spare-only or other partial-page programs leave the
	// existing main codeword intact instead of reporting a false 0-to-1 error.
	for index := range effective {
		effective[index] &= n.data[index]
	}
	if err := writable.ProgramAt(effective, int64(offset)); err != nil {
		n.status = qualcommNANDStatusOperationError
		n.nextChunk = 0
		n.pageLoaded = false
		return false
	}
	spareOffset := n.nextChunk / qualcommNANDCodewordDataSize *
		qualcommNANDCodewordSpareSize
	changedSpare := false
	for index := uint32(0); index < qualcommNANDCodewordSpareSize; index++ {
		current := n.spareData[spareOffset+index]
		effectiveSpare := current & n.data[qualcommNANDCodewordDataSize+index]
		if effectiveSpare != current {
			changedSpare = true
			n.spareData[spareOffset+index] = effectiveSpare
		}
	}
	if changedSpare {
		n.sparePages[page] = append([]byte(nil), n.spareData...)
	}
	n.status = 0
	n.nextChunk += qualcommNANDCodewordDataSize
	n.pageLoaded = false
	return true
}

func (n *QualcommNAND) readSpareWord() bool {
	page := uint64(n.address / qualcommNANDCodewordDataSize)
	column := n.address % qualcommNANDCodewordDataSize
	pageCount := n.capacity / uint64(n.pageSize)
	if page >= pageCount {
		n.failRead()
		n.readData = 0xffff
		return false
	}
	block := uint32(page / n.pagesPerEraseBlock)
	pageInBlock := page % n.pagesPerEraseBlock
	if _, bad := n.factoryBadBlocks[block]; bad && pageInBlock < 2 && column == 0 {
		n.status = 0
		n.readData = 0
		return true
	}
	if column > n.sparePageSize || 2 > n.sparePageSize-column {
		n.failRead()
		n.readData = 0xffff
		return false
	}
	if !n.loadSparePage(page, n.spareData) {
		n.status = qualcommNANDStatusError
		n.readData = 0xffff
		return false
	}
	n.status = 0
	n.readData = uint32(binary.LittleEndian.Uint16(n.spareData[column : column+2]))
	return true
}

func (n *QualcommNAND) loadSparePage(page uint64, output []byte) bool {
	if page >= n.capacity/uint64(n.pageSize) || uint32(len(output)) != n.sparePageSize {
		return false
	}
	for index := range output {
		output[index] = 0xff
	}
	if stored, ok := n.sparePages[page]; ok {
		copy(output, stored)
	} else if n.spareStorage != nil {
		count, err := n.spareStorage.ReadAt(
			output[:n.spareSize],
			int64(page*uint64(n.spareSize)),
		)
		if count != int(n.spareSize) || err != nil && !errors.Is(err, io.EOF) {
			return false
		}
	}
	block := uint32(page / n.pagesPerEraseBlock)
	pageInBlock := page % n.pagesPerEraseBlock
	if _, bad := n.factoryBadBlocks[block]; bad && pageInBlock < 2 {
		output[0] = 0
		output[1] = 0
	}
	return true
}

func (n *QualcommNAND) SparePageSize() uint32 {
	return n.sparePageSize
}

func (n *QualcommNAND) ReadSparePage(destination []byte, page uint64) error {
	if uint32(len(destination)) != n.sparePageSize {
		return ErrInvalidQualcommNAND
	}
	if page >= n.capacity/uint64(n.pageSize) {
		return ErrFlashBounds
	}
	if !n.loadSparePage(page, destination) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (n *QualcommNAND) ProgramSparePage(source []byte, page uint64) error {
	if uint32(len(source)) != n.sparePageSize {
		return ErrInvalidQualcommNAND
	}
	if page >= n.capacity/uint64(n.pageSize) {
		return ErrFlashBounds
	}
	if _, writable := n.storage.(qualcommNANDWritableStorage); !writable {
		return ErrFlashProgram
	}
	current := make([]byte, n.sparePageSize)
	if !n.loadSparePage(page, current) {
		return io.ErrUnexpectedEOF
	}
	changed := false
	for index, value := range source {
		effective := current[index] & value
		if effective != current[index] {
			current[index] = effective
			changed = true
		}
	}
	if changed {
		n.sparePages[page] = current
	}
	return nil
}

func (n *QualcommNAND) EraseSpareBlock(block uint32) error {
	if uint64(block) >= n.capacity/uint64(n.pageSize)/n.pagesPerEraseBlock {
		return ErrFlashBounds
	}
	if _, writable := n.storage.(qualcommNANDWritableStorage); !writable {
		return ErrFlashProgram
	}
	n.eraseSpareBlockUnchecked(uint64(block))
	return nil
}

func (n *QualcommNAND) eraseSpareBlockUnchecked(block uint64) {
	firstPage := block * n.pagesPerEraseBlock
	erased := make([]byte, n.sparePageSize)
	for index := range erased {
		erased[index] = 0xff
	}
	for page := firstPage; page < firstPage+n.pagesPerEraseBlock; page++ {
		if n.spareStorage == nil {
			delete(n.sparePages, page)
			continue
		}
		n.sparePages[page] = append([]byte(nil), erased...)
	}
}

func (n *QualcommNAND) failRead() {
	n.status = qualcommNANDStatusError
	n.nextChunk = 0
	n.pageLoaded = false
	for index := range n.data {
		n.data[index] = 0xff
	}
}

var (
	_ Device           = (*QualcommNAND)(nil)
	_ StatefulDevice   = (*QualcommNAND)(nil)
	_ NANDSpareStorage = (*QualcommNAND)(nil)
)
