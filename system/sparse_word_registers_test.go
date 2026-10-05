package system

import (
	"errors"
	"testing"
)

func TestSparseWordRegistersAllowsOnlyConfiguredWords(t *testing.T) {
	device, err := NewSparseWordRegisters([]uint32{0x3d0, 0x240, 0x280})
	check(t, err)
	check(t, device.Write(0x280, Width32, 2))
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
	check(t, err)
	restored, _ := NewSparseWordRegisters([]uint32{0x280, 0x240})
	check(t, restored.LoadState(state))
	value, _ := restored.Read(0x280, Width32)
	if value != 2 {
		t.Fatalf("restored word = %#x", value)
	}
	mismatch, _ := NewSparseWordRegisters([]uint32{0x240, 0x284})
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched layout state error = %v", err)
	}
	check(t, restored.Reset())
	value, _ = restored.Read(0x280, Width32)
	if value != 0 {
		t.Fatalf("reset word = %#x", value)
	}
}

func TestSparseWordRegistersApplyConfiguredResetValues(t *testing.T) {
	device, err := NewSparseWordRegistersWithConfig(SparseWordRegistersConfig{
		Offsets: []uint32{0, 0x40},
		Resets:  []SparseWordRegisterReset{{Offset: 0x40, Value: 0x00800000}},
	})
	check(t, err)
	if value, err := device.Read(0x40, Width32); err != nil || value != 0x00800000 {
		t.Fatalf("configured reset value = %#x, %v", value, err)
	}
	check(t, device.Write(0x40, Width32, 1))
	check(t, device.Reset())
	if value, _ := device.Read(0x40, Width32); value != 0x00800000 {
		t.Fatalf("reset value after reset = %#x", value)
	}
	if _, err := NewSparseWordRegistersWithConfig(SparseWordRegistersConfig{
		Offsets: []uint32{0}, Resets: []SparseWordRegisterReset{{Offset: 4, Value: 1}},
	}); err == nil {
		t.Fatal("unsupported reset offset accepted")
	}
}

func TestSparseWordRegistersReadClearWords(t *testing.T) {
	device, err := NewSparseWordRegistersWithConfig(SparseWordRegistersConfig{
		Offsets:          []uint32{0x380, 0x780},
		ReadClearOffsets: []uint32{0x380},
	})
	check(t, err)
	check(t, device.Write(0x380, Width32, 3))
	check(t, device.Write(0x780, Width32, 4))
	if value, err := device.Read(0x380, Width32); err != nil || value != 3 {
		t.Fatalf("first read-clear value = %#x, %v", value, err)
	}
	if value, _ := device.Read(0x380, Width32); value != 0 {
		t.Fatalf("cleared value = %#x", value)
	}
	if value, _ := device.Read(0x780, Width32); value != 4 {
		t.Fatalf("ordinary latched value = %#x", value)
	}

	check(t, device.Write(0x380, Width32, 5))
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewSparseWordRegistersWithConfig(SparseWordRegistersConfig{
		Offsets:          []uint32{0x780, 0x380},
		ReadClearOffsets: []uint32{0x380},
	})
	check(t, restored.LoadState(state))
	if value, _ := restored.Read(0x380, Width32); value != 5 {
		t.Fatalf("restored read-clear value = %#x", value)
	}

	mismatched, _ := NewSparseWordRegisters([]uint32{0x380, 0x780})
	if err := mismatched.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched read-clear layout state error = %v", err)
	}
	for _, offsets := range [][]uint32{{0x37c}, {0x380, 0x380}} {
		if _, err := NewSparseWordRegistersWithConfig(SparseWordRegistersConfig{
			Offsets: []uint32{0x380}, ReadClearOffsets: offsets,
		}); err == nil {
			t.Fatalf("accepted read-clear offsets %#v", offsets)
		}
	}
}
