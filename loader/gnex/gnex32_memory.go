package gnex

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type gnex32SymbolCell struct {
	data []byte
}

// GNEX32SymbolMemory is independent writable runtime storage initialized from
// a detached copy of the image descriptors.
type GNEX32SymbolMemory struct {
	symbols []gnex32SymbolCell
}

// GNEX32SymbolMemoryState is a detached copy of every runtime symbol. The
// descriptor layout remains owned by the source image and is validated when a
// state is restored.
type GNEX32SymbolMemoryState struct {
	Symbols [][]byte
}

// NewSymbolMemory creates fresh per-run 32-bit symbol storage. Raw image
// initializers remain untouched when a guest symbol changes.
func (image GNEX32Image) NewSymbolMemory() (*GNEX32SymbolMemory, error) {
	state := &GNEX32SymbolMemory{symbols: make([]gnex32SymbolCell, len(image.Symbols))}
	for index, symbol := range image.Symbols {
		if len(symbol.Data) != 4*int(symbol.Words) {
			return nil, fmt.Errorf("gnex: GNEX32 symbol %d initializer length mismatch", index)
		}
		switch symbol.Flags {
		case GNEX32SymbolWritable, GNEX32SymbolImageInitialized:
			state.symbols[index].data = bytes.Clone(symbol.Data)
		default:
			return nil, fmt.Errorf("gnex: GNEX32 symbol %d has unsupported flags %#x", index, symbol.Flags)
		}
	}
	return state, nil
}

func (state *GNEX32SymbolMemory) word(symbol, element int) (*gnex32SymbolCell, int, error) {
	if state == nil || symbol < 0 || symbol >= len(state.symbols) {
		return nil, 0, fmt.Errorf("gnex: GNEX32 symbol index %d out of range", symbol)
	}
	cell := &state.symbols[symbol]
	if element < 0 || element >= len(cell.data)/4 {
		return nil, 0, fmt.Errorf("gnex: GNEX32 symbol %d element %d out of range", symbol, element)
	}
	return cell, element * 4, nil
}

// ReadWord reads a little-endian 32-bit element from one symbol.
func (state *GNEX32SymbolMemory) ReadWord(symbol, element int) (uint32, error) {
	cell, offset, err := state.word(symbol, element)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(cell.data[offset:]), nil
}

// WriteWord changes a runtime symbol while retaining the image initializer.
func (state *GNEX32SymbolMemory) WriteWord(symbol, element int, value uint32) error {
	cell, offset, err := state.word(symbol, element)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(cell.data[offset:], value)
	return nil
}

// ReadBytes copies an arbitrary little-endian byte range from one symbol. The
// offset is relative to the first byte of element zero.
func (state *GNEX32SymbolMemory) ReadBytes(symbol, offset, size int) ([]byte, error) {
	if state == nil || symbol < 0 || symbol >= len(state.symbols) {
		return nil, fmt.Errorf("gnex: GNEX32 symbol index %d out of range", symbol)
	}
	data := state.symbols[symbol].data
	if offset < 0 || size < 0 || offset > len(data) || size > len(data)-offset {
		return nil, fmt.Errorf("gnex: GNEX32 symbol %d byte range %d:%d out of range", symbol, offset, offset+size)
	}
	return bytes.Clone(data[offset : offset+size]), nil
}

// WriteBytes atomically replaces an arbitrary byte range in one symbol.
func (state *GNEX32SymbolMemory) WriteBytes(symbol, offset int, value []byte) error {
	if state == nil || symbol < 0 || symbol >= len(state.symbols) {
		return fmt.Errorf("gnex: GNEX32 symbol index %d out of range", symbol)
	}
	data := state.symbols[symbol].data
	if offset < 0 || offset > len(data) || len(value) > len(data)-offset {
		return fmt.Errorf("gnex: GNEX32 symbol %d byte range %d:%d out of range", symbol, offset, offset+len(value))
	}
	copy(data[offset:], value)
	return nil
}

func (state *GNEX32SymbolMemory) Snapshot() GNEX32SymbolMemoryState {
	result := GNEX32SymbolMemoryState{Symbols: make([][]byte, len(state.symbols))}
	for index := range state.symbols {
		result.Symbols[index] = bytes.Clone(state.symbols[index].data)
	}
	return result
}

// Restore replaces symbol values only after the complete shape has been
// validated, so malformed state cannot partially mutate a running guest.
func (state *GNEX32SymbolMemory) Restore(saved GNEX32SymbolMemoryState) error {
	if state == nil || len(saved.Symbols) != len(state.symbols) {
		return fmt.Errorf("gnex: GNEX32 symbol state shape mismatch")
	}
	replacement := make([]gnex32SymbolCell, len(saved.Symbols))
	for index, data := range saved.Symbols {
		if len(data) != len(state.symbols[index].data) {
			return fmt.Errorf("gnex: GNEX32 symbol %d state length mismatch", index)
		}
		replacement[index].data = bytes.Clone(data)
	}
	state.symbols = replacement
	return nil
}
