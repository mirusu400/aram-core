package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

const sparseNANDSpareStateVersion = 1

var ErrInvalidNANDSpare = errors.New("invalid NAND spare media")

// StatefulNANDSpareStorage is persistent OOB media shared by a flash
// interface and the machine's media/snapshot lifecycle.
type StatefulNANDSpareStorage interface {
	NANDSpareStorage
	Reset() error
	SaveState() ([]byte, error)
	LoadState([]byte) error
}

// NANDSpareRangeStorage erases a physical range expressed in page units. It
// lets variable-block devices such as Flex-OneNAND erase SLC and MLC blocks
// without forcing their OOB store to assume one uniform erase size.
type NANDSpareRangeStorage interface {
	EraseSparePages(firstPage, pageCount uint64) error
}

type SparseNANDSpareConfig struct {
	PageSize           uint32
	PageCount          uint64
	PagesPerEraseBlock uint64
	Identity           string
	InitialData        []FlashSeed
}

// SparseNANDSpare stores only OOB pages changed by the guest. Its immutable
// baseline may contain generated factory metadata, while all other pages read
// as erased bytes.
type SparseNANDSpare struct {
	pageSize           uint32
	pageCount          uint64
	pagesPerEraseBlock uint64
	identity           string
	baseline           ReadOnlyStorage
	pages              map[uint64][]byte
}

func NewSparseNANDSpare(config SparseNANDSpareConfig) (*SparseNANDSpare, error) {
	if config.PageSize < 2 || config.PageSize > 4<<10 ||
		config.PageSize&(config.PageSize-1) != 0 || config.PageCount == 0 ||
		config.PagesPerEraseBlock == 0 || config.PageCount%config.PagesPerEraseBlock != 0 ||
		config.PageCount > uint64(^uint32(0)) || !validFlashIdentity(config.Identity) {
		return nil, ErrInvalidNANDSpare
	}
	capacity := config.PageCount * uint64(config.PageSize)
	if capacity/config.PageCount != uint64(config.PageSize) || capacity > uint64(^uint32(0)) {
		return nil, ErrInvalidNANDSpare
	}
	baseline, err := NewCOWFlashWithCapacityAndSeeds(
		erasedFlashStorage{size: int64(capacity)},
		capacity,
		config.PageSize,
		config.Identity,
		config.InitialData,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidNANDSpare, err)
	}
	return &SparseNANDSpare{
		pageSize: config.PageSize, pageCount: config.PageCount,
		pagesPerEraseBlock: config.PagesPerEraseBlock,
		identity:           baseline.Identity(), baseline: baseline,
		pages: make(map[uint64][]byte),
	}, nil
}

func (s *SparseNANDSpare) Reset() error {
	return nil
}

func (s *SparseNANDSpare) SparePageSize() uint32 {
	return s.pageSize
}

func (s *SparseNANDSpare) ReadSparePage(destination []byte, page uint64) error {
	if uint32(len(destination)) != s.pageSize {
		return ErrInvalidNANDSpare
	}
	if page >= s.pageCount {
		return ErrFlashBounds
	}
	if stored, ok := s.pages[page]; ok {
		copy(destination, stored)
		return nil
	}
	count, err := s.baseline.ReadAt(destination, int64(page*uint64(s.pageSize)))
	if count != len(destination) || err != nil && !errors.Is(err, io.EOF) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func (s *SparseNANDSpare) ProgramSparePage(source []byte, page uint64) error {
	if uint32(len(source)) != s.pageSize {
		return ErrInvalidNANDSpare
	}
	current := make([]byte, s.pageSize)
	if err := s.ReadSparePage(current, page); err != nil {
		return err
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
		s.pages[page] = current
	}
	return nil
}

func (s *SparseNANDSpare) EraseSpareBlock(block uint32) error {
	firstPage := uint64(block) * s.pagesPerEraseBlock
	return s.EraseSparePages(firstPage, s.pagesPerEraseBlock)
}

func (s *SparseNANDSpare) EraseSparePages(firstPage, pageCount uint64) error {
	if firstPage >= s.pageCount || pageCount == 0 || pageCount > s.pageCount-firstPage {
		return ErrFlashBounds
	}
	erased := bytes.Repeat([]byte{0xff}, int(s.pageSize))
	for page := firstPage; page < firstPage+pageCount; page++ {
		s.pages[page] = append([]byte(nil), erased...)
	}
	return nil
}

func (s *SparseNANDSpare) SaveState() ([]byte, error) {
	pages := make([]uint64, 0, len(s.pages))
	for page := range s.pages {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(left, right int) bool { return pages[left] < pages[right] })
	var output bytes.Buffer
	output.WriteString("NSPR")
	_ = binary.Write(&output, binary.LittleEndian, uint32(sparseNANDSpareStateVersion))
	_ = binary.Write(&output, binary.LittleEndian, s.pageSize)
	_ = binary.Write(&output, binary.LittleEndian, s.pageCount)
	_ = binary.Write(&output, binary.LittleEndian, s.pagesPerEraseBlock)
	_ = binary.Write(&output, binary.LittleEndian, uint16(len(s.identity)))
	output.WriteString(s.identity)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(pages)))
	for _, page := range pages {
		_ = binary.Write(&output, binary.LittleEndian, page)
		output.Write(s.pages[page])
	}
	return output.Bytes(), nil
}

func (s *SparseNANDSpare) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version, pageSize uint32
	var pageCount, pagesPerEraseBlock uint64
	var identitySize uint16
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "NSPR" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != sparseNANDSpareStateVersion ||
		binary.Read(reader, binary.LittleEndian, &pageSize) != nil || pageSize != s.pageSize ||
		binary.Read(reader, binary.LittleEndian, &pageCount) != nil || pageCount != s.pageCount ||
		binary.Read(reader, binary.LittleEndian, &pagesPerEraseBlock) != nil ||
		pagesPerEraseBlock != s.pagesPerEraseBlock ||
		binary.Read(reader, binary.LittleEndian, &identitySize) != nil || int(identitySize) > reader.Len() {
		return ErrInvalidState
	}
	identity := make([]byte, identitySize)
	if _, err := io.ReadFull(reader, identity); err != nil || string(identity) != s.identity {
		return ErrInvalidState
	}
	var count uint32
	if binary.Read(reader, binary.LittleEndian, &count) != nil || uint64(count) > s.pageCount ||
		uint64(reader.Len()) != uint64(count)*(8+uint64(s.pageSize)) {
		return ErrInvalidState
	}
	pages := make(map[uint64][]byte, count)
	for index := uint32(0); index < count; index++ {
		var page uint64
		data := make([]byte, s.pageSize)
		if binary.Read(reader, binary.LittleEndian, &page) != nil || page >= s.pageCount {
			return ErrInvalidState
		}
		if _, duplicate := pages[page]; duplicate {
			return ErrInvalidState
		}
		if _, err := io.ReadFull(reader, data); err != nil {
			return ErrInvalidState
		}
		pages[page] = data
	}
	s.pages = pages
	return nil
}

var (
	_ StatefulNANDSpareStorage = (*SparseNANDSpare)(nil)
	_ NANDSpareRangeStorage    = (*SparseNANDSpare)(nil)
)
