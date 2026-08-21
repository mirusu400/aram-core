package system

import (
	"errors"
	"testing"
)

func TestQualcommClockRegimeLatchesAlignedWords(t *testing.T) {
	device := NewQualcommClockRegime()
	for _, access := range []struct {
		offset uint32
		value  uint32
	}{{0x4d08, 0xffffffff}, {0x5054, 0xfd3a}, {0x5814, 0x12345678}} {
		if err := device.Write(access.offset, Width32, access.value); err != nil {
			t.Fatal(err)
		}
		value, err := device.Read(access.offset, Width32)
		if err != nil || value != access.value {
			t.Fatalf("register %#x = %#x error %v", access.offset, value, err)
		}
	}
	if _, err := device.Read(2, Width32); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("unaligned read error = %v", err)
	}
	if err := device.Write(0x5000, Width16, 0); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("wrong-width write error = %v", err)
	}
	if _, err := device.Read(0x3000, Width32); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("reserved-gap read error = %v", err)
	}
	if _, err := device.Read(QualcommClockRegimeWindowSize, Width32); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("out-of-range read error = %v", err)
	}
}

func TestQualcommClockRegimeStateRoundTripAndReset(t *testing.T) {
	device := NewQualcommClockRegime()
	_ = device.Write(0x5054, Width32, 0xfd3a)
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored := NewQualcommClockRegime()
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	value, _ := restored.Read(0x5054, Width32)
	if value != 0xfd3a {
		t.Fatalf("restored clock register = %#x", value)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated state error = %v", err)
	}
	if err := restored.Reset(); err != nil {
		t.Fatal(err)
	}
	value, _ = restored.Read(0x5054, Width32)
	if value != 0 {
		t.Fatalf("reset clock register = %#x", value)
	}
}
