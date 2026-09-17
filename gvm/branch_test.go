package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestBranches(t *testing.T) {
	for _, tt := range []struct {
		name      string
		op, value byte
		taken     bool
	}{
		{"nonzero taken", 0x42, 0xff, true}, {"nonzero not taken", 0x42, 0, false},
		{"zero taken", 0x43, 0, true}, {"zero not taken", 0x43, 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := gvm.New([]byte{5, tt.value, tt.op, 0, 7, 5, 9, 0xff})
			if err := v.Run(2); !errors.Is(err, gvm.ErrBudget) {
				t.Fatal(err)
			}
			want := 5
			if tt.taken {
				want = 7
			}
			if v.PC() != want || len(v.Stack()) != 0 {
				t.Fatalf("pc %d stack %x", v.PC(), v.Stack())
			}
			if err := v.Run(2); err != nil {
				t.Fatal(err)
			}
		})
	}
	v := gvm.New([]byte{0x41, 0, 4, 0xee, 0, 0xff})
	if err := v.Run(3); err != nil || v.PC() != 6 {
		t.Fatalf("jump/nop %v pc %d", err, v.PC())
	}
	// High operand byte proves BE16, while a budget bounds an infinite loop.
	code := make([]byte, 258)
	code[0] = 0x41
	code[1] = 1
	code[256] = 0xff
	v = gvm.New(code)
	if err := v.Run(2); err != nil || v.PC() != 257 {
		t.Fatalf("big endian target %v pc %d", err, v.PC())
	}
	v = gvm.New([]byte{0x41, 0, 0})
	if err := v.Run(100); !errors.Is(err, gvm.ErrBudget) || v.PC() != 0 {
		t.Fatalf("loop %v", err)
	}
}

func TestBranchSafety(t *testing.T) {
	for _, op := range []byte{0x41, 0x42, 0x43, 0x44} {
		for _, operands := range [][]byte{nil, {0}} {
			v := gvm.New(append([]byte{5, 1, op}, operands...))
			if err := v.Run(2); !errors.Is(err, gvm.ErrTruncated) || v.PC() != 3 || !reflect.DeepEqual(v.Stack(), []uint16{1}) {
				t.Fatalf("trunc %x: %v", op, err)
			}
		}
	}
	for _, op := range []byte{0x42, 0x43} {
		v := gvm.New([]byte{op, 0, 0})
		if err := v.Step(); !errors.Is(err, gvm.ErrStackUnderflow) {
			t.Fatalf("underflow %x: %v", op, err)
		}
	}
	for _, op := range []byte{0x41, 0x42, 0x43, 0x44} {
		value := byte(1)
		if op == 0x43 {
			value = 0
		}
		v := gvm.New([]byte{5, value, op, 0, 5})
		if err := v.Run(2); !errors.Is(err, gvm.ErrInvalidTarget) || v.PC() != 3 || len(v.Stack()) != 1 {
			t.Fatalf("target %x: %v", op, err)
		}
	}
	// An untaken target is not dereferenced or range-validated.
	v := gvm.New([]byte{5, 0, 0x42, 0xff, 0xff, 0xff})
	if err := v.Run(3); err != nil {
		t.Fatal(err)
	}
}

func TestCallCapacity(t *testing.T) {
	v := gvm.New([]byte{0x44, 0, 0})
	if err := v.Run(17); !errors.Is(err, gvm.ErrBudget) || v.PC() != 0 {
		t.Fatalf("17 calls: %v", err)
	}
	if err := v.Step(); !errors.Is(err, gvm.ErrReturnStackOverflow) || v.PC() != 1 {
		t.Fatalf("18th call: %v pc %d", err, v.PC())
	}
}
