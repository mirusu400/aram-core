package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func eqPush(code []byte, x uint16) []byte { return append(code, 6, byte(x>>8), byte(x)) }

func TestEqualityFullDomain(t *testing.T) {
	// One VM per relation, not per pair. All raw words exercise true and
	// unequal low-byte, high-byte and nonzero/truthiness discrimination.
	for _, mask := range []uint16{0, 1, 0x100, 0x8000, 0xffff} {
		t.Run(fmt.Sprintf("xor%x", mask), func(t *testing.T) {
			code := make([]byte, 0, 8*65536+1)
			for raw := uint32(0); raw < 65536; raw++ {
				code = eqPush(code, uint16(raw))
				code = eqPush(code, uint16(raw)^mask)
				code = append(code, 0x21, 0x96)
			}
			code = append(code, 0xff)
			v := New(code)
			want := uint16(0)
			if mask == 0 {
				want = 1
			}
			for raw := uint32(0); raw < 65536; raw++ {
				if err := v.Run(3); err != ErrBudget {
					t.Fatalf("raw%x: %v", raw, err)
				}
				if v.depth != 1 || v.stack[0] != want || v.stack[1] != 0 || v.PC() != int(raw)*8+7 || v.Halted() {
					t.Fatalf("raw%x stack%x pc%d", raw, v.Stack(), v.PC())
				}
				if err := v.Step(); err != nil {
					t.Fatal(err)
				}
			}
			if err := v.Run(1); err != nil || !v.Halted() {
				t.Fatal(err)
			}
		})
	}
}

func eqVMs(t *testing.T, code []byte, entry uint32) []*VM {
	t.Helper()
	at, err := NewAt(code, entry)
	if err != nil {
		t.Fatal(err)
	}
	offset := uint32(0)
	symbols, err := NewWithSymbols(code, entry, []SymbolRegion{{ProgramOffset: &offset, Length: uint32(len(code))}, {Length: 3, Initial: []byte{1, 2, 3}}})
	if err != nil {
		t.Fatal(err)
	}
	space := AddressSpace{FileLength: uint32(len(code)), RAM: []byte{1, 2, 3}, Symbols: []AddressSymbol{{Region: AddressFile, Length: uint32(len(code))}, {Region: AddressRAM, Length: 3}}}
	address, err := NewWithAddressSpace(code, entry, space)
	if err != nil {
		t.Fatal(err)
	}
	services, err := NewWithAddressSpaceAndServices(code, entry, space, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := []*VM{at, symbols, address, services}
	if entry == 0 {
		result = append(result, New(code))
	}
	return result
}

// Snapshot all owned state, including inaccessible backing and metadata.
// Setup and execution use public constructors and bytecode, never field writes.
func eqUnchanged(t *testing.T, v *VM, before VM, program []byte, symbols [][]byte) {
	t.Helper()
	if v.stack != before.stack || v.depth != before.depth || v.savedTop != before.savedTop || v.savedTopValid != before.savedTopValid || v.returns != before.returns || v.returnDepth != before.returnDepth || v.address != before.address || v.services != before.services || !bytes.Equal(v.Program(), program) {
		t.Fatal("unrelated state changed")
	}
	for i, want := range symbols {
		got, err := v.Symbol(uint8(i))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("symbol memory changed")
		}
	}
	if v.address != nil {
		got, err := v.ReadWord(0)
		if err != nil || got != 0x0201 {
			t.Fatal("RAM changed")
		}
	}
}
func eqSymbols(t *testing.T, v *VM) [][]byte {
	t.Helper()
	result := make([][]byte, len(v.symbols))
	for i := range result {
		var err error
		result[i], err = v.Symbol(uint8(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestEqualityDepthsAndTerminal(t *testing.T) {
	thresholds := []uint16{0, 1, 2, 0x7f, 0x80, 0xff, 0x100, 0x7ffe, 0x7fff, 0x8000, 0x8001, 0xff80, 0xfffe, 0xffff}
	for _, depth := range []int{2, 64, 65} {
		for _, a := range thresholds {
			for _, b := range thresholds {
				code := []byte{}
				for i := 0; i < depth-2; i++ {
					code = eqPush(code, uint16(0x9100+i))
				}
				code = eqPush(code, a)
				code = eqPush(code, b)
				code = append(code, 0x21)
				v := New(code)
				if err := v.Run(uint64(depth)); err != ErrBudget {
					t.Fatal(err)
				}
				before := *v
				want := before
				want.depth--
				want.stack[depth-1] = 0 // Approved host hygiene, explicitly unlike native retained top.
				want.stack[depth-2] = 0
				if a == b {
					want.stack[depth-2] = 1
				}
				if err := v.Run(0); err != ErrBudget || v.PC() != len(code)-1 {
					t.Fatal("zero budget")
				}
				if err := v.Run(1); err != ErrBudget || v.PC() != len(code) || v.Halted() {
					t.Fatalf("depth%d %x,%x: %v", depth, a, b, err)
				}
				eqUnchanged(t, v, want, code, nil)
				state := *v
				err := v.Step()
				var fault *ExecutionError
				if !errors.Is(err, ErrTruncated) || !errors.As(err, &fault) || fault.Offset != len(code) || v.PC() != len(code) || v.Step() != err || v.Run(0) != err {
					t.Fatalf("end fetch: %v", err)
				}
				eqUnchanged(t, v, state, code, nil)
			}
		}
	}
}

func TestEqualityTransactionalUnderflow(t *testing.T) {
	for _, depth := range []int{0, 1} {
		for _, marker := range []bool{false, true} {
			// Populate then pop a word to establish backing through real bytecode.
			code := eqPush(nil, 0xbeef)
			code = append(code, 0x96)
			steps := 2
			if depth == 1 {
				code = eqPush(code, 0x8000)
				steps++
			}
			if marker {
				code = append(code, 0x0b)
				steps++
			}
			offset := len(code)
			code = append(code, 0x21, 0xff)
			for _, v := range eqVMs(t, code, 0) {
				if err := v.Run(uint64(steps)); err != ErrBudget {
					t.Fatal(err)
				}
				before := *v
				symbols := eqSymbols(t, v)
				err := v.Step()
				var fault *ExecutionError
				if !errors.Is(err, ErrStackUnderflow) || !errors.As(err, &fault) || fault.Offset != offset || v.PC() != offset+1 || v.Halted() {
					t.Fatalf("depth%d: %v", depth, err)
				}
				for _, budget := range []uint64{0, 1, 20} {
					if v.Run(budget) != err || v.Step() != err || v.PC() != offset+1 {
						t.Fatal("fault not sticky")
					}
					eqUnchanged(t, v, before, code, symbols)
				}
			}
		}
	}
}

func TestEqualityOwnershipMarkersAndNestedReturns(t *testing.T) {
	for _, entry := range []uint32{0, 2} {
		for _, marker := range []bool{false, true} {
			// Entry calls entry+4, then entry+8. Both actual returns must survive.
			code := bytes.Repeat([]byte{0xf0}, int(entry))
			e := byte(entry)
			code = append(code, 0x44, 0, e+4, 0xff, 0x44, 0, e+8, 0x45)
			code = eqPush(code, 0xabcd)
			code = eqPush(code, 0xffff)
			code = eqPush(code, 0xffff)
			steps := 5
			if marker {
				code = append(code, 0x0b)
				steps++
			}
			code = append(code, 0x21, 0x45)
			original := append([]byte(nil), code...)
			vms := eqVMs(t, code, entry)
			code[len(code)-2] = 0xf0 // Must not change any constructor-owned instruction.
			for _, v := range vms {
				if err := v.Run(uint64(steps)); err != ErrBudget {
					t.Fatal(err)
				}
				before := *v
				symbols := eqSymbols(t, v)
				if before.returnDepth != 2 || before.savedTopValid != marker || (marker && before.savedTop != 2) {
					t.Fatal("fixture")
				}
				want := before
				want.depth = 2
				want.stack[1] = 1
				want.stack[2] = 0
				if err := v.Step(); err != nil || v.PC() != before.pc+1 || v.Halted() {
					t.Fatal(err)
				}
				eqUnchanged(t, v, want, original, symbols)
				snapshot := v.Stack()
				snapshot[0] = 0
				if !reflect.DeepEqual(v.Stack(), []uint16{0xabcd, 1}) {
					t.Fatal("stack ownership")
				}
				if err := v.Step(); err != nil || v.PC() != int(entry)+7 {
					t.Fatal("inner return", err)
				}
				if err := v.Step(); err != nil || v.PC() != int(entry)+3 {
					t.Fatal("outer return", err)
				}
				if err := v.Run(1); err != nil || !v.Halted() {
					t.Fatal(err)
				}
				halted := *v
				if v.Step() != nil || v.Run(0) != nil || v.PC() != halted.pc {
					t.Fatal("halt resumed")
				}
				eqUnchanged(t, v, halted, original, symbols)
			}
		}
	}
}
