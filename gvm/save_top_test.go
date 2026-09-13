package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// Internal assertions distinguish the real scalar assignment from a NOP.
// Invalid initial state is host bookkeeping, not native initialization evidence.
func saveTopCheck(t *testing.T, v *VM, want int32, valid bool) {
	t.Helper()
	if v.savedTopValid != valid || (valid && v.savedTop != want) {
		t.Fatalf("saved top = %d valid=%v, want %d valid=%v", v.savedTop, v.savedTopValid, want, valid)
	}
}

func TestSaveTopTerminal(t *testing.T) {
	v := New([]byte{0x0b})
	saveTopCheck(t, v, 0, false)
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	saveTopCheck(t, v, -1, true)
	if uint32(v.savedTop) != 0xffffffff {
		t.Fatal("empty sentinel lost upper bits")
	}
	if v.PC() != 1 || len(v.Stack()) != 0 || v.Halted() {
		t.Fatal("capture changed public state")
	}
	err := v.Step()
	var fault *ExecutionError
	if !errors.Is(err, ErrTruncated) || !errors.As(err, &fault) || fault.Offset != 1 || v.Step() != err || v.Run(0) != err {
		t.Fatalf("terminal fetch: %v", err)
	}
	saveTopCheck(t, v, -1, true)
}

func TestSaveTopAllDepthsAndBindings(t *testing.T) {
	for depth := 0; depth <= 65; depth++ {
		for mode := 0; mode < 5; mode++ {
			t.Run(fmt.Sprintf("depth%d/mode%d", depth, mode), func(t *testing.T) {
				// Nonzero entry and unrelated signed/raw data distinguish depth from value.
				code := []byte{0xf0, 0xf1}
				for i := 0; i < depth; i++ {
					value := uint16(0xf100 + i*17)
					code = append(code, 6, byte(value>>8), byte(value))
				}
				code = append(code, 0x0b, 0xff)
				var v *VM
				var err error
				space := AddressSpace{FileLength: uint32(len(code)), RAM: []byte{0x21, 0x83, 0x47}, Symbols: []AddressSymbol{
					{Region: AddressFile, Length: uint32(len(code))}, {Region: AddressRAM, Length: 3},
				}}
				switch mode {
				case 0:
					v = New(code[2:])
				case 1:
					v, err = NewAt(code, 2)
				case 2:
					v, err = NewWithSymbols(code, 2, []SymbolRegion{{Initial: []byte{0x21, 0x83, 0x47}, Length: 3}})
				case 3:
					v, err = NewWithAddressSpace(code, 2, space)
				case 4:
					v, err = NewWithAddressSpaceAndServices(code, 2, space, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				saveTopCheck(t, v, 0, false)
				if err = v.Run(uint64(depth)); err != ErrBudget {
					t.Fatal(err)
				}
				before, program, pc := v.Stack(), v.Program(), v.PC()
				symbols := make([][]byte, len(v.symbols))
				for i := range symbols {
					symbols[i], err = v.Symbol(uint8(i))
					if err != nil {
						t.Fatal(err)
					}
				}
				backing, returns, returnDepth := v.stack, v.returns, v.returnDepth
				if err = v.Run(0); err != ErrBudget || v.PC() != pc {
					t.Fatal("zero budget advanced")
				}
				saveTopCheck(t, v, 0, false)
				if err = v.Run(1); err != ErrBudget {
					t.Fatal(err)
				}
				saveTopCheck(t, v, int32(depth)-1, true)
				if v.PC() != pc+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), before) || v.stack != backing || v.returns != returns || v.returnDepth != returnDepth || !bytes.Equal(v.Program(), program) {
					t.Fatal("capture mutated unrelated state")
				}
				for i := range symbols {
					got, e := v.Symbol(uint8(i))
					if e != nil || !bytes.Equal(got, symbols[i]) {
						t.Fatal("capture changed symbol memory")
					}
				}
				if mode >= 3 {
					got, e := v.ReadWord(0)
					if e != nil || got != 0x8321 {
						t.Fatal("capture changed RAM")
					}
				}
				if err = v.Run(1); err != nil || !v.Halted() {
					t.Fatal("halt did not follow single-byte capture", err)
				}
				saveTopCheck(t, v, int32(depth)-1, true)
			})
		}
	}
}

func TestSaveTopOverwriteAndIsolation(t *testing.T) {
	// Reach every depth ascending and descending through public bytecode only.
	code := []byte{0x0b}
	for i := 0; i < 65; i++ {
		code = append(code, 6, 0xab, byte(i), 0x0b)
	}
	for i := 0; i < 65; i++ {
		code = append(code, 0x96, 0x0b)
	}
	code = append(code, 0xff)
	a, b := New(code), New(code)
	saveTopCheck(t, a, 0, false)
	saveTopCheck(t, b, 0, false)
	if err := a.Step(); err != nil {
		t.Fatal(err)
	}
	saveTopCheck(t, a, -1, true)
	saveTopCheck(t, b, 0, false)
	for depth := 1; depth <= 65; depth++ {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		saveTopCheck(t, a, int32(depth)-2, true) // Later depth changes are not retroactive.
		pc := a.PC()
		if err := a.Run(0); err != ErrBudget || a.PC() != pc {
			t.Fatal("zero budget changed pending overwrite")
		}
		saveTopCheck(t, a, int32(depth)-2, true)
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		saveTopCheck(t, a, int32(depth)-1, true)
		saveTopCheck(t, b, 0, false)
	}
	if err := b.Step(); err != nil {
		t.Fatal(err)
	}
	saveTopCheck(t, b, -1, true)
	saveTopCheck(t, a, 64, true)
	for depth := 64; depth >= 0; depth-- {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		saveTopCheck(t, a, int32(depth), true)
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		saveTopCheck(t, a, int32(depth)-1, true)
		saveTopCheck(t, b, -1, true)
	}
	if err := b.Step(); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(); err != nil {
		t.Fatal(err)
	}
	saveTopCheck(t, b, 0, true)
	saveTopCheck(t, a, -1, true)
}

func TestSaveTopNestedReturns(t *testing.T) {
	// Saved PCs9 and5 survive the metadata write at10, then both real returns.
	code := []byte{0xf0, 0xf1, 0x44, 0, 6, 0xff, 0x44, 0, 10, 0x45, 0x0b, 0x45}
	v, err := NewAt(code, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Run(2); err != ErrBudget || v.PC() != 10 {
		t.Fatal(err)
	}
	returns, depth := v.returns, v.returnDepth
	if depth != 2 {
		t.Fatal("fixture did not create nested returns")
	}
	if err = v.Step(); err != nil || v.PC() != 11 {
		t.Fatal(err)
	}
	saveTopCheck(t, v, -1, true)
	if v.returns != returns || v.returnDepth != depth {
		t.Fatal("capture changed saved returns")
	}
	if err = v.Run(3); err != nil || !v.Halted() || v.PC() != 6 || len(v.Stack()) != 0 || !bytes.Equal(v.Program(), code) {
		t.Fatal("nested return path", err)
	}
	saveTopCheck(t, v, -1, true)
}

func TestSaveTopInertDispatch(t *testing.T) {
	for _, prefix := range [][]byte{nil, {0x0b}} {
		for _, stop := range []byte{0xff, 0xf0, 0x0d} {
			code := append(append([]byte(nil), prefix...), stop, 0x0b)
			v := New(code)
			if len(prefix) > 0 {
				if err := v.Step(); err != nil {
					t.Fatal(err)
				}
			}
			valid := len(prefix) > 0
			saveTopCheck(t, v, -1, valid)
			err := v.Step()
			if stop == 0xff {
				if err != nil || !v.Halted() {
					t.Fatal("halt", err)
				}
			} else if err == nil {
				t.Fatal("fixture did not fault")
			}
			pc, stack := v.PC(), v.Stack()
			for _, budget := range []uint64{0, 1, 100} {
				if got := v.Run(budget); got != err {
					t.Fatal("sticky run", got)
				}
				if got := v.Step(); got != err {
					t.Fatal("sticky step", got)
				}
				if v.PC() != pc || !reflect.DeepEqual(v.Stack(), stack) || !bytes.Equal(v.Program(), code) {
					t.Fatal("inert dispatch changed state")
				}
				saveTopCheck(t, v, -1, valid)
			}
		}
	}
	var zero VM
	saveTopCheck(t, &zero, 0, false)
	if err := zero.Run(0); err != ErrBudget {
		t.Fatal(err)
	}
	if err := zero.Step(); !errors.Is(err, ErrTruncated) {
		t.Fatal(err)
	}
	saveTopCheck(t, &zero, 0, false)
}
