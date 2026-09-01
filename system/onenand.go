package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	OneNANDWindowSize = 0x00020000

	oneNANDPageSize        = 0x0800
	oneNANDEraseBlockSize  = 0x20000
	oneNANDSectorSize      = 0x0200
	oneNANDSpareSectorSize = 0x0010
	oneNANDBufferRAMSize   = 0x18000

	oneNANDManufacturerIDOffset   = 0x1e000
	oneNANDDeviceIDOffset         = 0x1e002
	oneNANDVersionIDOffset        = 0x1e004
	oneNANDDataBufferSizeOffset   = 0x1e006
	oneNANDBootBufferSizeOffset   = 0x1e008
	oneNANDBufferCountOffset      = 0x1e00a
	oneNANDTechnologyOffset       = 0x1e00c
	oneNANDStartAddress1Offset    = 0x1e200
	oneNANDStartAddress8Offset    = 0x1e20e
	oneNANDStartBufferOffset      = 0x1e400
	oneNANDCommandOffset          = 0x1e440
	oneNANDSystemConfig1Offset    = 0x1e442
	oneNANDSystemConfig2Offset    = 0x1e444
	oneNANDControllerStatusOffset = 0x1e480
	oneNANDInterruptStatusOffset  = 0x1e482
	oneNANDUnlockStartOffset      = 0x1e498
	oneNANDUnlockEndOffset        = 0x1e49a
	oneNANDWriteProtectOffset     = 0x1e49c
	oneNANDECCStatusOffset        = 0x1fe00
	oneNANDECCResultEndOffset     = 0x1fe12

	oneNANDCommandRead         = 0x0000
	oneNANDCommandReadSpare    = 0x0013
	oneNANDCommandProgram      = 0x0080
	oneNANDCommandProgramSpare = 0x001a
	oneNANDCommandUnlock       = 0x0023
	oneNANDCommandUnlockAll    = 0x0027
	oneNANDCommandLock         = 0x002a
	oneNANDCommandLockTight    = 0x002c
	oneNANDCommandEraseVerify  = 0x0071
	oneNANDCommandErase        = 0x0094
	oneNANDCommandOTPAccess    = 0x0065
	oneNANDCommandResetCore    = 0x00f0
	oneNANDCommandReset        = 0x00f3

	oneNANDStatusCommandError = 1 << 10
	oneNANDStatusEraseError   = 1 << 11
	oneNANDStatusProgramError = 1 << 12
	oneNANDStatusLoadError    = 1 << 13
	oneNANDStatusErrorMask    = oneNANDStatusCommandError | oneNANDStatusEraseError |
		oneNANDStatusProgramError | oneNANDStatusLoadError

	oneNANDInterruptReset   = 1 << 4
	oneNANDInterruptErase   = 1 << 5
	oneNANDInterruptProgram = 1 << 6
	oneNANDInterruptLoad    = 1 << 7
	oneNANDInterruptMaster  = 1 << 15

	oneNANDWriteProtectLocked   = 0x0002
	oneNANDWriteProtectUnlocked = 0x0004
	oneNANDStateVersion         = 3
)

var (
	ErrInvalidOneNAND = errors.New("invalid OneNAND geometry")
	ErrOneNANDMMIO    = errors.New("unsupported OneNAND access")
)

type OneNANDConfig struct {
	ManufacturerID uint16
	DeviceID       uint16
	VersionID      uint16
	TechnologyID   uint16
	DieBlockOffset uint32
	Capacity       uint64
	FlexGeometry   *OneNANDFlexGeometry
	Storage        ReadOnlyStorage
	Spare          NANDSpareStorage
}

// OneNANDFlexGeometry describes the raw FBA geometry exposed by a
// Flex-OneNAND device. SLCBoundary is inclusive; blocks after it use the MLC
// erase-block size. Capacity remains the nominal chip capacity, while the
// accessible main-data size shrinks as blocks are converted from MLC to SLC.
type OneNANDFlexGeometry struct {
	PageSize     uint32
	BlockCount   uint32
	SLCBoundary  uint32
	SLCBlockSize uint32
	MLCBlockSize uint32
}

type oneNANDGeometry struct {
	pageSize     uint32
	blockCount   uint32
	slcBoundary  uint32
	slcBlockSize uint32
	mlcBlockSize uint32
	flex         bool
}

type oneNANDWritableStorage interface {
	ProgramAt([]byte, int64) error
	EraseBlock(uint32) error
	BlockSize() uint32
}

// OneNAND models the 16-bit multiplexed Samsung flash interface used after
// the mask-ROM/QCSBL boundary. Operations complete synchronously, while the
// architected interrupt and controller-status bits preserve the polling
// contract observed by boot firmware.
type OneNAND struct {
	storage        ReadOnlyStorage
	writable       oneNANDWritableStorage
	spare          NANDSpareStorage
	manufacturerID uint16
	deviceID       uint16
	versionID      uint16
	technologyID   uint16
	capacity       uint64
	densityMask    uint32
	geometry       oneNANDGeometry

	addresses        [8]uint16
	startBuffer      uint16
	command          uint16
	systemConfig1    uint16
	systemConfig2    uint16
	controllerStatus uint16
	interruptStatus  uint16
	unlockStart      uint16
	unlockEnd        uint16
	writeProtect     uint16
	bootCycle        bool
	otpMode          bool
	bufferRAM        []byte
}

func normalizeOneNANDGeometry(capacity uint64, flex *OneNANDFlexGeometry) (oneNANDGeometry, error) {
	if capacity == 0 || capacity > uint64(^uint32(0)) {
		return oneNANDGeometry{}, ErrInvalidOneNAND
	}
	if flex == nil {
		if capacity%oneNANDEraseBlockSize != 0 {
			return oneNANDGeometry{}, ErrInvalidOneNAND
		}
		blockCount := uint32(capacity / oneNANDEraseBlockSize)
		return oneNANDGeometry{
			pageSize: oneNANDPageSize, blockCount: blockCount,
			slcBoundary:  blockCount - 1,
			slcBlockSize: oneNANDEraseBlockSize, mlcBlockSize: oneNANDEraseBlockSize,
		}, nil
	}
	geometry := oneNANDGeometry{
		pageSize: flex.PageSize, blockCount: flex.BlockCount,
		slcBoundary:  flex.SLCBoundary,
		slcBlockSize: flex.SLCBlockSize, mlcBlockSize: flex.MLCBlockSize,
		flex: true,
	}
	sectorsPerPage := geometry.pageSize / oneNANDSectorSize
	if geometry.pageSize < oneNANDPageSize || geometry.pageSize > 0x1000 ||
		geometry.pageSize%oneNANDSectorSize != 0 ||
		geometry.pageSize&(geometry.pageSize-1) != 0 ||
		sectorsPerPage == 0 || sectorsPerPage > 8 ||
		geometry.blockCount == 0 || geometry.blockCount > 0x1000 ||
		geometry.slcBoundary >= geometry.blockCount ||
		geometry.slcBlockSize < geometry.pageSize ||
		geometry.slcBlockSize%geometry.pageSize != 0 ||
		geometry.slcBlockSize&(geometry.slcBlockSize-1) != 0 ||
		geometry.mlcBlockSize != 2*geometry.slcBlockSize ||
		geometry.mlcBlockSize/geometry.pageSize > 0x80 ||
		capacity%uint64(geometry.mlcBlockSize) != 0 ||
		capacity/uint64(geometry.mlcBlockSize) != uint64(geometry.blockCount) {
		return oneNANDGeometry{}, ErrInvalidOneNAND
	}
	slcBlocks := uint64(geometry.slcBoundary) + 1
	accessible := slcBlocks*uint64(geometry.slcBlockSize) +
		(uint64(geometry.blockCount)-slcBlocks)*uint64(geometry.mlcBlockSize)
	if accessible == 0 || accessible > capacity {
		return oneNANDGeometry{}, ErrInvalidOneNAND
	}
	return geometry, nil
}

func NewOneNAND(config OneNANDConfig) (*OneNAND, error) {
	geometry, geometryErr := normalizeOneNANDGeometry(config.Capacity, config.FlexGeometry)
	if geometryErr != nil || config.Storage == nil || config.Storage.Size() <= 0 ||
		config.ManufacturerID == 0 || config.DeviceID == 0 ||
		config.Capacity < uint64(config.Storage.Size()) ||
		config.Spare != nil && config.Spare.SparePageSize() !=
			geometry.pageSize/oneNANDSectorSize*oneNANDSpareSectorSize {
		return nil, ErrInvalidOneNAND
	}
	densityMask := uint32(0)
	if config.DieBlockOffset != 0 {
		if config.DieBlockOffset&(config.DieBlockOffset-1) != 0 ||
			config.DieBlockOffset >= geometry.blockCount {
			return nil, ErrInvalidOneNAND
		}
		densityMask = config.DieBlockOffset
	} else {
		density := uint32(config.DeviceID>>4) & 0xf
		if density > 9 {
			return nil, ErrInvalidOneNAND
		}
		if config.DeviceID&0x8 != 0 {
			densityMask = uint32(1) << (6 + density)
		}
	}
	device := &OneNAND{
		storage: config.Storage, manufacturerID: config.ManufacturerID,
		deviceID: config.DeviceID, versionID: config.VersionID,
		technologyID: config.TechnologyID,
		capacity:     config.Capacity, densityMask: densityMask, geometry: geometry, spare: config.Spare,
		bufferRAM: make([]byte, oneNANDBufferRAMSize),
	}
	device.writable, _ = config.Storage.(oneNANDWritableStorage)
	if err := device.Reset(); err != nil {
		return nil, err
	}
	return device, nil
}

func (d *OneNAND) Reset() error {
	for index := range d.bufferRAM {
		d.bufferRAM[index] = 0xff
	}
	bootSize := min(len(d.bufferRAM), 0x1000, int(d.storage.Size()))
	if bootSize != 0 {
		count, err := d.storage.ReadAt(d.bufferRAM[:bootSize], 0)
		if count != bootSize || err != nil && !errors.Is(err, io.EOF) {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("seed OneNAND BootRAM: %w", err)
		}
	}
	d.resetRegisters(true)
	return nil
}

func (d *OneNAND) resetRegisters(cold bool) {
	clear(d.addresses[:])
	d.startBuffer = 0
	d.command = 0
	d.systemConfig1 = 0x40c0
	d.systemConfig2 = 0
	d.controllerStatus = 0
	d.interruptStatus = 0x8010
	if cold {
		d.interruptStatus = 0x8080
	}
	d.unlockStart = 0
	d.unlockEnd = 0
	d.writeProtect = oneNANDWriteProtectLocked
	d.bootCycle = false
	d.otpMode = false
}

func (d *OneNAND) Read(offset uint32, width Width) (uint32, error) {
	if d.bufferAccess(offset, width) {
		return valueOf(d.bufferRAM[offset : offset+uint32(width)]), nil
	}
	if width != Width16 || offset&1 != 0 {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrOneNANDMMIO, width*8, offset)
	}
	if offset >= oneNANDStartAddress1Offset && offset <= oneNANDStartAddress8Offset {
		return uint32(d.addresses[(offset-oneNANDStartAddress1Offset)/2]), nil
	}
	switch offset {
	case oneNANDManufacturerIDOffset:
		return uint32(d.manufacturerID), nil
	case oneNANDDeviceIDOffset:
		return uint32(d.deviceID), nil
	case oneNANDVersionIDOffset:
		return uint32(d.versionID), nil
	case oneNANDDataBufferSizeOffset:
		return d.geometry.pageSize, nil
	case oneNANDBootBufferSizeOffset:
		return oneNANDSectorSize, nil
	case oneNANDBufferCountOffset:
		return 0x0201, nil
	case oneNANDTechnologyOffset:
		return uint32(d.technologyID), nil
	case oneNANDStartBufferOffset:
		return uint32(d.startBuffer), nil
	case oneNANDCommandOffset:
		return uint32(d.command), nil
	case oneNANDSystemConfig1Offset:
		return uint32(d.systemConfig1 & 0xffe0), nil
	case oneNANDSystemConfig2Offset:
		return uint32(d.systemConfig2), nil
	case oneNANDControllerStatusOffset:
		return uint32(d.controllerStatus), nil
	case oneNANDInterruptStatusOffset:
		return uint32(d.interruptStatus), nil
	case oneNANDUnlockStartOffset:
		return uint32(d.unlockStart), nil
	case oneNANDUnlockEndOffset:
		return uint32(d.unlockEnd), nil
	case oneNANDWriteProtectOffset:
		return uint32(d.writeProtect), nil
	default:
		if offset >= oneNANDECCStatusOffset && offset <= oneNANDECCResultEndOffset {
			return 0, nil
		}
		return 0, fmt.Errorf("%w: read16 at 0x%x", ErrOneNANDMMIO, offset)
	}
}

func (d *OneNAND) Write(offset uint32, width Width, value uint32) error {
	if width == Width16 && value > 0xffff {
		return fmt.Errorf("%w: write16 value 0x%x at 0x%x", ErrOneNANDMMIO, value, offset)
	}
	if d.bootCommandAccess(offset, width) {
		return d.writeBootCommand(uint16(value))
	}
	if d.bufferAccess(offset, width) {
		putValue(d.bufferRAM[offset:offset+uint32(width)], value)
		return nil
	}
	if width != Width16 || offset&1 != 0 {
		return fmt.Errorf("%w: write%d value 0x%x at 0x%x", ErrOneNANDMMIO, width*8, value, offset)
	}
	if offset >= oneNANDStartAddress1Offset && offset <= oneNANDStartAddress8Offset {
		d.addresses[(offset-oneNANDStartAddress1Offset)/2] = uint16(value)
		return nil
	}
	switch offset {
	case oneNANDStartBufferOffset:
		d.startBuffer = uint16(value)
		return nil
	case oneNANDCommandOffset:
		command := uint16(value)
		d.command = command
		d.executeCommand()
		return nil
	case oneNANDSystemConfig1Offset:
		d.systemConfig1 = uint16(value)
		return nil
	case oneNANDSystemConfig2Offset:
		d.systemConfig2 = uint16(value)
		return nil
	case oneNANDInterruptStatusOffset:
		d.interruptStatus &= uint16(value)
		if d.interruptStatus&oneNANDInterruptMaster == 0 {
			d.controllerStatus &^= oneNANDStatusErrorMask
		}
		return nil
	case oneNANDUnlockStartOffset:
		d.unlockStart = uint16(value)
		d.unlockEnd = d.unlockStart
		return nil
	case oneNANDUnlockEndOffset:
		d.unlockEnd = uint16(value)
		return nil
	default:
		return fmt.Errorf("%w: write16 value 0x%x at 0x%x", ErrOneNANDMMIO, value, offset)
	}
}

func (d *OneNAND) bufferAccess(offset uint32, width Width) bool {
	return (width == Width8 || width == Width16 || width == Width32) &&
		offset%uint32(width) == 0 && uint64(offset)+uint64(width) <= uint64(len(d.bufferRAM))
}

func (d *OneNAND) bootCommandAccess(offset uint32, width Width) bool {
	return width == Width16 && (offset < 0x400 || offset >= 0x10000 && offset < 0x10020)
}

func (d *OneNAND) writeBootCommand(value uint16) error {
	if d.bootCycle {
		d.bootCycle = false
		if value == 0 {
			offset, ok := d.flashOffset()
			if !ok || !d.loadMain(offset, d.geometry.pageSize, 0x400) {
				d.controllerStatus |= oneNANDStatusCommandError | oneNANDStatusLoadError
			}
			d.addresses[7] = (d.addresses[7] + 4) & 0xff
		}
		return nil
	}
	switch value {
	case 0x00f0, 0x00f3:
		d.resetRegisters(false)
	case 0x00e0:
		d.bootCycle = true
	case 0x0090:
		clear(d.bufferRAM[:6])
		binary.LittleEndian.PutUint16(d.bufferRAM[0:2], d.manufacturerID)
		binary.LittleEndian.PutUint16(d.bufferRAM[2:4], d.deviceID)
		binary.LittleEndian.PutUint16(d.bufferRAM[4:6], d.writeProtect)
	default:
		return fmt.Errorf("%w: boot command 0x%x", ErrOneNANDMMIO, value)
	}
	return nil
}

func (d *OneNAND) executeCommand() {
	switch d.command {
	case oneNANDCommandRead:
		if d.otpMode {
			d.loadErasedOTP()
			d.interruptStatus |= oneNANDInterruptMaster | oneNANDInterruptLoad
			break
		}
		offset, ok := d.flashOffset()
		bufferOffset, size, bufferOK := d.mainBufferRange()
		if !ok || !bufferOK || !d.loadMain(offset, size, bufferOffset) ||
			!d.loadSpare(offset) {
			d.controllerStatus |= oneNANDStatusCommandError | oneNANDStatusLoadError
		}
		d.interruptStatus |= oneNANDInterruptMaster | oneNANDInterruptLoad
	case oneNANDCommandReadSpare:
		offset, ok := d.flashOffset()
		if !ok || !d.loadSpare(offset) {
			d.controllerStatus |= oneNANDStatusCommandError | oneNANDStatusLoadError
		}
		d.interruptStatus |= oneNANDInterruptMaster | oneNANDInterruptLoad
	case oneNANDCommandProgram:
		offset, ok := d.flashOffset()
		bufferOffset, size, bufferOK := d.mainBufferRange()
		if !ok || !bufferOK || !d.programMain(offset, bufferOffset, size) ||
			!d.programSpare(offset) {
			d.controllerStatus |= oneNANDStatusCommandError | oneNANDStatusProgramError
		}
		d.interruptStatus |= oneNANDInterruptMaster | oneNANDInterruptProgram
	case oneNANDCommandProgramSpare:
		offset, ok := d.flashOffset()
		if !ok || !d.programSpare(offset) {
			d.controllerStatus |= oneNANDStatusCommandError | oneNANDStatusProgramError
		}
		d.interruptStatus |= oneNANDInterruptMaster | oneNANDInterruptProgram
	case oneNANDCommandErase:
		block, ok := d.selectedBlock()
		if !ok || !d.eraseMainBlock(block) ||
			d.spare != nil && d.eraseSpareBlock(block) != nil {
			d.controllerStatus |= oneNANDStatusCommandError | oneNANDStatusEraseError
		}
		d.interruptStatus |= oneNANDInterruptMaster | oneNANDInterruptErase
	case oneNANDCommandUnlock:
		_, selected := d.selectedBlock()
		if !selected || uint32(d.unlockStart) >= d.blockCount() ||
			uint32(d.unlockEnd) >= d.blockCount() || d.unlockStart > d.unlockEnd {
			d.controllerStatus |= oneNANDStatusCommandError
		} else {
			d.writeProtect = oneNANDWriteProtectUnlocked
		}
		d.interruptStatus |= oneNANDInterruptMaster
	case oneNANDCommandUnlockAll:
		d.writeProtect = oneNANDWriteProtectUnlocked
		d.interruptStatus |= oneNANDInterruptMaster
	case oneNANDCommandLock, oneNANDCommandLockTight:
		d.writeProtect = oneNANDWriteProtectLocked
		d.interruptStatus |= oneNANDInterruptMaster
	case oneNANDCommandEraseVerify:
		d.interruptStatus |= oneNANDInterruptMaster
	case oneNANDCommandOTPAccess:
		// OTP ACCESS changes the address space used by subsequent normal read
		// commands. An unprogrammed device exposes erased data; RESET leaves the
		// mode. The mode-change command itself completes without an error bit.
		d.otpMode = true
		d.interruptStatus |= oneNANDInterruptMaster
	case oneNANDCommandResetCore, oneNANDCommandReset:
		d.resetRegisters(false)
	default:
		d.controllerStatus |= oneNANDStatusCommandError
		d.interruptStatus |= oneNANDInterruptMaster
	}
}

func (d *OneNAND) loadErasedOTP() {
	if offset, size, ok := d.mainBufferRange(); ok {
		for index := offset; index < offset+size; index++ {
			d.bufferRAM[index] = 0xff
		}
	}
	if offset, size, ok := d.spareBufferRange(); ok {
		for index := offset; index < offset+size; index++ {
			d.bufferRAM[index] = 0xff
		}
	}
}

func (d *OneNAND) flashOffset() (uint64, bool) {
	block, ok := d.selectedBlock()
	if !ok {
		return 0, false
	}
	blockOffset, blockSize, ok := d.blockRange(block)
	if !ok {
		return 0, false
	}
	page := uint64(d.addresses[7]>>2) & 0x7f
	if page >= uint64(blockSize/d.geometry.pageSize) {
		return 0, false
	}
	sector := uint64(d.addresses[7] & 3)
	offset := blockOffset + page*uint64(d.geometry.pageSize) + sector*oneNANDSectorSize
	return offset, offset < d.capacity
}

func (d *OneNAND) blockRange(block uint32) (uint64, uint32, bool) {
	if block >= d.geometry.blockCount {
		return 0, 0, false
	}
	if block <= d.geometry.slcBoundary {
		return uint64(block) * uint64(d.geometry.slcBlockSize), d.geometry.slcBlockSize, true
	}
	slcBlocks := uint64(d.geometry.slcBoundary) + 1
	offset := slcBlocks*uint64(d.geometry.slcBlockSize) +
		(uint64(block)-slcBlocks)*uint64(d.geometry.mlcBlockSize)
	return offset, d.geometry.mlcBlockSize, true
}

func (d *OneNAND) eraseMainBlock(block uint32) bool {
	if d.writable == nil {
		return false
	}
	offset, size, ok := d.blockRange(block)
	storageBlockSize := d.writable.BlockSize()
	if !ok || storageBlockSize == 0 || offset%uint64(storageBlockSize) != 0 ||
		size%storageBlockSize != 0 {
		return false
	}
	first := uint32(offset / uint64(storageBlockSize))
	for index := uint32(0); index < size/storageBlockSize; index++ {
		if d.writable.EraseBlock(first+index) != nil {
			return false
		}
	}
	return true
}

func (d *OneNAND) eraseSpareBlock(block uint32) error {
	if ranged, ok := d.spare.(NANDSpareRangeStorage); ok {
		offset, size, valid := d.blockRange(block)
		if !valid {
			return ErrFlashBounds
		}
		return ranged.EraseSparePages(
			offset/uint64(d.geometry.pageSize),
			uint64(size/d.geometry.pageSize),
		)
	}
	return d.spare.EraseSpareBlock(block)
}

func (d *OneNAND) selectedBlock() (uint32, bool) {
	raw := uint32(d.addresses[0])
	block := raw & 0x0fff
	if raw&0x8000 != 0 {
		if d.densityMask == 0 {
			return 0, false
		}
		// DFS selects the second die, whose FBA starts again at zero. Some
		// Samsung drivers leave the global die bit set in FBA as well as
		// asserting DFS, while generic OneNAND drivers clear it. Decode both
		// encodings to the same linear block number.
		block = block&(d.densityMask-1) | d.densityMask
	}
	return block, block < d.blockCount()
}

func (d *OneNAND) mainBufferRange() (uint32, uint32, bool) {
	buffer := uint32(d.startBuffer>>8) & 0xf
	sectorsPerPage := d.geometry.pageSize / oneNANDSectorSize
	count := uint32(d.startBuffer) & (sectorsPerPage - 1)
	if count == 0 {
		count = sectorsPerPage
	}
	sector := buffer & 3
	if sector+count > sectorsPerPage {
		return 0, 0, false
	}
	base := uint32(0)
	if buffer&8 != 0 {
		base = 0x400 + (buffer>>2&1)*d.geometry.pageSize
	}
	return base + sector*oneNANDSectorSize, count * oneNANDSectorSize, true
}

func (d *OneNAND) spareBufferRange() (uint32, uint32, bool) {
	buffer := uint32(d.startBuffer>>8) & 0xf
	sectorsPerPage := d.geometry.pageSize / oneNANDSectorSize
	count := uint32(d.startBuffer) & (sectorsPerPage - 1)
	if count == 0 {
		count = sectorsPerPage
	}
	sector := buffer & 3
	if sector+count > sectorsPerPage {
		return 0, 0, false
	}
	base := uint32(0x10000)
	if buffer&8 != 0 {
		sparePageSize := sectorsPerPage * oneNANDSpareSectorSize
		base = 0x10020 + (buffer>>2&1)*sparePageSize
	}
	return base + sector*oneNANDSpareSectorSize, count * oneNANDSpareSectorSize, true
}

func (d *OneNAND) loadMain(offset uint64, size uint32, bufferOffset uint32) bool {
	if offset >= d.capacity || uint64(size) > d.capacity-offset ||
		uint64(bufferOffset)+uint64(size) > uint64(len(d.bufferRAM)) {
		return false
	}
	target := d.bufferRAM[bufferOffset : bufferOffset+size]
	count, err := d.storage.ReadAt(target, int64(offset))
	return count == len(target) && (err == nil || errors.Is(err, io.EOF))
}

func (d *OneNAND) programMain(offset uint64, bufferOffset, size uint32) bool {
	if d.writable == nil || offset >= d.capacity || uint64(size) > d.capacity-offset ||
		uint64(bufferOffset)+uint64(size) > uint64(len(d.bufferRAM)) {
		return false
	}
	effective := make([]byte, size)
	count, err := d.storage.ReadAt(effective, int64(offset))
	if count != len(effective) || err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	for index := range effective {
		effective[index] &= d.bufferRAM[bufferOffset+uint32(index)]
	}
	return d.writable.ProgramAt(effective, int64(offset)) == nil
}

func (d *OneNAND) loadSpare(offset uint64) bool {
	page, pageOffset, bufferOffset, size, ok := d.spareTransfer(offset)
	if !ok {
		return false
	}
	target := d.bufferRAM[bufferOffset : bufferOffset+size]
	if d.spare == nil {
		for index := range target {
			target[index] = 0xff
		}
		return true
	}
	pageData := make([]byte, d.spare.SparePageSize())
	if d.spare.ReadSparePage(pageData, page) != nil {
		return false
	}
	copy(target, pageData[pageOffset:pageOffset+size])
	return true
}

func (d *OneNAND) programSpare(offset uint64) bool {
	page, pageOffset, bufferOffset, size, ok := d.spareTransfer(offset)
	if !ok {
		return false
	}
	source := d.bufferRAM[bufferOffset : bufferOffset+size]
	if d.spare == nil {
		return allBytes(source, 0xff)
	}
	pageData := make([]byte, d.spare.SparePageSize())
	for index := range pageData {
		pageData[index] = 0xff
	}
	copy(pageData[pageOffset:pageOffset+size], source)
	return d.spare.ProgramSparePage(pageData, page) == nil
}

func (d *OneNAND) spareTransfer(offset uint64) (
	page uint64,
	pageOffset uint32,
	bufferOffset uint32,
	size uint32,
	ok bool,
) {
	bufferOffset, size, ok = d.spareBufferRange()
	sector := uint32(offset % uint64(d.geometry.pageSize) / oneNANDSectorSize)
	pageOffset = sector * oneNANDSpareSectorSize
	sparePageSize := d.geometry.pageSize / oneNANDSectorSize * oneNANDSpareSectorSize
	if !ok || pageOffset+size > sparePageSize {
		return 0, 0, 0, 0, false
	}
	return offset / uint64(d.geometry.pageSize), pageOffset, bufferOffset, size, true
}

func (d *OneNAND) blockCount() uint32 {
	return d.geometry.blockCount
}

func allBytes(data []byte, value byte) bool {
	for _, candidate := range data {
		if candidate != value {
			return false
		}
	}
	return true
}

func (d *OneNAND) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("ONND")
	_ = binary.Write(&output, binary.LittleEndian, uint32(oneNANDStateVersion))
	_ = binary.Write(&output, binary.LittleEndian, d.manufacturerID)
	_ = binary.Write(&output, binary.LittleEndian, d.deviceID)
	_ = binary.Write(&output, binary.LittleEndian, d.versionID)
	_ = binary.Write(&output, binary.LittleEndian, d.technologyID)
	_ = binary.Write(&output, binary.LittleEndian, d.capacity)
	geometry := [6]uint32{
		d.geometry.pageSize, d.geometry.blockCount, d.geometry.slcBoundary,
		d.geometry.slcBlockSize, d.geometry.mlcBlockSize, 0,
	}
	if d.geometry.flex {
		geometry[5] = 1
	}
	_ = binary.Write(&output, binary.LittleEndian, geometry)
	_ = binary.Write(&output, binary.LittleEndian, d.addresses)
	for _, value := range []uint16{
		d.startBuffer, d.command, d.systemConfig1, d.systemConfig2,
		d.controllerStatus, d.interruptStatus, d.unlockStart, d.unlockEnd, d.writeProtect,
	} {
		_ = binary.Write(&output, binary.LittleEndian, value)
	}
	flags := [4]byte{}
	if d.bootCycle {
		flags[0] = 1
	}
	if d.otpMode {
		flags[1] = 1
	}
	output.Write(flags[:])
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.bufferRAM)))
	output.Write(d.bufferRAM)
	return output.Bytes(), nil
}

func (d *OneNAND) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version uint32
	var manufacturerID, deviceID, versionID, technologyID uint16
	var capacity uint64
	var addresses [8]uint16
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "ONND" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version < 1 || version > oneNANDStateVersion ||
		binary.Read(reader, binary.LittleEndian, &manufacturerID) != nil || manufacturerID != d.manufacturerID ||
		binary.Read(reader, binary.LittleEndian, &deviceID) != nil || deviceID != d.deviceID ||
		binary.Read(reader, binary.LittleEndian, &versionID) != nil || versionID != d.versionID ||
		binary.Read(reader, binary.LittleEndian, &technologyID) != nil || technologyID != d.technologyID ||
		binary.Read(reader, binary.LittleEndian, &capacity) != nil || capacity != d.capacity {
		return ErrInvalidState
	}
	if version >= 3 {
		var geometry [6]uint32
		flex := uint32(0)
		if d.geometry.flex {
			flex = 1
		}
		want := [6]uint32{
			d.geometry.pageSize, d.geometry.blockCount, d.geometry.slcBoundary,
			d.geometry.slcBlockSize, d.geometry.mlcBlockSize, flex,
		}
		if binary.Read(reader, binary.LittleEndian, &geometry) != nil || geometry != want {
			return ErrInvalidState
		}
	} else if d.geometry.flex {
		return ErrInvalidState
	}
	if binary.Read(reader, binary.LittleEndian, &addresses) != nil {
		return ErrInvalidState
	}
	registers := make([]uint16, 9)
	for index := range registers {
		if binary.Read(reader, binary.LittleEndian, &registers[index]) != nil {
			return ErrInvalidState
		}
	}
	var flags [4]byte
	var bufferSize uint32
	if _, err := io.ReadFull(reader, flags[:]); err != nil || flags[0] > 1 || flags[1] > 1 ||
		(version == 1 && flags[1] != 0) || flags[2] != 0 || flags[3] != 0 ||
		binary.Read(reader, binary.LittleEndian, &bufferSize) != nil ||
		bufferSize != uint32(len(d.bufferRAM)) || reader.Len() != len(d.bufferRAM) {
		return ErrInvalidState
	}
	bufferRAM := make([]byte, len(d.bufferRAM))
	if _, err := io.ReadFull(reader, bufferRAM); err != nil {
		return ErrInvalidState
	}
	d.addresses = addresses
	d.startBuffer, d.command = registers[0], registers[1]
	d.systemConfig1, d.systemConfig2 = registers[2], registers[3]
	d.controllerStatus, d.interruptStatus = registers[4], registers[5]
	d.unlockStart, d.unlockEnd, d.writeProtect = registers[6], registers[7], registers[8]
	d.bootCycle = flags[0] != 0
	d.otpMode = flags[1] != 0
	copy(d.bufferRAM, bufferRAM)
	return nil
}

var (
	_ Device         = (*OneNAND)(nil)
	_ StatefulDevice = (*OneNAND)(nil)
)
