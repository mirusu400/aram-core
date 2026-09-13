package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestPushAndExit(t *testing.T) {
	code := []byte{0x05, 0x80, 0x05, 0x7f, 0x06, 0xab, 0xcd, 0xff, 0xee}
	v := gvm.New(code)
	code[0] = 0xee
	if err := v.Run(4); err != nil {
		t.Fatal(err)
	}
	if !v.Halted() || v.PC() != 8 || !reflect.DeepEqual(v.Stack(), []uint16{0xff80, 0x7f, 0xabcd}) {
		t.Fatalf("state pc=%d stack=%x halt=%v", v.PC(), v.Stack(), v.Halted())
	}
	s := v.Stack()
	s[0] = 0
	if v.Stack()[0] != 0xff80 {
		t.Fatal("stack aliases VM")
	}
	if err := v.Step(); err != nil || v.PC() != 8 {
		t.Fatalf("halt step: %v", err)
	}
}

func TestArithmetic(t *testing.T) {
	for _, tt := range []struct {
		name       string
		op         byte
		a, b, want uint16
	}{
		{"add wrap", 0x12, 0xffff, 2, 1}, {"subtract wrap", 0x13, 1, 2, 0xffff}, {"multiply low16", 0x14, 0x8001, 3, 0x8003},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := gvm.New([]byte{6, byte(tt.a >> 8), byte(tt.a), 6, byte(tt.b >> 8), byte(tt.b), tt.op, 0xff})
			if err := v.Run(4); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(v.Stack(), []uint16{tt.want}) {
				t.Fatalf("stack=%x", v.Stack())
			}
		})
	}
}

func TestBudgetAndDeterminism(t *testing.T) {
	code := []byte{5, 1, 5, 2, 0x12, 0xff}
	a, b := gvm.New(code), gvm.New(code)
	if err := a.Run(0); !errors.Is(err, gvm.ErrBudget) || a.PC() != 0 {
		t.Fatalf("zero: %v pc %d", err, a.PC())
	}
	for i := 0; i < 3; i++ {
		if err := a.Run(1); !errors.Is(err, gvm.ErrBudget) {
			t.Fatalf("chunk: %v", err)
		}
	}
	if err := a.Run(1); err != nil {
		t.Fatal(err)
	}
	if err := b.Run(4); err != nil {
		t.Fatal(err)
	}
	if a.PC() != b.PC() || !reflect.DeepEqual(a.Stack(), b.Stack()) || a.Halted() != b.Halted() {
		t.Fatal("nondeterministic")
	}
	if err := a.Run(0); err != nil {
		t.Fatalf("halted zero: %v", err)
	}
}

func TestFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		code []byte
		want error
		pc   int
	}{
		{"empty", nil, gvm.ErrTruncated, 0}, {"missing i8", []byte{5}, gvm.ErrTruncated, 1},
		{"missing word", []byte{6}, gvm.ErrTruncated, 1}, {"short word", []byte{6, 1}, gvm.ErrTruncated, 1},
		{"add underflow", []byte{0x12}, gvm.ErrStackUnderflow, 1},
		{"sub underflow", []byte{5, 1, 0x13}, gvm.ErrStackUnderflow, 3},
		{"mul underflow", []byte{0x14}, gvm.ErrStackUnderflow, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := gvm.New(tt.code)
			err := v.Run(10)
			if !errors.Is(err, tt.want) || v.PC() != tt.pc {
				t.Fatalf("err=%v pc=%d", err, v.PC())
			}
			before := v.Stack()
			if again := v.Step(); again != err || v.PC() != tt.pc || !reflect.DeepEqual(before, v.Stack()) {
				t.Fatal("fault not stable")
			}
		})
	}
	for _, op := range []byte{1, 0xf3, 0xfe} {
		v := gvm.New([]byte{5, 7, op})
		err := v.Run(3)
		var unsupported *gvm.UnsupportedOpcodeError
		if !errors.As(err, &unsupported) || unsupported.Opcode != op || unsupported.Offset != 2 || v.PC() != 3 {
			t.Fatalf("unsupported %x: %v", op, err)
		}
	}
}

func TestStackCapacity(t *testing.T) {
	for _, op := range []byte{5, 6} {
		t.Run(string(rune(op)), func(t *testing.T) {
			var code []byte
			for i := 0; i < 66; i++ {
				code = append(code, op, 0)
				if op == 6 {
					code = append(code, byte(i))
				}
			}
			v := gvm.New(code)
			if err := v.Run(65); !errors.Is(err, gvm.ErrBudget) || len(v.Stack()) != 65 {
				t.Fatalf("capacity: %v len %d", err, len(v.Stack()))
			}
			pc := v.PC()
			before := v.Stack()
			if err := v.Step(); !errors.Is(err, gvm.ErrStackOverflow) || v.PC() != pc+1 || !reflect.DeepEqual(before, v.Stack()) {
				t.Fatalf("overflow: %v", err)
			}
		})
	}
}
