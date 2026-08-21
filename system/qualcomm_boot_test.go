package system

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestQualcommNANDPBLHandoffCarriesGeometry(t *testing.T) {
	handoff, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: 0x80028, TableAddress: 0x78001000,
		PageSize: 0x800, EraseBlockSize: 0x20000,
		FlashSize: 0x097c0000, BadBlockLimit: 0x14,
	})
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Entry != 0x80028 || handoff.Registers[0] != (RegisterSeed{cpu.RegisterR7, qualcommPBLMagic}) {
		t.Fatalf("PBL handoff = %+v", handoff)
	}
	table := handoff.Memory[0].Bytes
	want := [][2]uint32{{0x12f, 0x40}, {0x130, 0x4be}, {0x132, 0x800}, {0x133, 0x14}, {0x141, 6}, {0x15d, 0}}
	for index, entry := range want {
		offset := 0x2c + index*8
		if binary.LittleEndian.Uint32(table[offset:]) != entry[0] ||
			binary.LittleEndian.Uint32(table[offset+4:]) != entry[1] {
			t.Fatalf("PBL service %d = %x", index, table[offset:offset+8])
		}
	}
}

func TestQualcommNANDPBLHandoffRejectsNon2KPageGeometry(t *testing.T) {
	_, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: 0x80028, TableAddress: 0x78001000,
		PageSize: 0x1000, EraseBlockSize: 0x20000,
		FlashSize: 0x09800000, BadBlockLimit: 0x14,
	})
	if err == nil {
		t.Fatal("NAND2K handoff accepted 4 KiB pages")
	}
}

func TestQualcommBootControlAllowsOnlyEvidencedEarlyBootRegisters(t *testing.T) {
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0x0a40, Width32)
	if err != nil || value != 0x10000000 {
		t.Fatalf("hardware revision = %#x error %v", value, err)
	}
	if err := device.Write(0x024c, Width32, 0x55); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0x024c, Width32)
	if value != 0x55 {
		t.Fatalf("latched register = %#x", value)
	}
	value, err = device.Read(0x0380, Width32)
	if err != nil || value != 2 {
		t.Fatalf("NAND interface mode = %#x error %v", value, err)
	}
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 0 {
		t.Fatalf("NAND reset status = %#x error %v", value, err)
	}
	if err := device.Write(0x540c, Width32, 1); err != nil || device.WatchdogServices() != 1 {
		t.Fatalf("watchdog service error %v count %d", err, device.WatchdogServices())
	}
	if _, err := device.Read(0x0400, Width32); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("unknown read error = %v", err)
	}
	if err := device.Write(0x0400, Width32, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("unknown write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
	})
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if restored.WatchdogServices() != 1 {
		t.Fatalf("restored watchdog count = %d", restored.WatchdogServices())
	}
	wrong, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x20000000, NANDInterfaceMode: 2,
	})
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong revision state error = %v", err)
	}
}

func TestQualcommBootControlRejectsWrongNANDInterfaceState(t *testing.T) {
	device, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
	})
	state, _ := device.SaveState()
	other, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 4,
	})
	if err := other.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong NAND-interface state error = %v", err)
	}
}

func TestQualcommBootControlValidatesBoardConfigurationAndReset(t *testing.T) {
	invalid := []QualcommBootControlConfig{
		{HardwareRevision: 0x00000001, NANDInterfaceMode: 2},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 0},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 3},
	}
	for _, config := range invalid {
		if _, err := NewQualcommBootControl(config); err == nil {
			t.Fatalf("accepted invalid board configuration %+v", config)
		}
	}

	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x0380, Width32, 2); err != nil {
		t.Fatal(err)
	}
	if err := device.Reset(); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0x0380, Width32)
	if err != nil || value != 4 {
		t.Fatalf("reset NAND interface mode = %#x error %v", value, err)
	}
}

func TestQualcommSecondaryClockControlLatchesOnlyEvidencedRegisters(t *testing.T) {
	device := NewQualcommSecondaryClockControl()
	if err := device.Write(0x0430, Width32, 0x2d); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x0434, Width32, 4); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0x0430, Width32)
	if err != nil || value != 0x2d {
		t.Fatalf("secondary selector = %#x error %v", value, err)
	}
	if err := device.Write(0x0420, Width32, 0); !errors.Is(err, ErrQualcommSecondaryClockMMIO) {
		t.Fatalf("unknown secondary clock write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored := NewQualcommSecondaryClockControl()
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	value, _ = restored.Read(0x0434, Width32)
	if value != 4 {
		t.Fatalf("restored secondary data = %#x", value)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated secondary clock state error = %v", err)
	}
}
