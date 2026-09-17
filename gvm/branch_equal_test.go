package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// Internal fixtures keep exhaustive arithmetic checks small and verify private
// saved state without adding an inspection API. Constructor/call tests below
// separately establish operands and returns through synthetic bytecode.
func equalVM(code []byte, depth int, top uint16) *VM {
	v := New(code)
	v.depth = depth
	for i := 0; i < depth; i++ {
		v.stack[i] = uint16(0xa000 + i)
	}
	if depth > 0 {
		v.stack[depth-1] = top
	}
	v.savedTop, v.savedTopValid = -17, true
	v.returnDepth = 2
	v.returns[0], v.returns[1] = 7, 11
	return v
}

func equalFault(t *testing.T, v *VM, cause error) {
	t.Helper()
	before := *v
	program := v.Program()
	err := v.Step()
	var fault *ExecutionError
	if !errors.Is(err, cause) || !errors.As(err, &fault) || fault.Offset != before.pc {
		t.Fatalf("fault=%v want=%v offset=%d", err, cause, before.pc)
	}
	before.fault = err
	if before.pc < len(before.code) {
		before.pc++
	}
	check := func() {
		t.Helper()
		if !reflect.DeepEqual(&before, v) || !bytes.Equal(program, v.Program()) {
			t.Fatal("fault changed state except fetch/fault")
		}
	}
	check()
	for _, again := range []func() error{v.Step, func() error { return v.Run(0) }, func() error { return v.Run(9) }} {
		if again() != err {
			t.Fatal("fault not sticky")
		}
		check()
	}
}

func TestBranchEqualImmediateMatrix(t *testing.T) {
	for imm := 0; imm < 256; imm++ {
		// Independent arithmetic oracle, rather than copying the handler's casts.
		signed := imm
		if imm >= 128 {
			signed -= 256
		}
		equal := uint16(signed & 65535)
		tops := []uint16{equal, equal ^ 1, 0, 1, 0x7f, 0x80, 0xff, 0xff7f, 0xff80, 0xffff, 0x8000, 0x7fff}
		for _, top := range tops {
			for _, depth := range []int{1, 64, 65} {
				v := equalVM([]byte{0x3f, byte(imm), 0, 5, 0xff, 0xff}, depth, top)
				before := *v
				before.depth--
				before.stack[before.depth] = 0
				before.pc = 4
				if top == equal {
					before.pc = 5
				}
				if err := v.Step(); err != nil {
					t.Fatalf("imm=%02x top=%04x depth=%d: %v", imm, top, depth, err)
				}
				if !reflect.DeepEqual(&before, v) {
					t.Fatalf("imm=%02x top=%04x depth=%d pc=%d want=%d", imm, top, depth, v.pc, before.pc)
				}
			}
		}
	}
}

func TestBranchEqualFramingAndTargets(t *testing.T) {
	for _, depth := range []int{0, 1, 64, 65} {
		for _, top := range []uint16{0x80, 0xff80} {
			for tail := 0; tail < 3; tail++ {
				t.Run(fmt.Sprintf("framing%d/depth%d/top%x", tail, depth, top), func(t *testing.T) {
					v := equalVM([]byte{0x3f, 0x80, 0xff, 0xff}[:1+tail], depth, top)
					equalFault(t, v, ErrTruncated)
				})
			}
			for _, target := range []int{0, 3, 4, 65535} {
				t.Run(fmt.Sprintf("target%d/depth%d/top%x", target, depth, top), func(t *testing.T) {
					v := equalVM([]byte{0x3f, 0x80, byte(target >> 8), byte(target)}, depth, top)
					if depth == 0 {
						equalFault(t, v, ErrStackUnderflow)
						return
					}
					if top == 0xff80 && target >= 4 {
						equalFault(t, v, ErrInvalidTarget)
						return
					}
					prefix := append([]uint16(nil), v.stack[:depth-1]...)
					want := 4
					if top == 0xff80 {
						want = target
					}
					if err := v.Run(1); err != ErrBudget || v.pc != want || !reflect.DeepEqual(prefix, v.Stack()) {
						t.Fatalf("branch err=%v pc=%d", err, v.pc)
					}
					if want == 4 {
						equalFault(t, v, ErrTruncated)
					}
				})
			}
		}
	}
}

func TestBranchEqualNewAtAbsolute(t *testing.T) {
	// Only the maximal target requires a 64KiB program, never one VM per word.
	for _, target := range []int{0, 1, 0x1234, 0x8001, 0xffff} {
		size := target + 1
		if size < 16 {
			size = 16
		}
		code := make([]byte, size)
		copy(code[7:], []byte{5, 0x80, 0x3f, 0x80, byte(target >> 8), byte(target)})
		code[target] = 0xff
		v, err := NewAt(code, 7)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Run(2); err != ErrBudget || v.pc != target || v.depth != 0 {
			t.Fatalf("target=%x pc=%x err=%v", target, v.pc, err)
		}
		if err := v.Run(1); err != nil || !v.Halted() || v.pc != target+1 {
			t.Fatalf("target fetch: %v", err)
		}
	}
}

func TestBranchEqualBudgetAndHalt(t *testing.T) {
	for _, top := range []byte{0, 1} {
		v := New([]byte{5, top, 0x3f, 0, 0, 7, 0xff, 0xff})
		if err := v.Run(1); err != ErrBudget {
			t.Fatal(err)
		}
		before := *v
		if err := v.Run(0); err != ErrBudget || !reflect.DeepEqual(&before, v) {
			t.Fatal("zero budget advanced")
		}
		want := 6
		if top == 0 {
			want = 7
		}
		if err := v.Run(1); err != ErrBudget || v.pc != want || v.depth != 0 || v.halted {
			t.Fatalf("branch budget: %v", err)
		}
		if err := v.Run(1); err != nil || !v.halted {
			t.Fatalf("last-budget halt: %v", err)
		}
		before = *v
		for _, again := range []func() error{v.Step, func() error { return v.Run(0) }, func() error { return v.Run(10) }} {
			if err := again(); err != nil || !reflect.DeepEqual(&before, v) {
				t.Fatal("halt not inert")
			}
		}
	}
}

func TestBranchEqualConstructorsMemoryAndNestedReturns(t *testing.T) {
	for _, mode := range []string{"new", "at", "symbols", "address", "services"} {
		for _, top := range []byte{0, 1} {
			t.Run(fmt.Sprintf("%s/top%d", mode, top), func(t *testing.T) {
				// Two actual calls, save-top, branch, two actual returns, continuation.
				code := []byte{0x44, 0, 6, 5, 42, 0xff, 0x44, 0, 10, 0x45, 5, top, 0x0b, 0x3f, 0, 0, 18, 0x45, 0x45}
				original := append([]byte(nil), code...)
				ram := []byte{0x12, 0x34, 0x56, 0x78}
				space := AddressSpace{FileLength: uint32(len(code)), RAM: ram, Symbols: []AddressSymbol{{Region: AddressFile, Length: uint32(len(code))}, {Region: AddressRAM, Length: 4}}}
				var v *VM
				var err error
				switch mode {
				case "new":
					v = New(code)
				case "at":
					v, err = NewAt(code, 0)
				case "symbols":
					offset := uint32(0)
					v, err = NewWithSymbols(code, 0, []SymbolRegion{{ProgramOffset: &offset, Length: uint32(len(code))}, {Initial: ram, Length: 4}})
				case "address":
					v, err = NewWithAddressSpace(code, 0, space)
				case "services":
					v, err = NewWithAddressSpaceAndServices(code, 0, space, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				clear(code)
				clear(ram)
				snapshot := v.Program()
				clear(snapshot)
				if err := v.Run(4); err != ErrBudget || v.returnDepth != 2 || v.savedTop != 0 || !v.savedTopValid {
					t.Fatalf("prime: %v", err)
				}
				before := *v
				memory := []byte(nil)
				if len(v.symbols) > 0 {
					memory, _ = v.Symbol(1)
				}
				want := 17
				if top == 0 {
					want = 18
				}
				before.pc, before.depth, before.stack[0] = want, 0, 0
				if err := v.Step(); err != nil || !reflect.DeepEqual(&before, v) {
					t.Fatalf("branch changed unrelated state: %v", err)
				}
				checkMemory := func() {
					t.Helper()
					if !bytes.Equal(original, v.Program()) {
						t.Fatal("program changed")
					}
					if len(v.symbols) > 0 {
						file, _ := v.Symbol(0)
						current, _ := v.Symbol(1)
						if !bytes.Equal(file, original) || !bytes.Equal(memory, current) {
							t.Fatal("symbol memory changed")
						}
					}
				}
				checkMemory()
				if err := v.Step(); err != nil || v.pc != 9 {
					t.Fatalf("inner return: %v", err)
				}
				if err := v.Step(); err != nil || v.pc != 3 {
					t.Fatalf("outer return: %v", err)
				}
				if err := v.Run(2); err != nil || !v.halted || !reflect.DeepEqual(v.Stack(), []uint16{42}) || v.savedTop != 0 || !v.savedTopValid {
					t.Fatalf("continuation: %v", err)
				}
				checkMemory()
			})
		}
	}
}

func TestBranchEqualSavedStateAndMemoryFaultAtomicity(t *testing.T) {
	for _, valid := range []bool{false, true} {
		for _, tail := range []int{0, 1, 2, 3} {
			code := []byte{0x3f, 0x80, 0xff, 0xff}[:1+tail]
			v, err := NewWithAddressSpace(code, 0, AddressSpace{FileLength: uint32(len(code)), RAM: []byte{1, 2}, Symbols: []AddressSymbol{{Region: AddressFile, Length: uint32(len(code))}, {Region: AddressRAM, Length: 2}}})
			if err != nil {
				t.Fatal(err)
			}
			v.depth, v.stack[0] = 1, 0xff80
			v.savedTop, v.savedTopValid = -123, valid
			v.returnDepth, v.returns[0] = 1, 3
			ram := append([]byte(nil), v.address.ram...)
			bindings := append([]addressBinding(nil), v.address.bindings...)
			cause := ErrTruncated
			if tail == 3 {
				cause = ErrInvalidTarget
			}
			equalFault(t, v, cause)
			if !bytes.Equal(ram, v.address.ram) || !reflect.DeepEqual(bindings, v.address.bindings) {
				t.Fatal("fault changed RAM/bindings")
			}
		}
		for _, top := range []uint16{0, 1} {
			v := equalVM([]byte{0x3f, 0, 0, 0}, 1, top)
			v.savedTopValid = valid
			if err := v.Step(); err != nil || v.savedTop != -17 || v.savedTopValid != valid {
				t.Fatalf("saved top changed: %v", err)
			}
		}
	}
}

func TestBranchEqualLiveProgramOperands(t *testing.T) {
	// A legitimate symbol store changes the future immediate in the owned code.
	// Fetch must observe that store, not a constructor-time operand cache.
	offset := uint32(7)
	v, err := NewWithSymbols([]byte{0x36, 0, 0x80, 6, 0xff, 0x80, 0x3f, 0, 0, 11, 0xff, 0xff}, 0, []SymbolRegion{{ProgramOffset: &offset, Length: 2}})
	if err != nil {
		t.Fatal(err)
	}
	// Store writes ff80 LE, so the target high byte becomes ff. The unequal
	// 0080 control ignores that invalid target, while ff80 must fault atomically.
	if err := v.Run(2); err != ErrBudget {
		t.Fatal(err)
	}
	equalFault(t, v, ErrInvalidTarget)
	v, err = NewWithSymbols([]byte{0x36, 0, 0x80, 6, 0, 0x80, 0x3f, 0, 0, 11, 0xff, 0xff}, 0, []SymbolRegion{{ProgramOffset: &offset, Length: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(3); err != ErrBudget || v.pc != 10 || v.depth != 0 {
		t.Fatalf("live unequal: %v pc=%d", err, v.pc)
	}
}
