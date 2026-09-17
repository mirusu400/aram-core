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

func scalarProgram(words ...uint16) []byte {
	var code []byte
	for _, word := range words {
		code = append(code, 6, byte(word>>8), byte(word))
	}
	return append(code, 0xb4, 0xff)
}

// An int64 arithmetic oracle avoids copying the handler's narrow-width math.
func scalarExpected(x, v uint16, selector uint16) uint16 {
	a, b := int64(int16(x)), int64(int16(v))
	switch selector {
	case 0:
		return v
	case 1:
		return uint16(a + b)
	case 2:
		return uint16(a - b)
	case 3:
		return uint16(a * b)
	case 4, 5:
		aa, bb := a, b
		if aa < 0 {
			aa = -aa
		}
		if bb < 0 {
			bb = -bb
		}
		q := aa / bb
		if (a < 0) != (b < 0) {
			q = -q
		}
		if selector == 4 {
			return uint16(q)
		}
		return uint16(a - q*b)
	case 6:
		return x & v
	case 7:
		return x | v
	case 8:
		return v ^ 65535
	case 9:
		return x ^ v
	case 10:
		for i := uint16(0); i < v%32; i++ {
			if a < 0 && a%2 != 0 {
				a--
			}
			a /= 2
		}
		return uint16(a)
	case 11:
		a = int64(x)
		for i := uint16(0); i < v%32; i++ {
			a = (a * 2) % 65536
		}
		return uint16(a)
	}
	panic("test selector")
}

func TestArrayScalarOperations(t *testing.T) {
	values := []uint16{0x8000, 0xfff9, 0xffff, 0, 1, 7, 0x7fff, 0x5555, 0xaaaa}
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for selector := uint16(0); selector < 12; selector++ {
			for _, scalar := range []uint16{0, 1, 3, 0xfffd, 0xffff, 0x8000, 0x7fff, 15, 16, 31, 32, 33, 255} {
				if (selector == 4 || selector == 5) && scalar == 0 {
					continue
				}
				t.Run(fmt.Sprintf("r%d/op%d/v%x", region, selector, scalar), func(t *testing.T) {
					payload := bytes.Repeat([]byte{0x97}, 2*len(values)+4)
					expected := append([]byte(nil), payload...)
					for i, x := range values {
						binary.LittleEndian.PutUint16(payload[2+2*i:], x)
						binary.LittleEndian.PutUint16(expected[2+2*i:], scalarExpected(x, scalar, selector))
					}
					ref := uint16(1)
					if region == gvm.AddressFile {
						ref |= 0x4000
					}
					// Prefix survives and depth65 is legal for an operation that pops four.
					words := make([]uint16, 61)
					for i := range words {
						words[i] = uint16(i + 0x8000)
					}
					prefix := append([]uint16(nil), words...)
					words = append(words, ref, scalar, uint16(len(values)), selector)
					code := scalarProgram(words...)
					fileStart := len(code)
					code = append(code, payload...)
					original := append([]byte(nil), code...)
					ram := append([]byte(nil), payload...)
					v := addressVM(t, code, gvm.AddressSpace{FileStart: uint32(fileStart), FileLength: uint32(len(payload)), RAM: ram, Symbols: []gvm.AddressSymbol{
						{Region: region, Offset: 2, Length: 2}, // Operation intentionally crosses descriptor end.
						{Region: region, Length: uint32(len(payload))},
					}})
					// Constructor ownership isolates caller storage before operation.
					code[fileStart] = 0
					ram[0] = 0
					if err := v.Run(65); err != gvm.ErrBudget {
						t.Fatal(err)
					}
					pc := v.PC()
					if err := v.Run(0); err != gvm.ErrBudget || v.PC() != pc {
						t.Fatal("zero budget advanced")
					}
					if err := v.Step(); err != nil || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), prefix) || v.Halted() {
						t.Fatalf("operation: %v pc=%d stack=%x", err, v.PC(), v.Stack())
					}
					got, _ := v.Symbol(1)
					if !bytes.Equal(got, expected) {
						t.Fatalf("words: got%x want%x", got, expected)
					}
					if region == gvm.AddressFile {
						copy(original[fileStart:], expected)
					}
					if !bytes.Equal(v.Program(), original) {
						t.Fatal("unexpected code/other-region mutation")
					}
					got[0] = 0
					again, _ := v.Symbol(1)
					if !bytes.Equal(again, expected) {
						t.Fatal("snapshot aliases VM")
					}
					if err := v.Run(1); err != nil || !v.Halted() {
						t.Fatalf("halt: %v", err)
					}
				})
			}
		}
	}
}

func TestArrayScalarNonpositiveCount(t *testing.T) {
	for selector := uint16(0); selector < 12; selector++ {
		for _, count := range []uint16{0, 0xffff, 0x8000} {
			code := scalarProgram(0, 7, count, selector)
			v := addressVM(t, code, gvm.AddressSpace{RAM: []byte{0x12, 0x34}, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 2}}})
			if err := v.Run(5); err != gvm.ErrBudget || len(v.Stack()) != 0 || v.PC() != 13 {
				t.Fatalf("op%d count%x: %v", selector, count, err)
			}
			got, _ := v.Symbol(0)
			if !bytes.Equal(got, []byte{0x12, 0x34}) || !bytes.Equal(v.Program(), code) {
				t.Fatal("nonpositive count wrote memory")
			}
		}
	}
}

func TestArrayScalarExistingFaults(t *testing.T) {
	cases := []struct {
		name  string
		words []uint16
		ram   int
		want  error
	}{
		{"empty", nil, 8, gvm.ErrStackUnderflow},
		{"one", []uint16{0}, 8, gvm.ErrStackUnderflow},
		{"two", []uint16{0, 0}, 8, gvm.ErrStackUnderflow},
		{"three", []uint16{0, 0, 0}, 8, gvm.ErrStackUnderflow},
		{"negative-ref", []uint16{0x8000, 1, 1, 0}, 8, gvm.ErrInvalidAddress},
		{"negative-file-ref", []uint16{0xc000, 1, 1, 0}, 8, gvm.ErrInvalidAddress},
		{"missing-file", []uint16{0x4000, 1, 1, 0}, 8, gvm.ErrInvalidAddress},
		{"empty-arena", []uint16{0, 1, 0, 0}, 0, gvm.ErrInvalidAddress},
		{"odd-tail-start", []uint16{1, 1, 0, 0}, 3, gvm.ErrInvalidAddress},
		{"span", []uint16{0, 1, 5, 0}, 8, gvm.ErrInvalidAddress},
		{"max-positive-span", []uint16{0, 1, 0x7fff, 1}, 8, gvm.ErrInvalidAddress},
		{"reference-before-divisor", []uint16{0x8000, 0, 0, 4}, 8, gvm.ErrInvalidAddress},
		{"divisor-before-span", []uint16{0, 0, 5, 4}, 8, gvm.ErrDivideByZero},
		{"selector12", []uint16{0, 1, 1, 12}, 8, gvm.ErrInvalidArraySelector},
		{"negative-selector", []uint16{0, 1, 1, 0xffff}, 8, gvm.ErrInvalidArraySelector},
		{"selector-highbit", []uint16{0, 1, 1, 0x8000}, 8, gvm.ErrInvalidArraySelector},
		{"reference-before-selector", []uint16{0x8000, 0, 5, 12}, 8, gvm.ErrInvalidAddress},
		{"selector-before-span", []uint16{0, 0, 5, 12}, 8, gvm.ErrInvalidArraySelector},
	}
	for _, selector := range []uint16{4, 5} {
		for _, count := range []uint16{0, 1, 0xffff, 0x8000} {
			cases = append(cases, struct {
				name  string
				words []uint16
				ram   int
				want  error
			}{fmt.Sprintf("zero-op%d-count%x", selector, count), []uint16{0, 0, count, selector}, 8, gvm.ErrDivideByZero})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := scalarProgram(tc.words...)
			payload := bytes.Repeat([]byte{0xa7}, tc.ram)
			v := addressVM(t, code, gvm.AddressSpace{RAM: payload, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: uint32(tc.ram)}}})
			if err := v.Run(uint64(len(tc.words))); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			before := v.Stack()
			pc := v.PC()
			err := v.Step()
			var fault *gvm.ExecutionError
			if !errors.Is(err, tc.want) || !errors.As(err, &fault) || fault.Offset != pc || v.PC() != pc+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), before) {
				t.Fatalf("fault: %v pc%d stack%x", err, v.PC(), v.Stack())
			}
			for _, again := range []error{v.Step(), v.Run(0), v.Run(100)} {
				if again != err {
					t.Fatal("fault not sticky")
				}
			}
			got, _ := v.Symbol(0)
			if !bytes.Equal(got, payload) || !bytes.Equal(v.Program(), code) || !reflect.DeepEqual(v.Stack(), before) || v.PC() != pc+1 {
				t.Fatal("fault mutated state")
			}
		})
	}
}

func TestArrayScalarLegacyUnsupported(t *testing.T) {
	for _, words := range [][]uint16{nil, {0, 0, 0, 0}} {
		code := scalarProgram(words...)
		at, err := gvm.NewAt(code, 0)
		if err != nil {
			t.Fatal(err)
		}
		symbols, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0, 0}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []*gvm.VM{gvm.New(code), at, symbols} {
			if err := v.Run(uint64(len(words))); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			before := v.Stack()
			pc := v.PC()
			err := v.Step()
			var unsupported *gvm.UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 0xb4 || unsupported.Offset != pc || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), before) || v.Run(0) != err || v.Step() != err {
				t.Fatalf("legacy: %v", err)
			}
		}
	}
}

func TestArrayScalarCodeAliases(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, count := range []uint16{7, 8} {
			t.Run(fmt.Sprintf("r%d/count%d", region, count), func(t *testing.T) {
				ref := uint16(0)
				if region == gvm.AddressFile {
					ref = 0x4000
				}
				code := scalarProgram(ref, 0xffff, count, 0)
				v := addressVM(t, code, gvm.AddressSpace{FileLength: uint32(len(code)), RAM: code, Symbols: []gvm.AddressSymbol{
					{Region: region, Length: uint32(len(code))}, {Region: region, Offset: 2, Length: 4},
				}})
				if err := v.Run(4); err != gvm.ErrBudget {
					t.Fatal(err)
				}
				before := v.Stack()
				err := v.Step()
				if count == 8 {
					if !errors.Is(err, gvm.ErrInvalidAddress) || !reflect.DeepEqual(v.Stack(), before) || v.Run(0) != err {
						t.Fatalf("span rejection: %v", err)
					}
					got, _ := v.Symbol(0)
					if !bytes.Equal(got, code) || !bytes.Equal(v.Program(), code) {
						t.Fatal("partial alias store on failure")
					}
					return
				}
				if err != nil || v.PC() != 13 || len(v.Stack()) != 0 {
					t.Fatalf("alias: %v", err)
				}
				want := bytes.Repeat([]byte{0xff}, 14)
				got, _ := v.Symbol(0)
				if !bytes.Equal(got, want) {
					t.Fatalf("captured scalar/count: %x", got)
				}
				alias, _ := v.Symbol(1)
				if !bytes.Equal(alias, want[:4]) {
					t.Fatal("overlap not shared")
				}
				expectedCode := code
				if region == gvm.AddressFile {
					expectedCode = want
				}
				if !bytes.Equal(v.Program(), expectedCode) {
					t.Fatal("wrong instruction region")
				}
				if err := v.Step(); err != nil || !v.Halted() {
					t.Fatalf("future fetch: %v", err)
				}
			})
		}
	}
}

func TestArrayScalarCallReturnAndExactSpans(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, tc := range []struct{ index, count uint16 }{{0, 1}, {0, 0x7fff}, {0x3fff, 2}} {
			t.Run(fmt.Sprintf("r%d/start%x/count%d", region, tc.index, tc.count), func(t *testing.T) {
				size := 2 * (int(tc.index) + int(tc.count))
				payload := bytes.Repeat([]byte{0xa7}, size)
				ref := tc.index
				if region == gvm.AddressFile {
					ref |= 0x4000
				}
				callee := scalarProgram(ref, 0x1234, tc.count, 0)
				callee[len(callee)-1] = 0x45
				code := append([]byte{0x44, 0, 4, 0xff}, callee...)
				fileStart := len(code)
				code = append(code, payload...)
				v := addressVM(t, code, gvm.AddressSpace{FileStart: uint32(fileStart), FileLength: uint32(size), RAM: payload, Symbols: []gvm.AddressSymbol{{Region: region, Length: uint32(size)}}})
				if err := v.Run(6); err != gvm.ErrBudget || v.PC() != 17 || len(v.Stack()) != 0 {
					t.Fatalf("call/operation: %v pc%d", err, v.PC())
				}
				got, _ := v.Symbol(0)
				for i := 0; i < size; i += 2 {
					want := uint16(0xa7a7)
					if i >= 2*int(tc.index) {
						want = 0x1234
					}
					if binary.LittleEndian.Uint16(got[i:]) != want {
						t.Fatalf("word%d mismatch", i/2)
					}
				}
				if err := v.Run(2); err != nil || !v.Halted() || v.PC() != 4 {
					t.Fatalf("return/halt: %v pc%d", err, v.PC())
				}
			})
		}
	}
}
