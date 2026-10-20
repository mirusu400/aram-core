package gnex32

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func putWord(code []byte, offset int, word uint16) {
	binary.LittleEndian.PutUint16(code[offset:], word)
}

func TestPushXUsesIndexedElementOfIndexSymbol(t *testing.T) {
	code := make([]byte, 10)
	for offset, word := range map[int]uint16{
		0: 0x01, 2: 0, 4: 1, 6: 1, // target[indexArray[1]]
		8: 0x86,
	} {
		putWord(code, offset, word)
	}
	target := make([]byte, 12)
	for index, value := range []uint32{11, 22, 33} {
		binary.LittleEndian.PutUint32(target[index*4:], value)
	}
	indices := make([]byte, 8)
	binary.LittleEndian.PutUint32(indices[4:], 2)
	vm, err := New(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{
			{Flags: 0x100, Words: 3, Data: target},
			{Flags: 0x100, Words: 2, Data: indices},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); err != nil {
		t.Fatal(err)
	}
	if vm.StackTop() != 33 {
		t.Fatalf("pushx = %d, want 33", vm.StackTop())
	}
}

func TestPopIndexedSymbolUsesIndexSymbol(t *testing.T) {
	code := make([]byte, 18)
	for offset, word := range map[int]uint16{
		0: 0x05, 2: 42, // push immediate value
		4: 0x09, 6: 0, 8: 1, // popi destination[ indexSymbol ]
		10: 0x03, 12: 0, 14: 2, // push destination[2]
		16: 0x86,
	} {
		putWord(code, offset, word)
	}
	index := make([]byte, 4)
	binary.LittleEndian.PutUint32(index, 2)
	vm, err := New(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{
			{Flags: 1, Words: 3, Data: make([]byte, 12)},
			{Flags: 1, Words: 1, Data: index},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil {
		t.Fatal(err)
	}
	if vm.PC() != 18 || vm.StackDepth() != 1 || vm.StackTop() != 42 {
		t.Fatalf("indexed pop: pc=%d stack=%v", vm.PC(), vm.stack)
	}
	if got, _ := vm.memory.ReadWord(0, 0); got != 0 {
		t.Fatalf("neighbor overwritten: %d", got)
	}
}

func TestCandidateCallReturnAndSymbolRead(t *testing.T) {
	code := make([]byte, 24)
	putWord(code, 0, 0x86) // no-op routine that returns immediately
	putWord(code, 2, 0x85) // call code offset 0
	putWord(code, 4, 0)
	putWord(code, 6, 0x80)
	putWord(code, 8, 0x04) // read first word of symbol 0
	putWord(code, 10, 0)
	putWord(code, 12, 0x86) // return from the event handler
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160},
		Code:   code, Entry: 162,
		Symbols: []gnex.GNEX32Symbol{{Flags: 0x100, Words: 1, Data: []byte{0x78, 0x56, 0x34, 0x12}}},
	}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil {
		t.Fatal(err)
	}
	if !vm.Halted() || vm.PC() != 14 || vm.StackDepth() != 1 || vm.StackTop() != 0x12345678 {
		t.Fatalf("halt=%v pc=%d depth=%d top=%#x", vm.Halted(), vm.PC(), vm.StackDepth(), vm.StackTop())
	}
}

func TestCandidateDirectSymbolAssignments(t *testing.T) {
	code := make([]byte, 18)
	putWord(code, 0, 0x75)
	putWord(code, 2, 0)
	putWord(code, 4, 0xffff)
	putWord(code, 6, 0x74)
	putWord(code, 8, 1)
	putWord(code, 10, 0)
	putWord(code, 12, 0x04)
	putWord(code, 14, 1)
	putWord(code, 16, 0x86)
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{
			{Flags: 1, Words: 1, Data: make([]byte, 4)},
			{Flags: 1, Words: 1, Data: make([]byte, 4)},
		},
	}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil {
		t.Fatal(err)
	}
	if !vm.Halted() || vm.PC() != 18 || vm.StackDepth() != 1 || vm.StackTop() != 0xffffffff {
		t.Fatalf("direct assignment state: pc=%d stack=%#v", vm.PC(), vm.stack)
	}
	if got, _ := vm.memory.ReadWord(0, 0); got != 0xffffffff {
		t.Fatalf("source = %#x", got)
	}
	if got, _ := vm.memory.ReadWord(1, 0); got != 0xffffffff {
		t.Fatalf("destination = %#x", got)
	}
	if got := binary.LittleEndian.Uint32(image.Symbols[0].Data); got != 0 {
		t.Fatalf("input initializer changed: %#x", got)
	}
}

func TestCandidateDirectSymbolAssignmentUpdatesInitializedDestination(t *testing.T) {
	code := make([]byte, 6)
	putWord(code, 0, 0x75)
	putWord(code, 2, 0)
	putWord(code, 4, 7)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 0x100, Words: 1, Data: make([]byte, 4)}}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); err != nil || vm.PC() != 6 {
		t.Fatalf("initialized assignment = %v; pc=%d", err, vm.PC())
	}
	if got, err := vm.memory.ReadWord(0, 0); err != nil || got != 7 {
		t.Fatalf("initialized destination = %d, %v", got, err)
	}
}

func TestCandidateBranchOnUnequalImmediateAndDecrement(t *testing.T) {
	for _, tc := range []struct {
		input uint16
		want  uint32
	}{{3, 8}, {4, 9}} {
		code := make([]byte, 26)
		putWord(code, 0, 0x05)
		putWord(code, 2, tc.input)
		putWord(code, 4, 0x80)
		putWord(code, 6, 4)
		putWord(code, 8, 0)
		putWord(code, 10, 20+0x80)
		putWord(code, 12, 0x05)
		putWord(code, 14, 10)
		putWord(code, 16, 0x0f)
		putWord(code, 18, 0x86)
		putWord(code, 20, 0x05)
		putWord(code, 22, 8)
		putWord(code, 24, 0x86)
		vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(5); err != nil {
			t.Fatal(err)
		}
		if !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != tc.want {
			t.Fatalf("input=%d halt=%v stack=%#v; want %#x", tc.input, vm.Halted(), vm.stack, tc.want)
		}
	}
}

func TestCandidateSignedGreaterOrEqualImmediateBranch(t *testing.T) {
	for _, tc := range []struct {
		input uint16
		want  uint32
	}{
		{0xffff, 9}, {15, 9}, {16, 8}, {17, 8},
	} {
		code := make([]byte, 28)
		for offset, word := range map[int]uint16{
			0: 0x05, 2: tc.input,
			4: 0x7d, 6: 16, 8: 0, 10: 0x80 + 22,
			12: 0x05, 14: 9,
			16: 0x86,
			22: 0x05, 24: 8, 26: 0x86,
		} {
			putWord(code, offset, word)
		}
		vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(4); err != nil {
			t.Fatal(err)
		}
		if !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != tc.want {
			t.Fatalf("input=%#x halt=%v stack=%#v; want %#x", tc.input, vm.Halted(), vm.stack, tc.want)
		}
	}
}

func TestCandidateArithmeticUsesFullThirtyTwoBitWords(t *testing.T) {
	for _, tc := range []struct {
		opcode     uint16
		a, b, want uint32
	}{
		{0x13, 0xffffffff, 2, 1},
		{0x14, 0, 1, 0xffffffff},
		{0x50, 0x10000, 2, 0x20000},
		{0x50, 0x80000000, 2, 0},                   // multiplication wraps to 32 bits
		{0x51, 0xfffffff9, 2, 0xfffffffd},          // -7 / 2 = -3
		{0x51, 0x80000000, 0xffffffff, 0x80000000}, // MinInt32 / -1 wraps
		{0x52, 0xfffffff9, 2, 0xffffffff},          // -7 % 2 = -1
		{0x57, 0x80000000, 31, 1},                  // logical right shift
		{0x57, 0xffffffff, 32, 0},                  // shifts use Go/VM 32-bit word semantics
		{0x58, 0x12, 8, 0x1200},                    // shiftl
		{0x58, 1, 32, 0},
		{0x54, 0x9800, 0xb4, 0x98b4}, // or
	} {
		code := make([]byte, 4)
		putWord(code, 0, tc.opcode)
		putWord(code, 2, 0x86)
		vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
		if err != nil {
			t.Fatal(err)
		}
		vm.stack = []uint32{tc.a, tc.b}
		if err := vm.Run(2); err != nil {
			t.Fatal(err)
		}
		if !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != tc.want {
			t.Fatalf("opcode=%#x halt=%v stack=%#v; want %#x", tc.opcode, vm.Halted(), vm.stack, tc.want)
		}
	}
}

func TestDivisionByZeroLeavesOperandsAndPCUnchanged(t *testing.T) {
	for _, opcode := range []uint16{0x51, 0x52} {
		t.Run(fmt.Sprintf("%04x", opcode), func(t *testing.T) {
			code := make([]byte, 2)
			putWord(code, 0, opcode)
			vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
			if err != nil {
				t.Fatal(err)
			}
			vm.stack = []uint32{123, 0}
			if err := vm.Step(); !errors.Is(err, ErrDivisionByZero) {
				t.Fatalf("division by zero error = %v", err)
			}
			if vm.PC() != 0 || !slices.Equal(vm.stack, []uint32{123, 0}) {
				t.Fatalf("division by zero changed state: pc=%d stack=%v", vm.PC(), vm.stack)
			}
		})
	}
}

func TestCandidateSwapKeepsBothStackWords(t *testing.T) {
	code := make([]byte, 4)
	putWord(code, 0, 0x12)
	putWord(code, 2, 0x86)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	vm.stack = []uint32{0x12345678, 0x9abcdef0}
	if err := vm.Run(2); err != nil {
		t.Fatal(err)
	}
	if !vm.Halted() || vm.StackDepth() != 2 || vm.stack[0] != 0x9abcdef0 || vm.stack[1] != 0x12345678 {
		t.Fatalf("swap state: halt=%v stack=%#v", vm.Halted(), vm.stack)
	}
}

func TestCandidatePushWordUsesHighThenLowOperands(t *testing.T) {
	code := make([]byte, 8)
	putWord(code, 0, 0x07)
	putWord(code, 2, 0xabcd)
	putWord(code, 4, 0x1234)
	putWord(code, 6, 0x86)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); err != nil || !vm.Halted() || vm.StackTop() != 0xabcd1234 {
		t.Fatalf("pushw: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidatePushMediaImmediateUsesUnsignedIndex(t *testing.T) {
	code := make([]byte, 6)
	putWord(code, 0, 0x06)
	putWord(code, 2, 0xff00)
	putWord(code, 4, 0x86)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); err != nil || !vm.Halted() || vm.StackTop() != 0xff00 {
		t.Fatalf("pushmi: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateDirectSymbolIncrementUsesSignedImmediate(t *testing.T) {
	code := make([]byte, 16)
	for offset, word := range map[int]uint16{
		0: 0x75, 2: 0, 4: 1,
		6: 0x7a, 8: 0, 10: 0xfffe,
		12: 0x04, 14: 0,
	} {
		putWord(code, offset, word)
	}
	code = append(code, 0x86, 0)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || !vm.Halted() || vm.StackTop() != 0xffffffff {
		t.Fatalf("incz: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateUnconditionalJumpPreservesStack(t *testing.T) {
	code := make([]byte, 16)
	for offset, word := range map[int]uint16{
		0: 0x05, 2: 7,
		4: 0x81, 6: 0, 8: 0x90, // address relative to SGS prefix: code offset 16
		10: 0x05, 12: 9,
		14: 0x86,
	} {
		putWord(code, offset, word)
	}
	// Jump to a return just beyond the skipped instructions.
	code = append(code, 0x86, 0)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil || !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != 7 {
		t.Fatalf("ujp: err=%v pc=%d stack=%#v", err, vm.PC(), vm.stack)
	}
}

func TestCandidateLessThanImmediateJump(t *testing.T) {
	for _, tc := range []struct {
		opcode uint16
		value  uint16
		want   uint32
	}{{0x7c, 0xffff, 9}, {0x7c, 1, 7}, {0x7e, 0, 9}, {0x7e, 1, 7}, {0x7b, 1, 9}, {0x7b, 0, 7}} {
		code := make([]byte, 24)
		for offset, word := range map[int]uint16{
			0: 0x05, 2: tc.value,
			4: tc.opcode, 6: 0, 8: 0, 10: 0x92, // compare with zero, target code offset 18
			12: 0x05, 14: 7, 16: 0x86,
			18: 0x05, 20: 9, 22: 0x86,
		} {
			putWord(code, offset, word)
		}
		vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(4); err != nil || !vm.Halted() || vm.StackTop() != tc.want {
			t.Fatalf("opcode=%#x input=%d: err=%v pc=%d stack=%#v", tc.opcode, int16(tc.value), err, vm.PC(), vm.stack)
		}
	}
}

func TestCandidateEqualImmediateJump(t *testing.T) {
	for _, tc := range []struct {
		value uint16
		want  uint32
	}{{0, 9}, {1, 7}} {
		code := make([]byte, 24)
		for offset, word := range map[int]uint16{
			0: 0x05, 2: tc.value,
			4: 0x7f, 6: 0, 8: 0, 10: 0x92,
			12: 0x05, 14: 7, 16: 0x86,
			18: 0x05, 20: 9, 22: 0x86,
		} {
			putWord(code, offset, word)
		}
		vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(4); err != nil || !vm.Halted() || vm.StackTop() != tc.want {
			t.Fatalf("eqjp input=%d: err=%v stack=%#v", tc.value, err, vm.stack)
		}
	}
}

func TestCandidateDirectSymbolLValueStore(t *testing.T) {
	code := make([]byte, 12)
	for offset, word := range map[int]uint16{
		0: 0x8e, 2: 0,
		4: 0x05, 6: 42,
		8: 0x90, 10: 0x86,
	} {
		putWord(code, offset, word)
	}
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || !vm.Halted() {
		t.Fatalf("ldrz: err=%v pc=%d", err, vm.PC())
	}
	if got, err := vm.memory.ReadWord(0, 0); err != nil || got != 42 {
		t.Fatalf("stored symbol = %d, %v", got, err)
	}
}

func TestCandidateIndexedSetConstantReadsIndexSymbol(t *testing.T) {
	code := make([]byte, 16)
	for offset, word := range map[int]uint16{
		0: 0x69, 2: 0, 4: 1, 6: 42,
		8: 0x03, 10: 0, 12: 1,
		14: 0x86,
	} {
		putWord(code, offset, word)
	}
	indexData := []byte{1, 0, 0, 0}
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{
			{Flags: 1, Words: 2, Data: make([]byte, 8)},
			{Flags: 0x100, Words: 1, Data: indexData},
		}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil || !vm.Halted() || vm.StackTop() != 42 {
		t.Fatalf("isetc: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCompositeIndexedAssignmentOpcodes(t *testing.T) {
	tests := []struct {
		name        string
		words       []uint16
		destination int
		element     int
		want        uint32
	}{
		{
			name: "xseti", words: []uint16{0x61, 0, 1, 1, 2, 3},
			destination: 0, element: 2, want: 20,
		},
		{
			name: "xsetz", words: []uint16{0x63, 0, 1, 1, 4},
			destination: 0, element: 2, want: 77,
		},
		{
			name: "isetn", words: []uint16{0x67, 0, 3, 2, 2},
			destination: 0, element: 1, want: 30,
		},
		{
			name: "nsetx", words: []uint16{0x6b, 0, 1, 2, 1, 0},
			destination: 0, element: 1, want: 10,
		},
		{
			name: "nseti", words: []uint16{0x6c, 0, 2, 2, 3},
			destination: 0, element: 2, want: 20,
		},
		{
			name: "zsetx", words: []uint16{0x71, 4, 2, 1, 1},
			destination: 4, element: 0, want: 30,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code := make([]byte, 2*(len(test.words)+1))
			for index, word := range test.words {
				putWord(code, index*2, word)
			}
			putWord(code, len(test.words)*2, 0x86)
			vm, err := New(gnex.GNEX32Image{
				Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
				Symbols: []gnex.GNEX32Symbol{
					gnex32TestWords(0, 0, 0),
					gnex32TestWords(0, 2),
					gnex32TestWords(10, 20, 30),
					gnex32TestWords(1),
					gnex32TestWords(77),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := vm.Run(2); err != nil || !vm.Halted() {
				t.Fatalf("run: err=%v pc=%d", err, vm.PC())
			}
			got, err := vm.memory.ReadWord(test.destination, test.element)
			if err != nil || got != test.want {
				t.Fatalf("destination[%d] = %d, %v; want %d", test.element, got, err, test.want)
			}
		})
	}
}

func TestLDRIStoresThroughScalarIndexedLValue(t *testing.T) {
	code := make([]byte, 14)
	for offset, word := range map[int]uint16{
		0: 0x8a, 2: 0, 4: 1,
		6: 0x05, 8: 99,
		10: 0x90, 12: 0x86,
	} {
		putWord(code, offset, word)
	}
	vm, err := New(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{
			gnex32TestWords(0, 0, 0),
			gnex32TestWords(2),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || !vm.Halted() || vm.StackDepth() != 0 {
		t.Fatalf("run: err=%v pc=%d stack=%#v", err, vm.PC(), vm.stack)
	}
	if got, err := vm.memory.ReadWord(0, 2); err != nil || got != 99 {
		t.Fatalf("indexed lvalue = %d, %v", got, err)
	}
}

func gnex32TestWords(values ...uint32) gnex.GNEX32Symbol {
	data := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	return gnex.GNEX32Symbol{Flags: 1, Words: uint16(len(values)), Data: data}
}

func TestCandidatePopIndexedConstantStoresStackValue(t *testing.T) {
	code := make([]byte, 14)
	for offset, word := range map[int]uint16{
		0: 0x0a, 2: 0, 4: 1,
		6: 0x03, 8: 0, 10: 1,
		12: 0x86,
	} {
		putWord(code, offset, word)
	}
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 2, Data: make([]byte, 8)}}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	vm.stack = []uint32{42}
	if err := vm.Run(3); err != nil || !vm.Halted() || vm.StackTop() != 42 {
		t.Fatalf("popic: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateFixedIndexedSetConstant(t *testing.T) {
	code := make([]byte, 16)
	for offset, word := range map[int]uint16{
		0: 0x6f, 2: 0, 4: 1, 6: 0xfffe,
		8: 0x03, 10: 0, 12: 1,
		14: 0x86,
	} {
		putWord(code, offset, word)
	}
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 2, Data: make([]byte, 8)}}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil || !vm.Halted() || vm.StackTop() != 0xfffffffe {
		t.Fatalf("nsetc: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateEndHaltsMain(t *testing.T) {
	code := []byte{0xff, 0}
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); err != nil || !vm.Halted() || vm.StackDepth() != 0 {
		t.Fatalf("end: err=%v pc=%d stack=%#v", err, vm.PC(), vm.stack)
	}
}

func TestCandidateSavedStackRestoresOnlyValuesAboveMark(t *testing.T) {
	code := make([]byte, 14)
	for offset, word := range map[int]uint16{
		0: 0x05, 2: 9,
		4: 0x0c, // ssp
		6: 0x05, 8: 2,
		10: 0x0d, // rsp
		12: 0x86,
	} {
		putWord(code, offset, word)
	}
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); err != nil || !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != 9 {
		t.Fatalf("ssp/rsp: err=%v pc=%d stack=%#v", err, vm.PC(), vm.stack)
	}
}

func TestCandidateEventDispatchPreservesMemoryAndSetsEventData(t *testing.T) {
	code := []byte{0xff, 0, 0x04, 0, 0, 0, 0x86, 0}
	symbols := make([]gnex.GNEX32Symbol, 4)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: symbols}
	image.Callbacks.Timeout = 162
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); err != nil || !vm.Halted() {
		t.Fatalf("main: err=%v halted=%v", err, vm.Halted())
	}
	vm.stack = []uint32{99}
	vm.calls = []int{0}
	vm.marks = []int{0}
	vm.refs[0] = gnex32SymbolRef{symbol: 0}
	data := EventData{Data: 7, Data1: 8, Data2: 9, Data3: 10}
	if err := vm.BeginEventData(gnex.GNEX32EventTimeout, data); err != nil {
		t.Fatal(err)
	}
	if len(vm.stack) != 0 || len(vm.calls) != 0 || len(vm.marks) != 0 || len(vm.refs) != 0 {
		t.Fatalf("event entry retained execution state: stack=%v calls=%v marks=%v refs=%v",
			vm.stack, vm.calls, vm.marks, vm.refs)
	}
	for symbol, want := range data.values() {
		if got, err := vm.memory.ReadWord(symbol, 0); err != nil || got != want {
			t.Fatalf("event symbol %d = %d, %v; want %d", symbol, got, err, want)
		}
	}
	if err := vm.Run(2); err != nil || !vm.Halted() || vm.StackTop() != 7 {
		t.Fatalf("event: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateEventDispatchRejectsMissingSystemSymbolAtomically(t *testing.T) {
	code := []byte{0xff, 0, 0x86, 0}
	symbols := make([]gnex.GNEX32Symbol, 3)
	for index := range symbols {
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, uint32(100+index))
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: data}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Callbacks: gnex.GNEX32Callbacks{Result: 162}, Symbols: symbols,
	}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); err != nil {
		t.Fatal(err)
	}
	beforePC := vm.PC()
	if err := vm.BeginEventData(gnex.GNEX32EventResult, EventData{Data: 1, Data1: 2, Data2: 3, Data3: 4}); err == nil {
		t.Fatal("event with a missing swData3 symbol succeeded")
	}
	if !vm.Halted() || vm.PC() != beforePC {
		t.Fatalf("failed event changed control state: halted=%v pc=%d", vm.Halted(), vm.PC())
	}
	for symbol, want := range []uint32{100, 101, 102} {
		if got, err := vm.memory.ReadWord(symbol, 0); err != nil || got != want {
			t.Fatalf("failed event changed symbol %d to %d, %v", symbol, got, err)
		}
	}
}

func TestCandidateCopyImageCallsRendererAndConsumesThreeValues(t *testing.T) {
	code := []byte{0x94, 0, 0x86, 0}
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	vm.stack = []uint32{3, 4, 9}
	var called bool
	vm.copyImage = func(x, y int32, index int) error {
		called = true
		if x != 3 || y != 4 || index != 9 {
			t.Errorf("CopyImage(%d,%d,%d)", x, y, index)
		}
		return nil
	}
	if err := vm.Run(2); err != nil || !vm.Halted() || !called || vm.StackDepth() != 0 {
		t.Fatalf("CopyImage: err=%v called=%v stack=%#v", err, called, vm.stack)
	}
}

func TestCandidateSignedLessThan(t *testing.T) {
	code := make([]byte, 10)
	putWord(code, 0, 0x05)
	putWord(code, 2, 0xffff)
	putWord(code, 4, 0x05)
	putWord(code, 6, 1)
	putWord(code, 8, 0x5a)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != ErrBudget || vm.StackDepth() != 1 || vm.StackTop() != 1 {
		t.Fatalf("signed less-than: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateNotEqualComparison(t *testing.T) {
	code := make([]byte, 12)
	for offset, word := range map[int]uint16{
		0: 0x05, 2: 0,
		4: 0x05, 6: 1,
		8: 0x5e, 10: 0x86,
	} {
		putWord(code, offset, word)
	}
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(4); err != nil || !vm.Halted() || vm.StackTop() != 1 {
		t.Fatalf("ne: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateSignedGreaterThan(t *testing.T) {
	code := []byte{0x59, 0, 0x86, 0}
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	vm.stack = []uint32{2, 1}
	if err := vm.Run(2); err != nil || !vm.Halted() || vm.StackTop() != 1 {
		t.Fatalf("signed greater-than: err=%v stack=%#v", err, vm.stack)
	}
}

func TestCandidateIndexedLValueBitOperationsAndStore(t *testing.T) {
	code := make([]byte, 34)
	for offset, word := range map[int]uint16{
		0: 0x8d, 2: 0, 4: 1, // lvalue: symbol 0, element 1
		6: 0x05, 8: 0x90ff,
		10: 0x05, 12: 8,
		14: 0x57, // logical right shift -> 0x90
		16: 0x05, 18: 0xff,
		20: 0x53, // bitwise and
		22: 0x05, 24: 0x3f,
		26: 0x56, // bitwise xor -> 0xaf
		28: 0x90, // store through the lvalue
		30: 0x03, 32: 0,
	} {
		putWord(code, offset, word)
	}
	// The final indexed read needs its element operand and a return.
	code = append(code, 1, 0, 0x86, 0)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 2, Data: make([]byte, 8)}}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(11); err != nil {
		t.Fatal(err)
	}
	if !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != 0xaf {
		t.Fatalf("lvalue bit operations: halt=%v stack=%#v", vm.Halted(), vm.stack)
	}
	if got, _ := vm.memory.ReadWord(0, 0); got != 0 {
		t.Fatalf("neighbor changed: %#x", got)
	}
}

func TestCandidateIndexedLValueStoreUpdatesInitializedSymbol(t *testing.T) {
	code := make([]byte, 12)
	putWord(code, 0, 0x8d)
	putWord(code, 2, 0)
	putWord(code, 4, 0)
	putWord(code, 6, 0x05)
	putWord(code, 8, 7)
	putWord(code, 10, 0x90)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 0x100, Words: 1, Data: make([]byte, 4)}}}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); !errors.Is(err, ErrBudget) || vm.PC() != 12 || vm.StackDepth() != 0 {
		t.Fatalf("initialized lvalue store: err=%v pc=%d stack=%#v", err, vm.PC(), vm.stack)
	}
	if got, err := vm.memory.ReadWord(0, 0); err != nil || got != 7 {
		t.Fatalf("initialized symbol = %d, %v", got, err)
	}
}

func TestCandidateVMStopsAtUnknownOpcode(t *testing.T) {
	code := make([]byte, 8)
	putWord(code, 0, 0x92)
	putWord(code, 2, 1)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	var unsupported *UnsupportedServiceError
	if err := vm.Run(1); !errors.As(err, &unsupported) || unsupported.ID != 1 || unsupported.Offset != 0 {
		t.Fatalf("unknown opcode = %v", err)
	}
	if vm.Halted() || vm.PC() != 0 {
		t.Fatalf("VM advanced past unknown opcode: pc=%d halted=%v", vm.PC(), vm.Halted())
	}
}

func TestCandidateServiceBoundaryAppliesExplicitStackEffect(t *testing.T) {
	code := make([]byte, 12)
	putWord(code, 0, 0x05)
	putWord(code, 2, 4)
	putWord(code, 4, 0x92)
	putWord(code, 6, 1)
	putWord(code, 8, 0x86)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160}
	var calls int
	vm, err := NewWithServices(image, func(id uint16, stack []uint32) (ServiceResult, error) {
		calls++
		if id != 1 || len(stack) != 1 || stack[0] != 4 {
			t.Fatalf("service call id=%d stack=%#v", id, stack)
		}
		stack[0] = 99 // callback must not mutate VM storage directly
		return ServiceResult{Pop: 1, Push: []uint32{7}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != 7 {
		t.Fatalf("service result: calls=%d halt=%v stack=%#v", calls, vm.Halted(), vm.stack)
	}
}

func TestCandidateServiceBoundaryRejectsInvalidStackEffect(t *testing.T) {
	code := make([]byte, 4)
	putWord(code, 0, 0x92)
	putWord(code, 2, 1)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160}
	vm, err := NewWithServices(image, func(uint16, []uint32) (ServiceResult, error) {
		return ServiceResult{Pop: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, ErrServiceStackEffect) || vm.PC() != 0 {
		t.Fatalf("invalid effect = %v; pc=%d", err, vm.PC())
	}
}

func TestExecutionTraceRecordsOpcodeSitesAndServiceStack(t *testing.T) {
	code := make([]byte, 14)
	for offset, word := range map[int]uint16{
		0: 0x8e, 2: 0, // direct lvalue for symbol 0
		4: 0x05, 6: 7,
		8: 0x92, 10: 0x25,
		12: 0x86,
	} {
		putWord(code, offset, word)
	}
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}}}
	vm, err := NewWithServices(image, func(id uint16, stack []uint32) (ServiceResult, error) {
		if id != 0x25 || len(stack) != 2 {
			t.Fatalf("service %#x stack = %#v", id, stack)
		}
		return ServiceResult{Pop: 2}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	vm.EnableExecutionTrace()
	if err := vm.Run(4); err != nil {
		t.Fatal(err)
	}
	trace := vm.ExecutionTrace()
	if trace.InstructionCount != 4 || len(trace.Opcodes) != 4 {
		t.Fatalf("instruction trace = %+v", trace)
	}
	wantOpcodes := []uint16{0x05, 0x86, 0x8e, 0x92}
	for index, want := range wantOpcodes {
		if trace.Opcodes[index].Opcode != want || trace.Opcodes[index].Count != 1 {
			t.Fatalf("opcode[%d] = %+v, want %#x once", index, trace.Opcodes[index], want)
		}
	}
	if len(trace.Services) != 1 || trace.Services[0].ID != 0x25 ||
		trace.Services[0].Name != "SetGamma" || trace.Services[0].Count != 1 ||
		len(trace.Services[0].Calls) != 1 {
		t.Fatalf("service trace = %+v", trace.Services)
	}
	call := trace.Services[0].Calls[0]
	if call.Offset != 8 || call.StackDepth != 2 || call.StackBase != 0 || len(call.Stack) != 2 {
		t.Fatalf("call = %+v", call)
	}
	if !call.Stack[0].Reference || call.Stack[0].Symbol != 0 || call.Stack[0].Element != 0 ||
		call.Stack[1].Reference || call.Stack[1].Value != 7 {
		t.Fatalf("captured stack = %+v", call.Stack)
	}

	trace.Services[0].Calls[0].Stack[1].Value = 99
	if got := vm.ExecutionTrace().Services[0].Calls[0].Stack[1].Value; got != 7 {
		t.Fatalf("trace snapshot aliases collector: %d", got)
	}
	vm.ResetExecutionTrace()
	if reset := vm.ExecutionTrace(); reset.InstructionCount != 0 || len(reset.Services) != 0 {
		t.Fatalf("reset trace = %+v", reset)
	}
}

func TestExecutionTraceBoundsServiceSamplesAndKeepsCounts(t *testing.T) {
	state := newExecutionTraceState()
	for index := 0; index < traceCallsPerService+3; index++ {
		state.recordService(index*4, 0x25, []uint32{uint32(index)}, nil)
	}
	trace := state.snapshot()
	if len(trace.Services) != 1 || trace.Services[0].Count != traceCallsPerService+3 ||
		len(trace.Services[0].Calls) != traceCallsPerService || trace.Services[0].DroppedSamples != 3 {
		t.Fatalf("bounded service trace = %+v", trace.Services)
	}
}

func TestCandidateVMRejectsCallStackOverflow(t *testing.T) {
	code := make([]byte, 6)
	putWord(code, 0, 0x85)
	putWord(code, 2, 0)
	putWord(code, 4, 0x80)
	image := gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1025); !errors.Is(err, ErrCallStackOverflow) {
		t.Fatalf("recursive call = %v", err)
	}
}

func TestEveryMultiwordOpcodeRejectsTruncationAtomically(t *testing.T) {
	for value := 0; value <= 0xffff; value++ {
		opcode := uint16(value)
		size, known := gnex32InstructionSize(opcode)
		if !known || size == 2 {
			continue
		}
		t.Run(fmt.Sprintf("%04x", opcode), func(t *testing.T) {
			code := make([]byte, size-2)
			putWord(code, 0, opcode)
			vm, err := New(gnex.GNEX32Image{
				Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
				Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			vm.stack = []uint32{11, 22}
			vm.calls = []int{2}
			vm.marks = []int{1}
			vm.refs[0] = gnex32SymbolRef{symbol: 0}
			if err := vm.Step(); !errors.Is(err, ErrTruncated) {
				t.Fatalf("truncated opcode %#x error = %v", opcode, err)
			}
			if vm.PC() != 0 || !slices.Equal(vm.stack, []uint32{11, 22}) ||
				!slices.Equal(vm.calls, []int{2}) || !slices.Equal(vm.marks, []int{1}) ||
				vm.refs[0] != (gnex32SymbolRef{symbol: 0}) {
				t.Fatalf("truncated opcode %#x changed VM state: pc=%d stack=%v calls=%v marks=%v refs=%v",
					opcode, vm.PC(), vm.stack, vm.calls, vm.marks, vm.refs)
			}
		})
	}
}

func TestStackConsumingOpcodesRejectUnderflowAtomically(t *testing.T) {
	minimumDepth := map[uint16]int{
		0x09: 1, 0x0a: 1, 0x0b: 1, 0x0e: 1, 0x0f: 1, 0x10: 1, 0x11: 1,
		0x12: 2, 0x13: 2, 0x14: 2,
		0x50: 2, 0x51: 2, 0x52: 2, 0x53: 2, 0x54: 2, 0x55: 1,
		0x56: 2, 0x57: 2, 0x58: 2, 0x59: 2, 0x5a: 2, 0x5b: 2, 0x5c: 2,
		0x5d: 2, 0x5e: 2, 0x7b: 1, 0x7c: 1, 0x7d: 1, 0x7e: 1,
		0x7f: 1, 0x80: 1, 0x82: 1, 0x83: 1, 0x8b: 1, 0x8c: 1,
		0x8f: 1, 0x90: 2, 0x91: 2, 0x94: 3, 0x95: 4, 0x98: 7,
	}
	for opcode, depth := range minimumDepth {
		t.Run(fmt.Sprintf("%04x", opcode), func(t *testing.T) {
			size, known := gnex32InstructionSize(opcode)
			if !known {
				t.Fatalf("opcode %#x has no size", opcode)
			}
			code := make([]byte, size)
			putWord(code, 0, opcode)
			if opcode >= 0x7b && opcode <= 0x80 {
				putWord(code, size-2, 0x80)
			} else if opcode == 0x82 || opcode == 0x83 {
				putWord(code, size-2, 0x80)
			}
			vm, err := New(gnex.GNEX32Image{
				Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
				Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			vm.stack = make([]uint32, depth-1)
			before := slices.Clone(vm.stack)
			vm.copyImage = func(int32, int32, int) error { return nil }
			vm.copyImageDir = func(int32, int32, int, uint32) error { return nil }
			vm.copyImageEx = func(int32, int32, int, uint32, uint32, uint32, uint32) error { return nil }
			if err := vm.Step(); !errors.Is(err, ErrStackUnderflow) {
				t.Fatalf("opcode %#x underflow = %v", opcode, err)
			}
			if vm.PC() != 0 || !slices.Equal(vm.stack, before) {
				t.Fatalf("opcode %#x underflow changed state: pc=%d stack=%v", opcode, vm.PC(), vm.stack)
			}
		})
	}
}

func TestStackProducingOpcodesRejectOverflowAtomically(t *testing.T) {
	for _, opcode := range []uint16{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x10, 0x8a, 0x8d, 0x8e} {
		t.Run(fmt.Sprintf("%04x", opcode), func(t *testing.T) {
			size, _ := gnex32InstructionSize(opcode)
			code := make([]byte, size)
			putWord(code, 0, opcode)
			vm, err := New(gnex.GNEX32Image{
				Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
				Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			vm.stack = make([]uint32, 4096)
			vm.stack[4095] = 77
			if err := vm.Step(); !errors.Is(err, ErrStackOverflow) {
				t.Fatalf("opcode %#x overflow = %v", opcode, err)
			}
			if vm.PC() != 0 || len(vm.stack) != 4096 || vm.stack[4095] != 77 {
				t.Fatalf("opcode %#x overflow changed state: pc=%d depth=%d top=%d",
					opcode, vm.PC(), len(vm.stack), vm.stack[4095])
			}
		})
	}
}

func TestCandidateIndexedSymbolReadAndSignedImmediate(t *testing.T) {
	code := make([]byte, 12)
	putWord(code, 0, 0x03)
	putWord(code, 2, 0)
	putWord(code, 4, 1)
	putWord(code, 6, 0x05)
	putWord(code, 8, 0xffff)
	putWord(code, 10, 0x86)
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 0x100, Words: 2,
			Data: []byte{0x44, 0x33, 0x22, 0x11, 0x88, 0x77, 0x66, 0x55}}},
	}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil {
		t.Fatal(err)
	}
	if !vm.Halted() || vm.StackDepth() != 2 || vm.stack[0] != 0x55667788 || vm.stack[1] != 0xffffffff {
		t.Fatalf("indexed/immediate state: halt=%v stack=%#v", vm.Halted(), vm.stack)
	}
}

func TestCandidateIndexedSymbolReadRejectsBadElement(t *testing.T) {
	code := make([]byte, 6)
	putWord(code, 0, 0x03)
	putWord(code, 2, 0)
	putWord(code, 4, 1)
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 0x100, Words: 1, Data: make([]byte, 4)}},
	}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); err == nil || vm.PC() != 0 || vm.StackDepth() != 0 {
		t.Fatalf("out-of-range indexed read = %v; pc=%d depth=%d", err, vm.PC(), vm.StackDepth())
	}
}

func TestCandidateEqualityConsumesTwoValues(t *testing.T) {
	for _, tc := range []struct {
		left, right uint16
		want        uint32
	}{{1, 1, 1}, {0, 1, 0}} {
		code := make([]byte, 14)
		putWord(code, 0, 0x05)
		putWord(code, 2, tc.left)
		putWord(code, 4, 0x05)
		putWord(code, 6, tc.right)
		putWord(code, 8, 0x5d)
		putWord(code, 10, 0x86)
		vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(4); err != nil {
			t.Fatal(err)
		}
		if !vm.Halted() || vm.StackDepth() != 1 || vm.StackTop() != tc.want {
			t.Fatalf("equal(%d, %d): halt=%v stack=%#v", tc.left, tc.right, vm.Halted(), vm.stack)
		}
	}
}

func TestCandidateEqualityRejectsUnderflow(t *testing.T) {
	code := make([]byte, 2)
	putWord(code, 0, 0x5d)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, ErrStackUnderflow) || vm.PC() != 0 {
		t.Fatalf("equality underflow = %v; pc=%d", err, vm.PC())
	}
}

func TestCandidateDuplicateAndZeroBranch(t *testing.T) {
	for _, tc := range []struct {
		value uint16
		want  uint32
	}{
		{0, 0},
		{7, 7},
	} {
		code := make([]byte, 18)
		putWord(code, 0, 0x05)
		putWord(code, 2, tc.value)
		putWord(code, 4, 0x10) // duplicate condition for later use
		putWord(code, 6, 0x83) // candidate branch when top is zero
		putWord(code, 8, 0)
		putWord(code, 10, 0x90) // code offset 16, relative address 0x80+16
		putWord(code, 12, 0x86)
		putWord(code, 16, 0x86)
		vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(3); !errors.Is(err, ErrBudget) {
			t.Fatalf("branch(%d): %v; pc=%d", tc.value, err, vm.PC())
		}
		wantPC := 12
		if tc.value == 0 {
			wantPC = 16
		}
		if vm.PC() != wantPC || vm.StackDepth() != 1 || vm.StackTop() != tc.want {
			t.Fatalf("branch(%d): pc=%d stack=%#v", tc.value, vm.PC(), vm.stack)
		}
	}
}

func TestCandidateDuplicateRejectsUnderflow(t *testing.T) {
	code := make([]byte, 2)
	putWord(code, 0, 0x10)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Step(); !errors.Is(err, ErrStackUnderflow) || vm.PC() != 0 {
		t.Fatalf("duplicate underflow = %v; pc=%d", err, vm.PC())
	}
}

func TestCandidateStoreTopIntoSymbol(t *testing.T) {
	code := make([]byte, 10)
	putWord(code, 0, 0x05)
	putWord(code, 2, 42)
	putWord(code, 4, 0x0b)
	putWord(code, 6, 0)
	putWord(code, 8, 0x86)
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}},
	}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil {
		t.Fatal(err)
	}
	got, err := vm.memory.ReadWord(0, 0)
	if err != nil || got != 42 || vm.StackDepth() != 0 || image.Symbols[0].Data[0] != 0 {
		t.Fatalf("stored=%d error=%v stack=%#v source=%#v", got, err, vm.stack, image.Symbols[0].Data)
	}
}

func TestCandidateStoreUpdatesInitializedSymbolAndPops(t *testing.T) {
	code := make([]byte, 4)
	putWord(code, 0, 0x0b)
	putWord(code, 2, 0)
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160,
		Symbols: []gnex.GNEX32Symbol{{Flags: 0x100, Words: 1, Data: make([]byte, 4)}},
	}
	vm, err := New(image)
	if err != nil {
		t.Fatal(err)
	}
	vm.stack = []uint32{42}
	if err := vm.Step(); err != nil || vm.PC() != 4 || vm.StackDepth() != 0 {
		t.Fatalf("initialized store = %v; pc=%d stack=%#v", err, vm.PC(), vm.stack)
	}
	if got, err := vm.memory.ReadWord(0, 0); err != nil || got != 42 {
		t.Fatalf("initialized symbol = %d, %v", got, err)
	}
}

func TestCopyImageExOpcodeForwardsEveryTransformArgument(t *testing.T) {
	values := []uint16{10, 20, 2, 5, 1, 1, 3}
	code := make([]byte, len(values)*4+4)
	for index, value := range values {
		putWord(code, index*4, 0x05)
		putWord(code, index*4+2, value)
	}
	putWord(code, len(values)*4, 0x98)
	putWord(code, len(values)*4+2, 0x86)
	vm, err := New(gnex.GNEX32Image{Header: gnex.Header{BodyOffset: 160}, Code: code, Entry: 160})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	vm.copyImageEx = func(x, y int32, index int, alpha, zoom, mirror, rotation uint32) error {
		called = true
		if x != 10 || y != 20 || index != 2 || alpha != 5 || zoom != 1 || mirror != 1 || rotation != 3 {
			t.Fatalf("CopyImageEx args = %d,%d,%d,%d,%d,%d,%d", x, y, index, alpha, zoom, mirror, rotation)
		}
		return nil
	}
	if err := vm.Run(uint64(len(values) + 2)); err != nil {
		t.Fatal(err)
	}
	if !called || !vm.Halted() || vm.StackDepth() != 0 {
		t.Fatalf("CopyImageEx called=%v halted=%v stack=%#v", called, vm.Halted(), vm.stack)
	}
}
