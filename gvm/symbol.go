package gvm

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidSymbol       = errors.New("gvm: invalid symbol index")
	ErrInvalidElement      = errors.New("gvm: invalid symbol element")
	ErrInvalidSymbolRegion = errors.New("gvm: invalid symbol region")
)

// SymbolRegion explicitly binds one symbol to a mutable byte span. A nonnil
// ProgramOffset selects [offset, offset+Length) in the whole program and requires
// Initial==nil. A nil ProgramOffset selects independent RAM, requires Length to
// equal len(Initial), and copies Initial. Empty regions are allowed but cannot
// satisfy a two-byte store. This is a host binding, not an SGS symbol record.
type SymbolRegion struct {
	ProgramOffset *uint32
	Length        uint32
	Initial       []byte
}

// NewWithSymbols validates every binding before allocating VM storage. It
// clones program once and then builds program-backed views into that clone,
// preserving overlapping aliases and making stores visible to instruction
// fetch. Each RAM region is cloned separately, even if Initial slices overlap.
// The caller supplies all symbols. No reserved entries or raw pointers are
// inferred, and no image parsing or initialization services are performed.
func NewWithSymbols(program []byte, entry uint32, symbols []SymbolRegion) (*VM, error) {
	if uint64(entry) >= uint64(len(program)) {
		return nil, fmt.Errorf("%w: entry offset %d", ErrInvalidTarget, entry)
	}
	for i, region := range symbols {
		if region.ProgramOffset != nil {
			end := uint64(*region.ProgramOffset) + uint64(region.Length)
			if region.Initial != nil || end > uint64(len(program)) {
				return nil, fmt.Errorf("%w: symbol %d program span", ErrInvalidSymbolRegion, i)
			}
		} else if uint64(region.Length) != uint64(len(region.Initial)) {
			return nil, fmt.Errorf("%w: symbol %d RAM length", ErrInvalidSymbolRegion, i)
		}
	}
	v := New(program)
	v.pc = int(entry)
	v.symbols = make([][]byte, len(symbols))
	for i, region := range symbols {
		if region.ProgramOffset != nil {
			start := int(*region.ProgramOffset)
			end := start + int(region.Length)
			v.symbols[i] = v.code[start:end:end]
		} else {
			v.symbols[i] = append([]byte(nil), region.Initial...)
		}
	}
	return v, nil
}

// Program returns an independent copy of the current whole program, including
// symbol writes to program-backed spans. This is not a VM save-state schema.
func (v *VM) Program() []byte { return append([]byte(nil), v.code...) }

// Symbol returns an independent copy of a bound symbol span. Index width
// matches opcode36. This exposes neither host pointers nor mutable VM memory.
func (v *VM) Symbol(index uint8) ([]byte, error) {
	if int(index) >= len(v.symbols) {
		return nil, fmt.Errorf("%w: %d", ErrInvalidSymbol, index)
	}
	return append([]byte(nil), v.symbols[index]...), nil
}
