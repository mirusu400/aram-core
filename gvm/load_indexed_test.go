package gvm_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

// Both APIs derive the descriptor count from the exact current byte span.
// Even spans <=510 are host representational policy, not native validation.
func indexedLoadVM(t *testing.T, configured bool, code []byte, symbols [][]byte) *gvm.VM {
	t.Helper()
	var v *gvm.VM
	var err error
	if configured {
		space := gvm.AddressSpace{}
		for _, data := range symbols {
			space.Symbols = append(space.Symbols, gvm.AddressSymbol{Region: gvm.AddressRAM, Offset: uint32(len(space.RAM)), Length: uint32(len(data))})
			space.RAM = append(space.RAM, data...)
		}
		v, err = gvm.NewWithAddressSpace(code, 0, space)
	} else {
		bindings := make([]gvm.SymbolRegion, len(symbols))
		for i, data := range symbols {
			bindings[i] = gvm.SymbolRegion{Length: uint32(len(data)), Initial: data}
		}
		v, err = gvm.NewWithSymbols(code, 0, bindings)
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func indexedLoadUnchanged(t *testing.T, v *gvm.VM, program []byte, symbols [][]byte) {
	t.Helper()
	if !bytes.Equal(v.Program(), program) {
		t.Fatal("program mutated")
	}
	for i, want := range symbols {
		got, err := v.Symbol(uint8(i))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("symbol %d mutated: %x, %v", i, got, err)
		}
	}
}

func TestLoadIndexedUnsignedAndRaw16(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, index := range []byte{0, 127, 128, 254, 255} {
			for _, element := range []byte{0, 127, 128, 253, 254} {
				for _, value := range []uint16{0, 0x1234, 0x7fff, 0x8000, 0xff80, 0xffff} {
					t.Run(fmt.Sprintf("configured=%v/d=%d/e=%d/value=%04x", configured, index, element, value), func(t *testing.T) {
						symbols := make([][]byte, int(index)+1)
						symbols[index] = make([]byte, 510)
						binary.LittleEndian.PutUint16(symbols[index][int(element)*2:], value)
						code := []byte{3, index, element, 0xff}
						v := indexedLoadVM(t, configured, code, symbols)
						if err := v.Run(0); err != gvm.ErrBudget || v.PC() != 0 {
							t.Fatalf("zero budget: %v", err)
						}
						if err := v.Run(1); err != gvm.ErrBudget || v.PC() != 3 || !reflect.DeepEqual(v.Stack(), []uint16{value}) || v.Halted() {
							t.Fatalf("load: %v pc=%d stack=%x", err, v.PC(), v.Stack())
						}
						indexedLoadUnchanged(t, v, code, symbols)
						if err := v.Run(1); err != nil || !v.Halted() || v.PC() != 4 {
							t.Fatalf("halt: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestLoadIndexedGuardOrderAndAtomicity(t *testing.T) {
	cases := []struct {
		name     string
		operands []byte
		symbols  [][]byte
		depth    int
		want     error
	}{
		{"empty-success", []byte{0, 0}, [][]byte{{0x34, 0x92}}, 0, nil},
		{"last-slot", []byte{0, 0}, [][]byte{{0x34, 0x92}}, 64, nil},
		{"capacity", []byte{0, 0}, [][]byte{{0x34, 0x92}}, 65, gvm.ErrStackOverflow},
		{"capacity-before-symbol", []byte{255, 255}, nil, 65, gvm.ErrStackOverflow},
		{"capacity-before-shape", []byte{0, 255}, [][]byte{{1}}, 65, gvm.ErrStackOverflow},
		{"symbol-before-element", []byte{255, 255}, [][]byte{{1}}, 0, gvm.ErrInvalidSymbol},
		{"unsigned-symbol-128", []byte{128, 0}, make([][]byte, 128), 0, gvm.ErrInvalidSymbol},
		{"unsigned-symbol-255", []byte{255, 0}, make([][]byte, 255), 0, gvm.ErrInvalidSymbol},
		{"empty-count", []byte{0, 0}, [][]byte{nil}, 0, gvm.ErrInvalidElement},
		{"one-word-not-count-plus-one", []byte{0, 1}, [][]byte{{1, 2}}, 0, gvm.ErrInvalidElement},
		{"element255", []byte{0, 255}, [][]byte{make([]byte, 510)}, 0, gvm.ErrInvalidElement},
		{"odd-before-element", []byte{0, 255}, [][]byte{make([]byte, 509)}, 0, gvm.ErrInvalidSymbolRegion},
		{"short-full-word", []byte{0, 0}, [][]byte{{1}}, 0, gvm.ErrInvalidSymbolRegion},
		{"oversize-before-element", []byte{0, 0}, [][]byte{make([]byte, 512)}, 0, gvm.ErrInvalidSymbolRegion},
	}
	for _, depth := range []int{0, 64, 65} {
		for n := 0; n < 2; n++ {
			cases = append(cases, struct {
				name     string
				operands []byte
				symbols  [][]byte
				depth    int
				want     error
			}{fmt.Sprintf("truncated%d-depth%d", n, depth), []byte{255}[:n], nil, depth, gvm.ErrTruncated})
		}
	}
	for _, configured := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("configured=%v/%s", configured, tc.name), func(t *testing.T) {
				var code []byte
				for i := 0; i < tc.depth; i++ {
					code = append(code, 6, byte(i), byte(255-i))
				}
				offset := len(code)
				code = append(code, 3)
				code = append(code, tc.operands...)
				v := indexedLoadVM(t, configured, code, tc.symbols)
				if err := v.Run(uint64(tc.depth)); err != gvm.ErrBudget {
					t.Fatalf("setup: %v", err)
				}
				before := v.Stack()
				err := v.Step()
				if tc.want == nil {
					want := append(before, 0x9234)
					if err != nil || v.PC() != offset+3 || !reflect.DeepEqual(v.Stack(), want) {
						t.Fatalf("success: %v pc=%d stack=%x", err, v.PC(), v.Stack())
					}
				} else {
					var fault *gvm.ExecutionError
					if !errors.Is(err, tc.want) || !errors.As(err, &fault) || fault.Offset != offset || v.PC() != offset+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), before) {
						t.Fatalf("fault: %v pc=%d stack=%x", err, v.PC(), v.Stack())
					}
					if v.Step() != err || v.Run(0) != err || v.Run(100) != err || v.PC() != offset+1 || !reflect.DeepEqual(v.Stack(), before) {
						t.Fatal("fault not sticky/atomic")
					}
				}
				indexedLoadUnchanged(t, v, code, tc.symbols)
			})
		}
	}
}

func TestLoadIndexedAliasesAndReturns(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("configured=%v/file=%v", configured, file), func(t *testing.T) {
				// Call then load in the subroutine. Return must still reach the halt.
				code := []byte{0x44, 0, 4, 0xff, 3, 1, 1, 0x45, 0x11, 0x22, 0x33, 0x84, 0x55, 0x66}
				payload := []byte{0x11, 0x22, 0x33, 0x84, 0x55, 0x66}
				var v *gvm.VM
				var err error
				if configured {
					region := gvm.AddressRAM
					space := gvm.AddressSpace{RAM: payload}
					if file {
						region = gvm.AddressFile
						space.FileStart = 8
						space.FileLength = 6
					}
					space.Symbols = []gvm.AddressSymbol{{Region: region, Offset: 2, Length: 2}, {Region: region, Length: 6}}
					v, err = gvm.NewWithAddressSpace(code, 0, space)
				} else {
					bindings := []gvm.SymbolRegion{{Length: 2, Initial: payload[2:4]}, {Length: 6, Initial: payload}}
					if file {
						a, b := uint32(10), uint32(8)
						bindings = []gvm.SymbolRegion{{ProgramOffset: &a, Length: 2}, {ProgramOffset: &b, Length: 6}}
					}
					v, err = gvm.NewWithSymbols(code, 0, bindings)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Run(2); err != gvm.ErrBudget || v.PC() != 7 || !reflect.DeepEqual(v.Stack(), []uint16{0x8433}) {
					t.Fatalf("load: %v pc=%d", err, v.PC())
				}
				indexedLoadUnchanged(t, v, code, [][]byte{payload[2:4], payload})
				if err = v.Run(2); err != nil || !v.Halted() || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), []uint16{0x8433}) {
					t.Fatalf("return: %v pc=%d", err, v.PC())
				}
			})
		}
	}
}

func TestLoadIndexedInstructionAlias(t *testing.T) {
	for _, configured := range []bool{false, true} {
		code := []byte{3, 0, 1, 0xff}
		var v *gvm.VM
		var err error
		if configured {
			v, err = gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{FileLength: 4, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: 4}}})
		} else {
			offset := uint32(0)
			v, err = gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{ProgramOffset: &offset, Length: 4}})
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = v.Run(2); err != nil || !v.Halted() || !reflect.DeepEqual(v.Stack(), []uint16{0xff01}) {
			t.Fatalf("instruction alias: %v stack=%x", err, v.Stack())
		}
		indexedLoadUnchanged(t, v, code, [][]byte{code})
	}
}

func TestLoadIndexedExactSpanAndCurrentBytes(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, length := range []uint32{0, 1, 2, 3, 4} {
			t.Run(fmt.Sprintf("configured=%v/length=%d", configured, length), func(t *testing.T) {
				// An odd-offset view has extra backing bytes after its exact end.
				// A preceding store must be visible without synthetic metadata.
				code := []byte{0x31, 0, 1, 0x80, 3, 1, 1, 0xff, 0, 1, 2, 3, 4, 5, 6}
				var v *gvm.VM
				var err error
				if configured {
					v, err = gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{FileStart: 8, FileLength: 7, Symbols: []gvm.AddressSymbol{
						{Region: gvm.AddressFile, Offset: 1, Length: 4},
						{Region: gvm.AddressFile, Offset: 1, Length: length},
					}})
				} else {
					offset := uint32(9)
					v, err = gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{ProgramOffset: &offset, Length: 4}, {ProgramOffset: &offset, Length: length}})
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Step(); err != nil {
					t.Fatal(err)
				}
				program := v.Program()
				a, _ := v.Symbol(0)
				b, _ := v.Symbol(1)
				err = v.Step()
				if length == 4 {
					if err != nil || v.PC() != 7 || !reflect.DeepEqual(v.Stack(), []uint16{0xff80}) {
						t.Fatalf("current bytes: %v %x", err, v.Stack())
					}
				} else {
					want := gvm.ErrInvalidElement
					if length%2 != 0 {
						want = gvm.ErrInvalidSymbolRegion
					}
					if !errors.Is(err, want) || v.PC() != 5 || len(v.Stack()) != 0 || v.Step() != err {
						t.Fatalf("exact span: %v", err)
					}
				}
				indexedLoadUnchanged(t, v, program, [][]byte{a, b})
			})
		}
	}
}
