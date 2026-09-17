package gvm_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func icPush(code []byte, value uint16) []byte { return append(code, 6, byte(value>>8), byte(value)) }

func TestIncrementCompareIncrementFullDomain(t *testing.T) {
	// Three VMs, not one VM allocation per raw value. A full cycle tests every
	// input including ffff->0000 at each supported depth without guessed setters.
	for _, depth := range []int{1, 64, 65} {
		t.Run(fmt.Sprintf("depth%d", depth), func(t *testing.T) {
			code := make([]byte, 0, 3*depth+65536)
			prefix := make([]uint16, depth-1)
			for i := range prefix {
				prefix[i] = uint16(0x8100 + i)
				code = icPush(code, prefix[i])
			}
			code = icPush(code, 0)
			code = append(code, bytes.Repeat([]byte{0x0d}, 65536)...)
			v := gvm.New(code)
			if err := v.Run(uint64(depth)); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			start := v.PC()
			if err := v.Run(0); err != gvm.ErrBudget || v.PC() != start {
				t.Fatal("zero budget advanced")
			}
			for raw := uint32(0); raw < 65536; raw++ {
				if err := v.Step(); err != nil {
					t.Fatalf("raw%x: %v", raw, err)
				}
				stack := v.Stack()
				if len(stack) != depth || stack[depth-1] != uint16(raw+1) || v.PC() != start+int(raw)+1 || v.Halted() {
					t.Fatalf("raw%x pc%d stack%x", raw, v.PC(), stack)
				}
				if !reflect.DeepEqual(stack[:depth-1], prefix) {
					t.Fatalf("raw%x changed lower stack", raw)
				}
			}
			// Final increment succeeds as the final byte, and only the NEXT fetch fails.
			before := v.Stack()
			err := v.Step()
			var fault *gvm.ExecutionError
			if !errors.Is(err, gvm.ErrTruncated) || !errors.As(err, &fault) || fault.Offset != len(code) || v.PC() != len(code) || !reflect.DeepEqual(v.Stack(), before) || v.Step() != err {
				t.Fatalf("end fetch: %v", err)
			}
			if !bytes.Equal(v.Program(), code) {
				t.Fatal("increment changed code")
			}
		})
	}
}

func TestIncrementCompareSignedThresholdSweeps(t *testing.T) {
	// Each half-million-byte program reuses one VM for all raw words. Pop through
	// the existing verified ret-only wrapper, never inject or reset private state.
	boundaries := []uint16{0, 1, 2, 0x7ffe, 0x7fff, 0x8000, 0x8001, 0xfffe, 0xffff}
	for _, boundary := range boundaries {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("boundary%x/reverse%v", boundary, reverse), func(t *testing.T) {
				code := make([]byte, 0, 8*65536+1)
				for raw := uint32(0); raw < 65536; raw++ {
					a, b := uint16(raw), boundary
					if reverse {
						a, b = b, a
					}
					code = icPush(code, a)
					code = icPush(code, b)
					code = append(code, 0x1d, 0x96)
				}
				code = append(code, 0xff)
				v := gvm.New(code)
				for raw := uint32(0); raw < 65536; raw++ {
					if err := v.Run(2); err != gvm.ErrBudget {
						t.Fatal(err)
					}
					if err := v.Step(); err != nil {
						t.Fatalf("raw%x: %v", raw, err)
					}
					a, b := uint16(raw), boundary
					if reverse {
						a, b = b, a
					}
					// Flipping sign bits maps signed ordering into unsigned ordering.
					want := uint16(0)
					if a^0x8000 > b^0x8000 {
						want = 1
					}
					stack := v.Stack()
					if len(stack) != 1 || stack[0] != want || v.PC() != int(raw)*8+7 {
						t.Fatalf("a%x b%x got%x want%x", a, b, stack, want)
					}
					if err := v.Step(); err != nil {
						t.Fatal(err)
					}
				}
				if err := v.Step(); err != nil || !v.Halted() {
					t.Fatalf("halt: %v", err)
				}
			})
		}
	}
}

func TestIncrementCompareAllEqualities(t *testing.T) {
	code := make([]byte, 0, 8*65536+1)
	for raw := uint32(0); raw < 65536; raw++ {
		code = icPush(code, uint16(raw))
		code = icPush(code, uint16(raw))
		code = append(code, 0x1d, 0x96)
	}
	code = append(code, 0xff)
	v := gvm.New(code)
	for raw := uint32(0); raw < 65536; raw++ {
		if err := v.Run(3); err != gvm.ErrBudget {
			t.Fatalf("raw%x:%v", raw, err)
		}
		stack := v.Stack()
		if len(stack) != 1 || stack[0] != 0 {
			t.Fatalf("equal raw%x yielded%x", raw, stack)
		}
		if err := v.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Step(); err != nil || !v.Halted() {
		t.Fatal(err)
	}
}

func TestIncrementCompareFullStackAndTerminalByte(t *testing.T) {
	for _, depth := range []int{2, 64, 65} {
		for _, pair := range [][2]uint16{{0xffff, 0}, {0, 0xffff}, {0x7fff, 0x8000}, {0x8000, 0x7fff}, {0xffff, 0xffff}, {0, 0}} {
			var code []byte
			prefix := make([]uint16, depth-2)
			for i := range prefix {
				prefix[i] = uint16(0x9000 + i)
				code = icPush(code, prefix[i])
			}
			code = icPush(code, pair[0])
			code = icPush(code, pair[1])
			code = append(code, 0x1d)
			v := gvm.New(code)
			if err := v.Run(uint64(depth)); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			want := uint16(0)
			if pair[0]^0x8000 > pair[1]^0x8000 {
				want = 1
			}
			if err := v.Run(0); err != gvm.ErrBudget || v.PC() != len(code)-1 {
				t.Fatal("budget advanced")
			}
			if err := v.Step(); err != nil || !reflect.DeepEqual(v.Stack(), append(prefix, want)) || v.PC() != len(code) || v.Halted() {
				t.Fatalf("depth%d pair%x:%v stack%x", depth, pair, err, v.Stack())
			}
			before := v.Stack()
			err := v.Step()
			if !errors.Is(err, gvm.ErrTruncated) || !reflect.DeepEqual(v.Stack(), before) || v.Run(0) != err {
				t.Fatalf("end fetch: %v", err)
			}
		}
	}
}

func icConstructors(t *testing.T, code []byte, entry uint32) []*gvm.VM {
	t.Helper()
	offset := uint32(0)
	at, err := gvm.NewAt(code, entry)
	if err != nil {
		t.Fatal(err)
	}
	symbols, err := gvm.NewWithSymbols(code, entry, []gvm.SymbolRegion{{ProgramOffset: &offset, Length: uint32(len(code))}, {Length: 3, Initial: []byte{1, 2, 3}}})
	if err != nil {
		t.Fatal(err)
	}
	space := gvm.AddressSpace{FileLength: uint32(len(code)), RAM: []byte{1, 2, 3}, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: uint32(len(code))}, {Region: gvm.AddressRAM, Length: 3}}}
	address, err := gvm.NewWithAddressSpace(code, entry, space)
	if err != nil {
		t.Fatal(err)
	}
	services, err := gvm.NewWithAddressSpaceAndServices(code, entry, space, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := []*gvm.VM{at, symbols, address, services}
	if entry == 0 {
		result = append(result, gvm.New(code))
	}
	return result
}

func TestIncrementCompareStickyUnderflow(t *testing.T) {
	for _, tc := range []struct {
		op    byte
		depth int
	}{{0x0d, 0}, {0x1d, 0}, {0x1d, 1}} {
		code := []byte{0, 0}
		if tc.depth == 1 {
			code = icPush(code, 0x8000)
		}
		offset := len(code)
		code = append(code, tc.op)
		for _, v := range icConstructors(t, code, 0) {
			if err := v.Run(uint64(2 + tc.depth)); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			before := v.Stack()
			program := v.Program()
			err := v.Step()
			var fault *gvm.ExecutionError
			if !errors.Is(err, gvm.ErrStackUnderflow) || !errors.As(err, &fault) || fault.Offset != offset || v.PC() != offset+1 || v.Halted() {
				t.Fatalf("op%x depth%d: %v", tc.op, tc.depth, err)
			}
			if v.Step() != err || v.Run(0) != err || v.Run(20) != err || !reflect.DeepEqual(v.Stack(), before) || !bytes.Equal(v.Program(), program) {
				t.Fatal("sticky fault changed state")
			}
			if ram, e := v.Symbol(1); e == nil && !bytes.Equal(ram, []byte{1, 2, 3}) {
				t.Fatal("fault changed RAM")
			}
		}
	}
}

func TestIncrementCompareNonzeroEntryMemoryAndNestedReturns(t *testing.T) {
	for _, op := range []byte{0x0d, 0x1d} {
		// Entry2 calls6, which calls10. Actual returns must restore9 then5 exactly.
		code := []byte{0xf0, 0xf1, 0x44, 0, 6, 0xff, 0x44, 0, 10, 0x45}
		code = icPush(code, 0xabcd)
		code = icPush(code, 0xffff)
		want := []uint16{0xabcd, 0}
		pushes := 2
		if op == 0x1d {
			code = icPush(code, 0x8000)
			want = []uint16{0xabcd, 1}
			pushes++
		}
		code = append(code, op, 0x45)
		original := append([]byte(nil), code...)
		vms := icConstructors(t, code, 2)
		code[0] = 0 // Caller mutation must not affect any VM-owned program snapshot.
		for _, v := range vms {
			if err := v.Run(uint64(2 + pushes)); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			pc := v.PC()
			if err := v.Run(0); err != gvm.ErrBudget || v.PC() != pc {
				t.Fatal("budget advanced")
			}
			if err := v.Run(1); err != gvm.ErrBudget || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), want) {
				t.Fatalf("op%x: %v stack%x", op, err, v.Stack())
			}
			if err := v.Run(3); err != nil || !v.Halted() || v.PC() != 6 || !reflect.DeepEqual(v.Stack(), want) {
				t.Fatalf("nested returns: %v pc%d", err, v.PC())
			}
			if !bytes.Equal(v.Program(), original) {
				t.Fatal("operation changed code")
			}
			if file, e := v.Symbol(0); e == nil && !bytes.Equal(file, original) {
				t.Fatal("operation changed file view")
			}
			if ram, e := v.Symbol(1); e == nil && !bytes.Equal(ram, []byte{1, 2, 3}) {
				t.Fatal("operation changed RAM")
			}
			snapshot := v.Stack()
			snapshot[0] = 0
			if !reflect.DeepEqual(v.Stack(), want) {
				t.Fatal("stack snapshot aliases VM")
			}
		}
	}
}
