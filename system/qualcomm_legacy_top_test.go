package system

import (
	"errors"
	"testing"
)

func TestQualcommLegacyTopPageExposesOnlyConfiguredIdentification(t *testing.T) {
	device := NewQualcommLegacyTopPage(QualcommLegacyTopConfig{
		Version: 0x01020304, Identification: 0x12345678,
	})
	value, err := device.Read(qualcommLegacyTopVersionOffset, Width32)
	if err != nil || value != 0x01020304 {
		t.Fatalf("legacy top version = %#x error %v", value, err)
	}
	value, err = device.Read(qualcommLegacyTopIDOffset, Width32)
	if err != nil || value != 0x12345678 {
		t.Fatalf("legacy top identification = %#x error %v", value, err)
	}
	if _, err := device.Read(0xefc, Width32); !errors.Is(err, ErrQualcommLegacyTopMMIO) {
		t.Fatalf("unknown legacy top read error = %v", err)
	}
	if err := device.Write(qualcommLegacyTopIDOffset, Width32, 0); !errors.Is(err, ErrQualcommLegacyTopMMIO) {
		t.Fatalf("legacy top write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored := NewQualcommLegacyTopPage(QualcommLegacyTopConfig{
		Version: 0x01020304, Identification: 0x12345678,
	})
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	mismatch := NewQualcommLegacyTopPage(QualcommLegacyTopConfig{Identification: 1})
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched legacy top state error = %v", err)
	}
}

func TestQualcommLegacyTopPageProfilesWritableBootWords(t *testing.T) {
	device, err := NewQualcommLegacyTopPageWithConfig(QualcommLegacyTopConfig{
		Identification: 0x12345678,
		WritableOffsets: []uint32{
			qualcommLegacyTopIDOffset,
			qualcommLegacyTopIDOffset + 4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := device.Read(qualcommLegacyTopIDOffset, Width32); err != nil || value != 0x12345678 {
		t.Fatalf("writable identification reset = %#x error %v", value, err)
	}
	if err := device.Write(qualcommLegacyTopIDOffset+4, Width32, 0xaabbccdd); err != nil {
		t.Fatal(err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Reset(); err != nil {
		t.Fatal(err)
	}
	if value, err := device.Read(qualcommLegacyTopIDOffset+4, Width32); err != nil || value != 0 {
		t.Fatalf("reset boot word = %#x error %v", value, err)
	}
	if err := device.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if value, err := device.Read(qualcommLegacyTopIDOffset+4, Width32); err != nil || value != 0xaabbccdd {
		t.Fatalf("restored boot word = %#x error %v", value, err)
	}
	wrong, err := NewQualcommLegacyTopPageWithConfig(QualcommLegacyTopConfig{
		WritableOffsets: []uint32{qualcommLegacyTopIDOffset + 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched writable layout state error = %v", err)
	}
	for _, offsets := range [][]uint32{{3}, {QualcommLegacyTopWindowSize}, {8, 8}} {
		if _, err := NewQualcommLegacyTopPageWithConfig(QualcommLegacyTopConfig{
			WritableOffsets: offsets,
		}); !errors.Is(err, ErrQualcommLegacyTopMMIO) {
			t.Fatalf("invalid writable offsets %#v error = %v", offsets, err)
		}
	}
}
