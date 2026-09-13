package gvm_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

// These are explicit host representational restrictions, not inferred descriptor metadata.
func TestAddressIndexedRawEncoding(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressRAM, gvm.AddressFile} {
		for _, index := range []byte{0, 127, 128, 254, 255} {
			for _, element := range []byte{0, 127, 128, 254} {
				for _, offset := range []uint32{0, 1, 3, 0x7fff, 0x8000, 0xffff, 0x10000, 0x1ffff, 0x20000} {
					t.Run(fmt.Sprintf("r%d/i%d/e%d/off%x", region, index, element, offset), func(t *testing.T) {
						size := int(offset) + 510
						payload := bytes.Repeat([]byte{0xa7}, size)
						code := append([]byte{0x4c, index, element, 0xff}, payload...)
						symbols := make([]gvm.AddressSymbol, max(int(index)+1, 2))
						for i := range symbols {
							symbols[i] = gvm.AddressSymbol{Region: region}
						}
						symbols[index] = gvm.AddressSymbol{Region: region, Offset: offset, Length: 510}
						// Extra whole-region view observes bytes outside the selected descriptor too.
						observer := byte(0)
						if index == 0 {
							observer = 1
						}
						symbols[observer] = gvm.AddressSymbol{Region: region, Length: uint32(size)}
						v := addressVM(t, code, gvm.AddressSpace{FileStart: 4, FileLength: uint32(size), RAM: payload, Symbols: symbols})
						before, _ := v.Symbol(observer)
						if err := v.Run(0); err != gvm.ErrBudget || v.PC() != 0 {
							t.Fatalf("zero budget: %v", err)
						}
						want := uint16((uint64(offset) + 2*uint64(element)) >> 1)
						if region == gvm.AddressFile {
							want |= 0x4000
						}
						// Never dereference the encoding: negative/tag-colliding words are still valid results.
						if err := v.Run(1); err != gvm.ErrBudget || v.PC() != 3 || v.Halted() || !reflect.DeepEqual(v.Stack(), []uint16{want}) {
							t.Fatalf("address: %v pc=%d stack=%x want=%x", err, v.PC(), v.Stack(), want)
						}
						after, _ := v.Symbol(observer)
						if !bytes.Equal(before, after) || !bytes.Equal(v.Program(), code) {
							t.Fatal("memory changed")
						}
						if err := v.Run(1); err != nil || !v.Halted() || v.PC() != 4 {
							t.Fatalf("halt: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestAddressIndexedGuardOrder(t *testing.T) {
	cases := []struct {
		name     string
		operands []byte
		length   uint32
		index    byte
		depth    int
		want     error
	}{
		{"empty-stack", []byte{0, 0}, 2, 0, 0, nil},
		{"depth64", []byte{0, 0}, 2, 0, 64, nil},
		{"depth65", []byte{0, 0}, 2, 0, 65, gvm.ErrStackOverflow},
		{"capacity-before-symbol", []byte{255, 255}, 1, 0, 65, gvm.ErrStackOverflow},
		{"symbol-before-shape", []byte{255, 255}, 1, 0, 0, gvm.ErrInvalidSymbol},
		{"symbol128", []byte{128, 0}, 2, 0, 0, gvm.ErrInvalidSymbol},
		{"empty", []byte{0, 0}, 0, 0, 0, gvm.ErrInvalidElement},
		{"one-word", []byte{0, 1}, 2, 0, 0, gvm.ErrInvalidElement},
		{"element255", []byte{0, 255}, 510, 0, 0, gvm.ErrInvalidElement},
		{"short", []byte{0, 0}, 1, 0, 0, gvm.ErrInvalidSymbolRegion},
		{"odd-before-element", []byte{0, 255}, 509, 0, 0, gvm.ErrInvalidSymbolRegion},
		{"oversize", []byte{0, 0}, 512, 0, 0, gvm.ErrInvalidSymbolRegion},
	}
	for _, depth := range []int{0, 64, 65} {
		for n := 0; n < 2; n++ {
			cases = append(cases, struct {
				name     string
				operands []byte
				length   uint32
				index    byte
				depth    int
				want     error
			}{fmt.Sprintf("truncated%d-depth%d", n, depth), []byte{255}[:n], 1, 0, depth, gvm.ErrTruncated})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var code []byte
			for i := 0; i < tc.depth; i++ {
				code = append(code, 6, byte(i), byte(255-i))
			}
			offset := len(code)
			code = append(code, 0x4c)
			code = append(code, tc.operands...)
			// Exact descriptor span, with backing bytes beyond it. Offset is deliberately odd.
			payload := bytes.Repeat([]byte{0x97}, 515)
			v := addressVM(t, code, gvm.AddressSpace{RAM: payload, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Offset: 3, Length: tc.length}, {Region: gvm.AddressRAM, Length: 515}}})
			if err := v.Run(uint64(tc.depth)); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			before := v.Stack()
			memory, _ := v.Symbol(1)
			err := v.Step()
			if tc.want == nil {
				want := append(before, 1)
				if err != nil || v.PC() != offset+3 || !reflect.DeepEqual(v.Stack(), want) {
					t.Fatalf("success: %v pc=%d stack=%x", err, v.PC(), v.Stack())
				}
			} else {
				var fault *gvm.ExecutionError
				if !errors.Is(err, tc.want) || !errors.As(err, &fault) || fault.Offset != offset || v.PC() != offset+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), before) {
					t.Fatalf("fault: %v pc=%d stack=%x want=%v", err, v.PC(), v.Stack(), tc.want)
				}
				if v.Step() != err || v.Run(0) != err || v.Run(100) != err || v.PC() != offset+1 || !reflect.DeepEqual(v.Stack(), before) {
					t.Fatal("fault not sticky/atomic")
				}
			}
			after, _ := v.Symbol(1)
			if !bytes.Equal(memory, after) || !bytes.Equal(v.Program(), code) {
				t.Fatal("memory changed")
			}
		})
	}
}

func TestAddressIndexedConfigurationFirst(t *testing.T) {
	for _, depth := range []int{0, 64, 65} {
		for _, operands := range [][]byte{nil, {255}, {255, 255}} {
			var code []byte
			for i := 0; i < depth; i++ {
				code = append(code, 5, 1)
			}
			offset := len(code)
			code = append(code, 0x4c)
			code = append(code, operands...)
			at, e := gvm.NewAt(code, 0)
			if e != nil {
				t.Fatal(e)
			}
			symbols, e := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{1, 2}}})
			if e != nil {
				t.Fatal(e)
			}
			for _, v := range []*gvm.VM{gvm.New(code), at, symbols} {
				if e := v.Run(uint64(depth)); e != gvm.ErrBudget {
					t.Fatal(e)
				}
				before := v.Stack()
				err := v.Step()
				var unsupported *gvm.UnsupportedOpcodeError
				if !errors.As(err, &unsupported) || unsupported.Opcode != 0x4c || unsupported.Offset != offset || v.PC() != offset+1 || v.Halted() {
					t.Fatalf("configuration first: %v", err)
				}
				if v.Step() != err || v.Run(0) != err || v.Run(100) != err || !reflect.DeepEqual(v.Stack(), before) || !bytes.Equal(v.Program(), code) {
					t.Fatal("legacy failure mutated state")
				}
			}
		}
	}
}

func TestAddressIndexedReturnAndInstructionAlias(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressRAM, gvm.AddressFile} {
		code := []byte{0x44, 0, 4, 0xff, 0x4c, 0, 1, 0x45}
		v := addressVM(t, code, gvm.AddressSpace{FileLength: 8, RAM: code, Symbols: []gvm.AddressSymbol{{Region: region, Length: 8}}})
		want := uint16(1)
		if region == gvm.AddressFile {
			want |= 0x4000
		}
		if err := v.Run(2); err != gvm.ErrBudget || v.PC() != 7 || !reflect.DeepEqual(v.Stack(), []uint16{want}) {
			t.Fatalf("call/address: %v pc=%d stack=%x", err, v.PC(), v.Stack())
		}
		if err := v.Run(0); err != gvm.ErrBudget || v.PC() != 7 {
			t.Fatal("budget changed return state")
		}
		if err := v.Run(2); err != nil || !v.Halted() || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), []uint16{want}) {
			t.Fatalf("return/halt: %v pc=%d", err, v.PC())
		}
		memory, _ := v.Symbol(0)
		if !bytes.Equal(memory, code) || !bytes.Equal(v.Program(), code) {
			t.Fatal("instruction alias mutated")
		}
	}
}

func TestAddressIndexedFullBindingSpan(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressRAM, gvm.AddressFile} {
		for _, tc := range []struct {
			name   string
			offset uint32
			length uint32
			valid  bool
		}{
			{"exact-word", 3, 2, true},
			{"last-byte-not-full-word", 4, 2, false},
			{"whole-descriptor-not-only-selected-word", 1, 6, false},
			{"end-not-member", 5, 2, false},
			{"wide-offset-sum", ^uint32(0), 2, false},
		} {
			t.Run(fmt.Sprintf("r%d/%s", region, tc.name), func(t *testing.T) {
				code := []byte{0x4c, 0, 0, 0xff, 0xa7, 0xa7, 0xa7, 0xa7, 0xa7}
				v, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{
					FileStart: 4, FileLength: 5, RAM: code[4:],
					Symbols: []gvm.AddressSymbol{{Region: region, Offset: tc.offset, Length: tc.length}},
				})
				if !tc.valid {
					if !errors.Is(err, gvm.ErrInvalidSymbolRegion) || v != nil {
						t.Fatalf("invalid full binding accepted: vm=%v err=%v", v, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := uint16(1)
				if region == gvm.AddressFile {
					want |= 0x4000
				}
				if err := v.Step(); err != nil || v.PC() != 3 || !reflect.DeepEqual(v.Stack(), []uint16{want}) {
					t.Fatalf("last full word: %v pc=%d stack=%x", err, v.PC(), v.Stack())
				}
				if !bytes.Equal(v.Program(), code) {
					t.Fatal("address push changed payload or instruction bytes")
				}
			})
		}
	}
}
