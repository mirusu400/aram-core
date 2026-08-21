package system

import (
	"encoding/binary"
	"reflect"
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
	if want := []uint32{
		0x0008,
		0x00bc, 0x00c0,
		0x058c, 0x0590, 0x059c,
		0x05a0, 0x05a4, 0x05b0, 0x05b4, 0x05b8,
		0x05c4, 0x05c8, 0x05cc, 0x05d8,
		0x0a34,
		0x0a54, 0x0a58,
		0x0b34,
		0x0c0c, 0x0c2c, 0x0c38, 0x0c3c, 0x0c40,
		0x200c,
		0x2840,
		0x4100, 0x4104, 0x4108,
		0x4110, 0x4114, 0x4118, 0x411c,
		0x4128, 0x412c, 0x4130, 0x4134, 0x4138, 0x413c,
		0x423c,
		0x4600, 0x4604, 0x4614,
		0x533c,
	}; !reflect.DeepEqual(profile.BootControlWritableOffsets, want) {
		t.Fatalf("SCH-W830 boot-control writable offsets = %#v", profile.BootControlWritableOffsets)
	}
	if want := []uint32{0x5000, 0x5100, 0x5200}; !reflect.DeepEqual(profile.BootControlSBIControllers, want) {
		t.Fatalf("SCH-W830 boot-control SBI controllers = %#v", profile.BootControlSBIControllers)
	}
	if profile.BootControlSBICompletionStatus != 0x0494 {
		t.Fatalf(
			"SCH-W830 boot-control SBI completion status = %#x",
			profile.BootControlSBICompletionStatus,
		)
	}
	if want := []uint32{
		0x4000, 0x4004, 0x4008,
		0x4010, 0x4014, 0x4018, 0x401c,
		0x4020, 0x4024, 0x4028, 0x402c,
		0x4030, 0x4034, 0x4038,
		0x4200, 0x4204, 0x4208,
		0x4210, 0x4214, 0x4218, 0x421c,
		0x4220, 0x4224, 0x4228, 0x422c,
		0x4230, 0x4234, 0x4238,
	}; !reflect.DeepEqual(profile.BootControlHalfwordOffsets, want) {
		t.Fatalf("SCH-W830 boot-control halfword offsets = %#v", profile.BootControlHalfwordOffsets)
	}
	if want := []uint32{
		0x0594, 0x0598, 0x05a8, 0x05ac,
		0x05bc, 0x05c0, 0x05d0, 0x05d4,
	}; !reflect.DeepEqual(profile.PrimaryClockWritableOffsets, want) {
		t.Fatalf("SCH-W830 primary-clock writable offsets = %#v", profile.PrimaryClockWritableOffsets)
	}
	if len(profile.ClockRegimeSleepControllers) != 2 ||
		profile.ClockRegimeSleepControllers[0] != 0x5200 ||
		profile.ClockRegimeSleepControllers[1] != 0x5244 {
		t.Fatalf(
			"SCH-W830 clock-regime sleep controllers = %#v",
			profile.ClockRegimeSleepControllers,
		)
	}
	if profile.LegacyTopIdentification != 0 {
		t.Fatalf("SCH-W830 legacy top identification = %#x", profile.LegacyTopIdentification)
	}
	if profile.LegacyTopVersion != 0 {
		t.Fatalf("SCH-W830 legacy top version = %#x", profile.LegacyTopVersion)
	}
	wantLatched := []LatchedRegisterProfile{
		{ID: "ssbi-register-02188", Address: 0x91002188, Width: Width16},
		{ID: "ssbi-register-0218a", Address: 0x9100218a, Width: Width16},
		{ID: "ssbi-register-0218c", Address: 0x9100218c, Width: Width16},
		{ID: "ssbi-register-0552a", Address: 0x9100552a, Width: Width16},
	}
	if !reflect.DeepEqual(profile.LatchedRegisters, wantLatched) {
		t.Fatalf("SCH-W830 latched registers = %#v", profile.LatchedRegisters)
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

func TestBoardProfileAppliesLatchedRegisters(t *testing.T) {
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		LatchedRegisters: []LatchedRegisterProfile{
			{ID: "external-control", Address: 0x91000002, Width: Width16, ResetValue: 0x12},
		},
	}
	bus := NewBus()
	if err := profile.ApplyLatchedRegisters(bus); err != nil {
		t.Fatal(err)
	}
	var data [2]byte
	if err := bus.Read(0x91000002, data[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint16(data[:]) != 0x12 {
		t.Fatalf("profiled latched register = %x error %v", data, err)
	}
	binary.LittleEndian.PutUint16(data[:], 0x3456)
	if err := bus.Write(0x91000002, data[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	clear(data[:])
	_ = bus.Read(0x91000002, data[:], cpu.PermissionRead)
	if binary.LittleEndian.Uint16(data[:]) != 0x3456 {
		t.Fatalf("updated profiled latched register = %x", data)
	}
}

func TestBoardProfileRejectsInvalidLatchedRegisters(t *testing.T) {
	for _, registers := range [][]LatchedRegisterProfile{
		{{ID: "bad", Address: 1, Width: Width16}},
		{{ID: "bad", Address: 0x1000, Width: Width8, ResetValue: 0x100}},
		{
			{ID: "one", Address: 0x1000, Width: Width32},
			{ID: "two", Address: 0x1002, Width: Width16},
		},
	} {
		profile := BoardProfile{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			LatchedRegisters: registers,
		}
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid latched registers: %+v", registers)
		}
	}
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		ReadOnlyRegisters: []ReadOnlyRegisterProfile{
			{ID: "read-only", Address: 0x1000, Width: Width32},
		},
		LatchedRegisters: []LatchedRegisterProfile{
			{ID: "latched", Address: 0x1002, Width: Width16},
		},
	}
	if err := profile.Validate(); err == nil {
		t.Fatal("BoardProfile accepted overlapping read-only and latched registers")
	}
}

func TestBoardProfileRejectsInvalidCompatibilityWritableOffsets(t *testing.T) {
	for _, profile := range []BoardProfile{
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			BootControlWritableOffsets: []uint32{0x5a0, 0x5a0},
		},
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			PrimaryClockWritableOffsets: []uint32{qualcommPrimaryClockModeOffset},
		},
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			BootControlSBIControllers: []uint32{0x5000, 0x5000},
		},
	} {
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid compatibility offsets: %+v", profile)
		}
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
