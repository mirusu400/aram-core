package gvm

import (
	"errors"
	"testing"
)

func appendImmediate(code []byte, value uint16) []byte {
	return append(code, 6, byte(value>>8), byte(value))
}

func TestTextStyleOpcodesNormalizeLowBytesAndReplaceSubsets(t *testing.T) {
	var code []byte
	for _, value := range []uint16{0x01ff, 0x02ff, 0x0380, 0x04ff} {
		code = appendImmediate(code, value)
	}
	code = append(code, 0x66)
	code = appendImmediate(code, 6)
	code = append(code, 0x67)
	code = appendImmediate(code, 183)
	code = appendImmediate(code, 365)
	code = append(code, 0x68)
	code = appendImmediate(code, 5)
	code = append(code, 0x69)

	v, err := NewWithAddressSpaceAndServices(code, 0, AddressSpace{}, &ServiceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(12); !errors.Is(err, ErrBudget) {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := v.services.textStyle, (textStyleState{mode: 2, primary: 1, secondary: 109, variant: 2}); got != want {
		t.Fatalf("text style = %+v, want %+v", got, want)
	}
	if len(v.Stack()) != 0 {
		t.Fatalf("stack = %v, want empty", v.Stack())
	}
}

func TestTextStyleDefaultsMatchNativeInitializer(t *testing.T) {
	v, err := NewWithAddressSpaceAndServices([]byte{0}, 0, AddressSpace{}, &ServiceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.services.textStyle, (textStyleState{mode: 2, primary: 3}); got != want {
		t.Fatalf("default text style = %+v, want %+v", got, want)
	}
}

func TestTextStyleOpcodeFailureBoundaries(t *testing.T) {
	for _, opcode := range []byte{0x66, 0x67, 0x68, 0x69} {
		v, err := NewWithAddressSpaceAndServices([]byte{opcode}, 0, AddressSpace{}, &ServiceConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Step(); !errors.Is(err, ErrStackUnderflow) {
			t.Fatalf("opcode %02x error = %v, want stack underflow", opcode, err)
		}
	}

	v := New([]byte{0x66})
	var unsupported *UnsupportedOpcodeError
	if err := v.Step(); !errors.As(err, &unsupported) || unsupported.Opcode != 0x66 {
		t.Fatalf("legacy constructor error = %v", err)
	}
}
