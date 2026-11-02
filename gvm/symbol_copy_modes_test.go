package gvm_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

// The GVM2X handlers 23..2d copy one word between two symbol elements. Each
// side is addressed as a fixed element (symbol, element), an indexed element
// (symbol, index symbol, index element) or an element selected by the first
// word of another symbol (symbol, index symbol). Operands name the destination
// side first.
func symbolCopyFixture(t *testing.T, program []byte) *gvm.VM {
	t.Helper()
	words := [][]uint16{
		{0, 0, 0, 0},                     // 0: destination array
		{9, 2},                           // 1: index words, element 1 selects 2
		{0x1000, 0x2000, 0x3000, 0x4000}, // 2: source array
		{3},                              // 3: first word selects 3
		{7, 1},                           // 4: index words, element 1 selects 1
	}
	regions := make([]gvm.SymbolRegion, len(words))
	for index, values := range words {
		initial := make([]byte, 2*len(values))
		for element, value := range values {
			binary.LittleEndian.PutUint16(initial[2*element:], value)
		}
		regions[index] = gvm.SymbolRegion{Length: uint32(len(initial)), Initial: initial}
	}
	vm, err := gvm.NewWithSymbols(program, 0, regions)
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestSymbolCopyAddressingModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		program []byte
		element int
		want    uint16
	}{
		{"23 indexed from indexed", []byte{0x23, 0, 1, 1, 2, 4, 1}, 2, 0x2000},
		{"24 indexed from first-word index", []byte{0x24, 0, 1, 1, 2, 3}, 2, 0x4000},
		{"25 indexed from fixed element", []byte{0x25, 0, 1, 1, 2, 2}, 2, 0x3000},
		{"26 indexed from first word", []byte{0x26, 0, 1, 1, 3}, 2, 3},
		{"28 first-word index from indexed", []byte{0x28, 0, 3, 2, 4, 1}, 3, 0x2000},
		{"2a first-word index from fixed element", []byte{0x2a, 0, 3, 2, 2}, 3, 0x3000},
		{"2d fixed element from indexed", []byte{0x2d, 0, 1, 2, 4, 1}, 1, 0x2000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := symbolCopyFixture(t, append(append([]byte(nil), tc.program...), 0xff))
			if err := vm.Run(1); !errors.Is(err, gvm.ErrBudget) {
				t.Fatal(err)
			}
			destination, _ := vm.Symbol(0)
			for element := 0; element < 4; element++ {
				want := uint16(0)
				if element == tc.element {
					want = tc.want
				}
				if got := binary.LittleEndian.Uint16(destination[2*element:]); got != want {
					t.Fatalf("destination[%d] = %#04x, want %#04x (all %x)", element, got, want, destination)
				}
			}
			if vm.PC() != len(tc.program) || len(vm.Stack()) != 0 {
				t.Fatalf("pc=%d stack=%v, want pc=%d", vm.PC(), vm.Stack(), len(tc.program))
			}
		})
	}
}

func TestSymbolCopyAddressingModesFaultAtomically(t *testing.T) {
	for _, tc := range []struct {
		name    string
		program []byte
		want    error
	}{
		{"23 source index out of range", []byte{0x23, 0, 1, 1, 2, 1, 0}, gvm.ErrInvalidElement},
		{"25 destination index element out of range", []byte{0x25, 0, 1, 2, 2, 0}, gvm.ErrInvalidElement},
		{"26 unknown source symbol", []byte{0x26, 0, 1, 1, 9}, gvm.ErrInvalidSymbol},
		{"28 destination index out of range", []byte{0x28, 0, 1, 2, 4, 1}, gvm.ErrInvalidElement},
		{"2a source element out of range", []byte{0x2a, 0, 3, 2, 4}, gvm.ErrInvalidElement},
		{"2d truncated", []byte{0x2d, 0, 1, 2, 4}, gvm.ErrTruncated},
		{"23 truncated", []byte{0x23, 0, 1, 1, 2, 4}, gvm.ErrTruncated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := symbolCopyFixture(t, tc.program)
			before, _ := vm.Symbol(0)
			before = append([]byte(nil), before...)
			err := vm.Step()
			after, _ := vm.Symbol(0)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(after, before) || vm.PC() != 1 {
				t.Fatalf("err=%v pc=%d destination=%x, want %v with no write", err, vm.PC(), after, tc.want)
			}
		})
	}
}
