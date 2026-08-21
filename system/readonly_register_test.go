package system

import (
	"errors"
	"testing"
)

func TestReadOnlyRegisterRequiresExactAccess(t *testing.T) {
	register, err := NewReadOnlyRegister(Width16, 0x300)
	if err != nil {
		t.Fatal(err)
	}
	value, err := register.Read(0, Width16)
	if err != nil || value != 0x300 {
		t.Fatalf("Read = %#x, %v", value, err)
	}
	if _, err := register.Read(0, Width8); !errors.Is(err, ErrReadOnlyRegisterMMIO) {
		t.Fatalf("wrong-width read error = %v", err)
	}
	if _, err := register.Read(2, Width16); !errors.Is(err, ErrReadOnlyRegisterMMIO) {
		t.Fatalf("wrong-offset read error = %v", err)
	}
	if err := register.Write(0, Width16, 0); !errors.Is(err, ErrReadOnlyRegisterMMIO) {
		t.Fatalf("write error = %v", err)
	}
}

func TestReadOnlyRegisterValidatesValueAndState(t *testing.T) {
	if _, err := NewReadOnlyRegister(Width8, 0x100); !errors.Is(err, ErrReadOnlyRegisterMMIO) {
		t.Fatalf("overflow error = %v", err)
	}
	if _, err := NewReadOnlyRegister(Width(3), 0); !errors.Is(err, ErrReadOnlyRegisterMMIO) {
		t.Fatalf("invalid-width error = %v", err)
	}
	register, err := NewReadOnlyRegister(Width32, 0x12345678)
	if err != nil {
		t.Fatal(err)
	}
	state, err := register.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if err := register.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if err := register.LoadState(state[:15]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated state error = %v", err)
	}
	wrongWidth, err := NewReadOnlyRegister(Width16, 0x5678)
	if err != nil {
		t.Fatal(err)
	}
	if err := wrongWidth.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong-width state error = %v", err)
	}
	wrongValue, err := NewReadOnlyRegister(Width32, 0x87654321)
	if err != nil {
		t.Fatal(err)
	}
	if err := wrongValue.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong-value state error = %v", err)
	}
}
