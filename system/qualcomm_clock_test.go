package system

import (
	"errors"
	"testing"
)

func TestQualcommPrimaryClockExposesOnlyConfiguredModeStatus(t *testing.T) {
	device, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0})
	if err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(qualcommPrimaryClockModeOffset, Width32)
	if err != nil || value != 0 {
		t.Fatalf("primary clock status = %#x error %v", value, err)
	}
	if _, err := device.Read(0x570, Width32); !errors.Is(err, ErrQualcommPrimaryClockMMIO) {
		t.Fatalf("unknown primary clock read error = %v", err)
	}
	for _, offset := range qualcommPrimaryClockWritableOffsets {
		if err := device.Write(offset, Width32, offset); err != nil {
			t.Fatalf("primary clock latch %#x: %v", offset, err)
		}
		value, err = device.Read(offset, Width32)
		if err != nil || value != offset {
			t.Fatalf("primary clock latch %#x = %#x error %v", offset, value, err)
		}
	}
	if err := device.Write(qualcommPrimaryClockModeOffset, Width32, 0); !errors.Is(err, ErrQualcommPrimaryClockMMIO) {
		t.Fatalf("primary clock status write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0})
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	value, _ = restored.Read(0x580, Width32)
	if value != 0x580 {
		t.Fatalf("restored primary clock latch = %#x", value)
	}
	mismatch, _ := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 1})
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched primary clock state error = %v", err)
	}
	if _, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0x10}); err == nil {
		t.Fatal("accepted out-of-range primary clock status")
	}
}

func TestQualcommPrimaryClockProfilesAdditionalWritableOffsets(t *testing.T) {
	config := QualcommPrimaryClockConfig{Status: 0, WritableOffsets: []uint32{0x05a8}}
	device, err := NewQualcommPrimaryClockControl(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x05a8, Width32, 0x55aa); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0x05a8, Width32)
	if err != nil || value != 0x55aa {
		t.Fatalf("profiled primary-clock latch = %#x error %v", value, err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewQualcommPrimaryClockControl(config)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	value, _ = restored.Read(0x05a8, Width32)
	if value != 0x55aa {
		t.Fatalf("restored profiled primary-clock latch = %#x", value)
	}
	unprofiled, _ := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0})
	if err := unprofiled.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched primary-clock profile state error = %v", err)
	}
	for _, offsets := range [][]uint32{{0x05a8, 0x05a8}, {qualcommPrimaryClockModeOffset}, {2}, {QualcommPrimaryClockWindowSize}} {
		if _, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
			Status: 0, WritableOffsets: offsets,
		}); err == nil {
			t.Fatalf("accepted invalid primary-clock writable offsets %#v", offsets)
		}
	}
}
