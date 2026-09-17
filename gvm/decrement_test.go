package gvm_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestDecrementTopWord(t *testing.T) {
	for _, depth := range []int{1, 64, 65} {
		for _, value := range []uint16{0, 1, 0x7fff, 0x8000, 0xffff} {
			t.Run(fmt.Sprintf("depth%d/value%x", depth, value), func(t *testing.T) {
				var code []byte
				var before []uint16
				for i := 0; i < depth; i++ {
					x := uint16(i + 17)
					if i == depth-1 {
						x = value
					}
					code = append(code, 6, byte(x>>8), byte(x))
					before = append(before, x)
				}
				offset := len(code)
				code = append(code, 0x0e, 0xff)
				v := gvm.New(code)
				if err := v.Run(uint64(depth)); err != gvm.ErrBudget {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(v.Stack(), before) {
					t.Fatal("push setup")
				}
				if err := v.Run(0); err != gvm.ErrBudget || v.PC() != offset {
					t.Fatal("zero budget changed state")
				}
				want := append([]uint16(nil), before...)
				want[len(want)-1]--
				if err := v.Run(1); err != gvm.ErrBudget || v.PC() != offset+1 || !reflect.DeepEqual(v.Stack(), want) || v.Halted() {
					t.Fatalf("decrement: %v pc%d stack%x want%x", err, v.PC(), v.Stack(), want)
				}
				if !bytes.Equal(v.Program(), code) {
					t.Fatal("program mutated")
				}
				if err := v.Run(1); err != nil || !v.Halted() || v.PC() != offset+2 {
					t.Fatalf("halt: %v", err)
				}
			})
		}
	}
}

func TestDecrementTopFaultAndEndFetch(t *testing.T) {
	v, err := gvm.NewAt([]byte{0xff, 0x0e}, 1)
	if err != nil {
		t.Fatal(err)
	}
	first := v.Step()
	var fault *gvm.ExecutionError
	if !errors.Is(first, gvm.ErrStackUnderflow) || !errors.As(first, &fault) || fault.Offset != 1 || v.PC() != 2 || v.Halted() || len(v.Stack()) != 0 {
		t.Fatalf("underflow: %v pc%d", first, v.PC())
	}
	for _, again := range []error{v.Step(), v.Run(0), v.Run(99)} {
		if again != first || v.PC() != 2 || v.Halted() || len(v.Stack()) != 0 {
			t.Fatal("not sticky")
		}
	}
	if !bytes.Equal(v.Program(), []byte{0xff, 0x0e}) {
		t.Fatal("fault changed memory")
	}
	v = gvm.New([]byte{5, 0, 0x0e})
	if err := v.Run(2); err != gvm.ErrBudget || v.PC() != 3 || !reflect.DeepEqual(v.Stack(), []uint16{0xffff}) {
		t.Fatalf("last-byte opcode: %v", err)
	}
	if err := v.Step(); !errors.Is(err, gvm.ErrTruncated) || v.PC() != 3 {
		t.Fatalf("end fetch: %v", err)
	}
}

func TestDecrementTopPreservesSavedReturnAndRAM(t *testing.T) {
	code := []byte{5, 0, 0x44, 0, 6, 0xff, 0x0e, 0x45}
	v, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 4, Initial: []byte{1, 2, 3, 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(3); err != gvm.ErrBudget || v.PC() != 7 || !reflect.DeepEqual(v.Stack(), []uint16{0xffff}) {
		t.Fatalf("call/decrement: %v", err)
	}
	if err := v.Run(2); err != nil || !v.Halted() || v.PC() != 6 {
		t.Fatalf("return: %v", err)
	}
	ram, err := v.Symbol(0)
	if err != nil || !bytes.Equal(ram, []byte{1, 2, 3, 4}) || !bytes.Equal(v.Program(), code) {
		t.Fatal("memory changed")
	}
}
