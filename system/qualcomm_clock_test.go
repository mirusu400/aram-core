package system

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestQualcommPrimaryClockExposesControllableDigitalInputs(t *testing.T) {
	device, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0xf})
	if err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(qualcommPrimaryGPIOInputOffset, Width32)
	if err != nil || value != 0xf {
		t.Fatalf("primary clock status = %#x error %v", value, err)
	}
	if err := device.SetInputLine(1, false); err != nil {
		t.Fatal(err)
	}
	if got := device.InputStatus(); got != 0xd {
		t.Fatalf("primary input status after low line = %#x", got)
	}
	if err := device.SetInputLine(1, true); err != nil {
		t.Fatal(err)
	}
	if err := device.SetInputStatus(5); err != nil {
		t.Fatal(err)
	}
	if err := device.SetInputLine(4, false); !errors.Is(err, ErrQualcommPrimaryClockMMIO) {
		t.Fatalf("out-of-range input line error = %v", err)
	}
	if err := device.SetInputStatus(0x10); !errors.Is(err, ErrQualcommPrimaryClockMMIO) {
		t.Fatalf("out-of-range input status error = %v", err)
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
	if err := device.Write(qualcommPrimaryGPIOInputOffset, Width32, 0); !errors.Is(err, ErrQualcommPrimaryClockMMIO) {
		t.Fatalf("primary clock status write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0xf})
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got := restored.InputStatus(); got != 5 {
		t.Fatalf("restored primary input status = %#x", got)
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

func TestQualcommPrimaryClockProfilesAndMigratesAdditionalInputs(t *testing.T) {
	legacy, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0xf})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.SetInputLine(0, false); err != nil {
		t.Fatal(err)
	}
	v5, err := legacy.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	v4 := make([]byte, len(v5)-4)
	copy(v4[:8], v5[:8])
	copy(v4[8:], v5[12:])
	binary.LittleEndian.PutUint32(v4[4:8], 4)

	expandedConfig := QualcommPrimaryClockConfig{Status: 0x1f, InputMask: 0x1f}
	expanded, err := NewQualcommPrimaryClockControl(expandedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := expanded.LoadState(v4); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("exact expanded-input migration error = %v", err)
	}
	if err := expanded.LoadStateSubset(v4); err != nil {
		t.Fatal(err)
	}
	if got := expanded.InputStatus(); got != 0x1e {
		t.Fatalf("migrated expanded input status = %#x, want old lines plus reset-high line 4", got)
	}
	if err := expanded.SetInputLine(4, false); err != nil {
		t.Fatal(err)
	}
	if got := expanded.InputStatus(); got != 0x0e {
		t.Fatalf("profiled fifth input after low = %#x", got)
	}
	state, err := expanded.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewQualcommPrimaryClockControl(expandedConfig)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got := restored.InputStatus(); got != 0x0e {
		t.Fatalf("restored fifth input status = %#x", got)
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
	for _, offsets := range [][]uint32{{0x05a8, 0x05a8}, {qualcommPrimaryGPIOInputOffset}, {2}, {QualcommPrimaryClockWindowSize}} {
		if _, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
			Status: 0, WritableOffsets: offsets,
		}); err == nil {
			t.Fatalf("accepted invalid primary-clock writable offsets %#v", offsets)
		}
	}
}
