package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// These tests specify a CANDIDATE restricted host domain, not native guards.
// Only observed, representable, non-growing restores are proposed for support.
func restorePush(code []byte, value uint16) []byte {
	return append(code, 6, byte(value>>8), byte(value))
}

func restoreUnsupported(t *testing.T, v *VM) {
	t.Helper()
	before := *v
	program := v.Program()
	err := v.Step()
	var unsupported *UnsupportedOpcodeError
	if !errors.As(err, &unsupported) || unsupported.Opcode != 0x0c || unsupported.Offset != before.pc {
		t.Fatalf("want typed unsupported0c at %d, got %v", before.pc, err)
	}
	before.pc++
	before.fault = err
	for _, again := range []func() error{v.Step, func() error { return v.Run(0) }, func() error { return v.Run(20) }} {
		if !reflect.DeepEqual(&before, v) || !bytes.Equal(program, v.Program()) {
			t.Fatal("unsupported restore changed state except fetch/fault")
		}
		if again() != err {
			t.Fatal("unsupported fault not sticky")
		}
	}
	if !reflect.DeepEqual(&before, v) {
		t.Fatal("sticky fault mutated state")
	}
}

func TestRestoreTopPublicDepthMarkerMatrix(t *testing.T) {
	failures, first := 0, ""
	for savedDepth := 0; savedDepth <= 65; savedDepth++ {
		for current := 0; current <= 65; current++ {
			var code []byte
			for i := 0; i < savedDepth; i++ {
				code = restorePush(code, uint16(0x1100+i))
			}
			code = append(code, 0x0b)
			steps := savedDepth + 1
			for i := savedDepth; i < current; i++ {
				code = restorePush(code, uint16(0x2200+i))
				steps++
			}
			for i := current; i < savedDepth; i++ {
				code = append(code, 0x96)
				steps++
			}
			offset := len(code)
			code = append(code, 0x0c, 0x0c, 0xff)
			v := New(code)
			if err := v.Run(uint64(steps)); err != ErrBudget {
				t.Fatalf("prime %d/%d: %v", savedDepth, current, err)
			}
			if savedDepth > current {
				restoreUnsupported(t, v)
				continue
			}
			want := append([]uint16(nil), v.Stack()[:savedDepth]...)
			if err := v.Step(); err != nil {
				failures++
				if first == "" {
					first = fmt.Sprintf("savedDepth=%d current=%d: %v", savedDepth, current, err)
				}
				continue
			}
			if v.PC() != offset+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), want) || !bytes.Equal(v.Program(), code) {
				t.Fatalf("restore mismatch %d/%d", savedDepth, current)
			}
			if err := v.Step(); err != nil || v.PC() != offset+2 || !reflect.DeepEqual(v.Stack(), want) {
				t.Fatalf("repeat %d/%d: %v", savedDepth, current, err)
			}
			if err := v.Run(1); err != nil || !v.Halted() {
				t.Fatalf("halt: %v", err)
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d supported-domain failures among 2211 non-growing pairs (2145 growing controls checked); first: %s", failures, first)
	}
}

func TestRestoreTopScalarNotSnapshotAndOverwrite(t *testing.T) {
	// Save [1111,2222], then increment top and add a word. Restore must retain
	// 2223, not recover historical2222. A later0b replaces the single marker.
	code := restorePush(nil, 0x1111)
	code = restorePush(code, 0x2222)
	code = append(code, 0x0b, 0x0d)
	code = restorePush(code, 0x3333)
	code = append(code, 0x0c, 0x96, 0x0b)
	code = restorePush(code, 0x4444)
	code = append(code, 0x0c, 0x0c, 0xff)
	v := New(code)
	if err := v.Run(6); err != ErrBudget || !reflect.DeepEqual(v.Stack(), []uint16{0x1111, 0x2223}) {
		t.Fatalf("scalar restore: %v stack=%x", err, v.Stack())
	}
	if err := v.Run(6); err != nil || !v.Halted() || !reflect.DeepEqual(v.Stack(), []uint16{0x1111}) || v.savedTop != 0 || !v.savedTopValid {
		t.Fatalf("overwrite/repeat: %v stack=%x", err, v.Stack())
	}
}

func TestRestoreTopGrowingAfterClearedPopRejected(t *testing.T) {
	for _, pop := range [][]byte{{0x96}, {0x3f, 7, 0, 0xff}} {
		code := []byte{5, 7, 0x0b}
		code = append(code, pop...)
		code = append(code, 0x0c)
		// For3f use an unequal immediate so the invalid target is ignored.
		if len(pop) > 1 {
			code[4] = 8
		}
		v := New(code)
		if err := v.Run(3); err != ErrBudget || len(v.Stack()) != 0 || v.stack[0] != 0 {
			t.Fatalf("pop prime: %v", err)
		}
		restoreUnsupported(t, v)
	}
}

func TestRestoreTopExactBackingAndMarkerPreservation(t *testing.T) {
	for savedDepth := 0; savedDepth <= 65; savedDepth++ {
		for current := savedDepth; current <= 65; current++ {
			v := New([]byte{0x0c})
			v.depth, v.savedTop, v.savedTopValid = current, int32(savedDepth)-1, true
			for i := range v.stack {
				v.stack[i] = uint16(0x9000 + i)
			}
			v.returnDepth, v.returns[0], v.returns[1] = 2, 19, 23
			before := *v
			before.pc, before.depth = 1, savedDepth
			if err := v.Step(); err != nil {
				t.Fatalf("savedDepth=%d current=%d: %v", savedDepth, current, err)
			}
			if !reflect.DeepEqual(&before, v) {
				t.Fatalf("restore must assign depth only, savedDepth=%d current=%d", savedDepth, current)
			}
		}
	}
}

func TestRestoreTopInvalidMarkerDomain(t *testing.T) {
	for _, valid := range []bool{false, true} {
		for _, marker := range []int32{-2147483648, -2, -1, 0, 63, 64, 65, 2147483647} {
			for _, depth := range []int{0, 1, 64, 65} {
				if valid && marker >= -1 && marker <= 64 && int64(marker)+1 <= int64(depth) {
					continue
				}
				v := New([]byte{0x0c})
				v.depth, v.savedTop, v.savedTopValid = depth, marker, valid
				for i := range v.stack {
					v.stack[i] = uint16(i + 1)
				}
				restoreUnsupported(t, v)
			}
		}
	}
	// An untouched VM has no observed0b, including when current depth is zero.
	restoreUnsupported(t, New([]byte{0x0c}))
}

func TestRestoreTopMemoryCallsAndBudgets(t *testing.T) {
	// Nested calls save distinct actual PCs. Empty marker, one pushed word,
	// restore, two returns and a continuation verify operand/return separation.
	code := []byte{0x44, 0, 6, 5, 42, 0xff, 0x44, 0, 10, 0x45, 0x0b, 5, 7, 0x0c, 0x45}
	ram := []byte{1, 2, 3, 4}
	v, err := NewWithAddressSpace(code, 0, AddressSpace{FileLength: uint32(len(code)), RAM: ram, Symbols: []AddressSymbol{{Region: AddressFile, Length: uint32(len(code))}, {Region: AddressRAM, Length: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(4); err != ErrBudget {
		t.Fatal(err)
	}
	before := *v
	if err := v.Run(0); err != ErrBudget || !reflect.DeepEqual(&before, v) {
		t.Fatal("zero budget advanced")
	}
	before.pc, before.depth = 14, 0
	if err := v.Run(1); err != ErrBudget {
		t.Fatalf("restore budget: %v", err)
	}
	if !reflect.DeepEqual(&before, v) {
		t.Fatal("restore changed backing/marker/returns")
	}
	file, _ := v.Symbol(0)
	memory, _ := v.Symbol(1)
	if !bytes.Equal(file, code) || !bytes.Equal(memory, ram) || !bytes.Equal(v.Program(), code) {
		t.Fatal("restore changed memory")
	}
	if err := v.Step(); err != nil || v.PC() != 9 {
		t.Fatalf("inner return: %v", err)
	}
	if err := v.Step(); err != nil || v.PC() != 3 {
		t.Fatalf("outer return: %v", err)
	}
	if err := v.Run(2); err != nil || !v.Halted() || !reflect.DeepEqual(v.Stack(), []uint16{42}) {
		t.Fatalf("continuation: %v", err)
	}
	before = *v
	for _, again := range []func() error{v.Step, func() error { return v.Run(0) }, func() error { return v.Run(9) }} {
		if err := again(); err != nil || !reflect.DeepEqual(&before, v) {
			t.Fatal("halt not inert")
		}
	}
}

func TestRestoreTopEndFetchAndExistingFault(t *testing.T) {
	v := New([]byte{0x0b, 0x0c})
	if err := v.Run(2); err != ErrBudget || v.PC() != 2 {
		t.Fatalf("one-byte end restore: %v", err)
	}
	before := *v
	err := v.Step()
	var execution *ExecutionError
	if !errors.Is(err, ErrTruncated) || !errors.As(err, &execution) || execution.Offset != 2 {
		t.Fatalf("end fetch: %v", err)
	}
	before.fault = err
	if !reflect.DeepEqual(&before, v) || v.Step() != err || v.Run(0) != err {
		t.Fatal("end fault mutated state or not sticky")
	}
	// Unsupported0c without a marker stays unsupported even when followed by0b.
	v = New([]byte{0x0c, 0x0b, 0x0c})
	restoreUnsupported(t, v)
}
