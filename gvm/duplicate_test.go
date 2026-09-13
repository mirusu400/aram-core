package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func duplicatePush(code []byte, value uint16) []byte {
	return append(code, 6, byte(value>>8), byte(value))
}

func TestDuplicateTerminal(t *testing.T) {
	v := New([]byte{6, 0x80, 0x01, 0x0f})
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	if s := v.Stack(); len(s) != 2 || s[0] != 0x8001 || s[1] != 0x8001 || v.PC() != 4 {
		t.Fatalf("pc%d stack%x", v.PC(), s)
	}
	before := v.Stack()
	err := v.Step()
	var fault *ExecutionError
	if !errors.Is(err, ErrTruncated) || !errors.As(err, &fault) || fault.Offset != 4 || v.PC() != 4 || v.Step() != err || v.Run(0) != err || !reflect.DeepEqual(v.Stack(), before) {
		t.Fatalf("next fetch: %v", err)
	}
}

func TestDuplicateAllRawWords(t *testing.T) {
	// Two VMs cover all raw16 values at depth1 and64 without per-value allocation
	// of VM/program storage. Existing96 pops discard the two test operands.
	for _, depth := range []int{1, 64} {
		t.Run(fmt.Sprintf("depth%d", depth), func(t *testing.T) {
			var code []byte
			prefix := make([]uint16, depth-1)
			for i := range prefix {
				prefix[i] = uint16(0x9200 + i)
				code = duplicatePush(code, prefix[i])
			}
			code = append(code, 0x0b)
			start := len(code)
			for raw := uint32(0); raw < 65536; raw++ {
				code = duplicatePush(code, uint16(raw))
				code = append(code, 0x0f, 0x96, 0x96)
			}
			code = append(code, 0xff)
			v := New(code)
			if err := v.Run(uint64(depth)); err != ErrBudget {
				t.Fatal(err)
			}
			marker, valid := v.savedTop, v.savedTopValid
			for raw := uint32(0); raw < 65536; raw++ {
				if err := v.Step(); err != nil {
					t.Fatal(err)
				}
				if err := v.Step(); err != nil {
					t.Fatalf("raw%x: %v", raw, err)
				}
				stack := v.Stack()
				if len(stack) != depth+1 || stack[depth-1] != uint16(raw) || stack[depth] != uint16(raw) || !reflect.DeepEqual(stack[:depth-1], prefix) || v.PC() != start+int(raw)*6+4 || v.Halted() {
					t.Fatalf("raw%x stack%x pc%d", raw, stack, v.PC())
				}
				if v.savedTop != marker || v.savedTopValid != valid {
					t.Fatal("duplicate changed saved index")
				}
				if err := v.Run(2); err != ErrBudget {
					t.Fatal(err)
				}
			}
			if err := v.Step(); err != nil || !v.Halted() {
				t.Fatal("halt", err)
			}
			if !bytes.Equal(v.Program(), code) {
				t.Fatal("duplicate changed program")
			}
		})
	}
}

func TestDuplicateDepthConstructorMatrix(t *testing.T) {
	for depth := 0; depth <= 65; depth++ {
		for mode := 0; mode < 5; mode++ {
			t.Run(fmt.Sprintf("depth%d/mode%d", depth, mode), func(t *testing.T) {
				code := []byte{0xf0, 0xf1, 0x0b}
				for i := 0; i < depth; i++ {
					code = duplicatePush(code, uint16(0x8100+i))
				}
				code = append(code, 0x0f, 0xff)
				space := AddressSpace{FileLength: uint32(len(code)), RAM: []byte{0x13, 0x87, 0x51}, Symbols: []AddressSymbol{{Region: AddressFile, Length: uint32(len(code))}, {Region: AddressRAM, Length: 3}}}
				var v *VM
				var err error
				switch mode {
				case 0:
					v = New(code[2:])
				case 1:
					v, err = NewAt(code, 2)
				case 2:
					v, err = NewWithSymbols(code, 2, []SymbolRegion{{Initial: []byte{0x13, 0x87, 0x51}, Length: 3}})
				case 3:
					v, err = NewWithAddressSpace(code, 2, space)
				case 4:
					v, err = NewWithAddressSpaceAndServices(code, 2, space, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Run(uint64(depth + 1)); err != ErrBudget {
					t.Fatal(err)
				}
				before, program, pc := v.Stack(), v.Program(), v.PC()
				backing, returns, returnDepth := v.stack, v.returns, v.returnDepth
				marker, valid := v.savedTop, v.savedTopValid
				symbols := make([][]byte, len(v.symbols))
				for i := range symbols {
					symbols[i], err = v.Symbol(uint8(i))
					if err != nil {
						t.Fatal(err)
					}
				}
				if err = v.Run(0); err != ErrBudget || v.PC() != pc || !reflect.DeepEqual(v.Stack(), before) {
					t.Fatal("zero budget changed state")
				}
				err = v.Run(1)
				if depth == 0 || depth == 65 {
					want := ErrStackUnderflow
					if depth == 65 {
						want = ErrStackOverflow
					}
					var fault *ExecutionError
					if !errors.Is(err, want) || !errors.As(err, &fault) || fault.Offset != pc || v.stack != backing || !reflect.DeepEqual(v.Stack(), before) {
						t.Fatalf("guard fault: %v", err)
					}
					if v.Step() != err || v.Run(0) != err || v.Run(100) != err || v.stack != backing {
						t.Fatal("fault not sticky")
					}
				} else {
					want := append(append([]uint16(nil), before...), before[len(before)-1])
					if err != ErrBudget || !reflect.DeepEqual(v.Stack(), want) {
						t.Fatalf("duplicate err=%v stack%x", err, v.Stack())
					}
				}
				if v.PC() != pc+1 || v.Halted() || v.savedTop != marker || v.savedTopValid != valid || v.returns != returns || v.returnDepth != returnDepth || !bytes.Equal(v.Program(), program) {
					t.Fatal("duplicate changed unrelated state")
				}
				for i := range symbols {
					got, e := v.Symbol(uint8(i))
					if e != nil || !bytes.Equal(got, symbols[i]) {
						t.Fatal("symbol changed")
					}
				}
				if mode >= 3 {
					got, e := v.ReadWord(0)
					if e != nil || got != 0x8713 {
						t.Fatal("RAM changed")
					}
				}
				if depth > 0 && depth < 65 {
					if err = v.Run(1); err != nil || !v.Halted() {
						t.Fatal("next halt", err)
					}
				}
			})
		}
	}
}

func TestDuplicateRepeatedAndSavedTop(t *testing.T) {
	code := duplicatePush(nil, 0xabcd)
	for depth := 1; depth < 65; depth++ {
		if depth == 32 {
			code = append(code, 0x0b)
		}
		code = append(code, 0x0f)
	}
	code = append(code, 0x0f)
	v := New(code)
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	for depth := 1; depth < 65; depth++ {
		if depth == 32 {
			if err := v.Step(); err != nil {
				t.Fatal(err)
			}
		}
		marker, valid := v.savedTop, v.savedTopValid
		if err := v.Step(); err != nil {
			t.Fatal(err)
		}
		if v.savedTop != marker || v.savedTopValid != valid || len(v.Stack()) != depth+1 {
			t.Fatal("repeated duplicate changed marker or depth")
		}
		for _, word := range v.Stack() {
			if word != 0xabcd {
				t.Fatal("raw word changed")
			}
		}
	}
	if v.savedTop != 31 || !v.savedTopValid {
		t.Fatal("capture fixture failed")
	}
	before := v.Stack()
	err := v.Step()
	if !errors.Is(err, ErrStackOverflow) || v.Step() != err || !reflect.DeepEqual(v.Stack(), before) || v.savedTop != 31 || !v.savedTopValid {
		t.Fatal("overflow changed state", err)
	}
}

func TestDuplicateNestedReturns(t *testing.T) {
	code := []byte{0xf0, 0xf1, 0x44, 0, 6, 0xff, 0x44, 0, 10, 0x45}
	code = duplicatePush(code, 0xffff)
	code = append(code, 0x0b, 0x0f, 0x45)
	v, err := NewAt(code, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Run(4); err != ErrBudget || v.PC() != 14 {
		t.Fatal(err)
	}
	returns, depth := v.returns, v.returnDepth
	if depth != 2 {
		t.Fatal("nested fixture")
	}
	if err = v.Step(); err != nil || v.PC() != 15 || v.returns != returns || v.returnDepth != depth || v.savedTop != 0 || !v.savedTopValid {
		t.Fatal("duplicate changed metadata", err)
	}
	if err = v.Run(3); err != nil || !v.Halted() || v.PC() != 6 || !reflect.DeepEqual(v.Stack(), []uint16{0xffff, 0xffff}) || !bytes.Equal(v.Program(), code) {
		t.Fatal("nested return path", err)
	}
}

func TestDuplicateInertHaltAndFault(t *testing.T) {
	for _, stop := range []byte{0xff, 0xf0, 0x0d} {
		code := []byte{stop, 0x0f}
		v := New(code)
		err := v.Step()
		if stop == 0xff {
			if err != nil || !v.Halted() {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("fixture did not fault")
		}
		for _, budget := range []uint64{0, 1, 100} {
			if v.Run(budget) != err || v.Step() != err || v.PC() != 1 || len(v.Stack()) != 0 || v.savedTopValid || !bytes.Equal(v.Program(), code) {
				t.Fatal("inert duplicate dispatched")
			}
		}
	}
}
