package system

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestQualcommPrimaryClockExposesControllableDigitalInputs(t *testing.T) {
	device, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0xf})
	check(t, err)
	value, err := device.Read(qualcommPrimaryGPIOInputOffset, Width32)
	if err != nil || value != 0xf {
		t.Fatalf("primary clock status = %#x error %v", value, err)
	}
	check(t, device.SetInputLine(1, false))
	if got := device.InputStatus(); got != 0xd {
		t.Fatalf("primary input status after low line = %#x", got)
	}
	check(t, device.SetInputLine(1, true))
	check(t, device.SetInputStatus(5))
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
	check(t, err)
	restored, _ := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{Status: 0xf})
	check(t, restored.LoadState(state))
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
	check(t, err)
	check(t, legacy.SetInputLine(0, false))
	v5, err := legacy.SaveState()
	check(t, err)
	v4 := make([]byte, len(v5)-4)
	copy(v4[:8], v5[:8])
	copy(v4[8:], v5[12:])
	binary.LittleEndian.PutUint32(v4[4:8], 4)

	expandedConfig := QualcommPrimaryClockConfig{Status: 0x1f, InputMask: 0x1f}
	expanded, err := NewQualcommPrimaryClockControl(expandedConfig)
	check(t, err)
	if err := expanded.LoadState(v4); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("exact expanded-input migration error = %v", err)
	}
	check(t, expanded.LoadStateSubset(v4))
	if got := expanded.InputStatus(); got != 0x1e {
		t.Fatalf("migrated expanded input status = %#x, want old lines plus reset-high line 4", got)
	}
	check(t, expanded.SetInputLine(4, false))
	if got := expanded.InputStatus(); got != 0x0e {
		t.Fatalf("profiled fifth input after low = %#x", got)
	}
	state, err := expanded.SaveState()
	check(t, err)
	restored, _ := NewQualcommPrimaryClockControl(expandedConfig)
	check(t, restored.LoadState(state))
	if got := restored.InputStatus(); got != 0x0e {
		t.Fatalf("restored fifth input status = %#x", got)
	}
}

func TestQualcommPrimaryClockProfilesAdditionalWritableOffsets(t *testing.T) {
	config := QualcommPrimaryClockConfig{Status: 0, WritableOffsets: []uint32{0x05a8}}
	device, err := NewQualcommPrimaryClockControl(config)
	check(t, err)
	check(t, device.Write(0x05a8, Width32, 0x55aa))
	value, err := device.Read(0x05a8, Width32)
	if err != nil || value != 0x55aa {
		t.Fatalf("profiled primary-clock latch = %#x error %v", value, err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewQualcommPrimaryClockControl(config)
	check(t, restored.LoadState(state))
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
