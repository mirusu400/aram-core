package gnex

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type gnex32SymbolCell struct {
	data []byte
}

// GNEX32SymbolMemory is independent runtime storage initialized from an image.
// Both observed descriptor flags are writable at runtime. Flag 0x100 carries
// initialized values in this image, including symbol 0xe4, which the guest
// assigns during the title's input handler.
type GNEX32SymbolMemory struct {
	symbols []gnex32SymbolCell
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
		case 1, 0x100:
		default:
			return nil, fmt.Errorf("gnex: GNEX32 symbol %d has unsupported flags %#x", index, symbol.Flags)
		}
		state.symbols[index].data = bytes.Clone(symbol.Data)
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
