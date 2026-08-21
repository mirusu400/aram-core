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
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
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
	if err := device.Write(0x0014, Width32, 0x1f7); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0x0014, Width32)
	if value != 0x1f7 {
		t.Fatalf("boot-control latch 0x14 = %#x", value)
	}
	if err := device.Write(0x0228, Width32, 1); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0x0228, Width32)
	if value != 1 {
		t.Fatalf("boot-control latch 0x228 = %#x", value)
	}
	if err := device.Write(0x0000, Width32, 0x160000); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0x0000, Width32)
	if value != 0x160000 {
		t.Fatalf("boot-control latch 0 = %#x", value)
	}
	if err := device.Write(0x0004, Width32, 0x200000d3); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0x0004, Width32)
	if value != 0x200000d3 {
		t.Fatalf("boot-control latch 4 = %#x", value)
	}
	if err := device.Write(0x0010, Width32, 3); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0x0010, Width32)
	if value != 3 {
		t.Fatalf("boot-control latch 0x10 = %#x", value)
	}
	for _, offset := range []uint32{
		0x0030, 0x0034, 0x0038, 0x003c, 0x0040, 0x0044, 0x0068,
		0x0400, 0x0404, 0x0430, 0x0434, 0x0458, 0x045c,
	} {
		if err := device.Write(offset, Width32, offset); err != nil {
			t.Fatalf("clock-control latch %#x: %v", offset, err)
		}
		value, _ = device.Read(offset, Width32)
		if value != offset {
			t.Fatalf("clock-control latch %#x = %#x", offset, value)
		}
	}
	for _, offset := range qualcommInterruptConfigWritableOffsets {
		if err := device.Write(offset, Width32, offset); err != nil {
			t.Fatalf("IRQ configuration latch %#x: %v", offset, err)
		}
		value, err = device.Read(offset, Width32)
		if err != nil || value != offset {
			t.Fatalf("IRQ configuration latch %#x = %#x error %v", offset, value, err)
		}
	}
	for _, offset := range []uint32{
		0x0084, 0x0088, 0x008c, 0x0090, 0x0094, 0x0098,
		0x00a4, 0x00a8, 0x00ac, 0x00b0, 0x00b4, 0x00b8,
		0x00c4, 0x00c8, 0x00cc, 0x00d0, 0x00d4, 0x00d8, 0x00dc, 0x00e0,
		0x00e4, 0x00e8, 0x00ec, 0x00f0, 0x00f4, 0x00f8, 0x00fc, 0x0100, 0x0114,
		0x0124, 0x0128,
	} {
		value, err = device.Read(offset, Width32)
		if err != nil || value != 0 {
			t.Fatalf("clock bank reset latch %#x = %#x error %v", offset, value, err)
		}
	}
	for _, offset := range []uint32{0x0104, 0x0108, 0x0118, 0x013c, 0x0200, 0x021c, 0x0260, 0x0a00} {
		value, err = device.Read(offset, Width32)
		if err != nil || value != 0 {
			t.Fatalf("boot-control reset latch %#x = %#x error %v", offset, value, err)
		}
	}
	value, err = device.Read(0x0380, Width32)
	if err != nil || value != 2 {
		t.Fatalf("NAND interface mode = %#x error %v", value, err)
	}
	value, err = device.Read(0x1100, Width32)
	if err != nil || value != 0x5680 {
		t.Fatalf("EBI memory configuration = %#x error %v", value, err)
	}
	if err := device.Write(0x1024, Width32, 9); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0x1024, Width32)
	if value != 9 {
		t.Fatalf("MPMC dynamic refresh = %#x", value)
	}
	value, err = device.Read(0x1004, Width32)
	if err != nil || value != 0 {
		t.Fatalf("MPMC status = %#x error %v", value, err)
	}
	value, err = device.Read(0x0274, Width32)
	if err != nil || value != 1 {
		t.Fatalf("clock mode status = %#x error %v", value, err)
	}
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 0 {
		t.Fatalf("NAND reset status = %#x error %v", value, err)
	}
	if err := device.Write(0x0380, Width32, 2|8); err != nil {
		t.Fatal(err)
	}
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 2 {
		t.Fatalf("NAND ready status = %#x error %v", value, err)
	}
	if err := device.Write(0x0380, Width32, 2); err != nil {
		t.Fatal(err)
	}
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 2 {
		t.Fatalf("latched NAND ready status = %#x error %v", value, err)
	}
	if err := device.Write(0x0414, Width32, 2); err != nil {
		t.Fatal(err)
	}
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 0 {
		t.Fatalf("cleared NAND ready status = %#x error %v", value, err)
	}
	if err := device.Write(0x0380, Width32, 2|8); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x540c, Width32, 1); err != nil || device.WatchdogServices() != 1 {
		t.Fatalf("watchdog service error %v count %d", err, device.WatchdogServices())
	}
	if err := device.Write(0x540c, Width8, 1); err != nil || device.WatchdogServices() != 2 {
		t.Fatalf("byte watchdog service error %v count %d", err, device.WatchdogServices())
	}
	tick0, err := device.Read(0x5408, Width32)
	if err != nil {
		t.Fatal(err)
	}
	tick0Stable, _ := device.Read(0x5408, Width32)
	tick1, _ := device.Read(0x5408, Width32)
	tick1Stable, _ := device.Read(0x5408, Width32)
	if tick0Stable != tick0 || tick1 != tick0+1 || tick1Stable != tick1 {
		t.Fatalf("unstable time tick sequence = %#x %#x %#x %#x", tick0, tick0Stable, tick1, tick1Stable)
	}
	if err := device.Write(0x54c4, Width32, 0xfff00002); err != nil {
		t.Fatal(err)
	}
	value, err = device.Read(0x54c4, Width32)
	if err != nil || value != 0xfff00002 {
		t.Fatalf("time tick match = %#x error %v", value, err)
	}
	value, err = device.Read(0x54c0, Width32)
	if err != nil || value != 1 {
		t.Fatalf("time tick match ready = %#x error %v", value, err)
	}
	if _, err := device.Read(0x0474, Width32); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("unknown read error = %v", err)
	}
	if err := device.Write(0x0474, Width32, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("unknown write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
	})
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if restored.WatchdogServices() != 2 {
		t.Fatalf("restored watchdog count = %d", restored.WatchdogServices())
	}
	restoredTick, err := restored.Read(0x5408, Width32)
	if err != nil || restoredTick != tick1+1 {
		t.Fatalf("restored time tick = %#x error %v", restoredTick, err)
	}
	value, err = restored.Read(0x0488, Width32)
	if err != nil || value != 2 {
		t.Fatalf("restored NAND ready status = %#x error %v", value, err)
	}
	wrong, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x20000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
	})
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong revision state error = %v", err)
	}
}

func TestQualcommBootControlRejectsWrongNANDInterfaceState(t *testing.T) {
	device, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
	})
	state, _ := device.SaveState()
	other, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 4,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
	})
	if err := other.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong NAND-interface state error = %v", err)
	}
}

func TestQualcommBootControlValidatesBoardConfigurationAndReset(t *testing.T) {
	invalid := []QualcommBootControlConfig{
		{HardwareRevision: 0x00000001, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0x5680, NANDReady: NewStatusSignal()},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 0, EBIMemoryConfiguration: 0x5680, NANDReady: NewStatusSignal()},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 3, EBIMemoryConfiguration: 0x5680, NANDReady: NewStatusSignal()},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0, NANDReady: NewStatusSignal()},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0x5680, ClockModeStatus: 2, NANDReady: NewStatusSignal()},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0x5680},
	}
	for _, config := range invalid {
		if _, err := NewQualcommBootControl(config); err == nil {
			t.Fatalf("accepted invalid board configuration %+v", config)
		}
	}

	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 4,
		EBIMemoryConfiguration: 0x5880, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
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
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 0 {
		t.Fatalf("reset NAND ready status = %#x error %v", value, err)
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
	if err := device.Write(0x0408, Width32, 0x11223344); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0x0430, Width32)
	if err != nil || value != 0x2d {
		t.Fatalf("secondary selector = %#x error %v", value, err)
	}
	value, err = device.Read(0x0408, Width32)
	if err != nil || value != 0x11223344 {
		t.Fatalf("secondary clock 0x408 value = %#x error %v", value, err)
	}
	if err := device.Write(0x0420, Width32, 0); !errors.Is(err, ErrQualcommSecondaryClockMMIO) {
		t.Fatalf("unknown secondary clock write error = %v", err)
	}
	value, err = device.Read(qualcommSecondaryClockDisabledStatusOffset, Width32)
	if err != nil || value != 0x10 {
		t.Fatalf("secondary disabled status = %#x error %v", value, err)
	}
	if err := device.Write(qualcommSecondaryClockDisabledStatusOffset, Width32, 0); !errors.Is(err, ErrQualcommSecondaryClockMMIO) {
		t.Fatalf("secondary status write error = %v", err)
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
