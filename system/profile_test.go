package system

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestSCHW830BoardProfileAppliesEvidenceBackedIRAM(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(profile.HLECalls) != 0 {
		t.Fatalf("SCH-W830 unexpectedly enables failure-path HLE calls: %+v", profile.HLECalls)
	}
	if profile.NANDReadID != 0xecaa {
		t.Fatalf("SCH-W830 NAND read ID = %#x", profile.NANDReadID)
	}
	if profile.PrimaryClockStatus != 0 {
		t.Fatalf("SCH-W830 primary clock status = %#x", profile.PrimaryClockStatus)
	}
	if profile.BootClockModeStatus != 1 {
		t.Fatalf("SCH-W830 boot clock mode status = %#x", profile.BootClockModeStatus)
	}
	if profile.LegacyTopIdentification != 0 {
		t.Fatalf("SCH-W830 legacy top identification = %#x", profile.LegacyTopIdentification)
	}
	if profile.LegacyTopVersion != 0 {
		t.Fatalf("SCH-W830 legacy top version = %#x", profile.LegacyTopVersion)
	}
	bus := NewBus()
	if err := profile.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapRAM("ebi-overlap-check", 0x07fff000, 0x1000); err == nil {
		t.Fatal("board profile did not map 128 MiB EBI RAM")
	}
	if err := bus.MapRAM("overlap-check", 0x7800f000, 0x1000); err == nil {
		t.Fatal("board profile did not map PBL IRAM")
	}
	if err := bus.MapRAM("high-vector-overlap-check", 0xffff5000, 0x1000); err == nil {
		t.Fatal("board profile did not map high-vector IRAM")
	}
}

func TestBoardProfileRejectsDuplicateHLECallAddress(t *testing.T) {
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		HLECalls: []HLECallProfile{
			{
				ID: "one", Contract: "fixture.one", Address: 0x1000,
				Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
			},
			{
				ID: "two", Contract: "fixture.two", Address: 0x1000,
				Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
			},
		},
	}
	if err := profile.Validate(); err == nil {
		t.Fatal("BoardProfile accepted duplicate HLE call address")
	}
}

func TestBoardProfileRejectsOverlappingMemory(t *testing.T) {
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		Memory: []MemoryRegionProfile{
			{ID: "one", Kind: MemoryRAM, Address: 0x1000, Size: 0x100},
			{ID: "two", Kind: MemoryRAM, Address: 0x1080, Size: 0x100},
		},
	}
	if err := profile.Validate(); err == nil {
		t.Fatal("BoardProfile accepted overlapping memory")
	}
}
