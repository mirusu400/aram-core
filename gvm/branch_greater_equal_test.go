package gvm_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

// All operands and saved return addresses are established by synthetic bytecode,
// never by mutating private VM state. Malformed cases specify host safety policy.
func greaterEqualPushes(depth int, top int16) []byte {
	var code []byte
	for i := 0; i < depth; i++ {
		value := uint16(0xa000 + i)
		if i == depth-1 {
			value = uint16(top)
		}
		code = append(code, 6, byte(value>>8), byte(value))
	}
	return code
}

func greaterEqualPrime(t *testing.T, v *gvm.VM, depth int) {
	t.Helper()
	if err := v.Run(uint64(depth)); err != gvm.ErrBudget {
		t.Fatalf("prime: %v", err)
	}
}

func greaterEqualFault(t *testing.T, v *gvm.VM, cause error, offset int) {
	t.Helper()
	stack, program := v.Stack(), v.Program()
	err := v.Step()
	var fault *gvm.ExecutionError
	if !errors.Is(err, cause) || !errors.As(err, &fault) || fault.Offset != offset {
		t.Fatalf("fault=%v want=%v at %d", err, cause, offset)
	}
	pc := offset + 1
	if cause == gvm.ErrTruncated && offset == len(program) {
		pc = offset
	}
	for _, again := range []func() error{v.Step, func() error { return v.Run(0) }, func() error { return v.Run(10) }} {
		if v.PC() != pc || v.Halted() || !reflect.DeepEqual(stack, v.Stack()) || !bytes.Equal(program, v.Program()) {
			t.Fatal("fault committed state")
		}
		if again() != err {
			t.Fatal("fault not sticky")
		}
	}
	if v.PC() != pc || v.Halted() || !reflect.DeepEqual(stack, v.Stack()) || !bytes.Equal(program, v.Program()) {
		t.Fatal("sticky fault mutated state")
	}
}

func TestBranchGreaterEqualSignedMatrix(t *testing.T) {
	for _, top := range []int16{-32768, -129, -128, -1, 0, 1, 126, 127, 128, 32767} {
		for _, immediate := range []int8{-128, -1, 0, 1, 126, 127} {
			for _, depth := range []int{1, 65} {
				t.Run(fmt.Sprintf("top%d/imm%d/depth%d", top, immediate, depth), func(t *testing.T) {
					code := greaterEqualPushes(depth, top)
					offset := len(code)
					target := offset + 5
					code = append(code, 0x3d, byte(immediate), byte(target>>8), byte(target), 0xff, 0xff)
					v := gvm.New(code)
					greaterEqualPrime(t, v, depth)
					prefix := v.Stack()[:depth-1]
					want := offset + 4
					if int(top) >= int(immediate) {
						want = target
					}
					if err := v.Step(); err != nil {
						t.Fatal(err)
					}
					if v.PC() != want || v.Halted() || !reflect.DeepEqual(v.Stack(), append([]uint16(nil), prefix...)) || !bytes.Equal(v.Program(), code) {
						t.Fatalf("pc=%d want=%d stack=%v", v.PC(), want, v.Stack())
					}
					if err := v.Run(1); err != nil || !v.Halted() || v.PC() != want+1 {
						t.Fatalf("halt: %v pc=%d", err, v.PC())
					}
				})
			}
		}
	}
}

func TestBranchGreaterEqualAbsoluteTargets(t *testing.T) {
	for _, target := range []int{0, 0x1234, 0x8001, 0xffff} {
		t.Run(fmt.Sprintf("%04x", target), func(t *testing.T) {
			code := make([]byte, 65536)
			copy(code[9:], []byte{5, 0xff, 0x3d, 0xff, byte(target >> 8), byte(target)})
			code[target] = 0xff
			v, err := gvm.NewAt(code, 9)
			if err != nil {
				t.Fatal(err)
			}
			greaterEqualPrime(t, v, 1)
			if err := v.Step(); err != nil || v.PC() != target || len(v.Stack()) != 0 || v.Halted() {
				t.Fatalf("target=%x pc=%x err=%v", target, v.PC(), err)
			}
			if err := v.Run(1); err != nil || !v.Halted() || v.PC() != target+1 {
				t.Fatalf("target fetch: %v", err)
			}
		})
	}
}

func TestBranchGreaterEqualHostFaultOrder(t *testing.T) {
	for _, depth := range []int{0, 1, 65} {
		for _, top := range []int16{-1, 0, 1} {
			for n := 0; n < 3; n++ {
				t.Run(fmt.Sprintf("trunc%d/depth%d/top%d", n, depth, top), func(t *testing.T) {
					code := greaterEqualPushes(depth, top)
					offset := len(code)
					code = append(code, 0x3d)
					code = append(code, []byte{0, 0xff, 0xff}[:n]...)
					v := gvm.New(code)
					greaterEqualPrime(t, v, depth)
					greaterEqualFault(t, v, gvm.ErrTruncated, offset)
				})
			}
		}
	}
	for _, target := range []int{4, 5, 65535} {
		v := gvm.New([]byte{0x3d, 0, byte(target >> 8), byte(target)})
		greaterEqualFault(t, v, gvm.ErrStackUnderflow, 0)
	}
	for _, depth := range []int{1, 65} {
		for _, top := range []int16{0, 1} {
			for _, extra := range []int{0, 1, 65535} {
				t.Run(fmt.Sprintf("invalid/depth%d/top%d/extra%d", depth, top, extra), func(t *testing.T) {
					code := greaterEqualPushes(depth, top)
					offset := len(code)
					target := offset + 4 + extra
					if extra == 65535 {
						target = 65535
					}
					code = append(code, 0x3d, 0, byte(target>>8), byte(target))
					v := gvm.New(code)
					greaterEqualPrime(t, v, depth)
					greaterEqualFault(t, v, gvm.ErrInvalidTarget, offset)
				})
			}
		}
	}
}

func TestBranchGreaterEqualUnusedTargetsEndFetch(t *testing.T) {
	for _, depth := range []int{1, 65} {
		for _, extra := range []int{0, 1, 65535} {
			code := greaterEqualPushes(depth, -1)
			offset := len(code)
			target := offset + 4 + extra
			if extra == 65535 {
				target = 65535
			}
			code = append(code, 0x3d, 0, byte(target>>8), byte(target))
			v := gvm.New(code)
			greaterEqualPrime(t, v, depth)
			prefix := append([]uint16(nil), v.Stack()[:depth-1]...)
			if err := v.Run(1); err != gvm.ErrBudget || v.PC() != len(code) || v.Halted() || !reflect.DeepEqual(prefix, v.Stack()) {
				t.Fatalf("fallthrough: %v pc=%d", err, v.PC())
			}
			greaterEqualFault(t, v, gvm.ErrTruncated, len(code))
		}
	}
}

func TestBranchGreaterEqualBudget(t *testing.T) {
	for _, top := range []byte{0, 0xff} {
		v := gvm.New([]byte{5, top, 0x3d, 0, 0, 7, 0xff, 0xff})
		greaterEqualPrime(t, v, 1)
		stack := v.Stack()
		if err := v.Run(0); err != gvm.ErrBudget || v.PC() != 2 || !reflect.DeepEqual(stack, v.Stack()) {
			t.Fatalf("zero budget: %v", err)
		}
		want := 6
		if top == 0 {
			want = 7
		}
		if err := v.Run(1); err != gvm.ErrBudget || v.PC() != want || len(v.Stack()) != 0 || v.Halted() {
			t.Fatalf("one budget: %v pc=%d", err, v.PC())
		}
		if err := v.Run(1); err != nil || !v.Halted() || v.PC() != want+1 {
			t.Fatalf("resume: %v", err)
		}
		if err := v.Run(0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBranchGreaterEqualMemoryAndCallReturnPreserved(t *testing.T) {
	for _, top := range []byte{0, 0xff} {
		// Two nested calls establish distinct saved PCs. Both paths return twice.
		code := []byte{0x44, 0, 6, 5, 42, 0xff, 0x44, 0, 10, 0x45, 5, top, 0x3d, 0, 0, 17, 0x45, 0x45}
		v, err := gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{
			FileLength: uint32(len(code)), RAM: []byte{0x12, 0x34, 0x56, 0x78},
			Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: uint32(len(code))}, {Region: gvm.AddressRAM, Length: 4}},
		})
		if err != nil {
			t.Fatal(err)
		}
		greaterEqualPrime(t, v, 3)
		ram, _ := v.Symbol(1)
		checkMemory := func() {
			t.Helper()
			file, e := v.Symbol(0)
			current, e2 := v.Symbol(1)
			word, e3 := v.ReadWord(0)
			if e != nil || e2 != nil || e3 != nil || word != 0x3412 || !bytes.Equal(file, code) || !bytes.Equal(v.Program(), code) || !bytes.Equal(current, ram) {
				t.Fatal("branch changed memory")
			}
		}
		want := 16
		if top == 0 {
			want = 17
		}
		if err := v.Step(); err != nil || v.PC() != want || len(v.Stack()) != 0 {
			t.Fatalf("branch: %v pc=%d", err, v.PC())
		}
		checkMemory()
		if err := v.Step(); err != nil || v.PC() != 9 {
			t.Fatalf("inner return: %v pc=%d", err, v.PC())
		}
		if err := v.Step(); err != nil || v.PC() != 3 {
			t.Fatalf("outer return: %v pc=%d", err, v.PC())
		}
		if err := v.Run(2); err != nil || !v.Halted() || !reflect.DeepEqual(v.Stack(), []uint16{42}) {
			t.Fatalf("continuation: %v stack=%v", err, v.Stack())
		}
		checkMemory()
	}
}
