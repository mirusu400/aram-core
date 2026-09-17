package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// Synthetic contract tests. Internal snapshots verify safety policy without
// adding public state APIs or depending on a native malformed-input outcome.
func branchImmediateVM(t *testing.T, code []byte, entry, depth int, top int16, configured bool) *VM {
	t.Helper()
	var v *VM
	var err error
	if configured {
		v, err = NewWithAddressSpace(code, uint32(entry), AddressSpace{FileStart: 0, FileLength: uint32(len(code)), RAM: []byte{0x12, 0x34, 0x56, 0x78}, Symbols: []AddressSymbol{{Region: AddressFile, Length: uint32(len(code))}, {Region: AddressRAM, Length: 4}}})
	} else {
		v, err = NewAt(code, uint32(entry))
	}
	if err != nil {
		t.Fatal(err)
	}
	for i := range v.stack {
		v.stack[i] = uint16(0xa000 + i)
	}
	v.depth = depth
	if depth > 0 {
		v.stack[depth-1] = uint16(top)
	}
	for i := range v.returns {
		v.returns[i] = 100 + i
	}
	v.returnDepth = len(v.returns)
	return v
}

func branchImmediateUnchanged(t *testing.T, v *VM) func() {
	t.Helper()
	code := append([]byte(nil), v.code...)
	returns, returnDepth := v.returns, v.returnDepth
	var ram []byte
	if v.address != nil {
		ram = append([]byte(nil), v.address.ram...)
	}
	symbols := make([][]byte, len(v.symbols))
	for i := range symbols {
		symbols[i] = append([]byte(nil), v.symbols[i]...)
	}
	return func() {
		t.Helper()
		if !bytes.Equal(code, v.code) || returns != v.returns || returnDepth != v.returnDepth {
			t.Fatal("program or return stack changed")
		}
		if v.address != nil && !bytes.Equal(ram, v.address.ram) {
			t.Fatal("RAM changed")
		}
		for i := range symbols {
			if !bytes.Equal(symbols[i], v.symbols[i]) {
				t.Fatalf("symbol %d changed", i)
			}
		}
	}
}

func TestBranchImmediateSignedMatrix(t *testing.T) {
	for _, top := range []int16{-32768, -129, -128, -1, 0, 1, 126, 127, 128, 32767} {
		for _, immediate := range []int8{-128, -1, 0, 1, 127} {
			for _, depth := range []int{1, 65} {
				for _, configured := range []bool{false, true} {
					t.Run(fmt.Sprintf("top%d/imm%d/depth%d/address%t", top, immediate, depth, configured), func(t *testing.T) {
						v := branchImmediateVM(t, []byte{0xff, 0x3c, byte(immediate), 0, 6, 0xff, 0xff}, 1, depth, top, configured)
						unchanged := branchImmediateUnchanged(t, v)
						prefix := append([]uint16(nil), v.Stack()[:depth-1]...)
						wantPC := 5
						if int(top) < int(immediate) {
							wantPC = 6
						}
						if err := v.Step(); err != nil {
							t.Fatal(err)
						}
						if v.PC() != wantPC || v.depth != depth-1 || !reflect.DeepEqual(v.Stack(), prefix) || v.Halted() || v.fault != nil {
							t.Fatalf("pc=%d stack=%v halted=%t fault=%v", v.PC(), v.Stack(), v.Halted(), v.fault)
						}
						unchanged()
					})
				}
			}
		}
	}
}

func TestBranchImmediateWholeBufferTargets(t *testing.T) {
	for _, target := range []int{0, 0x1234, 0x8001, 0xffff} {
		t.Run(fmt.Sprintf("%04x", target), func(t *testing.T) {
			code := make([]byte, 65536)
			copy(code[9:], []byte{0x3c, 0, byte(target >> 8), byte(target)})
			code[target] = 0xff
			v := branchImmediateVM(t, code, 9, 65, -1, false)
			unchanged := branchImmediateUnchanged(t, v)
			if err := v.Step(); err != nil || v.PC() != target || v.depth != 64 {
				t.Fatalf("target=%x pc=%x depth=%d err=%v", target, v.PC(), v.depth, err)
			}
			unchanged()
			if err := v.Step(); err != nil || !v.Halted() {
				t.Fatalf("target fetch: %v", err)
			}
		})
	}
}

func TestBranchImmediateFaults(t *testing.T) {
	type testCase struct {
		name     string
		operands []byte
		depth    int
		top      int16
		cause    error
	}
	var cases []testCase
	for n := 0; n < 3; n++ {
		for _, depth := range []int{0, 1, 65} {
			for _, top := range []int16{-1, 1} {
				cases = append(cases, testCase{fmt.Sprintf("truncated%d/depth%d/top%d", n, depth, top), []byte{0, 0, 0}[:n], depth, top, ErrTruncated})
			}
		}
	}
	cases = append(cases, testCase{"underflow-before-invalid-target", []byte{0, 0xff, 0xff}, 0, 0, ErrStackUnderflow})
	for _, target := range []int{6, 7, 65535} {
		for _, depth := range []int{1, 65} {
			cases = append(cases, testCase{fmt.Sprintf("invalid%d/depth%d", target, depth), []byte{0, byte(target >> 8), byte(target)}, depth, -1, ErrInvalidTarget})
		}
	}
	for _, tc := range cases {
		for _, configured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/address%t", tc.name, configured), func(t *testing.T) {
				code := append([]byte{0xff, 0xff, 0x3c}, tc.operands...)
				v := branchImmediateVM(t, code, 2, tc.depth, tc.top, configured)
				unchanged := branchImmediateUnchanged(t, v)
				stack, depth := v.stack, v.depth
				err := v.Step()
				var fault *ExecutionError
				if !errors.Is(err, tc.cause) || !errors.As(err, &fault) || fault.Offset != 2 {
					t.Fatalf("fault=%v want=%v at 2", err, tc.cause)
				}
				for _, again := range []func() error{v.Step, func() error { return v.Run(0) }, func() error { return v.Run(10) }} {
					if v.PC() != 3 || v.stack != stack || v.depth != depth || v.Halted() {
						t.Fatal("fault committed state")
					}
					unchanged()
					if again() != err {
						t.Fatal("fault is not sticky")
					}
				}
				if v.PC() != 3 || v.stack != stack || v.depth != depth || v.Halted() {
					t.Fatal("sticky fault changed state")
				}
				unchanged()
			})
		}
	}
}

func TestBranchImmediateUnusedTargetAndFallthroughEnd(t *testing.T) {
	for _, target := range []int{6, 7, 65535} {
		for _, top := range []int16{0, 1} {
			t.Run(fmt.Sprintf("target%d/top%d", target, top), func(t *testing.T) {
				v := branchImmediateVM(t, []byte{0xff, 0xff, 0x3c, 0, byte(target >> 8), byte(target)}, 2, 65, top, true)
				unchanged := branchImmediateUnchanged(t, v)
				prefix := v.Stack()[:64]
				if err := v.Step(); err != nil || v.PC() != 6 || !reflect.DeepEqual(v.Stack(), prefix) || v.Halted() || v.fault != nil {
					t.Fatalf("fallthrough: pc=%d err=%v", v.PC(), err)
				}
				unchanged()
				stack := v.stack
				err := v.Step()
				var fault *ExecutionError
				if !errors.Is(err, ErrTruncated) || !errors.As(err, &fault) || fault.Offset != 6 || v.PC() != 6 {
					t.Fatalf("end fetch: %v pc=%d", err, v.PC())
				}
				if v.Step() != err || v.Run(0) != err || v.Run(1) != err || v.stack != stack || v.depth != 64 || v.Halted() {
					t.Fatal("end fetch not sticky/transactional")
				}
				unchanged()
			})
		}
	}
}

func TestBranchImmediateBudget(t *testing.T) {
	for _, top := range []int16{-1, 0} {
		t.Run(fmt.Sprint(top), func(t *testing.T) {
			v := branchImmediateVM(t, []byte{0xff, 0x3c, 0, 0, 6, 0xff, 0xff}, 1, 1, top, true)
			unchanged := branchImmediateUnchanged(t, v)
			stack := v.stack
			if err := v.Run(0); err != ErrBudget || v.PC() != 1 || v.depth != 1 || v.stack != stack || v.fault != nil || v.Halted() {
				t.Fatalf("zero budget: %v", err)
			}
			unchanged()
			wantPC := 5
			if top < 0 {
				wantPC = 6
			}
			if err := v.Run(1); err != ErrBudget || v.PC() != wantPC || v.depth != 0 || v.fault != nil || v.Halted() {
				t.Fatalf("one budget: %v pc=%d", err, v.PC())
			}
			unchanged()
			if err := v.Run(1); err != nil || !v.Halted() {
				t.Fatalf("resume: %v", err)
			}
		})
	}
}
