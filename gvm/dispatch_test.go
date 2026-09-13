package gvm

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	gruntime "github.com/mirusu400/aram-core/runtime"
)

func dispatchStart(t *testing.T, v *VM, entry uint32) {
	t.Helper()
	if started, err := v.BeginDispatch(entry); !started || err != nil {
		t.Fatalf("BeginDispatch(%d) = %v, %v", entry, started, err)
	}
}

func TestBeginDispatchPreconditions(t *testing.T) {
	for _, budget := range []uint64{0, 1} {
		v := New([]byte{0, 0xff})
		if err := v.Run(budget); err != ErrBudget {
			t.Fatal(err)
		}
		for _, entry := range []uint32{0, 1, 2, ^uint32(0)} {
			before := *v
			if started, err := v.BeginDispatch(entry); started || err != ErrDispatchActive {
				t.Fatalf("busy %d: %v %v", entry, started, err)
			}
			if !reflect.DeepEqual(before, *v) {
				t.Fatal("busy mutated VM")
			}
		}
		if err := v.Run(2); err != nil {
			t.Fatal(err)
		}
	}
	var zero VM
	if started, err := zero.BeginDispatch(0); started || err != ErrDispatchActive {
		t.Fatalf("zero VM: %v %v", started, err)
	}
	for _, code := range [][]byte{nil, {1}, {0x12}} {
		v := New(code)
		fault := v.Step()
		if fault == nil {
			t.Fatal("missing fixture fault")
		}
		for _, halted := range []bool{false, true} {
			v.halted = halted // Fault must win even over an internally inconsistent halt.
			for _, entry := range []uint32{0, 1, ^uint32(0)} {
				before := *v
				if started, err := v.BeginDispatch(entry); started || err != fault {
					t.Fatalf("fault identity: %v %v", started, err)
				}
				if !reflect.DeepEqual(before, *v) || v.Run(0) != fault || v.Step() != fault {
					t.Fatal("fault mutated or cleared")
				}
			}
		}
	}
}

func TestBeginDispatchTransactionAndBacking(t *testing.T) {
	// Guest halt with live operands and two outstanding calls.
	v := New([]byte{5, 7, 0x0b, 0x44, 0, 6, 0x44, 0, 9, 0xff, 0xff})
	if err := v.Run(5); err != nil {
		t.Fatal(err)
	}
	if len(v.Stack()) != 1 || v.returnDepth != 2 || !v.savedTopValid {
		t.Fatal("fixture")
	}
	for i := v.depth; i < len(v.stack); i++ {
		v.stack[i] = uint16(0x8000 + i)
	}
	for i := v.returnDepth; i < len(v.returns); i++ {
		v.returns[i] = 1000 + i
	}
	before := *v
	program := v.Program()
	for _, entry := range []uint32{0, uint32(len(program)), uint32(len(program) + 1), 0x80000000, ^uint32(0), 0} {
		started, err := v.BeginDispatch(entry)
		if started {
			t.Fatal("unexpected start")
		}
		if entry == 0 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var execution *ExecutionError
			if !errors.Is(err, ErrInvalidTarget) || err == ErrInvalidTarget || errors.As(err, &execution) {
				t.Fatalf("invalid control error: %v", err)
			}
		}
		if !reflect.DeepEqual(before, *v) || !bytes.Equal(program, v.Program()) {
			t.Fatal("transaction changed state")
		}
	}
	dispatchStart(t, v, 10)
	before.pc, before.halted, before.depth, before.returnDepth, before.savedTopValid = 10, false, 0, 0, false
	if !reflect.DeepEqual(before, *v) || &before.code[0] != &v.code[0] {
		t.Fatal("success changed more than dispatch control fields")
	}
	if v.PC() != 10 || v.Halted() || len(v.Stack()) != 0 {
		t.Fatal("automatic execution")
	}
	if err := v.Run(0); err != ErrBudget {
		t.Fatal(err)
	}
	if err := v.Run(1); err != nil || !v.Halted() {
		t.Fatalf("final budget halt: %v", err)
	}
	empty := &VM{halted: true, savedTop: 64, savedTopValid: true}
	snapshot := *empty
	if started, err := empty.BeginDispatch(0); started || err != nil || !reflect.DeepEqual(snapshot, *empty) {
		t.Fatal("empty zero not no-op")
	}
}

func TestBeginDispatchMarkerPolicy(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		code := []byte{5, 7, 0x0b, 0xff}
		if fresh {
			code = append(code, 0x0b)
		}
		code = append(code, 0x0c, 0xff)
		v := New(code)
		if err := v.Run(3); err != nil {
			t.Fatal(err)
		}
		if started, err := v.BeginDispatch(0); started || err != nil || !v.savedTopValid {
			t.Fatal("zero cleared marker")
		}
		dispatchStart(t, v, 4)
		if v.savedTop != 0 || v.savedTopValid || v.stack[0] != 7 {
			t.Fatal("raw marker/backing not retained")
		}
		err := v.Run(3)
		if fresh {
			if err != nil || !v.Halted() || v.savedTop != -1 || !v.savedTopValid {
				t.Fatalf("fresh0b: %v", err)
			}
		} else {
			var unsupported *UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 0x0c || unsupported.Offset != 4 {
				t.Fatalf("host fail-closed policy: %v", err)
			}
		}
	}
}

func TestBeginDispatchFullBufferAndBudget(t *testing.T) {
	code := make([]byte, 0x10004)
	code[0] = 0xff
	copy(code[0x10001:], []byte{0x41, 0, 0}) // Absolute branch back into the original buffer.
	v := New(code)
	if err := v.Run(1); err != nil {
		t.Fatal(err)
	}
	dispatchStart(t, v, 0x10001)
	if v.PC() != 0x10001 || !bytes.Equal(v.Program(), code) {
		t.Fatal("entry masked or buffer rebased")
	}
	if err := v.Run(0); err != ErrBudget || v.PC() != 0x10001 {
		t.Fatal("zero budget progressed")
	}
	if err := v.Run(1); err != ErrBudget || v.PC() != 0 {
		t.Fatalf("branch: %v pc%d", err, v.PC())
	}
	if started, err := v.BeginDispatch(1); started || err != ErrDispatchActive {
		t.Fatal("suspended dispatch replaced")
	}
	if err := v.Run(1); err != nil {
		t.Fatal(err)
	}
	dispatchStart(t, v, uint32(len(code)-1))
	if err := v.Run(1); err != ErrBudget || v.PC() != len(code) {
		t.Fatalf("last nop: %v", err)
	}
	fault := v.Step()
	if !errors.Is(fault, ErrTruncated) {
		t.Fatal(fault)
	}
	if started, err := v.BeginDispatch(1); started || err != fault {
		t.Fatal("end-fetch fault cleared")
	}
}

func TestBeginDispatchNestedReturns(t *testing.T) {
	// First dispatch exits within a call. The next must not retain that return.
	code := []byte{0x44, 0, 3, 0xff, 0x44, 0, 8, 0xff, 0x44, 0, 12, 0x45, 5, 9, 0x45}
	v := New(code)
	if err := v.Run(2); err != nil || v.returnDepth != 1 {
		t.Fatalf("fixture: %v", err)
	}
	dispatchStart(t, v, 4)
	if err := v.Run(6); err != nil || !v.Halted() || v.PC() != 8 || v.returnDepth != 0 || !reflect.DeepEqual(v.Stack(), []uint16{9}) {
		t.Fatalf("nested return: %v pc%d stack%v returns%d", err, v.PC(), v.Stack(), v.returnDepth)
	}
}

func TestBeginDispatchConstructorFamilies(t *testing.T) {
	constructors := map[string]func([]byte) (*VM, error){
		"New":      func(p []byte) (*VM, error) { return New(p), nil },
		"NewAt":    func(p []byte) (*VM, error) { return NewAt(p, 0) },
		"symbols":  func(p []byte) (*VM, error) { return NewWithSymbols(p, 0, nil) },
		"address":  func(p []byte) (*VM, error) { return NewWithAddressSpace(p, 0, AddressSpace{}) },
		"services": func(p []byte) (*VM, error) { return NewWithAddressSpaceAndServices(p, 0, AddressSpace{}, nil) },
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			for _, op := range []byte{0x51, 0xb9, 0xa1} {
				v, err := construct([]byte{5, 7, 0xff, op})
				if err != nil {
					t.Fatal(err)
				}
				if v.PC() != 0 || v.Halted() {
					t.Fatal("constructor entry zero changed")
				}
				if err := v.Run(2); err != nil || !reflect.DeepEqual(v.Stack(), []uint16{7}) {
					t.Fatalf("initial run: %v", err)
				}
				dispatchStart(t, v, 3)
				err = v.Step()
				if name == "services" {
					if !errors.Is(err, ErrStackUnderflow) {
						t.Fatal(err)
					}
				} else {
					var unsupported *UnsupportedOpcodeError
					if !errors.As(err, &unsupported) || unsupported.Opcode != op {
						t.Fatalf("legacy service changed: %v", err)
					}
				}
			}
		})
	}
}

func TestBeginDispatchAliasesAndServices(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			// Each dispatch stores into shared program data, then reads its overlapping alias.
			code := []byte{0xff, 6, 0x12, 0x34, 0x0a, 0, 4, 1, 0xff, 0, 0}
			clock := new(gruntime.Clock)
			random := gruntime.NewRandom(123, 4)
			if err := random.SetLCG214013Seed("dispatch", 99); err != nil {
				t.Fatal(err)
			}
			var v *VM
			var err error
			if legacy {
				off := uint32(9)
				v, err = NewWithSymbols(code, 0, []SymbolRegion{{ProgramOffset: &off, Length: 2}, {ProgramOffset: &off, Length: 2}, {Initial: []byte{4, 5}, Length: 2}})
			} else {
				v, err = NewWithAddressSpaceAndServices(code, 0, AddressSpace{FileStart: 9, FileLength: 2, RAM: []byte{4, 5}, Symbols: []AddressSymbol{{Region: AddressFile, Length: 2}, {Region: AddressFile, Length: 2}, {Region: AddressRAM, Length: 2}, {Region: AddressRAM, Length: 2}}}, &ServiceConfig{Clock: clock, ClockPolicy: FixedOffsetNoDST, Random: random, RandomStream: "dispatch", DeviceQuery: &DeviceQueryProfile{Width: 128, Height: 128, AudioType: 5}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Run(1); err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 2; n++ {
				before := *v
				program := v.Program()
				symbols := make([][]byte, len(v.symbols))
				pointers := make([]*byte, len(v.symbols))
				for i := range symbols {
					symbols[i], err = v.Symbol(uint8(i))
					if err != nil {
						t.Fatal(err)
					}
					pointers[i] = &v.symbols[i][0]
				}
				cs, rs := clock.Snapshot(), random.Snapshot()
				dispatchStart(t, v, 1)
				before.pc, before.halted, before.depth, before.returnDepth, before.savedTopValid = 1, false, 0, 0, false
				if !reflect.DeepEqual(before, *v) || !bytes.Equal(program, v.Program()) || before.address != v.address || before.services != v.services || &before.code[0] != &v.code[0] {
					t.Fatal("storage or binding replaced")
				}
				if !reflect.DeepEqual(cs, clock.Snapshot()) || !reflect.DeepEqual(rs, random.Snapshot()) {
					t.Fatal("service mutation")
				}
				for i := range symbols {
					got, _ := v.Symbol(uint8(i))
					if !bytes.Equal(got, symbols[i]) || pointers[i] != &v.symbols[i][0] {
						t.Fatal("symbol changed")
					}
				}
				if !legacy {
					if &v.address.file[0] != &v.code[9] || &v.address.ram[0] != &v.symbols[2][0] || &v.symbols[2][0] != &v.symbols[3][0] || v.services.clock != clock || v.services.random != random {
						t.Fatal("alias topology")
					}
					v.symbols[2][0]++
					if v.symbols[3][0] != byte(5+n) {
						t.Fatal("RAM alias lost")
					}
				}
				if err := v.Run(4); err != nil || !reflect.DeepEqual(v.Stack(), []uint16{0x1234}) {
					t.Fatalf("alias execution: %v %x", err, v.Stack())
				}
				if v.Program()[9] != 0x34 {
					t.Fatal("file write detached from program")
				}
				v.symbols[1][0] = byte(0x70 + n)
				if v.Program()[9] != byte(0x70+n) || v.symbols[0][0] != byte(0x70+n) {
					t.Fatal("alias mutation lost")
				}
			}
		})
	}
}
