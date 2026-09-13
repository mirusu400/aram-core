package gvm_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestAllUnsupportedOpcodes(t *testing.T) {
	supported := map[byte]bool{0: true, 4: true, 5: true, 6: true, 0x0a: true, 0x12: true, 0x13: true, 0x14: true, 0x15: true, 0x1f: true, 0x31: true, 0x36: true, 0x3c: true, 0x3e: true, 0x41: true, 0x42: true, 0x43: true, 0x44: true, 0x45: true, 0x96: true, 0x97: true, 0xff: true}
	for i := 0; i < 256; i++ {
		op := byte(i)
		if supported[op] {
			continue
		}
		t.Run(fmt.Sprintf("%02x", op), func(t *testing.T) {
			v := gvm.New([]byte{op})
			err := v.Step()
			var e *gvm.UnsupportedOpcodeError
			if !errors.As(err, &e) || e.Offset != 0 || e.Opcode != op || v.Halted() || v.PC() != 1 {
				t.Fatalf("unsupported: %v", err)
			}
			if err.Error() == "" || v.Run(0) != err {
				t.Fatal("fault must remain stable")
			}
		})
	}
}

func TestFaultOffsetAndStackAtomicity(t *testing.T) {
	v := gvm.New([]byte{5, 9, 0x13})
	err := v.Run(10)
	var e *gvm.ExecutionError
	if !errors.As(err, &e) || e.Offset != 2 || !errors.Is(err, gvm.ErrStackUnderflow) || err.Error() == "" {
		t.Fatalf("fault: %v", err)
	}
	if !reflect.DeepEqual(v.Stack(), []uint16{9}) || v.Halted() || v.Run(0) != err {
		t.Fatal("fault mutated operands or resumed")
	}
	// Overflow precedes the immediate read, even when the immediate is absent.
	var code []byte
	for i := 0; i < 65; i++ {
		code = append(code, 5, 1)
	}
	code = append(code, 6)
	v = gvm.New(code)
	if err := v.Run(66); !errors.Is(err, gvm.ErrStackOverflow) {
		t.Fatalf("guard order: %v", err)
	}
	var zero gvm.VM
	if err := zero.Step(); !errors.Is(err, gvm.ErrTruncated) {
		t.Fatalf("zero VM: %v", err)
	}
}

func FuzzBoundedDeterminism(f *testing.F) {
	for _, code := range [][]byte{nil, {0xff}, {5, 0x80, 6, 0xff, 0xff, 0x12, 0xff}, {0x41, 0, 0}, {0x44, 0, 0}, {5, 1, 0x42, 0, 0}, {0x45}} {
		f.Add(code, uint8(32))
	}
	f.Fuzz(func(t *testing.T, code []byte, limit uint8) {
		if len(code) > 4096 {
			t.Skip()
		}
		a, b := gvm.New(code), gvm.New(code)
		ea := a.Run(uint64(limit))
		eb := b.Run(uint64(limit))
		if fmt.Sprint(ea) != fmt.Sprint(eb) || a.PC() != b.PC() || a.Halted() != b.Halted() || !reflect.DeepEqual(a.Stack(), b.Stack()) {
			t.Fatal("nondeterministic")
		}
		if a.PC() < 0 || a.PC() > len(code) || len(a.Stack()) > 65 {
			t.Fatal("unbounded state")
		}
		if ea != nil && !errors.Is(ea, gvm.ErrBudget) {
			pc, s := a.PC(), a.Stack()
			if a.Step() != ea || a.PC() != pc || !reflect.DeepEqual(a.Stack(), s) {
				t.Fatal("fault resumed")
			}
		}
	})
}
