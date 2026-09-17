package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func storeStackProgram(depth int, value uint16) []byte {
	var code []byte
	for i := 0; i < depth; i++ {
		if i == depth-1 {
			code = append(code, 6, byte(value>>8), byte(value))
		} else {
			code = append(code, 5, byte(i))
		}
	}
	return code
}

func TestStoreStackContract(t *testing.T) {
	for _, depth := range []int{1, 64} {
		for _, index := range []int{0, 127, 128, 255} {
			for _, size := range []int{2, 3, 511, 512} {
				for _, value := range []uint16{0, 0x8000, 0xffff, 0x1234} {
					t.Run(fmt.Sprintf("d%d/i%d/n%d/v%x", depth, index, size, value), func(t *testing.T) {
						code := storeStackProgram(depth, value)
						offset := len(code)
						code = append(code, 0x0a, byte(index))
						initial := bytes.Repeat([]byte{0xa5}, size)
						bindings := make([]SymbolRegion, index+1)
						bindings[index] = SymbolRegion{Length: uint32(size), Initial: initial}
						v, err := NewWithSymbols(code, 0, bindings)
						if err != nil {
							t.Fatal(err)
						}
						if err = v.Run(uint64(depth)); !errors.Is(err, ErrBudget) {
							t.Fatal(err)
						}
						before := v.Stack()
						if v.Run(0) != ErrBudget || v.PC() != offset {
							t.Fatal("zero budget moved")
						}
						if err = v.Step(); err != nil {
							t.Fatal(err)
						}
						want := append([]byte(nil), initial...)
						want[0], want[1] = byte(value), byte(value>>8)
						got, _ := v.Symbol(uint8(index))
						if !bytes.Equal(got, want) || v.PC() != offset+2 || len(v.Stack()) != depth-1 || (depth > 1 && !reflect.DeepEqual(v.Stack(), before[:depth-1])) {
							t.Fatalf("store state: %x pc%d stack%v", got, v.PC(), v.Stack())
						}
						if !bytes.Equal(initial, bytes.Repeat([]byte{0xa5}, size)) || !bytes.Equal(code, v.Program()) {
							t.Fatal("caller input changed")
						}
						got[0] ^= 0xff
						snapshot := v.Program()
						snapshot[0] ^= 0xff
						again, _ := v.Symbol(uint8(index))
						if !bytes.Equal(again, want) || !bytes.Equal(v.Program(), code) {
							t.Fatal("snapshot escaped")
						}
						if err = v.Step(); !errors.Is(err, ErrTruncated) {
							t.Fatalf("final operand consumed incorrectly: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestStoreStackFaultOrder(t *testing.T) {
	cases := []struct {
		name    string
		depth   int
		operand bool
		index   byte
		size    int
		want    error
	}{
		{"missing-empty", 0, false, 0, 2, ErrTruncated}, {"missing-full", 65, false, 255, 0, ErrTruncated},
		{"overflow-invalid", 65, true, 255, 0, ErrStackOverflow}, {"overflow-valid", 65, true, 0, 2, ErrStackOverflow},
		{"invalid-empty", 0, true, 255, 2, ErrInvalidSymbol}, {"invalid-one", 1, true, 1, 2, ErrInvalidSymbol},
		{"zero-empty", 0, true, 0, 0, ErrInvalidSymbolRegion}, {"one-empty", 0, true, 0, 1, ErrInvalidSymbolRegion},
		{"zero-one", 1, true, 0, 0, ErrInvalidSymbolRegion}, {"one-one", 1, true, 0, 1, ErrInvalidSymbolRegion},
		{"underflow", 0, true, 0, 2, ErrStackUnderflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := storeStackProgram(tc.depth, 0x8000)
			offset := len(code)
			code = append(code, 0x0a)
			if tc.operand {
				code = append(code, tc.index)
			}
			v, err := NewWithSymbols(code, 0, []SymbolRegion{{Length: uint32(tc.size), Initial: bytes.Repeat([]byte{0x55}, tc.size)}})
			if err != nil {
				t.Fatal(err)
			}
			if err = v.Run(uint64(tc.depth)); err != ErrBudget {
				t.Fatal(err)
			}
			// Internal snapshots also ensure inaccessible slots and saved PCs stay intact.
			v.returns[0] = 19
			v.returnDepth = 1
			stack, returns := v.stack, v.returns
			before, _ := v.Symbol(0)
			err = v.Step()
			var e *ExecutionError
			if !errors.Is(err, tc.want) || !errors.As(err, &e) || e.Offset != offset || v.PC() != offset+1 {
				t.Fatalf("fault: %v pc%d", err, v.PC())
			}
			for _, repeat := range []error{v.Step(), v.Run(0), v.Run(99)} {
				if repeat != err {
					t.Fatal("not sticky")
				}
			}
			after, _ := v.Symbol(0)
			if v.stack != stack || v.depth != tc.depth || v.returns != returns || v.returnDepth != 1 || !bytes.Equal(before, after) || !bytes.Equal(code, v.Program()) || v.Halted() {
				t.Fatal("fault mutated state")
			}
		})
	}
}

func TestStoreStackSharedViews(t *testing.T) {
	for _, region := range []AddressRegion{AddressFile, AddressRAM} {
		t.Run(fmt.Sprint(region), func(t *testing.T) {
			code := []byte{0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 5, 0x80, 0x0a, 0, 0xff}
			ram := bytes.Repeat([]byte{0xaa}, 5)
			v, err := NewWithAddressSpace(code, 5, AddressSpace{FileLength: 5, RAM: ram, Symbols: []AddressSymbol{{Region: region, Offset: 1, Length: 3}, {Region: region, Offset: 2, Length: 2}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = v.Run(2); err != ErrBudget {
				t.Fatal(err)
			}
			a, _ := v.Symbol(0)
			b, _ := v.Symbol(1)
			if !bytes.Equal(a, []byte{0x80, 0xff, 0xaa}) || !bytes.Equal(b, []byte{0xff, 0xaa}) || len(v.Stack()) != 0 || v.PC() != 9 {
				t.Fatalf("views %x %x", a, b)
			}
			addr := uint16(1)
			if region == AddressFile {
				addr |= 0x4000
			}
			word, err := v.ReadWord(addr)
			if err != nil || word != 0xaaff {
				t.Fatalf("word %x %v", word, err)
			}
			a[0] = 0
			b[0] = 0
			ram[1] = 0
			code[1] = 0
			again, _ := v.Symbol(0)
			if !bytes.Equal(again, []byte{0x80, 0xff, 0xaa}) {
				t.Fatal("isolation")
			}
			if err = v.Run(1); err != nil || !v.Halted() {
				t.Fatal(err)
			}
		})
	}
	// Shared address mode permits short descriptors when the full word is safe.
	for _, offset := range []uint32{0, 3, 4} {
		for _, length := range []uint32{0, 1} {
			if offset+length > 4 {
				continue
			}
			t.Run(fmt.Sprintf("bounded/%d/%d", offset, length), func(t *testing.T) {
				v, err := NewWithAddressSpace([]byte{5, 1, 0x0a, 0}, 0, AddressSpace{RAM: make([]byte, 4), Symbols: []AddressSymbol{{Region: AddressRAM, Offset: offset, Length: length}, {Region: AddressRAM, Offset: 0, Length: 4}}})
				if err != nil {
					t.Fatal(err)
				}
				err = v.Run(2)
				if offset == 0 {
					got, _ := v.Symbol(1)
					if err != ErrBudget || v.PC() != 4 || len(v.Stack()) != 0 || !bytes.Equal(got, []byte{1, 0, 0, 0}) {
						t.Fatalf("global word: %v %x", err, got)
					}
				} else if !errors.Is(err, ErrInvalidSymbolRegion) || v.PC() != 3 || !reflect.DeepEqual(v.Stack(), []uint16{1}) {
					t.Fatalf("bound: %v", err)
				}
			})
		}
	}
}

func TestStoreStackFetchAliases(t *testing.T) {
	for _, address := range []bool{false, true} {
		for _, self := range []bool{false, true} {
			t.Run(fmt.Sprintf("address%v/self%v", address, self), func(t *testing.T) {
				code := []byte{0xcc, 6, 0, 0xff, 0x0a, 0, 0xfe, 0xff}
				offset := uint32(6)
				if self {
					offset = 5
				}
				var v *VM
				var err error
				if address {
					v, err = NewWithAddressSpace(code, 1, AddressSpace{FileStart: 1, FileLength: 7, Symbols: []AddressSymbol{{Region: AddressFile, Offset: offset - 1, Length: 2}}})
				} else {
					v, err = NewWithSymbols(code, 1, []SymbolRegion{{ProgramOffset: &offset, Length: 2}})
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Run(2); err != ErrBudget || v.PC() != 6 || len(v.Stack()) != 0 {
					t.Fatalf("store %v pc%d", err, v.PC())
				}
				want := append([]byte(nil), code...)
				want[offset], want[offset+1] = 0xff, 0
				if !bytes.Equal(v.Program(), want) {
					t.Fatal("whole program incoherent")
				}
				budget := uint64(1)
				if self {
					budget = 2
				}
				if err = v.Run(budget); err != nil || !v.Halted() {
					t.Fatalf("future fetch: %v", err)
				}
			})
		}
	}
}

func TestStoreStackReturnPreservation(t *testing.T) {
	// call subroutine, push -1 using actual05, store, return to halt.
	v, err := NewWithSymbols([]byte{0x44, 0, 4, 0xff, 5, 0xff, 0x0a, 0, 0x45}, 0, []SymbolRegion{{Length: 2, Initial: []byte{0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Run(2); err != ErrBudget {
		t.Fatal(err)
	}
	returns, depth := v.returns, v.returnDepth
	if err = v.Run(1); err != ErrBudget || v.returns != returns || v.returnDepth != depth || v.PC() != 8 {
		t.Fatalf("saved PC changed: %v", err)
	}
	if err = v.Run(2); err != nil || !v.Halted() || v.PC() != 4 || v.returnDepth != 0 {
		t.Fatalf("return: %v", err)
	}
	got, _ := v.Symbol(0)
	if !bytes.Equal(got, []byte{0xff, 0xff}) {
		t.Fatalf("store %x", got)
	}
}

func TestStoreStackGlobalBounds(t *testing.T) {
	for _, region := range []AddressRegion{AddressFile, AddressRAM} {
		for _, offset := range []uint32{1, 2, 3, 4} {
			for _, length := range []uint32{0, 1} {
				if offset+length > 4 {
					continue
				}
				for _, depth := range []int{0, 1, 65} {
					t.Run(fmt.Sprintf("r%d/o%d/n%d/d%d", region, offset, length, depth), func(t *testing.T) {
						code := append([]byte{0xaa, 0xaa, 0xaa, 0xaa}, storeStackProgram(depth, 0x8000)...)
						op := len(code)
						code = append(code, 0x0a, 0)
						v, err := NewWithAddressSpace(code, 4, AddressSpace{FileLength: 4, RAM: bytes.Repeat([]byte{0xaa}, 4), Symbols: []AddressSymbol{{Region: region, Offset: offset, Length: length}, {Region: region, Length: 4}}})
						if err != nil {
							t.Fatal(err)
						}
						if err = v.Run(uint64(depth)); err != ErrBudget {
							t.Fatal(err)
						}
						before, _ := v.Symbol(1)
						stack := v.stack
						var want error
						if depth == 65 {
							want = ErrStackOverflow
						} else if offset+2 > 4 {
							want = ErrInvalidSymbolRegion
						} else if depth == 0 {
							want = ErrStackUnderflow
						}
						err = v.Step()
						got, _ := v.Symbol(1)
						if want != nil {
							if !errors.Is(err, want) || v.PC() != op+1 || v.stack != stack || !bytes.Equal(got, before) || !bytes.Equal(v.Program(), code) || v.Step() != err || v.Run(0) != err {
								t.Fatalf("transaction: %v", err)
							}
						} else {
							before[offset], before[offset+1] = 0, 0x80
							if err != nil || v.PC() != op+2 || len(v.Stack()) != 0 || !bytes.Equal(got, before) {
								t.Fatalf("global span: %v %x", err, got)
							}
						}
					})
				}
			}
		}
	}
}
