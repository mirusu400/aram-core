package system

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

var (
	ErrInvalidFlash      = errors.New("invalid flash geometry")
	ErrFlashBounds       = errors.New("flash access out of bounds")
	ErrFlashProgram      = errors.New("flash programming requires erase")
	ErrInvalidFlashState = errors.New("invalid flash overlay state")
)

type ReadOnlyStorage interface {
	io.ReaderAt
	Size() int64
}

// FlashSeed describes generated, non-firmware bytes present on a newly
// provisioned flash device. Seeds are part of the immutable factory baseline:
// guest writes remain copy-on-write, and FactoryReset reveals the seeds again.
type FlashSeed struct {
	Offset uint64
	Data   []byte
}

// COWFlash provides byte-addressable NAND programming semantics over an
// immutable factory baseline. Dirty erase blocks live only in the overlay;
// FactoryReset discards them and reveals the firmware image plus any generated
// provisioning metadata again.
type COWFlash struct {
	mu        sync.Mutex
	base      ReadOnlyStorage
	size      uint64
	blockSize uint32
	identity  string
	seeds     map[uint32][]byte
	blocks    map[uint32][]byte
}

func NewCOWFlash(base ReadOnlyStorage, blockSize uint32, identity string) (*COWFlash, error) {
	if base == nil || base.Size() <= 0 {
		return nil, ErrInvalidFlash
	}
	return NewCOWFlashWithCapacity(base, uint64(base.Size()), blockSize, identity)
}

// NewCOWFlashWithCapacity creates a writable physical flash view over a
// possibly shorter logical image. Bytes after the represented image are
// erased (0xff) until programmed, which lets normalized downloader packages
// omit unused NAND tail blocks without making those physical blocks
// unwritable.
func NewCOWFlashWithCapacity(
	base ReadOnlyStorage,
	capacity uint64,
	blockSize uint32,
	identity string,
) (*COWFlash, error) {
	return NewCOWFlashWithCapacityAndSeeds(base, capacity, blockSize, identity, nil)
}

// NewCOWFlashWithCapacityAndSeeds creates a writable flash view whose factory
// baseline includes generated provisioning metadata in addition to the
// immutable firmware image. Seed order does not affect the derived identity.
func NewCOWFlashWithCapacityAndSeeds(
	base ReadOnlyStorage,
	capacity uint64,
	blockSize uint32,
	identity string,
	seeds []FlashSeed,
) (*COWFlash, error) {
	if base == nil || base.Size() <= 0 || capacity < uint64(base.Size()) ||
		capacity > uint64(^uint32(0)) || blockSize == 0 || blockSize > 16<<20 ||
		blockSize&(blockSize-1) != 0 || capacity%uint64(blockSize) != 0 ||
		!validFlashIdentity(identity) {
		return nil, ErrInvalidFlash
	}
	normalizedSeeds := make([]FlashSeed, len(seeds))
	for index, seed := range seeds {
		if len(seed.Data) == 0 || seed.Offset >= capacity ||
			uint64(len(seed.Data)) > capacity-seed.Offset {
			return nil, ErrInvalidFlash
		}
		normalizedSeeds[index] = FlashSeed{
			Offset: seed.Offset,
			Data:   append([]byte(nil), seed.Data...),
		}
	}
	sort.Slice(normalizedSeeds, func(left, right int) bool {
		return normalizedSeeds[left].Offset < normalizedSeeds[right].Offset
	})
	for index := 1; index < len(normalizedSeeds); index++ {
		previous := normalizedSeeds[index-1]
		if previous.Offset+uint64(len(previous.Data)) > normalizedSeeds[index].Offset {
			return nil, ErrInvalidFlash
		}
	}
	flash := &COWFlash{
		base: base, size: capacity, blockSize: blockSize,
		identity: identity, seeds: make(map[uint32][]byte), blocks: make(map[uint32][]byte),
	}
	for _, seed := range normalizedSeeds {
		for position, value := range seed.Data {
			address := seed.Offset + uint64(position)
			blockIndex := uint32(address / uint64(blockSize))
			block, ok := flash.seeds[blockIndex]
			if !ok {
				var err error
				block, err = flash.cloneBaseBlock(blockIndex)
				if err != nil {
					return nil, ErrInvalidFlash
				}
				flash.seeds[blockIndex] = block
			}
			blockOffset := uint32(address % uint64(blockSize))
			if block[blockOffset]&value != value {
				return nil, ErrInvalidFlash
			}
			block[blockOffset] = value
		}
	}
	if len(normalizedSeeds) != 0 {
		flash.identity = seededFlashIdentity(identity, capacity, normalizedSeeds)
	}
	return flash, nil
}

func (f *COWFlash) Size() int64 {
	return int64(f.size)
}

func (f *COWFlash) BlockSize() uint32 {
	return f.blockSize
}

func (f *COWFlash) Identity() string {
	return f.identity
}

func (f *COWFlash) DirtyBlocks() []uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	indices := make([]uint32, 0, len(f.blocks))
	for index := range f.blocks {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(left, right int) bool { return indices[left] < indices[right] })
	return indices
}

func (f *COWFlash) ReadAt(destination []byte, offset int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count, partial, err := f.resolveRange(offset, len(destination))
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	output := destination[:count]
	for index := range output {
		output[index] = 0xff
	}
	baseSize := uint64(f.base.Size())
	start := uint64(offset)
	if start < baseSize {
		baseCount := int(min(uint64(count), baseSize-start))
		read, readErr := f.base.ReadAt(output[:baseCount], offset)
		if read != baseCount {
			if readErr == nil {
				readErr = io.ErrUnexpectedEOF
			}
			return read, readErr
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return read, readErr
		}
	}
	f.copyBlocks(output, start, f.seeds)
	f.copyBlocks(output, start, f.blocks)
	if partial {
		return count, io.EOF
	}
	return count, nil
}

// ProgramAt applies NAND's one-way 1-to-0 programming rule atomically across
// every touched block. Call EraseBlock before attempting to restore any bit.
func (f *COWFlash) ProgramAt(source []byte, offset int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	count, partial, err := f.resolveRange(offset, len(source))
	if err != nil {
		return err
	}
	if partial || count != len(source) {
		return ErrFlashBounds
	}
	if count == 0 {
		return nil
	}
	start := uint64(offset)
	first := uint32(start / uint64(f.blockSize))
	last := uint32((start + uint64(count) - 1) / uint64(f.blockSize))
	if first == last {
		block, dirty := f.blocks[first]
		if !dirty {
			block, err = f.cloneFactoryBlock(first)
			if err != nil {
				return err
			}
		}
		blockOffset := int(start % uint64(f.blockSize))
		target := block[blockOffset : blockOffset+count]
		for index, value := range source {
			if target[index]&value != value {
				return fmt.Errorf("%w at 0x%x", ErrFlashProgram, start+uint64(index))
			}
		}
		copy(target, source)
		if !dirty {
			f.blocks[first] = block
		}
		return nil
	}

	blocks := make([][]byte, int(last-first)+1)
	for index := first; index <= last; index++ {
		block, dirty := f.blocks[index]
		if !dirty {
			block, err = f.cloneFactoryBlock(index)
		}
		if err != nil {
			return err
		}
		blocks[index-first] = block
	}
	for position := 0; position < count; {
		address := start + uint64(position)
		blockIndex := uint32(address / uint64(f.blockSize))
		blockOffset := int(address % uint64(f.blockSize))
		chunkSize := min(count-position, int(f.blockSize)-blockOffset)
		target := blocks[blockIndex-first][blockOffset : blockOffset+chunkSize]
		for index, value := range source[position : position+chunkSize] {
			if target[index]&value != value {
				return fmt.Errorf("%w at 0x%x", ErrFlashProgram, address+uint64(index))
			}
		}
		position += chunkSize
	}
	for position := 0; position < count; {
		address := start + uint64(position)
		blockIndex := uint32(address / uint64(f.blockSize))
		blockOffset := int(address % uint64(f.blockSize))
		chunkSize := min(count-position, int(f.blockSize)-blockOffset)
		copy(
			blocks[blockIndex-first][blockOffset:blockOffset+chunkSize],
			source[position:position+chunkSize],
		)
		position += chunkSize
	}
	for index, block := range blocks {
		blockIndex := first + uint32(index)
		if _, dirty := f.blocks[blockIndex]; !dirty {
			f.blocks[blockIndex] = block
		}
	}
	return nil
}

func (f *COWFlash) EraseBlock(index uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if uint64(index) >= f.blockCount() {
		return ErrFlashBounds
	}
	block := bytes.Repeat([]byte{0xff}, int(f.blockSize))
	f.blocks[index] = block
	return nil
}

func (f *COWFlash) FactoryReset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks = make(map[uint32][]byte)
}

func (f *COWFlash) SaveState() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	indices := make([]uint32, 0, len(f.blocks))
	for index := range f.blocks {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(left, right int) bool { return indices[left] < indices[right] })
	var output bytes.Buffer
	output.WriteString("ARFW")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_ = binary.Write(&output, binary.LittleEndian, f.size)
	_ = binary.Write(&output, binary.LittleEndian, f.blockSize)
	_ = binary.Write(&output, binary.LittleEndian, uint16(len(f.identity)))
	output.WriteString(f.identity)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(indices)))
	for _, index := range indices {
		_ = binary.Write(&output, binary.LittleEndian, index)
		output.Write(f.blocks[index])
	}
	return output.Bytes(), nil
}

func (f *COWFlash) LoadState(state []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	reader := bytes.NewReader(state)
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "ARFW" {
		return ErrInvalidFlashState
	}
	var version uint32
	var size uint64
	var blockSize uint32
	var identityLength uint16
	if binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 ||
		binary.Read(reader, binary.LittleEndian, &size) != nil || size != f.size ||
		binary.Read(reader, binary.LittleEndian, &blockSize) != nil || blockSize != f.blockSize ||
		binary.Read(reader, binary.LittleEndian, &identityLength) != nil ||
		int(identityLength) > reader.Len() {
		return ErrInvalidFlashState
	}
	identity := make([]byte, identityLength)
	if _, err := io.ReadFull(reader, identity); err != nil || string(identity) != f.identity {
		return ErrInvalidFlashState
	}
	var count uint32
	if binary.Read(reader, binary.LittleEndian, &count) != nil || uint64(count) > f.blockCount() {
		return ErrInvalidFlashState
	}
	expected := uint64(count) * (4 + uint64(f.blockSize))
	if expected != uint64(reader.Len()) {
		return ErrInvalidFlashState
	}
	blocks := make(map[uint32][]byte, count)
	for component := uint32(0); component < count; component++ {
		var index uint32
		if binary.Read(reader, binary.LittleEndian, &index) != nil ||
			uint64(index) >= f.blockCount() {
			return ErrInvalidFlashState
		}
		if _, duplicate := blocks[index]; duplicate {
			return ErrInvalidFlashState
		}
		block := make([]byte, f.blockSize)
		if _, err := io.ReadFull(reader, block); err != nil {
			return ErrInvalidFlashState
		}
		blocks[index] = block
	}
	f.blocks = blocks
	return nil
}

func (f *COWFlash) resolveRange(offset int64, length int) (int, bool, error) {
	if offset < 0 {
		return 0, false, ErrFlashBounds
	}
	if length == 0 {
		return 0, false, nil
	}
	start := uint64(offset)
	if start >= f.size {
		return 0, false, ErrFlashBounds
	}
	available := f.size - start
	if uint64(length) > available {
		return int(available), true, nil
	}
	return length, false, nil
}

func (f *COWFlash) cloneFactoryBlock(index uint32) ([]byte, error) {
	if block, ok := f.seeds[index]; ok {
		return append([]byte(nil), block...), nil
	}
	return f.cloneBaseBlock(index)
}

func (f *COWFlash) cloneBaseBlock(index uint32) ([]byte, error) {
	block := bytes.Repeat([]byte{0xff}, int(f.blockSize))
	offset := int64(uint64(index) * uint64(f.blockSize))
	baseSize := uint64(f.base.Size())
	if uint64(offset) < baseSize {
		baseCount := int(min(uint64(len(block)), baseSize-uint64(offset)))
		count, err := f.base.ReadAt(block[:baseCount], offset)
		if count != baseCount || err != nil && !errors.Is(err, io.EOF) {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
	return block, nil
}

func (f *COWFlash) copyBlocks(destination []byte, start uint64, blocks map[uint32][]byte) {
	end := start + uint64(len(destination))
	first := uint32(start / uint64(f.blockSize))
	last := uint32((end - 1) / uint64(f.blockSize))
	for index := first; index <= last; index++ {
		block, ok := blocks[index]
		if !ok {
			continue
		}
		blockStart := uint64(index) * uint64(f.blockSize)
		copyStart := max(start, blockStart)
		copyEnd := min(end, blockStart+uint64(f.blockSize))
		copy(
			destination[copyStart-start:copyEnd-start],
			block[copyStart-blockStart:copyEnd-blockStart],
		)
	}
}

func (f *COWFlash) blockCount() uint64 {
	return f.size / uint64(f.blockSize)
}

func validFlashIdentity(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 255 && strings.IndexByte(value, 0) < 0
}

func seededFlashIdentity(identity string, capacity uint64, seeds []FlashSeed) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, "aram-flash-seeds-v1\x00")
	_, _ = io.WriteString(hash, identity)
	_, _ = hash.Write([]byte{0})
	var scalar [8]byte
	binary.LittleEndian.PutUint64(scalar[:], capacity)
	_, _ = hash.Write(scalar[:])
	for _, seed := range seeds {
		binary.LittleEndian.PutUint64(scalar[:], seed.Offset)
		_, _ = hash.Write(scalar[:])
		binary.LittleEndian.PutUint64(scalar[:], uint64(len(seed.Data)))
		_, _ = hash.Write(scalar[:])
		_, _ = hash.Write(seed.Data)
	}
	return "flash-seeds-v1:" + hex.EncodeToString(hash.Sum(nil))
}

var _ io.ReaderAt = (*COWFlash)(nil)
