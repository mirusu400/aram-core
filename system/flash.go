package system

import (
	"bytes"
	"encoding/binary"
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

// COWFlash provides byte-addressable NAND programming semantics over an
// immutable firmware image. Dirty erase blocks live only in the overlay;
// FactoryReset discards them and reveals the original image again.
type COWFlash struct {
	mu        sync.Mutex
	base      ReadOnlyStorage
	size      uint64
	blockSize uint32
	identity  string
	blocks    map[uint32][]byte
}

func NewCOWFlash(base ReadOnlyStorage, blockSize uint32, identity string) (*COWFlash, error) {
	if base == nil || base.Size() <= 0 || uint64(base.Size()) > uint64(^uint32(0)) ||
		blockSize == 0 || blockSize > 16<<20 || blockSize&(blockSize-1) != 0 ||
		uint64(base.Size())%uint64(blockSize) != 0 || !validFlashIdentity(identity) {
		return nil, ErrInvalidFlash
	}
	return &COWFlash{
		base: base, size: uint64(base.Size()), blockSize: blockSize,
		identity: identity, blocks: make(map[uint32][]byte),
	}, nil
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
	read, err := f.base.ReadAt(output, offset)
	if read != count {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return read, err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return read, err
	}
	f.copyOverlay(output, uint64(offset))
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
	pending := make(map[uint32][]byte, int(last-first)+1)
	for index := first; index <= last; index++ {
		block, err := f.cloneBlock(index)
		if err != nil {
			return err
		}
		pending[index] = block
	}
	for index, value := range source {
		address := start + uint64(index)
		blockIndex := uint32(address / uint64(f.blockSize))
		blockOffset := uint32(address % uint64(f.blockSize))
		current := pending[blockIndex][blockOffset]
		if current&value != value {
			return fmt.Errorf("%w at 0x%x", ErrFlashProgram, address)
		}
		pending[blockIndex][blockOffset] = value
	}
	for index, block := range pending {
		f.blocks[index] = block
	}
	return nil
}

func (f *COWFlash) EraseBlock(index uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if uint64(index) >= f.blockCount() {
		return ErrFlashBounds
	}
	block := make([]byte, f.blockSize)
	for position := range block {
		block[position] = 0xff
	}
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

func (f *COWFlash) cloneBlock(index uint32) ([]byte, error) {
	if block, ok := f.blocks[index]; ok {
		return append([]byte(nil), block...), nil
	}
	block := make([]byte, f.blockSize)
	offset := int64(uint64(index) * uint64(f.blockSize))
	count, err := f.base.ReadAt(block, offset)
	if count != len(block) || (err != nil && !errors.Is(err, io.EOF)) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return block, nil
}

func (f *COWFlash) copyOverlay(destination []byte, start uint64) {
	end := start + uint64(len(destination))
	first := uint32(start / uint64(f.blockSize))
	last := uint32((end - 1) / uint64(f.blockSize))
	for index := first; index <= last; index++ {
		block, ok := f.blocks[index]
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

var _ io.ReaderAt = (*COWFlash)(nil)
