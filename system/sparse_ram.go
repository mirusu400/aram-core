package system

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

const sparseRAMPageSize = uint32(4096)

type sparseRAM struct {
	pages   map[uint32][]byte
	nonzero map[uint32]uint32
}

func newSparseRAM() *sparseRAM {
	return &sparseRAM{
		pages:   make(map[uint32][]byte),
		nonzero: make(map[uint32]uint32),
	}
}

func (m *sparseRAM) read(offset uint32, destination []byte) {
	clear(destination)
	for len(destination) != 0 {
		pageIndex := offset / sparseRAMPageSize
		pageOffset := offset % sparseRAMPageSize
		count := min(len(destination), int(sparseRAMPageSize-pageOffset))
		if page := m.pages[pageIndex]; page != nil {
			copy(destination[:count], page[pageOffset:uint32(pageOffset)+uint32(count)])
		}
		offset += uint32(count)
		destination = destination[count:]
	}
}

func (m *sparseRAM) write(offset uint32, source []byte) {
	for len(source) != 0 {
		pageIndex := offset / sparseRAMPageSize
		pageOffset := offset % sparseRAMPageSize
		count := min(len(source), int(sparseRAMPageSize-pageOffset))
		page := m.pages[pageIndex]
		if page == nil {
			if allZero(source[:count]) {
				offset += uint32(count)
				source = source[count:]
				continue
			}
			page = make([]byte, sparseRAMPageSize)
			m.pages[pageIndex] = page
		}
		nonzero := m.nonzero[pageIndex]
		for index, value := range source[:count] {
			position := int(pageOffset) + index
			old := page[position]
			if old == 0 && value != 0 {
				nonzero++
			} else if old != 0 && value == 0 {
				nonzero--
			}
			page[position] = value
		}
		if nonzero == 0 {
			delete(m.pages, pageIndex)
			delete(m.nonzero, pageIndex)
		} else {
			m.nonzero[pageIndex] = nonzero
		}
		offset += uint32(count)
		source = source[count:]
	}
}

func (m *sparseRAM) reset() {
	m.pages = make(map[uint32][]byte)
	m.nonzero = make(map[uint32]uint32)
}

func (m *sparseRAM) saveState(size uint32) ([]byte, error) {
	indices := make([]uint32, 0, len(m.pages))
	for index := range m.pages {
		if uint64(index)*uint64(sparseRAMPageSize) >= uint64(size) ||
			len(m.pages[index]) != int(sparseRAMPageSize) || m.nonzero[index] == 0 {
			return nil, fmt.Errorf("invalid sparse RAM page %d", index)
		}
		indices = append(indices, index)
	}
	sort.Slice(indices, func(left, right int) bool { return indices[left] < indices[right] })
	stateSize := uint64(16) + uint64(len(indices))*uint64(4+sparseRAMPageSize)
	if stateSize > uint64(^uint32(0)) || stateSize > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("sparse RAM state is too large")
	}
	state := make([]byte, int(stateSize))
	copy(state, "SPRM")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	binary.LittleEndian.PutUint32(state[8:12], sparseRAMPageSize)
	binary.LittleEndian.PutUint32(state[12:16], uint32(len(indices)))
	position := 16
	for _, index := range indices {
		binary.LittleEndian.PutUint32(state[position:position+4], index)
		position += 4
		copy(state[position:position+int(sparseRAMPageSize)], m.pages[index])
		position += int(sparseRAMPageSize)
	}
	return state, nil
}

func decodeSparseRAMState(size uint32, state []byte) (*sparseRAM, error) {
	if len(state) < 16 || string(state[:4]) != "SPRM" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 ||
		binary.LittleEndian.Uint32(state[8:12]) != sparseRAMPageSize {
		return nil, ErrInvalidState
	}
	pageCount := binary.LittleEndian.Uint32(state[12:16])
	wantSize := uint64(16) + uint64(pageCount)*uint64(4+sparseRAMPageSize)
	if wantSize != uint64(len(state)) {
		return nil, ErrInvalidState
	}
	decoded := newSparseRAM()
	position := 16
	for pageNumber := uint32(0); pageNumber < pageCount; pageNumber++ {
		pageIndex := binary.LittleEndian.Uint32(state[position : position+4])
		position += 4
		pageEnd := uint64(pageIndex+1) * uint64(sparseRAMPageSize)
		if uint64(pageIndex)*uint64(sparseRAMPageSize) >= uint64(size) ||
			pageEnd < uint64(pageIndex)*uint64(sparseRAMPageSize) ||
			decoded.pages[pageIndex] != nil {
			return nil, ErrInvalidState
		}
		page := append([]byte(nil), state[position:position+int(sparseRAMPageSize)]...)
		position += int(sparseRAMPageSize)
		nonzero := uint32(len(page) - bytes.Count(page, []byte{0}))
		if nonzero == 0 {
			return nil, ErrInvalidState
		}
		if pageEnd > uint64(size) {
			validBytes := uint64(size) - uint64(pageIndex)*uint64(sparseRAMPageSize)
			if !allZero(page[validBytes:]) {
				return nil, ErrInvalidState
			}
		}
		decoded.pages[pageIndex] = page
		decoded.nonzero[pageIndex] = nonzero
	}
	return decoded, nil
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
