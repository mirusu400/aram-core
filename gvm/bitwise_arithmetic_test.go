package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestBitwiseArithmeticOpcodes(t *testing.T) {
	tests := []struct {
		op    byte
		a, b  uint16
		want  uint16
		unary bool
	}{
		{0x16, 0xfff9, 3, 0xffff, false},
		{0x17, 0xa55a, 0x0ff0, 0x0550, false},
		{0x18, 0xa500, 0x00f5, 0xa5f5, false},
		{0x19, 0, 0, 1, true},
		{0x19, 0x55aa, 0, 0, true},
		{0x1a, 0xa55a, 0x0ff0, 0xaaaa, false},
		{0x1b, 0x8000, 1, 0xc000, false},
		{0x1c, 0x4001, 1, 0x8002, false},
	}
	for _, test := range tests {
		program := []byte{0x06, byte(test.a >> 8), byte(test.a)}
		steps := uint64(2)
		if !test.unary {
			program = append(program, 0x06, byte(test.b>>8), byte(test.b))
			steps++
		}
		program = append(program, test.op, 0xff)
		vm := gvm.New(program)
		if err := vm.Run(steps); !errors.Is(err, gvm.ErrBudget) {
			t.Fatalf("opcode 0x%02x: %v", test.op, err)
		}
		if got := vm.Stack(); !reflect.DeepEqual(got, []uint16{test.want}) {
			t.Fatalf("opcode 0x%02x stack=%x want=%x", test.op, got, test.want)
		}
		if err := vm.Run(1); err != nil || !vm.Halted() {
			t.Fatalf("opcode 0x%02x halt: %v", test.op, err)
		}
	}
}

func TestBitwiseArithmeticFaultsAreAtomic(t *testing.T) {
	for _, op := range []byte{0x16, 0x17, 0x18, 0x1a, 0x1b, 0x1c} {
		vm := gvm.New([]byte{0x05, 1, op})
		if err := vm.Step(); err != nil {
			t.Fatal(err)
		}
		before := vm.Stack()
		err := vm.Step()
		if !errors.Is(err, gvm.ErrStackUnderflow) || !reflect.DeepEqual(vm.Stack(), before) {
			t.Fatalf("opcode 0x%02x underflow=%v stack=%x", op, err, vm.Stack())
		}
	}
	vm := gvm.New([]byte{0x05, 7, 0x05, 0, 0x16})
	if err := vm.Run(2); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	before := vm.Stack()
	err := vm.Step()
	if !errors.Is(err, gvm.ErrDivideByZero) || !reflect.DeepEqual(vm.Stack(), before) {
		t.Fatalf("modulo zero=%v stack=%x", err, vm.Stack())
	}
	vm = gvm.New([]byte{0x19})
	err = vm.Step()
	if !errors.Is(err, gvm.ErrStackUnderflow) || len(vm.Stack()) != 0 {
		t.Fatalf("complement underflow=%v stack=%x", err, vm.Stack())
	}
}
