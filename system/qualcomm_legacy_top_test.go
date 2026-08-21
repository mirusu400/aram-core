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
