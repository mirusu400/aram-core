package gvm_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func storeRAMCode(words ...uint16) []byte {
	var code []byte
	for _, w := range words {
		code = append(code, 6, byte(w>>8), byte(w))
	}
	return append(code, 0x4f)
}

func TestStoreRAMDirectBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int
		index uint16
		valid bool
	}{
		{"zero", 2, 0, true}, {"last", 8, 3, true}, {"odd-last", 5, 1, true},
		{"tag-bit-is-direct", 32770, 0x4000, true}, {"max-positive", 65536, 0x7fff, true},
		{"negative-min", 65536, 0x8000, false}, {"negative-one", 65536, 0xffff, false},
		{"empty", 0, 0, false}, {"one-byte", 1, 0, false}, {"odd-end", 3, 1, false},
		{"fullword-end", 4, 2, false}, {"tag-bit-outside", 2, 0x4000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, value := range []uint16{0, 0x1234, 0x8000, 0xffff} {
				code := storeRAMCode(0x55aa, tc.index, value)
				ram := bytes.Repeat([]byte{0xa5}, tc.size)
				original := append([]byte(nil), ram...)
				v, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{FileLength: uint32(len(code)), RAM: ram, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: uint32(len(ram))}, {Region: gvm.AddressFile, Length: uint32(len(code))}}})
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Run(3); err != gvm.ErrBudget || v.PC() != 9 {
					t.Fatalf("setup: %v pc=%d", err, v.PC())
				}
				before := v.Stack()
				snapshot, _ := v.Symbol(0)
				err = v.Step()
				if tc.valid {
					if err != nil || v.PC() != 10 || v.Halted() || !reflect.DeepEqual(v.Stack(), []uint16{0x55aa}) {
						t.Fatalf("store: %v pc=%d stack=%x", err, v.PC(), v.Stack())
					}
					want := append([]byte(nil), original...)
					want[2*int(tc.index)] = byte(value)
					want[2*int(tc.index)+1] = byte(value >> 8)
					got, _ := v.Symbol(0)
					if !bytes.Equal(got, want) {
						t.Fatalf("RAM mismatch for %04x", value)
					}
					if v.Run(0) != gvm.ErrBudget {
						t.Fatal("zero budget")
					}
					if e := v.Step(); !errors.Is(e, gvm.ErrTruncated) || v.PC() != 10 {
						t.Fatalf("inline operand consumed: %v", e)
					}
				} else {
					var e *gvm.ExecutionError
					if !errors.As(err, &e) || e.Offset != 9 || !errors.Is(err, gvm.ErrInvalidAddress) || v.PC() != 10 {
						t.Fatalf("bounds: %v pc=%d", err, v.PC())
					}
					if !reflect.DeepEqual(before, v.Stack()) {
						t.Fatal("fault popped stack")
					}
					got, _ := v.Symbol(0)
					if !bytes.Equal(got, original) {
						t.Fatal("fault wrote RAM")
					}
					for _, again := range []error{v.Step(), v.Run(0), v.Run(100)} {
						if again != err {
							t.Fatal("fault not identical/sticky")
						}
					}
				}
				if !bytes.Equal(snapshot, original) || !bytes.Equal(ram, original) || !bytes.Equal(v.Program(), code) {
					t.Fatal("ownership/snapshot changed")
				}
				file, _ := v.Symbol(1)
				if !bytes.Equal(file, code) {
					t.Fatal("file changed")
				}
			}
		})
	}
}

func TestStoreRAMDepthAndBudgets(t *testing.T) {
	for _, depth := range []int{0, 1, 2, 65} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			var code []byte
			for i := 0; i < depth; i++ {
				code = append(code, 5, 0)
			}
			code = append(code, 0x4f, 5, 0x80, 0xff)
			v, e := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{RAM: []byte{1, 2}})
			if e != nil {
				t.Fatal(e)
			}
			if e = v.Run(uint64(depth)); e != gvm.ErrBudget {
				t.Fatal(e)
			}
			before := v.Stack()
			e = v.Run(1)
			if depth < 2 {
				var fault *gvm.ExecutionError
				if !errors.Is(e, gvm.ErrStackUnderflow) || !errors.As(e, &fault) || fault.Offset != 2*depth || v.PC() != 2*depth+1 || !reflect.DeepEqual(v.Stack(), before) {
					t.Fatalf("underflow: %v", e)
				}
				if v.Step() != e || v.Run(0) != e {
					t.Fatal("sticky")
				}
				word, _ := v.ReadWord(0)
				if word != 0x0201 {
					t.Fatal("underflow wrote")
				}
			} else {
				if e != gvm.ErrBudget || v.PC() != 2*depth+1 || len(v.Stack()) != depth-2 {
					t.Fatalf("pop: %v", e)
				}
				word, e := v.ReadWord(0)
				if e != nil || word != 0 {
					t.Fatalf("no symbols: %x %v", word, e)
				}
				if e = v.Run(2); e != nil || !v.Halted() || v.Stack()[depth-2] != 0xff80 {
					t.Fatalf("next instruction: %v", e)
				}
			}
		})
	}
	// Underflow takes precedence over an empty configured region.
	v, _ := gvm.NewWithAddressSpace([]byte{5, 0xff, 0x4f}, 0, gvm.AddressSpace{})
	if e := v.Run(2); !errors.Is(e, gvm.ErrStackUnderflow) {
		t.Fatal(e)
	}
}

func TestStoreRAMLegacyPrecedence(t *testing.T) {
	for _, depth := range []int{0, 1, 2, 65} {
		words := make([]uint16, depth)
		for i := range words {
			words[i] = 0xffff
		}
		code := storeRAMCode(words...)
		a, e := gvm.NewAt(code, 0)
		if e != nil {
			t.Fatal(e)
		}
		b, e := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Initial: []byte{1, 2}, Length: 2}})
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range []*gvm.VM{gvm.New(code), a, b} {
			if e := v.Run(uint64(depth)); e != gvm.ErrBudget {
				t.Fatal(e)
			}
			before := v.Stack()
			err := v.Step()
			var u *gvm.UnsupportedOpcodeError
			if !errors.As(err, &u) || u.Opcode != 0x4f || u.Offset != 3*depth || v.PC() != 3*depth+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), before) {
				t.Fatalf("legacy: %v", err)
			}
			if v.Step() != err || v.Run(0) != err || v.Run(100) != err {
				t.Fatal("legacy sticky")
			}
		}
	}
}

func TestStoreRAMAliasesOwnershipAndReturn(t *testing.T) {
	// Call a store subroutine, then return to the ordinary halt.
	code := []byte{0x44, 0, 4, 0xff, 5, 1, 6, 0xbe, 0xef, 0x4f, 0x45}
	original := append([]byte(nil), code...)
	ram := []byte{1, 2, 3, 4, 5}
	bindings := []gvm.AddressSymbol{{Region: gvm.AddressRAM, Offset: 2, Length: 1}, {Region: gvm.AddressRAM, Offset: 1, Length: 4}, {Region: gvm.AddressRAM, Offset: 2, Length: 2}, {Region: gvm.AddressFile, Length: uint32(len(code))}}
	v, e := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{FileLength: uint32(len(code)), RAM: ram, Symbols: bindings})
	if e != nil {
		t.Fatal(e)
	}
	snap, _ := v.Symbol(1)
	code[0] = 0xff
	ram[2] = 0
	bindings[0].Offset = 0
	if e = v.Run(4); e != gvm.ErrBudget || v.PC() != 10 || len(v.Stack()) != 0 {
		t.Fatalf("call/store: %v pc=%d", e, v.PC())
	}
	for i, want := range [][]byte{{0xef}, {2, 0xef, 0xbe, 5}, {0xef, 0xbe}, original} {
		got, _ := v.Symbol(uint8(i))
		if !bytes.Equal(got, want) {
			t.Fatalf("alias %d: %x", i, got)
		}
		if len(got) > 0 {
			got[0] ^= 0xff
		}
	}
	if !bytes.Equal(snap, []byte{2, 3, 4, 5}) || ram[3] != 4 || !bytes.Equal(v.Program(), original) {
		t.Fatal("ownership")
	}
	if e = v.Run(1); e != gvm.ErrBudget || v.PC() != 3 {
		t.Fatalf("return lost: %v pc=%d", e, v.PC())
	}
	if e = v.Run(1); e != nil || !v.Halted() {
		t.Fatal(e)
	}
}
