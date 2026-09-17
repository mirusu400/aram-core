package gvm_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

// These tests deliberately use only exported contracts. Opcode09 is an indexed
// raw16 store, with eager operands and transactional host guards after fetch.
func indexed09Prefix(depth int, value uint16) []byte {
	var code []byte
	for i := 0; i < depth; i++ {
		v := uint16(i)
		if i == depth-1 {
			v = value
		}
		code = append(code, 6, byte(v>>8), byte(v))
	}
	return code
}

func indexed09RAM(t *testing.T, address bool, code []byte, entry uint32, index, size int) (*gvm.VM, []byte) {
	t.Helper()
	initial := bytes.Repeat([]byte{0xa5}, size)
	var v *gvm.VM
	var err error
	if address {
		symbols := make([]gvm.AddressSymbol, index+1)
		for i := range symbols {
			symbols[i].Region = gvm.AddressRAM
		}
		symbols[index] = gvm.AddressSymbol{Region: gvm.AddressRAM, Length: uint32(size)}
		v, err = gvm.NewWithAddressSpace(code, entry, gvm.AddressSpace{RAM: initial, Symbols: symbols})
	} else {
		symbols := make([]gvm.SymbolRegion, index+1)
		symbols[index] = gvm.SymbolRegion{Length: uint32(size), Initial: initial}
		v, err = gvm.NewWithSymbols(code, entry, symbols)
	}
	if err != nil {
		t.Fatal(err)
	}
	return v, initial
}

func TestStoreIndexed09Contract(t *testing.T) {
	cases := []struct {
		depth, index, element, size int
		value                       uint16
	}{
		{1, 0, 0, 2, 0}, {1, 128, 127, 256, 0x8000},
		{1, 255, 254, 510, 0xffff}, {64, 255, 254, 510, 0x1234},
		{64, 0, 1, 6, 0xff00}, {1, 127, 128, 258, 0x00ff},
	}
	for _, address := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("address%v/d%d/i%d/e%d/v%x", address, tc.depth, tc.index, tc.element, tc.value), func(t *testing.T) {
				code := indexed09Prefix(tc.depth, tc.value)
				offset := len(code)
				code = append(code, 9, byte(tc.index), byte(tc.element), 0xff)
				v, initial := indexed09RAM(t, address, code, 0, tc.index, tc.size)
				if err := v.Run(uint64(tc.depth)); err != gvm.ErrBudget {
					t.Fatal(err)
				}
				before := v.Stack()
				if err := v.Run(0); err != gvm.ErrBudget || v.PC() != offset {
					t.Fatal("zero budget changed state")
				}
				if err := v.Step(); err != nil {
					t.Fatalf("09 must store: %v", err)
				}
				want := bytes.Repeat([]byte{0xa5}, tc.size)
				want[2*tc.element], want[2*tc.element+1] = byte(tc.value), byte(tc.value>>8)
				got, err := v.Symbol(uint8(tc.index))
				if err != nil || !bytes.Equal(got, want) || v.PC() != offset+3 || len(v.Stack()) != tc.depth-1 || (tc.depth > 1 && !reflect.DeepEqual(v.Stack(), before[:tc.depth-1])) || v.Halted() {
					t.Fatalf("store state: symbol=%x stack=%v pc=%d err=%v", got, v.Stack(), v.PC(), err)
				}
				if !bytes.Equal(code, v.Program()) || !bytes.Equal(initial, bytes.Repeat([]byte{0xa5}, tc.size)) {
					t.Fatal("caller inputs changed")
				}
				got[0] ^= 0xff
				initial[0] ^= 0xff
				snapshot := v.Program()
				snapshot[0] ^= 0xff
				if stack := v.Stack(); len(stack) != 0 {
					stack[0] ^= 0xffff
				}
				again, _ := v.Symbol(uint8(tc.index))
				if !bytes.Equal(again, want) || !bytes.Equal(code, v.Program()) || (tc.depth > 1 && !reflect.DeepEqual(v.Stack(), before[:tc.depth-1])) {
					t.Fatal("inspection/input alias escaped")
				}
				if err := v.Run(1); err != nil || !v.Halted() || v.PC() != offset+4 {
					t.Fatalf("next fetch: %v pc=%d", err, v.PC())
				}
			})
		}
	}
}

func TestStoreIndexed09GuardOrderAndStickyState(t *testing.T) {
	cases := []struct {
		name        string
		depth, size int
		operands    []byte
		want        error
	}{
		{"missing-both-before-overflow", 65, 1, nil, gvm.ErrTruncated},
		{"missing-element-before-overflow", 65, 1, []byte{255}, gvm.ErrTruncated},
		{"missing-both-before-underflow", 0, 2, nil, gvm.ErrTruncated},
		{"missing-element-before-symbol", 0, 2, []byte{255}, gvm.ErrTruncated},
		{"overflow-before-symbol", 65, 1, []byte{255, 255}, gvm.ErrStackOverflow},
		{"overflow-before-shape", 65, 1, []byte{0, 255}, gvm.ErrStackOverflow},
		{"overflow-valid-destination", 65, 2, []byte{0, 0}, gvm.ErrStackOverflow},
		{"symbol-before-shape-underflow", 0, 1, []byte{255, 255}, gvm.ErrInvalidSymbol},
		{"symbol-before-element", 1, 2, []byte{1, 255}, gvm.ErrInvalidSymbol},
		{"odd-before-element-underflow", 0, 3, []byte{0, 255}, gvm.ErrInvalidSymbolRegion},
		{"one-byte-global-space-not-fallback", 1, 1, []byte{0, 0}, gvm.ErrInvalidSymbolRegion},
		{"oversized-before-element-underflow", 0, 512, []byte{0, 255}, gvm.ErrInvalidSymbolRegion},
		{"empty-count-before-underflow", 0, 0, []byte{0, 0}, gvm.ErrInvalidElement},
		{"element-before-underflow", 0, 2, []byte{0, 1}, gvm.ErrInvalidElement},
		{"element255-count255", 1, 510, []byte{0, 255}, gvm.ErrInvalidElement},
		{"underflow-valid-word", 0, 2, []byte{0, 0}, gvm.ErrStackUnderflow},
	}
	for _, address := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("address%v/%s", address, tc.name), func(t *testing.T) {
				// An actual call keeps a saved return live while the store faults.
				code := []byte{0x44, 0, 4, 0xff}
				code = append(code, indexed09Prefix(tc.depth, 0x8000)...)
				offset := len(code)
				code = append(code, 9)
				code = append(code, tc.operands...)
				v, _ := indexed09RAM(t, address, code, 0, 0, tc.size)
				if err := v.Run(uint64(tc.depth + 1)); err != gvm.ErrBudget {
					t.Fatal(err)
				}
				stack, program := v.Stack(), v.Program()
				symbol, _ := v.Symbol(0)
				err := v.Step()
				var execution *gvm.ExecutionError
				if !errors.Is(err, tc.want) || !errors.As(err, &execution) || execution.Offset != offset {
					t.Errorf("fault=%v want=%v at%d", err, tc.want, offset)
				}
				if v.PC() != offset+1 {
					t.Errorf("fault PC=%d want%d", v.PC(), offset+1)
				}
				for _, again := range []error{v.Step(), v.Run(0), v.Run(99)} {
					if again != err {
						t.Error("fault not sticky")
					}
				}
				after, _ := v.Symbol(0)
				if !reflect.DeepEqual(stack, v.Stack()) || !bytes.Equal(program, v.Program()) || !bytes.Equal(symbol, after) || v.Halted() || v.PC() != offset+1 {
					t.Fatal("fault/retry mutated public state")
				}
				// Saved returns have no public snapshot API. Their preservation on success
				// is verified by nested real calls below, never by private field access.
			})
		}
	}
}

func TestStoreIndexed09SharedViews(t *testing.T) {
	for _, mode := range []string{"legacy-file", "address-file", "address-ram"} {
		t.Run(mode, func(t *testing.T) {
			code := append(bytes.Repeat([]byte{0xa5}, 7), 6, 0x12, 0x34, 9, 0, 1, 0xff)
			ram := bytes.Repeat([]byte{0xa5}, 7)
			var v *gvm.VM
			var err error
			if mode == "legacy-file" {
				a, b := uint32(1), uint32(4)
				v, err = gvm.NewWithSymbols(code, 7, []gvm.SymbolRegion{{ProgramOffset: &a, Length: 4}, {ProgramOffset: &b, Length: 2}})
			} else {
				region := gvm.AddressFile
				if mode == "address-ram" {
					region = gvm.AddressRAM
				}
				v, err = gvm.NewWithAddressSpace(code, 7, gvm.AddressSpace{FileLength: 7, RAM: ram, Symbols: []gvm.AddressSymbol{{Region: region, Offset: 1, Length: 4}, {Region: region, Offset: 4, Length: 2}}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = v.Run(2); err != gvm.ErrBudget {
				t.Fatalf("09 shared store: %v", err)
			}
			a, _ := v.Symbol(0)
			b, _ := v.Symbol(1)
			if !bytes.Equal(a, []byte{0xa5, 0xa5, 0x34, 0x12}) || !bytes.Equal(b, []byte{0x12, 0xa5}) || v.PC() != 13 || len(v.Stack()) != 0 {
				t.Fatalf("shared views %x %x pc%d", a, b, v.PC())
			}
			want := append([]byte(nil), code...)
			if mode != "address-ram" {
				want[3], want[4] = 0x34, 0x12
			}
			if !bytes.Equal(v.Program(), want) || !bytes.Equal(ram, bytes.Repeat([]byte{0xa5}, 7)) || code[3] != 0xa5 {
				t.Fatal("backing/ownership")
			}
			if err = v.Step(); err != nil || !v.Halted() {
				t.Fatalf("halt: %v", err)
			}
		})
	}
}

func TestStoreIndexed09OperandAndFutureFetchAliases(t *testing.T) {
	for _, address := range []bool{false, true} {
		for _, self := range []bool{false, true} {
			t.Run(fmt.Sprintf("address%v/self%v", address, self), func(t *testing.T) {
				// Entry and FileStart are nonzero. Self overwrites BOTH inline bytes with
				// ff00, while future overwrites an unsupported next opcode with ff00.
				code := []byte{0xcc, 6, 0, 0xff, 9, 0, 0, 0xff, 0xff}
				offset := uint32(5)
				if !self {
					offset = 7
					code[7] = 0xfe
				}
				var v *gvm.VM
				var err error
				if address {
					v, err = gvm.NewWithAddressSpace(code, 1, gvm.AddressSpace{FileStart: 1, FileLength: 8, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Offset: offset - 1, Length: 2}}})
				} else {
					v, err = gvm.NewWithSymbols(code, 1, []gvm.SymbolRegion{{ProgramOffset: &offset, Length: 2}})
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Run(2); err != gvm.ErrBudget || v.PC() != 7 || len(v.Stack()) != 0 {
					t.Fatalf("alias store: %v pc%d", err, v.PC())
				}
				want := append([]byte(nil), code...)
				want[offset], want[offset+1] = 0xff, 0
				if !bytes.Equal(want, v.Program()) {
					t.Fatalf("program=%x want=%x", v.Program(), want)
				}
				if err = v.Step(); err != nil || !v.Halted() || v.PC() != 8 {
					t.Fatalf("future fetch: %v pc%d", err, v.PC())
				}
			})
		}
	}
}

func TestStoreIndexed09SavedReturns(t *testing.T) {
	for _, address := range []bool{false, true} {
		t.Run(fmt.Sprintf("address%v", address), func(t *testing.T) {
			// Two live saved PCs. Return through both after the store, preserving the
			// lower data-stack word. Targets are whole-buffer absolute offsets.
			code := []byte{0x44, 0, 4, 0xff, 5, 7, 0x44, 0, 10, 0x45, 6, 0x80, 0, 9, 0, 1, 0x45}
			v, _ := indexed09RAM(t, address, code, 0, 0, 4)
			if err := v.Run(4); err != gvm.ErrBudget || v.PC() != 13 {
				t.Fatalf("setup: %v pc%d", err, v.PC())
			}
			if err := v.Step(); err != nil || v.PC() != 16 || !reflect.DeepEqual(v.Stack(), []uint16{7}) {
				t.Fatalf("store: %v pc%d stack%v", err, v.PC(), v.Stack())
			}
			for _, wantPC := range []int{9, 3} {
				if err := v.Step(); err != nil || v.PC() != wantPC || v.Halted() {
					t.Fatalf("saved return: %v pc%d want%d", err, v.PC(), wantPC)
				}
			}
			if err := v.Step(); err != nil || !v.Halted() || v.PC() != 4 {
				t.Fatalf("halt: %v", err)
			}
			got, _ := v.Symbol(0)
			if !bytes.Equal(got, []byte{0xa5, 0xa5, 0, 0x80}) {
				t.Fatalf("word=%x", got)
			}
		})
	}
}

func TestStoreIndexed09DescriptorBoundsNotGlobalFallback(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, tc := range []struct {
			length  uint32
			element byte
			want    error
		}{
			{0, 0, gvm.ErrInvalidElement},
			{1, 0, gvm.ErrInvalidSymbolRegion},
			{2, 1, gvm.ErrInvalidElement},
		} {
			t.Run(fmt.Sprintf("region%d/length%d", region, tc.length), func(t *testing.T) {
				// Every selected address has a complete word in the global region.
				// This must not bypass the descriptor shape and authoritative count.
				code := append(bytes.Repeat([]byte{0xa5}, 8), 5, 1, 9, 0, tc.element)
				v, err := gvm.NewWithAddressSpace(code, 8, gvm.AddressSpace{
					FileLength: 8, RAM: bytes.Repeat([]byte{0xa5}, 8),
					Symbols: []gvm.AddressSymbol{
						{Region: region, Offset: 1, Length: tc.length},
						{Region: region, Length: 8},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Step(); err != nil {
					t.Fatal(err)
				}
				before, _ := v.Symbol(1)
				err = v.Step()
				if !errors.Is(err, tc.want) {
					t.Errorf("fault=%v want=%v", err, tc.want)
				}
				for _, again := range []error{v.Step(), v.Run(0), v.Run(9)} {
					if again != err {
						t.Error("fault not sticky")
					}
				}
				after, _ := v.Symbol(1)
				if !bytes.Equal(before, after) || !bytes.Equal(code, v.Program()) ||
					!reflect.DeepEqual(v.Stack(), []uint16{1}) || v.PC() != 11 || v.Halted() {
					t.Fatal("descriptor failure modified public state")
				}
			})
		}
	}
}
