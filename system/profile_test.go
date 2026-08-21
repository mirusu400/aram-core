package system

import "testing"

func TestSCHW830BoardProfileAppliesEvidenceBackedIRAM(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	if err := profile.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapRAM("overlap-check", 0x7800f000, 0x1000); err == nil {
		t.Fatal("board profile did not map PBL IRAM")
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
