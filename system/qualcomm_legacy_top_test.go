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
	check(t, err)
	restored := NewQualcommLegacyTopPage(QualcommLegacyTopConfig{
		Version: 0x01020304, Identification: 0x12345678,
	})
	check(t, restored.LoadState(state))
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
	check(t, err)
	if value, err := device.Read(qualcommLegacyTopIDOffset, Width32); err != nil || value != 0x12345678 {
		t.Fatalf("writable identification reset = %#x error %v", value, err)
	}
	check(t, device.Write(qualcommLegacyTopIDOffset+4, Width32, 0xaabbccdd))
	state, err := device.SaveState()
	check(t, err)
	check(t, device.Reset())
	if value, err := device.Read(qualcommLegacyTopIDOffset+4, Width32); err != nil || value != 0 {
		t.Fatalf("reset boot word = %#x error %v", value, err)
	}
	check(t, device.LoadState(state))
	if value, err := device.Read(qualcommLegacyTopIDOffset+4, Width32); err != nil || value != 0xaabbccdd {
		t.Fatalf("restored boot word = %#x error %v", value, err)
	}
	wrong, err := NewQualcommLegacyTopPageWithConfig(QualcommLegacyTopConfig{
		WritableOffsets: []uint32{qualcommLegacyTopIDOffset + 8},
	})
	check(t, err)
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

func TestQualcommLegacyTopPageAliasesVectoredInterruptController(t *testing.T) {
	vectored, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 8, Bank0Sources: 4,
			GroupCount: 1,
			Groups: [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig{{
				Source: 1, EnableOffset: 0x14, StatusOffset: 0x88, ValidMask: 0x03,
			}},
		},
		nil,
	)
	check(t, err)
	aperture := &qualcommLegacyTopVectoredAperture{controller: vectored}
	device, err := NewQualcommLegacyTopPageWithConfig(QualcommLegacyTopConfig{
		VectoredInterruptOffset:   0x544,
		VectoredInterruptAperture: aperture,
	})
	check(t, err)

	check(t, device.Write(0x544+0x14, Width32, 0x02))
	if value, err := device.Read(0x544+0x14, Width32); err != nil || value != 0x02 {
		t.Fatalf("aliased compact-VIC group enable = %#x error %v", value, err)
	}
	if value, err := vectored.Read(0x14, Width32); err != nil || value != 0x02 {
		t.Fatalf("direct compact-VIC group enable = %#x error %v", value, err)
	}

	for _, config := range []QualcommLegacyTopConfig{
		{VectoredInterruptOffset: 0x544},
		{VectoredInterruptOffset: QualcommLegacyTopWindowSize - 4, VectoredInterruptAperture: aperture},
	} {
		if _, err := NewQualcommLegacyTopPageWithConfig(config); !errors.Is(err, ErrQualcommLegacyTopMMIO) {
			t.Fatalf("invalid compact-VIC alias %+v error = %v", config, err)
		}
	}
}

type qualcommLegacyTopVectoredAperture struct {
	controller *QualcommVectoredInterruptController
}

func (d *qualcommLegacyTopVectoredAperture) Reset() error {
	return d.controller.Reset()
}

func (d *qualcommLegacyTopVectoredAperture) Read(offset uint32, width Width) (uint32, error) {
	return d.controller.Read(offset-QualcommVectoredInterruptControllerBaseOffset, width)
}

func (d *qualcommLegacyTopVectoredAperture) Write(offset uint32, width Width, value uint32) error {
	return d.controller.Write(offset-QualcommVectoredInterruptControllerBaseOffset, width, value)
}
