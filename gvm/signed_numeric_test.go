package gvm_test

import (
	"errors"
	"github.com/mirusu400/aram-core/gvm"
	"reflect"
	"testing"
)

func TestSignedDivideAndCompare(t *testing.T) {
	for _, op := range []byte{0x15, 0x1f} {
		for _, a := range []int16{-32768, -32767, -7, -1, 0, 1, 3, 7, 32767} {
			for _, b := range []int16{-32768, -7, -3, -1, 0, 1, 3, 7, 32767} {
				if op == 0x15 && b == 0 {
					continue
				}
				av, bv := uint16(a), uint16(b)
				v := gvm.New([]byte{0x06, byte(av >> 8), byte(av), 0x06, byte(bv >> 8), byte(bv), op, 0xff})
				if err := v.Run(3); !errors.Is(err, gvm.ErrBudget) {
					t.Fatal(err)
				}
				var want uint16
				if op == 0x15 {
					want = uint16(int32(a) / int32(b))
				} else if a >= b {
					want = 1
				}
				if !reflect.DeepEqual(v.Stack(), []uint16{want}) || v.PC() != 7 {
					t.Fatalf("op %x %d,%d: stack %x want %x PC %d", op, a, b, v.Stack(), want, v.PC())
				}
				if err := v.Run(1); err != nil || !v.Halted() {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestSignedNumericFaultPolicies(t *testing.T) {
	for _, op := range []byte{0x15, 0x1f} {
		for _, prefix := range [][]byte{nil, {0x05, 0}} {
			v := gvm.New(append(append([]byte(nil), prefix...), op, 0xff))
			if len(prefix) > 0 {
				if err := v.Step(); err != nil {
					t.Fatal(err)
				}
			}
			before := v.Stack()
			err := v.Step()
			if !errors.Is(err, gvm.ErrStackUnderflow) || v.PC() != len(prefix)+1 || !reflect.DeepEqual(before, v.Stack()) {
				t.Fatalf("underflow %x %v", op, err)
			}
			if v.Step() != err || v.Run(0) != err {
				t.Fatal("underflow not sticky")
			}
		}
	}
	for _, a := range []uint16{0, 1, 0x8000, 0xffff} {
		v := gvm.New([]byte{0x06, byte(a >> 8), byte(a), 0x05, 0, 0x15, 0xff})
		if err := v.Run(2); !errors.Is(err, gvm.ErrBudget) {
			t.Fatal(err)
		}
		before := v.Stack()
		err := v.Step()
		if !errors.Is(err, gvm.ErrDivideByZero) || !reflect.DeepEqual(before, v.Stack()) || v.PC() != 6 || v.Halted() {
			t.Fatalf("zero policy %v %x", err, v.Stack())
		}
		if v.Step() != err || v.Run(99) != err || !reflect.DeepEqual(before, v.Stack()) {
			t.Fatal("zero fault not sticky")
		}
	}
}

func TestSignedNumericPrefixEndAndReturn(t *testing.T) {
	for _, op := range []byte{0x15, 0x1f} {
		var p []byte
		var want []uint16
		for i := 0; i < 63; i++ {
			p = append(p, 0x05, byte(i))
			want = append(want, uint16(i))
		}
		p = append(p, 0x05, 7, 0x05, 3, op)
		result := uint16(1)
		if op == 0x15 {
			result = 2
		}
		want = append(want, result)
		v := gvm.New(p)
		if err := v.Run(66); !errors.Is(err, gvm.ErrBudget) || !reflect.DeepEqual(v.Stack(), want) || v.PC() != len(p) {
			t.Fatalf("full stack / end opcode %x: %v", op, err)
		}
		if err := v.Step(); !errors.Is(err, gvm.ErrTruncated) || !reflect.DeepEqual(v.Stack(), want) {
			t.Fatalf("following fetch %x: %v", op, err)
		}
		v = gvm.New([]byte{0x05, 7, 0x05, 3, 0x44, 0, 8, 0xff, op, 0x45})
		if err := v.Run(6); err != nil || v.PC() != 8 || !reflect.DeepEqual(v.Stack(), []uint16{result}) {
			t.Fatalf("numeric return preservation %x: %v", op, err)
		}
	}
}

func TestAddressSpaceGuardOrderAndReturn(t *testing.T) {
	p := make([]byte, 0, 131)
	for i := 0; i < 65; i++ {
		p = append(p, 0x4d, 0)
	}
	p = append(p, 0x4d)
	v := addressVM(t, p, gvm.AddressSpace{RAM: []byte{1}, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM}}})
	if err := v.Run(65); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if err := v.Step(); !errors.Is(err, gvm.ErrTruncated) {
		t.Fatalf("missing index precedence %v", err)
	}
	// A successful tagged-address instruction must leave the saved return PC intact.
	v = addressVM(t, []byte{0x44, 0, 4, 0xff, 0x4d, 0, 0x45}, gvm.AddressSpace{RAM: []byte{1, 2, 3, 4}, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 0}}})
	if err := v.Run(2); !errors.Is(err, gvm.ErrBudget) || v.PC() != 6 {
		t.Fatalf("operand advance %d %v", v.PC(), err)
	}
	if got, err := v.ReadWord(1); err != nil || got != 0x0403 {
		t.Fatalf("global read beyond descriptor %x %v", got, err)
	}
	if err := v.Run(2); err != nil || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), []uint16{0}) {
		t.Fatalf("return preservation %v", err)
	}
}
