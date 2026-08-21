package system

import (
	"errors"
	"testing"
)

func TestSparseWordRegistersAllowsOnlyConfiguredWords(t *testing.T) {
	device, err := NewSparseWordRegisters([]uint32{0x3d0, 0x240, 0x280})
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x280, Width32, 2); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0x280, Width32)
	if err != nil || value != 2 {
		t.Fatalf("latched word = %#x error %v", value, err)
	}
	if _, err := device.Read(0x284, Width32); !errors.Is(err, ErrSparseWordRegistersMMIO) {
		t.Fatalf("unknown word read error = %v", err)
	}
	if err := device.Write(0x280, Width16, 0); !errors.Is(err, ErrSparseWordRegistersMMIO) {
		t.Fatalf("wrong-width write error = %v", err)
	}
}

func TestSparseWordRegistersValidatesLayoutAndState(t *testing.T) {
	for _, offsets := range [][]uint32{nil, {2}, {4, 4}} {
		if _, err := NewSparseWordRegisters(offsets); err == nil {
			t.Fatalf("accepted offsets %#v", offsets)
		}
	}
	device, _ := NewSparseWordRegisters([]uint32{0x240, 0x280})
	_ = device.Write(0x280, Width32, 2)
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewSparseWordRegisters([]uint32{0x280, 0x240})
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	value, _ := restored.Read(0x280, Width32)
	if value != 2 {
		t.Fatalf("restored word = %#x", value)
	}
	mismatch, _ := NewSparseWordRegisters([]uint32{0x240, 0x284})
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched layout state error = %v", err)
	}
	if err := restored.Reset(); err != nil {
		t.Fatal(err)
	}
	value, _ = restored.Read(0x280, Width32)
	if value != 0 {
		t.Fatalf("reset word = %#x", value)
	}
}
