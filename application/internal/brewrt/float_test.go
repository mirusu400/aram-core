package brewrt

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestBREWFloatHelpersUseSoftFloatWords(t *testing.T) {
	tests := []struct {
		name      string
		slot      uint32
		selector  uint32
		first     float64
		second    float64
		wantFloat float64
		wantBool  bool
	}{
		{"add", helperFloatOpSlot, 0, 1.5, 2, 3.5, false},
		{"subtract", helperFloatOpSlot, 1, 1.5, 2, -0.5, false},
		{"multiply", helperFloatOpSlot, 2, 1.5, 2, 3, false},
		{"divide", helperFloatOpSlot, 3, 1.5, 2, 0.75, false},
		{"power", helperFloatOpSlot, 9, 3, 2, 9, false},
		{"less", helperFloatCmpSlot, 4, 1.5, 2, 0, true},
		{"less-equal", helperFloatCmpSlot, 5, 2, 2, 0, true},
		{"equal", helperFloatCmpSlot, 6, 2, 2, 0, true},
		{"greater", helperFloatCmpSlot, 7, 2, 1.5, 0, true},
		{"greater-equal", helperFloatCmpSlot, 8, 2, 2, 0, true},
		{"nan-compare", helperFloatCmpSlot, 6, math.NaN(), math.NaN(), 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newSyntheticRuntime(t)
			first, second := math.Float64bits(test.first), math.Float64bits(test.second)
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: uint32(first),
				cpu.RegisterR1: uint32(first >> 32),
				cpu.RegisterR2: uint32(second),
				cpu.RegisterR3: uint32(second >> 32),
				cpu.RegisterSP: stackBase,
				cpu.RegisterLR: returnTrap | 1,
			} {
				if err := runtime.cpu.WriteRegister(register, value); err != nil {
					t.Fatal(err)
				}
			}
			var selector [4]byte
			binary.LittleEndian.PutUint32(selector[:], test.selector)
			if err := runtime.cpu.WriteMemory(stackBase, selector[:]); err != nil {
				t.Fatal(err)
			}
			handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + test.slot*2 + 2)
			if err != nil || !handled {
				t.Fatalf("float helper handled=%v err=%v", handled, err)
			}
			low, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				t.Fatal(err)
			}
			if test.slot == helperFloatCmpSlot {
				if low != boolWord(test.wantBool) {
					t.Fatalf("comparison = %d, want %d", low, boolWord(test.wantBool))
				}
				return
			}
			high, err := runtime.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				t.Fatal(err)
			}
			if bits := uint64(high)<<32 | uint64(low); bits != math.Float64bits(test.wantFloat) {
				t.Fatalf("floating result bits = %016x, want %016x", bits, math.Float64bits(test.wantFloat))
			}
		})
	}
}

func TestBREWFloatToWideUsesSoftFloatValueAndByteCapacity(t *testing.T) {
	for _, test := range []struct {
		name       string
		size       uint32
		wantStatus uint32
		wantText   string
	}{
		{"enough space", 40, 1, "31.076421"},
		{"exact space", 20, 1, "31.076421"},
		{"too small", 18, 0, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newSyntheticRuntime(t)
			value := math.Float64bits(31.076421335401918)
			destination := heapBase + 0x100
			for register, word := range map[uint32]uint32{
				cpu.RegisterR0: uint32(value),
				cpu.RegisterR1: uint32(value >> 32),
				cpu.RegisterR2: destination,
				cpu.RegisterR3: test.size,
				cpu.RegisterLR: returnTrap | 1,
			} {
				if err := runtime.cpu.WriteRegister(register, word); err != nil {
					t.Fatal(err)
				}
			}
			handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperFloatToWStrSlot*2 + 2)
			if err != nil || !handled {
				t.Fatalf("FLOATTOWSTR handled=%v err=%v", handled, err)
			}
			status, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				t.Fatal(err)
			}
			if status != test.wantStatus {
				t.Fatalf("status = %d, want %d", status, test.wantStatus)
			}
			var encoded [20]byte
			if err := runtime.cpu.ReadMemory(destination, encoded[:]); err != nil {
				t.Fatal(err)
			}
			for index := 0; index < len(test.wantText); index++ {
				if got := binary.LittleEndian.Uint16(encoded[index*2:]); got != uint16(test.wantText[index]) {
					t.Fatalf("unit %d = %q, want %q", index, got, test.wantText[index])
				}
			}
			if got := binary.LittleEndian.Uint16(encoded[len(test.wantText)*2:]); got != 0 {
				t.Fatalf("terminator = %d, want 0", got)
			}
		})
	}
}

func TestBREWFloatHelperRejectsUnknownSelector(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stackBase); err != nil {
		t.Fatal(err)
	}
	var selector [4]byte
	binary.LittleEndian.PutUint32(selector[:], 99)
	if err := runtime.cpu.WriteMemory(stackBase, selector[:]); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperFloatOpSlot*2 + 2)
	if err == nil || !strings.Contains(err.Error(), "unsupported f_op selector 99") {
		t.Fatalf("unknown float selector error = %v", err)
	}
}
