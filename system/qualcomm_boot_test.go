package system

import (
	"bytes"
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
	check(t, err)
	if handoff.Entry != 0x80028 || handoff.Registers[0] != (RegisterSeed{cpu.RegisterR7, qualcommPBLMagic}) {
		t.Fatalf("PBL handoff = %+v", handoff)
	}
	table := handoff.Memory[0].Bytes
	want := [][2]uint32{{0x12f, 0x40}, {0x130, 0x4be}, {0x132, 0x800}, {0x133, 0x14}, {0x141, 6}, {0x15d, 0}}
	if len(table) != qualcommPBLFeatureDataHeaderSize+len(want)*8 {
		t.Fatalf("PBL service table size = %#x, want %#x", len(table), qualcommPBLFeatureDataHeaderSize+len(want)*8)
	}
	for index, entry := range want {
		offset := 0x2c + index*8
		if binary.LittleEndian.Uint32(table[offset:]) != entry[0] ||
			binary.LittleEndian.Uint32(table[offset+4:]) != entry[1] {
			t.Fatalf("PBL service %d = %x", index, table[offset:offset+8])
		}
	}
}

func TestQualcommNANDPBLHandoffCarriesLegacyFeatureData(t *testing.T) {
	handoff, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: 0x80000, TableAddress: 0x78001000, LegacyFeatureDataAddress: 0xffff6044,
		PageSize: 0x800, EraseBlockSize: 0x20000,
		FlashSize: 0x20000000, BadBlockLimit: 0x14,
	})
	check(t, err)
	if len(handoff.Memory) != 2 || handoff.Memory[1].Address != 0xffff6044 {
		t.Fatalf("legacy PBL memory = %+v", handoff.Memory)
	}
	table := handoff.Memory[1].Bytes
	want := [][2]uint32{{0x108, 0x40}, {0x109, 0x1000}, {0x10b, 0x800}, {0x10c, 0x14}, {0x115, 6}, {0x131, 0}}
	for index, entry := range want {
		offset := qualcommPBLFeatureDataHeaderSize + index*8
		if binary.LittleEndian.Uint32(table[offset:]) != entry[0] ||
			binary.LittleEndian.Uint32(table[offset+4:]) != entry[1] {
			t.Fatalf("legacy PBL feature %d = %x", index, table[offset:offset+8])
		}
	}
}

func TestQualcommNANDPBLHandoffCarriesSharedDataEndPointer(t *testing.T) {
	handoff, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: 0x800000, TableAddress: 0x78001000,
		ServiceTableHeaderSize:   0x30,
		HeaderFeatureDataAddress: 0x78002100,
		HeaderFeatures: []QualcommPBLHeaderFeature{
			{Selector: qualcommPBLHeaderFlashBlockCount, Value: 0x0400},
			{Selector: qualcommPBLHeaderSLCBlockCount, Value: 0x0010},
			{Selector: qualcommPBLHeaderBadBlockLimit, Value: 0x0014},
		},
		SharedDataAddress: 0x78002000, SharedDataSize: 0x68,
		PageSize: 0x800, EraseBlockSize: 0x20000,
		FlashSize: 0x20000000, BadBlockLimit: 0x14,
	})
	check(t, err)
	if len(handoff.Registers) != 4 ||
		handoff.Registers[2] != (RegisterSeed{Register: cpu.RegisterR9, Value: 0x78002100}) ||
		handoff.Registers[3] != (RegisterSeed{Register: cpu.RegisterR11, Value: 0x78002068}) {
		t.Fatalf("PBL shared-data registers = %+v", handoff.Registers)
	}
	if len(handoff.Memory) != 3 || handoff.Memory[2].Address != 0x78002000 ||
		len(handoff.Memory[2].Bytes) != 0x68 ||
		!bytes.Equal(handoff.Memory[2].Bytes, make([]byte, 0x68)) {
		t.Fatalf("PBL shared-data memory = %+v", handoff.Memory)
	}
	if features := handoff.Memory[1]; features.Address != 0x78002100 ||
		len(features.Bytes) != qualcommPBLHeaderFeatureDataSize ||
		binary.LittleEndian.Uint32(features.Bytes[0x08:]) != 1 ||
		binary.LittleEndian.Uint32(features.Bytes[0x0c:]) != 0x0400 ||
		binary.LittleEndian.Uint32(features.Bytes[0x10:]) != 1 ||
		binary.LittleEndian.Uint32(features.Bytes[0x14:]) != 0x0010 ||
		binary.LittleEndian.Uint32(features.Bytes[0x18:]) != 1 ||
		binary.LittleEndian.Uint32(features.Bytes[0x1c:]) != 0x14 {
		t.Fatalf("PBL header features = %+v", features)
	}
	if table := handoff.Memory[0].Bytes; len(table) != 0x30+qualcommPBLServiceEntryCount*8 ||
		binary.LittleEndian.Uint32(table[0x30:]) != 0x012f {
		t.Fatalf("PBL generation-specific service table = %x", table)
	}
}

func TestQualcommNANDPBLHandoffCarriesSmallPageGeometry(t *testing.T) {
	handoff, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry:        0x80000,
		StackPointer: 0x03f40000,
		TableAddress: 0x78001000, LegacyFeatureDataAddress: 0xffff6044,
		FixedFeatureDataAddress: 0x78002000,
		FixedFeatureFirst:       0xf9,
		FixedFeatureSlotCount:   5,
		FixedFeatures: []QualcommPBLFixedFeature{
			{Selector: 0xf9, Value: 0x20},
			{Selector: 0xfa, Value: 0x200},
			{Selector: 0xfc, Value: 0x4000},
			{Selector: 0xfd, Value: 0x14},
		},
		PageSize: 0x200, EraseBlockSize: 0x4000,
		FlashSize: 0x10000000, BadBlockLimit: 0x14,
	})
	check(t, err)
	if handoff.ID != "qualcomm.pbl-hle.nand-v1" {
		t.Fatalf("small-page PBL handoff ID = %q", handoff.ID)
	}
	if handoff.Registers[len(handoff.Registers)-1] != (RegisterSeed{
		Register: cpu.RegisterSP,
		Value:    0x03f40000,
	}) {
		t.Fatalf("small-page PBL stack = %+v", handoff.Registers)
	}
	for _, table := range []struct {
		address  uint32
		expected [][2]uint32
	}{
		{0x78001000, [][2]uint32{{0x12f, 0x20}, {0x130, 0x4000}, {0x132, 0x200}, {0x133, 0x14}, {0x141, qualcommPBLFlashTypeNAND}}},
		{0xffff6044, [][2]uint32{{0x108, 0x20}, {0x109, 0x4000}, {0x10b, 0x200}, {0x10c, 0x14}, {0x115, qualcommPBLFlashTypeNAND}}},
	} {
		var memory []byte
		for _, seed := range handoff.Memory {
			if seed.Address == table.address {
				memory = seed.Bytes
				break
			}
		}
		if memory == nil {
			t.Fatalf("small-page PBL table %#x is absent", table.address)
		}
		for index, entry := range table.expected {
			offset := qualcommPBLFeatureDataHeaderSize + index*8
			if binary.LittleEndian.Uint32(memory[offset:]) != entry[0] ||
				binary.LittleEndian.Uint32(memory[offset+4:]) != entry[1] {
				t.Fatalf("small-page PBL table %#x entry %d = %x", table.address, index, memory[offset:offset+8])
			}
		}
	}
	var fixed []byte
	for _, seed := range handoff.Memory {
		if seed.Address == 0x78002000 {
			fixed = seed.Bytes
			break
		}
	}
	wantFixed := [][2]uint32{{1, 0x20}, {1, 0x200}, {0, 0}, {1, 0x4000}, {1, 0x14}}
	if len(fixed) != len(wantFixed)*8 {
		t.Fatalf("small-page fixed PBL table size = %#x", len(fixed))
	}
	for index, entry := range wantFixed {
		if binary.LittleEndian.Uint32(fixed[index*8:]) != entry[0] ||
			binary.LittleEndian.Uint32(fixed[index*8+4:]) != entry[1] {
			t.Fatalf("small-page fixed PBL slot %d = %x", index, fixed[index*8:index*8+8])
		}
	}
}

func TestQualcommNANDPBLHandoffRejectsUnsupportedGeometry(t *testing.T) {
	_, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: 0x80028, TableAddress: 0x78001000,
		PageSize: 0x1000, EraseBlockSize: 0x20000,
		FlashSize: 0x09800000, BadBlockLimit: 0x14,
	})
	if err == nil {
		t.Fatal("PBL handoff accepted unsupported 4 KiB pages")
	}
	_, err = NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: 0x80000, TableAddress: 0x78001000, LegacyFeatureDataAddress: 0xffff6042,
		PageSize: 0x800, EraseBlockSize: 0x20000,
		FlashSize: 0x20000000, BadBlockLimit: 0x14,
	})
	if err == nil {
		t.Fatal("NAND2K handoff accepted an unaligned legacy feature address")
	}
	for _, config := range []QualcommNANDPBLConfig{
		{
			Entry: 0x800000, TableAddress: 0x78001000,
			HeaderFeatureDataAddress: 0x78002100,
			HeaderFeatures: []QualcommPBLHeaderFeature{{
				Selector: 0x0157, Value: 1,
			}},
			PageSize: 0x800, EraseBlockSize: 0x20000,
			FlashSize: 0x20000000, BadBlockLimit: 0x14,
		},
		{
			Entry: 0x800000, TableAddress: 0x78001000,
			HeaderFeatureDataAddress: 0x78002100,
			PageSize:                 0x800, EraseBlockSize: 0x20000,
			FlashSize: 0x20000000, BadBlockLimit: 0x14,
		},
		{
			Entry: 0x800000, TableAddress: 0x78001000,
			HeaderFeatureDataAddress: 0x78002100,
			HeaderFeatures: []QualcommPBLHeaderFeature{
				{Selector: 0x0159, Value: 1},
				{Selector: 0x0159, Value: 2},
			},
			PageSize: 0x800, EraseBlockSize: 0x20000,
			FlashSize: 0x20000000, BadBlockLimit: 0x14,
		},
		{
			Entry: 0x800000, TableAddress: 0x78001000,
			SharedDataAddress: 0x78002002, SharedDataSize: 0x68,
			PageSize: 0x800, EraseBlockSize: 0x20000,
			FlashSize: 0x20000000, BadBlockLimit: 0x14,
		},
		{
			Entry: 0x800000, TableAddress: 0x78001000,
			SharedDataAddress: 0x78002000,
			PageSize:          0x800, EraseBlockSize: 0x20000,
			FlashSize: 0x20000000, BadBlockLimit: 0x14,
		},
		{
			Entry: 0x800000, TableAddress: 0x78001000,
			FixedFeatureDataAddress: 0x78002000,
			FixedFeatureFirst:       0xf9,
			PageSize:                0x800, EraseBlockSize: 0x20000,
			FlashSize: 0x20000000, BadBlockLimit: 0x14,
		},
		{
			Entry: 0x800000, TableAddress: 0x78001000,
			FixedFeatureDataAddress: 0x78002000,
			FixedFeatureFirst:       0xf9,
			FixedFeatureSlotCount:   1,
			FixedFeatures: []QualcommPBLFixedFeature{
				{Selector: 0xfa, Value: 1},
			},
			PageSize: 0x800, EraseBlockSize: 0x20000,
			FlashSize: 0x20000000, BadBlockLimit: 0x14,
		},
	} {
		if _, err := NewQualcommNANDPBLHandoff(config); err == nil {
			t.Fatalf("NAND2K handoff accepted invalid shared data %+v", config)
		}
	}
}

func TestQualcommBootControlAllowsOnlyEvidencedEarlyBootRegisters(t *testing.T) {
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
	})
	check(t, err)
	value, err := device.Read(0x0a40, Width32)
	if err != nil || value != 0x10000000 {
		t.Fatalf("hardware revision = %#x error %v", value, err)
	}
	check(t, device.Write(0x024c, Width32, 0x55))
	value, _ = device.Read(0x024c, Width32)
	if value != 0x55 {
		t.Fatalf("latched register = %#x", value)
	}
	check(t, device.Write(0x0014, Width32, 0x1f7))
	value, _ = device.Read(0x0014, Width32)
	if value != 0x1f7 {
		t.Fatalf("boot-control latch 0x14 = %#x", value)
	}
	check(t, device.Write(0x0228, Width32, 1))
	value, _ = device.Read(0x0228, Width32)
	if value != 1 {
		t.Fatalf("boot-control latch 0x228 = %#x", value)
	}
	check(t, device.Write(0x0000, Width32, 0x160000))
	value, _ = device.Read(0x0000, Width32)
	if value != 0x160000 {
		t.Fatalf("boot-control latch 0 = %#x", value)
	}
	check(t, device.Write(0x0004, Width32, 0x200000d3))
	value, _ = device.Read(0x0004, Width32)
	if value != 0x200000d3 {
		t.Fatalf("boot-control latch 4 = %#x", value)
	}
	check(t, device.Write(0x0010, Width32, 3))
	value, _ = device.Read(0x0010, Width32)
	if value != 3 {
		t.Fatalf("boot-control latch 0x10 = %#x", value)
	}
	for _, offset := range []uint32{
		0x0030, 0x0034, 0x0038, 0x003c, 0x0040, 0x0044, 0x0068,
		0x0400, 0x0404, 0x0430, 0x0434, 0x0458, 0x045c, 0x0aa4,
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
	check(t, device.Write(0x1024, Width32, 9))
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
	check(t, device.Write(0x0380, Width32, 2|8))
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 2 {
		t.Fatalf("NAND ready status = %#x error %v", value, err)
	}
	check(t, device.Write(0x0380, Width32, 2))
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 2 {
		t.Fatalf("latched NAND ready status = %#x error %v", value, err)
	}
	check(t, device.Write(0x0414, Width32, 2))
	value, err = device.Read(0x0488, Width32)
	if err != nil || value != 0 {
		t.Fatalf("cleared NAND ready status = %#x error %v", value, err)
	}
	check(t, device.Write(0x0380, Width32, 2|8))
	if err := device.Write(0x540c, Width32, 1); err != nil || device.WatchdogServices() != 1 {
		t.Fatalf("watchdog service error %v count %d", err, device.WatchdogServices())
	}
	if err := device.Write(0x540c, Width8, 1); err != nil || device.WatchdogServices() != 2 {
		t.Fatalf("byte watchdog service error %v count %d", err, device.WatchdogServices())
	}
	tick0, err := device.Read(0x5408, Width32)
	check(t, err)
	tick0Stable, _ := device.Read(0x5408, Width32)
	tick1, _ := device.Read(0x5408, Width32)
	tick1Stable, _ := device.Read(0x5408, Width32)
	if tick0Stable != tick0 || tick1 != tick0+1 || tick1Stable != tick1 {
		t.Fatalf("unstable time tick sequence = %#x %#x %#x %#x", tick0, tick0Stable, tick1, tick1Stable)
	}
	check(t, device.Write(0x54c4, Width32, 0xfff00002))
	value, err = device.Read(0x54c4, Width32)
	if err != nil || value != 0xfff00002 {
		t.Fatalf("time tick match = %#x error %v", value, err)
	}
	value, err = device.Read(0x54c0, Width32)
	if err != nil || value != 1 {
		t.Fatalf("time tick match ready = %#x error %v", value, err)
	}
	if err := device.Write(0x0474, Width32, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("unknown write error = %v", err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1, NANDReady: NewStatusSignal(),
	})
	check(t, restored.LoadState(state))
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

func TestQualcommBootControlDrivesProfiledVectoredGroupStatus(t *testing.T) {
	config := QualcommVectoredInterruptConfig{
		SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
		GroupCount: 1,
		Groups: [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig{{
			Source: 14, EnableOffset: 0x14, StatusOffset: 0x88, ValidMask: 0x03,
		}},
	}
	response := QualcommBootGroupedStatusResponse{
		Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
		GroupStatusOffset: 0x88, GroupMask: 0x02,
	}
	signalOnlyResponse := QualcommBootGroupedStatusResponse{
		Offset: 0x0380, NANDReadyMask: 0x01,
		GroupStatusOffset: 0x88, GroupMask: 0x01,
	}
	newDevice := func() (*QualcommBootControl, *QualcommVectoredInterruptController, *StatusSignal) {
		vectored, err := NewQualcommVectoredInterruptController(config, &interruptLineProbe{})
		check(t, err)
		ready := NewStatusSignal()
		device, err := NewQualcommBootControl(QualcommBootControlConfig{
			HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
			EBIMemoryConfiguration: 0x5880, ClockModeStatus: 1,
			NANDReady: ready, VectoredInterruptController: vectored,
			GroupedStatusResponses: []QualcommBootGroupedStatusResponse{
				response, signalOnlyResponse,
			},
		})
		check(t, err)
		return device, vectored, ready
	}

	device, vectored, ready := newDevice()
	if status, err := vectored.Read(0x88, Width32); err != nil || status != 0 {
		t.Fatalf("reset grouped status = %#x error %v", status, err)
	}
	ready.Set(2)
	if status, err := device.Read(0x0488, Width32); err != nil || status != 0 {
		t.Fatalf("unarmed NAND-ready grouped status = %#x error %v", status, err)
	}
	check(t, device.Write(0x0380, Width32, 2|8))
	if status, err := vectored.Read(0x88, Width32); err != nil || status != 2 {
		t.Fatalf("asserted grouped status = %#x error %v", status, err)
	}
	check(t, device.Write(0x0380, Width32, 2))
	if status, err := vectored.Read(0x88, Width32); err != nil || status != 0 {
		t.Fatalf("deasserted grouped status = %#x error %v", status, err)
	}
	ready.Set(2)
	if status, err := device.Read(0x0488, Width32); err != nil || status != 2 {
		t.Fatalf("NAND-ready grouped status = %#x error %v", status, err)
	}
	check(t, device.Write(0x0414, Width32, 2))
	if status, err := device.Read(0x0488, Width32); err != nil || status != 0 {
		t.Fatalf("acknowledged NAND-ready grouped status = %#x error %v", status, err)
	}
	ready.Set(1)
	if status, err := device.Read(0x0488, Width32); err != nil || status != 1 {
		t.Fatalf("signal-only grouped status = %#x error %v", status, err)
	}
	check(t, device.Write(0x0414, Width32, 1))
	if status, err := device.Read(0x0488, Width32); err != nil || status != 0 {
		t.Fatalf("acknowledged signal-only grouped status = %#x error %v", status, err)
	}
	ready.Set(2)
	if status, err := device.Read(0x0488, Width32); err != nil || status != 2 {
		t.Fatalf("reasserted NAND-ready grouped status = %#x error %v", status, err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, restoredVectored, restoredReady := newDevice()
	check(t, restored.LoadState(state))
	if status, err := restoredVectored.Read(0x88, Width32); err != nil || status != 2 {
		t.Fatalf("restored grouped status = %#x error %v", status, err)
	}
	restoredReady.Clear(2)
	if status, err := restored.Read(0x0488, Width32); err != nil || status != 0 {
		t.Fatalf("restored armed grouped status = %#x error %v", status, err)
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

func TestQualcommBootControlProfilesReadableWatchdogService(t *testing.T) {
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WatchdogServiceReadable: true, NANDReady: NewStatusSignal(),
	})
	check(t, err)
	value, err := device.Read(0x540c, Width32)
	if err != nil || value != 0 {
		t.Fatalf("reset watchdog service = %#x error %v", value, err)
	}
	check(t, device.Write(0x540c, Width32, 1))
	value, err = device.Read(0x540c, Width32)
	if err != nil || value != 1 {
		t.Fatalf("serviced watchdog value = %#x error %v", value, err)
	}

	writeOnly, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady: NewStatusSignal(),
	})
	check(t, err)
	if _, err := writeOnly.Read(0x540c, Width32); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("write-only watchdog read error = %v", err)
	}
}

func TestQualcommBootControlProfilesAdditionalWritableOffsets(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets: []uint32{0x05a0}, NANDReady: NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, device.Write(0x05a0, Width32, 0x12345678))
	value, err := device.Read(0x05a0, Width32)
	if err != nil || value != 0x12345678 {
		t.Fatalf("profiled boot-control latch = %#x error %v", value, err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewQualcommBootControl(config)
	check(t, restored.LoadState(state))
	value, _ = restored.Read(0x05a0, Width32)
	if value != 0x12345678 {
		t.Fatalf("restored profiled boot-control latch = %#x", value)
	}
	unprofiledConfig := config
	unprofiledConfig.WritableOffsets = nil
	unprofiled, _ := NewQualcommBootControl(unprofiledConfig)
	if err := unprofiled.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched boot-control profile state error = %v", err)
	}
	for _, offsets := range [][]uint32{{0x05a0, 0x05a0}, {0x0274}, {2}, {QualcommBootControlWindowSize}} {
		invalid := config
		invalid.WritableOffsets = offsets
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid boot-control writable offsets %#v", offsets)
		}
	}
}

func TestQualcommBootControlModelsAbsentSDCCCardCommandTimeout(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets: []uint32{0x0c0c, 0x0c38},
		SDCCControllers: []QualcommSDCCControllerConfig{{Base: 0x0c00}},
		NANDReady:       NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	if status, err := device.Read(0x0c34, Width32); err != nil || status != 0 {
		t.Fatalf("reset SDCC status = %#x error %v", status, err)
	}
	check(t, device.Write(0x0c0c, Width32, 0x00000445))
	if status, err := device.Read(0x0c34, Width32); err != nil || status != 0x04 {
		t.Fatalf("absent-card SDCC command status = %#x error %v", status, err)
	}
	if err := device.Write(0x0c34, Width32, 0x04); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("SDCC status write error = %v", err)
	}
	check(t, device.Write(0x0c38, Width32, 0x04))
	if status, err := device.Read(0x0c34, Width32); err != nil || status != 0 {
		t.Fatalf("cleared SDCC status = %#x error %v", status, err)
	}
	// A command value without CPSM enable is only a configuration latch.
	check(t, device.Write(0x0c0c, Width32, 0x45))
	if status, err := device.Read(0x0c34, Width32); err != nil || status != 0 {
		t.Fatalf("disabled SDCC command status = %#x error %v", status, err)
	}

	check(t, device.Write(0x0c0c, Width32, 0x00000448))
	state, err := device.SaveState()
	check(t, err)
	restored, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, restored.LoadState(state))
	if status, err := restored.Read(0x0c34, Width32); err != nil || status != 0x04 {
		t.Fatalf("restored absent-card SDCC status = %#x error %v", status, err)
	}

	for _, mutate := range []func(*QualcommBootControlConfig){
		func(config *QualcommBootControlConfig) { config.WritableOffsets = []uint32{0x0c38} },
		func(config *QualcommBootControlConfig) { config.WritableOffsets = []uint32{0x0c0c} },
		func(config *QualcommBootControlConfig) {
			config.ReadOnlyRegisters = []QualcommBootReadOnlyRegister{{Offset: 0x0c34}}
		},
		func(config *QualcommBootControlConfig) {
			config.SDCCControllers = []QualcommSDCCControllerConfig{
				{Base: 0x0c00}, {Base: 0x0c00},
			}
		},
	} {
		invalid := config
		mutate(&invalid)
		invalid.NANDReady = NewStatusSignal()
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid absent-card SDCC profile: %+v", invalid)
		}
	}
}

func TestQualcommBootControlModelsPresentSDMemoryCardDiscovery(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets: []uint32{0x0c0c, 0x0c38},
		SDCCControllers: []QualcommSDCCControllerConfig{{
			Base: 0x0c00, CardPresent: true,
		}},
		NANDReady: NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)

	// CMD8 carries a short response and must echo the supplied voltage/check
	// pattern. The native DC17 driver accepts the command through CMDRESPEND.
	check(t, device.Write(0x0c0c, Width32, 0x00000448))
	if status, err := device.Read(0x0c34, Width32); err != nil || status != 0x40 {
		t.Fatalf("present-card CMD8 status = %#x error %v", status, err)
	}
	if response, err := device.Read(0x0c14, Width32); err != nil || response != 0x1aa {
		t.Fatalf("present-card CMD8 response = %#x error %v", response, err)
	}
	check(t, device.Write(0x0c38, Width32, 0x40))

	// A memory-only card intentionally times out CMD5 (SDIO), then reports a
	// powered-up high-capacity OCR through ACMD41.
	check(t, device.Write(0x0c0c, Width32, 0x00000445))
	if status, err := device.Read(0x0c34, Width32); err != nil || status != 0x04 {
		t.Fatalf("present-memory-card CMD5 status = %#x error %v", status, err)
	}
	check(t, device.Write(0x0c38, Width32, 0x04))
	check(t, device.Write(0x0c0c, Width32, 0x00000469))
	if status, err := device.Read(0x0c34, Width32); err != nil || status != 0x40 {
		t.Fatalf("present-card ACMD41 status = %#x error %v", status, err)
	}
	if response, err := device.Read(0x0c14, Width32); err != nil || response != 0xc0ff8000 {
		t.Fatalf("present-card ACMD41 response = %#x error %v", response, err)
	}
}

func TestQualcommBootControlRoutesAbsentSDCCCardTimeoutThroughVectoredGroup(t *testing.T) {
	probe := &interruptLineProbe{}
	vectored, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
			GroupCount: 1,
			Groups: [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig{{
				Source: 19, EnableOffset: 0x1c, StatusOffset: 0x90, ValidMask: 0x01,
			}},
		},
		probe,
	)
	check(t, err)
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets: []uint32{0x0c0c, 0x0c38, 0x0c3c},
		SDCCControllers: []QualcommSDCCControllerConfig{{
			Base: 0x0c00, GroupStatusOffset: 0x90, GroupMask: 0x01,
		}},
		NANDReady: NewStatusSignal(), VectoredInterruptController: vectored,
	})
	check(t, err)

	// Logical source 19 is reverse-packed as bank 1 bit 4. Firmware enables
	// the group child when it registers logical SDCC interrupt 70.
	check(t, device.Write(QualcommVectoredInterruptControllerBaseOffset+0x1c, Width32, 0x01))
	check(t, device.Write(QualcommVectoredInterruptControllerBaseOffset+qualcommVICEnable1Offset, Width32, 1<<4))
	check(t, device.Write(0x0c0c, Width32, 0x00000445))
	if status, err := device.Read(QualcommVectoredInterruptControllerBaseOffset+0x90, Width32); err != nil || status != 0 {
		t.Fatalf("masked SDCC grouped status = %#x error %v", status, err)
	}
	if probe.irq {
		t.Fatal("masked SDCC command timeout asserted vectored IRQ")
	}
	check(t, device.Write(0x0c3c, Width32, 0x04))
	if status, err := device.Read(QualcommVectoredInterruptControllerBaseOffset+0x90, Width32); err != nil || status != 1 {
		t.Fatalf("SDCC grouped status = %#x error %v", status, err)
	}
	if !probe.irq {
		t.Fatal("SDCC command timeout did not assert vectored IRQ")
	}
	check(t, device.Write(0x0c38, Width32, 0x04))
	if status, err := device.Read(QualcommVectoredInterruptControllerBaseOffset+0x90, Width32); err != nil || status != 0 {
		t.Fatalf("cleared SDCC grouped status = %#x error %v", status, err)
	}
	if probe.irq {
		t.Fatal("SDCC CLEAR did not deassert vectored IRQ")
	}
}

func TestQualcommBootControlProfilesInterruptWindowWritableOverrides(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		InterruptWindowWritableOffsets: []uint32{0x0904},
		NANDReady:                      NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	if value, err := device.Read(0x0904, Width32); err != nil || value != 0 {
		t.Fatalf("reset interrupt-window override = %#x error %v", value, err)
	}
	check(t, device.Write(0x0904, Width32, 0x12345678))
	if value, err := device.Read(0x0904, Width32); err != nil || value != 0x12345678 {
		t.Fatalf("interrupt-window override = %#x error %v", value, err)
	}
	if _, err := device.Read(0x0904, Width16); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("narrow interrupt-window override read error = %v", err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, restored.LoadState(state))
	if value, err := restored.Read(0x0904, Width32); err != nil || value != 0x12345678 {
		t.Fatalf("restored interrupt-window override = %#x error %v", value, err)
	}

	legacyConfig := config
	legacyConfig.InterruptWindowWritableOffsets = nil
	legacyConfig.NANDReady = NewStatusSignal()
	legacy, err := NewQualcommBootControl(legacyConfig)
	check(t, err)
	if _, err := legacy.Read(0x0904, Width32); !errors.Is(err, ErrQualcommInterruptControllerMMIO) {
		t.Fatalf("legacy INT_CLEAR_1 read error = %v", err)
	}
	if err := legacy.Write(0x0904, Width32, 0); err != nil {
		t.Fatalf("legacy INT_CLEAR_1 write error = %v", err)
	}
	if err := legacy.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched interrupt-window profile state error = %v", err)
	}

	for _, offsets := range [][]uint32{
		{0x0904, 0x0904},
		{0x0902},
		{0x08fc},
		{0x0a00},
	} {
		invalid := config
		invalid.InterruptWindowWritableOffsets = offsets
		invalid.NANDReady = NewStatusSignal()
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid interrupt-window writable offsets %#v", offsets)
		}
	}
}

func TestQualcommBootControlProfilesWritableRegisterResetValues(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets: []uint32{0x0c00},
		RegisterResets:  []QualcommBootRegisterReset{{Offset: 0x0c00, Value: 1}},
		NANDReady:       NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	value, err := device.Read(0x0c00, Width32)
	if err != nil || value != 1 {
		t.Fatalf("profiled register reset value = %#x error %v", value, err)
	}
	check(t, device.Write(0x0c00, Width32, 0x43))
	state, err := device.SaveState()
	check(t, err)
	check(t, device.Reset())
	value, err = device.Read(0x0c00, Width32)
	if err != nil || value != 1 {
		t.Fatalf("reset profiled register = %#x error %v", value, err)
	}

	restored, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, restored.LoadState(state))
	value, err = restored.Read(0x0c00, Width32)
	if err != nil || value != 0x43 {
		t.Fatalf("restored profiled register = %#x error %v", value, err)
	}

	mismatchedConfig := config
	mismatchedConfig.RegisterResets = []QualcommBootRegisterReset{{Offset: 0x0c00, Value: 2}}
	mismatched, err := NewQualcommBootControl(mismatchedConfig)
	check(t, err)
	if err := mismatched.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched register reset profile state error = %v", err)
	}

	for _, resets := range [][]QualcommBootRegisterReset{
		{{Offset: 0x0c04, Value: 1}},
		{{Offset: 0x0c00, Value: 1}, {Offset: 0x0c00, Value: 2}},
	} {
		invalid := config
		invalid.RegisterResets = resets
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid boot-control register resets %#v", resets)
		}
	}
}

func TestQualcommBootControlProfilesHalfwordOffsets(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		HalfwordOffsets: []uint32{0x4038}, NANDReady: NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, device.Write(0x4038, Width16, 0xabcd))
	value, err := device.Read(0x4038, Width16)
	if err != nil || value != 0xabcd {
		t.Fatalf("profiled halfword latch = %#x error %v", value, err)
	}
	if err := device.Write(0x4038, Width32, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("word write to halfword error = %v", err)
	}
	if err := device.Write(0x4038, Width16, 0x10000); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("oversized halfword write error = %v", err)
	}
	if _, err := device.Read(0x4038, Width32); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("word read from halfword error = %v", err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewQualcommBootControl(config)
	check(t, restored.LoadState(state))
	value, _ = restored.Read(0x4038, Width16)
	if value != 0xabcd {
		t.Fatalf("restored halfword latch = %#x", value)
	}
	unprofiledConfig := config
	unprofiledConfig.HalfwordOffsets = nil
	unprofiled, _ := NewQualcommBootControl(unprofiledConfig)
	if err := unprofiled.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched halfword profile state error = %v", err)
	}

	for _, offsets := range [][]uint32{
		{0x4038, 0x4038}, {1}, {QualcommBootControlWindowSize},
		{0x0000}, {0x0002}, {0x0900}, {0x0a40},
	} {
		invalid := config
		invalid.HalfwordOffsets = offsets
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid boot-control halfword offsets %#v", offsets)
		}
	}
	overlappingExtra := config
	overlappingExtra.WritableOffsets = []uint32{0x4038}
	if _, err := NewQualcommBootControl(overlappingExtra); err == nil {
		t.Fatal("accepted overlapping word and halfword registers")
	}
}

func TestQualcommBootControlProfilesMixedWidthOffsets(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:   []uint32{0x0e20},
		MixedWidthOffsets: []uint32{0x0e20},
		NANDReady:         NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, device.Write(0x0e20, Width32, 0xaaaa5555))
	check(t, device.Write(0x0e20, Width16, 0x1234))
	word, err := device.Read(0x0e20, Width32)
	if err != nil || word != 0xaaaa1234 {
		t.Fatalf("mixed-width word = %#x error %v", word, err)
	}
	halfword, err := device.Read(0x0e20, Width16)
	if err != nil || halfword != 0x1234 {
		t.Fatalf("mixed-width halfword = %#x error %v", halfword, err)
	}
	if err := device.Write(0x0e20, Width8, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("byte write to mixed-width register error = %v", err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewQualcommBootControl(config)
	check(t, restored.LoadState(state))
	word, _ = restored.Read(0x0e20, Width32)
	if word != 0xaaaa1234 {
		t.Fatalf("restored mixed-width word = %#x", word)
	}
	unprofiledConfig := config
	unprofiledConfig.MixedWidthOffsets = nil
	unprofiled, _ := NewQualcommBootControl(unprofiledConfig)
	if err := unprofiled.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched mixed-width profile state error = %v", err)
	}
	for _, offsets := range [][]uint32{{0x0e20, 0x0e20}, {0x0e22}, {0x0e24}, {0x0a40}} {
		invalid := config
		invalid.MixedWidthOffsets = offsets
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid mixed-width offsets %#v", offsets)
		}
	}
}

func TestQualcommBootControlProfilesByteWritableOffsets(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:     []uint32{0x3404},
		ByteWritableOffsets: []uint32{0x3404},
		NANDReady:           NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, device.Write(0x3404, Width32, 0xaaaa5555))
	check(t, device.Write(0x3404, Width8, 0x12))
	word, err := device.Read(0x3404, Width32)
	if err != nil || word != 0xaaaa5512 {
		t.Fatalf("byte-writable word = %#x error %v", word, err)
	}
	value, err := device.Read(0x3404, Width8)
	if err != nil || value != 0x12 {
		t.Fatalf("byte-writable byte = %#x error %v", value, err)
	}
	if err := device.Write(0x3404, Width16, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("halfword write to byte-writable register error = %v", err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewQualcommBootControl(config)
	check(t, restored.LoadState(state))
	word, _ = restored.Read(0x3404, Width32)
	if word != 0xaaaa5512 {
		t.Fatalf("restored byte-writable word = %#x", word)
	}
	unprofiledConfig := config
	unprofiledConfig.ByteWritableOffsets = nil
	unprofiled, _ := NewQualcommBootControl(unprofiledConfig)
	if err := unprofiled.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched byte-writable profile state error = %v", err)
	}
	for _, offsets := range [][]uint32{{0x3404, 0x3404}, {0x3402}, {0x3408}, {0x0a40}} {
		invalid := config
		invalid.ByteWritableOffsets = offsets
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid byte-writable offsets %#v", offsets)
		}
	}
}

func TestQualcommBootControlProfilesLegacyUARTControllers(t *testing.T) {
	halfwordOffsets := make([]uint32, 0, len(qualcommLegacyUARTHalfwordRegisterOffsets))
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		halfwordOffsets = append(halfwordOffsets, 0x4000+relative)
	}
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		HalfwordOffsets:       halfwordOffsets,
		LegacyUARTControllers: []uint32{0x4000},
		NANDReady:             NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, device.Write(0x4008, Width16, 0x77))
	for _, width := range []Width{Width8, Width16} {
		value, err := device.Read(0x4008, width)
		if err != nil || value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
			t.Fatalf("legacy UART status read%d = %#x error %v", width*8, value, err)
		}
	}
	check(t, device.Write(0x4014, Width16, 0x31))
	value, err := device.Read(0x4014, Width8)
	if err != nil || value != 0 {
		t.Fatalf("legacy UART idle ISR = %#x error %v", value, err)
	}
	if err := device.Write(0x400c, Width8, 'A'); err != nil {
		t.Fatalf("legacy UART transmit byte: %v", err)
	}
	if value, err := device.Read(0x400c, Width8); err != nil || value != 0 {
		t.Fatalf("legacy UART empty receive FIFO = %#x error %v", value, err)
	}
	if err := device.Write(0x400c, Width16, 'A'); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("wide legacy UART FIFO write error = %v", err)
	}
	if _, err := device.Read(0x400c, Width16); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("wide legacy UART FIFO read error = %v", err)
	}
	for _, controllers := range [][]uint32{
		{0x4000, 0x4000}, {2}, {0x08e0}, {0xffe0},
	} {
		invalid := config
		invalid.LegacyUARTControllers = controllers
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid legacy UART controllers %#v", controllers)
		}
	}
	missingRegister := config
	missingRegister.HalfwordOffsets = missingRegister.HalfwordOffsets[:len(missingRegister.HalfwordOffsets)-1]
	if _, err := NewQualcommBootControl(missingRegister); err == nil {
		t.Fatal("accepted legacy UART with missing halfword register")
	}
}

func TestQualcommBootControlProfilesMixedWidthLegacyUARTController(t *testing.T) {
	wordOffsets := make([]uint32, 0, len(qualcommLegacyUARTHalfwordRegisterOffsets))
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets = append(wordOffsets, 0x4200+relative)
	}
	wordOffsets = append(wordOffsets, 0x4200+qualcommLegacyUARTFIFOOffset)
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:       wordOffsets,
		MixedWidthOffsets:     wordOffsets,
		LegacyUARTControllers: []uint32{0x4200},
		LegacyUARTReceiveData: []QualcommLegacyUARTReceiveData{{
			Controller:         0x4200,
			InterruptSource:    55,
			DelayInstructions:  1,
			EchoTransmit:       true,
			TransmitFrameBytes: 1,
			TransmitResponse:   []byte{0x6d, 0x00},
			Data:               []byte{0x3b, 0x00},
		}},
		NANDReady: NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	if err := device.Write(0x4238, Width32, 0); err != nil {
		t.Fatalf("word-wide legacy UART configuration write: %v", err)
	}
	if err := device.Write(0x4238, Width16, 0x1234); err != nil {
		t.Fatalf("halfword legacy UART configuration write: %v", err)
	}
	if err := device.Write(0x420c, Width32, 'A'); err != nil {
		t.Fatalf("word-wide legacy UART FIFO write: %v", err)
	}
	if err := device.Write(0x420c, Width32, 0x100); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("multi-byte legacy UART FIFO write error = %v", err)
	}
	check(t, device.Write(0x4214, Width32, qualcommLegacyUARTInterruptRXStale))
	if value, err := device.Read(0x4208, Width32); err != nil ||
		value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("word-wide legacy UART pre-enable status = %#x error %v", value, err)
	}
	check(t, device.Write(0x4210, Width32, qualcommLegacyUARTCommandRXEnable))
	if value, err := device.Read(0x4208, Width32); err != nil ||
		value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("word-wide legacy UART armed status = %#x error %v", value, err)
	}
	check(t, device.Advance(1))
	if value, err := device.Read(0x4210, Width32); err != nil ||
		value != qualcommLegacyUARTInterruptRXStale {
		t.Fatalf("word-wide legacy UART masked receive status = %#x error %v", value, err)
	}
	if value, err := device.Read(0x0958, Width32); err != nil || value != 1<<23 {
		t.Fatalf("word-wide legacy UART interrupt source = %#x error %v", value, err)
	}
	value, err := device.Read(0x4208, Width32)
	if err != nil || value != qualcommLegacyUARTStatusRXReady|
		qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("word-wide legacy UART status = %#x error %v", value, err)
	}
	if value, err := device.Read(0x420c, Width32); err != nil || value != 0x3b {
		t.Fatalf("word-wide legacy UART first receive byte = %#x error %v", value, err)
	}
	state, err := device.SaveState()
	check(t, err)
	if value, err := device.Read(0x420c, Width32); err != nil || value != 0x00 {
		t.Fatalf("word-wide legacy UART second receive byte = %#x error %v", value, err)
	}
	value, err = device.Read(0x4208, Width32)
	if err != nil || value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("word-wide legacy UART drained status = %#x error %v", value, err)
	}
	if value, err := device.Read(0x0958, Width32); err != nil || value != 0 {
		t.Fatalf("word-wide legacy UART drained interrupt source = %#x error %v", value, err)
	}
	check(t, device.Write(0x420c, Width32, 0xa5))
	if value, err := device.Read(0x420c, Width32); err != nil || value != 0xa5 {
		t.Fatalf("word-wide legacy UART transmit echo = %#x error %v", value, err)
	}
	if value, err := device.Read(0x420c, Width32); err != nil || value != 0x6d {
		t.Fatalf("word-wide legacy UART transmit response status = %#x error %v", value, err)
	}
	if value, err := device.Read(0x420c, Width32); err != nil || value != 0x00 {
		t.Fatalf("word-wide legacy UART transmit response detail = %#x error %v", value, err)
	}
	if value, err := device.Read(0x0958, Width32); err != nil || value != 0 {
		t.Fatalf("word-wide legacy UART drained echo interrupt source = %#x error %v", value, err)
	}
	restored, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, restored.LoadState(state))
	if value, err := restored.Read(0x420c, Width32); err != nil || value != 0x00 {
		t.Fatalf("restored word-wide legacy UART receive byte = %#x error %v", value, err)
	}
	if value, err := device.Read(0x4214, Width32); err != nil || value != 0 {
		t.Fatalf("word-wide legacy UART idle ISR = %#x error %v", value, err)
	}
	for _, receiveData := range [][]QualcommLegacyUARTReceiveData{
		{{Controller: 0x4000, Data: []byte{1}}},
		{{Controller: 0x4200}},
		{{Controller: 0x4200, InterruptSource: 64, Data: []byte{1}}},
		{{Controller: 0x4200, Data: []byte{1}}, {Controller: 0x4200, Data: []byte{2}}},
	} {
		invalid := config
		invalid.LegacyUARTReceiveData = receiveData
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid legacy UART receive data %#v", receiveData)
		}
	}
}

func TestQualcommBootControlModelsT0CardExchange(t *testing.T) {
	wordOffsets := make([]uint32, 0, len(qualcommLegacyUARTHalfwordRegisterOffsets)+1)
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets = append(wordOffsets, 0x4100+relative)
	}
	wordOffsets = append(wordOffsets, 0x4100+qualcommLegacyUARTFIFOOffset)
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:       wordOffsets,
		MixedWidthOffsets:     wordOffsets,
		LegacyUARTControllers: []uint32{0x4100},
		LegacyUARTReceiveData: []QualcommLegacyUARTReceiveData{{
			Controller:      0x4100,
			InterruptSource: 55,
			EchoTransmit:    true,
			T0Card:          true,
			Data:            []byte{0x3b, 0x00},
		}},
		NANDReady: NewStatusSignal(),
	}
	newDevice := func() *QualcommBootControl {
		device, err := NewQualcommBootControl(config)
		check(t, err)
		return device
	}
	readByte := func(device *QualcommBootControl, want byte) {
		t.Helper()
		value, err := device.Read(0x410c, Width32)
		if err != nil || value != uint32(want) {
			t.Fatalf("T=0 receive byte = %#x error %v, want %#x", value, err, want)
		}
	}
	writeByte := func(device *QualcommBootControl, value byte) {
		t.Helper()
		if err := device.Write(0x410c, Width32, uint32(value)); err != nil {
			t.Fatalf("T=0 transmit byte %#x: %v", value, err)
		}
	}

	device := newDevice()
	check(t, device.Write(0x4114, Width32, qualcommLegacyUARTInterruptRXStale))
	check(t, device.Write(0x4110, Width32, qualcommLegacyUARTCommandRXEnable))
	readByte(device, 0x3b)
	readByte(device, 0x00)

	for _, value := range []byte{0x00, 0xa4, 0x00, 0x04, 0x02} {
		writeByte(device, value)
		readByte(device, value)
	}
	readByte(device, 0xa4)
	writeByte(device, 0x3f)
	readByte(device, 0x3f)

	state, err := device.SaveState()
	check(t, err)
	restored := newDevice()
	check(t, restored.LoadState(state))
	writeByte(restored, 0x00)
	readByte(restored, 0x00)
	readByte(restored, 0x61)
	readByte(restored, byte(len(legacyUARTT0MasterFileFCP)))

	for _, value := range []byte{0x00, 0xc0, 0x00, 0x00, byte(len(legacyUARTT0MasterFileFCP))} {
		writeByte(restored, value)
		readByte(restored, value)
	}
	readByte(restored, 0xc0)
	for _, value := range legacyUARTT0MasterFileFCP {
		readByte(restored, value)
	}
	readByte(restored, 0x90)
	readByte(restored, 0x00)

	// P2=0x0c selects the file without returning FCI/FCP data.  Samsung DC18
	// uses this form after its initial ICCID probe and expects an immediate
	// success status rather than 61xx followed by GET RESPONSE.
	for _, value := range []byte{0x00, 0xa4, 0x00, 0x0c, 0x02} {
		writeByte(restored, value)
		readByte(restored, value)
	}
	readByte(restored, 0xa4)
	for _, value := range []byte{0x3f, 0x00} {
		writeByte(restored, value)
		readByte(restored, value)
	}
	readByte(restored, 0x90)
	readByte(restored, 0x00)

	// Optional personalization EFs are absent on the minimal home-network
	// profile.  Reporting zero-filled GID/DCK files makes Samsung's SIM-lock
	// layer treat the card as a mismatching service-provider group.
	for _, value := range []byte{0x00, 0xa4, 0x00, 0x04, 0x02} {
		writeByte(restored, value)
		readByte(restored, value)
	}
	readByte(restored, 0xa4)
	for _, value := range []byte{0x6f, 0x3e} {
		writeByte(restored, value)
		readByte(restored, value)
	}
	readByte(restored, 0x6a)
	readByte(restored, 0x82)
}

func TestLegacyUARTT0SKTelecomSubscriberFilesAreCoherent(t *testing.T) {
	tests := []struct {
		file uint16
		want []byte
	}{
		{0x2fe2, []byte{0x98, 0x28, 0x50, 0x51, 0x80, 0x31, 0x10, 0x82, 0x88, 0xf8}},
		{0x6f07, []byte{0x08, 0x94, 0x05, 0x50, 0x21, 0x43, 0x65, 0x87, 0x09}},
		{0x6fad, []byte{0x00, 0x00, 0x00, 0x02}},
		{0x6f78, []byte{0x00, 0x01}},
		{0x6f7b, bytes.Repeat([]byte{0xff}, 12)},
	}
	for _, test := range tests {
		data, ok := legacyUARTT0FileData(test.file)
		if !ok || !bytes.Equal(data, test.want) {
			t.Fatalf("USIM file %#04x = %x/%t, want %x", test.file, data, ok, test.want)
		}
		if got := legacyUARTT0FileSize(test.file); got != len(test.want) {
			t.Fatalf("USIM file %#04x size = %d, want %d", test.file, got, len(test.want))
		}
		fcp := legacyUARTT0FileFCP(test.file)
		if len(fcp) < 27 || binary.BigEndian.Uint16(fcp[25:27]) != uint16(len(test.want)) {
			t.Fatalf("USIM file %#04x FCP = %x", test.file, fcp)
		}
	}
	for _, file := range []uint16{0x6f2c, 0x6f32, 0x6f3e, 0x6f3f} {
		if !legacyUARTT0FileAbsent(file) {
			t.Fatalf("optional personalization file %#04x is present", file)
		}
	}
}

func TestQualcommBootControlPublishesImmediateLegacyUARTReceiveData(t *testing.T) {
	wordOffsets := make([]uint32, 0, len(qualcommLegacyUARTHalfwordRegisterOffsets)+1)
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets = append(wordOffsets, 0x4100+relative)
	}
	wordOffsets = append(wordOffsets, 0x4100+qualcommLegacyUARTFIFOOffset)
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:       wordOffsets,
		MixedWidthOffsets:     wordOffsets,
		LegacyUARTControllers: []uint32{0x4100},
		LegacyUARTReceiveData: []QualcommLegacyUARTReceiveData{{
			Controller: 0x4100, InterruptSource: 55,
			Data: []byte{0x3b, 0x00},
		}},
		NANDReady: NewStatusSignal(),
	})
	check(t, err)
	check(t, device.Write(0x4114, Width32, qualcommLegacyUARTInterruptRXStale))
	check(t, device.Write(0x4110, Width32, qualcommLegacyUARTCommandRXEnable))
	if value, err := device.Read(0x4108, Width32); err != nil ||
		value != qualcommLegacyUARTStatusRXReady|qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("immediate legacy UART status = %#x error %v", value, err)
	}
	if value, err := device.Read(0x410c, Width32); err != nil || value != 0x3b {
		t.Fatalf("immediate legacy UART first byte = %#x error %v", value, err)
	}
}

func TestQualcommBootControlPublishesLegacyUARTReceiveDataOnProfiledCommand(t *testing.T) {
	wordOffsets := make([]uint32, 0, len(qualcommLegacyUARTHalfwordRegisterOffsets)+1)
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets = append(wordOffsets, 0x4100+relative)
	}
	wordOffsets = append(wordOffsets, 0x4100+qualcommLegacyUARTFIFOOffset)
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:       wordOffsets,
		MixedWidthOffsets:     wordOffsets,
		LegacyUARTControllers: []uint32{0x4100},
		LegacyUARTReceiveData: []QualcommLegacyUARTReceiveData{{
			Controller: 0x4100, InterruptSource: 55,
			ReceiveCommand: 0x60, Data: []byte{0x3b, 0x00},
		}},
		NANDReady: NewStatusSignal(),
	})
	check(t, err)
	check(t, device.Write(0x4110, Width32, qualcommLegacyUARTCommandRXEnable))
	if value, readErr := device.Read(0x4108, Width32); readErr != nil ||
		value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("pre-trigger legacy UART status = %#x error %v", value, readErr)
	}
	check(t, device.Write(0x4110, Width32, 0x60))
	if value, readErr := device.Read(0x4108, Width32); readErr != nil ||
		value != qualcommLegacyUARTStatusRXReady|qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("triggered legacy UART status = %#x error %v", value, readErr)
	}
}

func TestQualcommBootControlPublishesLegacyUARTReceiveAfterActivation(t *testing.T) {
	wordOffsets := make([]uint32, 0, len(qualcommLegacyUARTHalfwordRegisterOffsets)+1)
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets = append(wordOffsets, 0x4100+relative)
	}
	wordOffsets = append(wordOffsets, 0x4100+qualcommLegacyUARTFIFOOffset)
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:       wordOffsets,
		MixedWidthOffsets:     wordOffsets,
		LegacyUARTControllers: []uint32{0x4100},
		LegacyUARTReceiveData: []QualcommLegacyUARTReceiveData{{
			Controller: 0x4100, InterruptSource: 55,
			ReceiveCommand: 0x04, ActivationCommand: 0x60,
			DelayInstructions: 2, Data: []byte{0x3b, 0x00},
		}},
		NANDReady: NewStatusSignal(),
	}
	newDevice := func() *QualcommBootControl {
		device, err := NewQualcommBootControl(config)
		check(t, err)
		return device
	}

	device := newDevice()
	check(t, device.Write(0x4114, Width32, qualcommLegacyUARTInterruptRXStale))
	check(t, device.Write(0x4110, Width32, 0x04))
	if value, err := device.Read(0x4108, Width32); err != nil ||
		value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("pre-activation legacy UART status = %#x error %v", value, err)
	}

	check(t, device.Write(0x4110, Width32, 0x60))
	if value, err := device.Read(0x4108, Width32); err != nil ||
		value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("post-activation legacy UART status = %#x error %v", value, err)
	}
	check(t, device.Advance(1))

	state, err := device.SaveState()
	check(t, err)
	restored := newDevice()
	check(t, restored.LoadState(state))
	check(t, restored.Advance(1))
	check(t, restored.Write(0x4110, Width32, 0x04))
	if value, err := restored.Read(0x4108, Width32); err != nil ||
		value != qualcommLegacyUARTStatusRXReady|qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("activated legacy UART status = %#x error %v", value, err)
	}
}

func TestQualcommBootControlSupportsUARTDMReadSideFIFOAlias(t *testing.T) {
	wordOffsets := make([]uint32, 0, len(qualcommLegacyUARTHalfwordRegisterOffsets)+1)
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wordOffsets = append(wordOffsets, 0x4100+relative)
	}
	wordOffsets = append(wordOffsets, 0x4100+qualcommLegacyUARTFIFOOffset)
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:       wordOffsets,
		MixedWidthOffsets:     wordOffsets,
		LegacyUARTControllers: []uint32{0x4100},
		LegacyUARTReceiveData: []QualcommLegacyUARTReceiveData{{
			Controller: 0x4100, InterruptSource: 55,
			ReceiveFIFOOffset: 0x10, EchoTransmit: true,
			Data: []byte{0x3b, 0x00},
		}},
		NANDReady: NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, device.Write(0x4110, Width32, qualcommLegacyUARTCommandRXEnable))
	for index, want := range []uint32{0x3b, 0x00} {
		if value, readErr := device.Read(0x4110, Width32); readErr != nil || value != want {
			t.Fatalf("UARTDM receive byte %d = %#x error %v", index, value, readErr)
		}
	}
	if value, readErr := device.Read(0x4108, Width32); readErr != nil ||
		value != qualcommLegacyUARTStatusTXReady|qualcommLegacyUARTStatusTXEmpty {
		t.Fatalf("drained UARTDM status = %#x error %v", value, readErr)
	}
	check(t, device.Write(0x410c, Width32, 0xa5))
	if value, readErr := device.Read(0x4110, Width32); readErr != nil || value != 0xa5 {
		t.Fatalf("UARTDM transmit echo = %#x error %v", value, readErr)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, restored.LoadState(state))
}

func TestQualcommBootControlProfilesCompletionEvents(t *testing.T) {
	event := QualcommCompletionEventConfig{
		StartOffset:           0x0e04,
		StartMask:             1,
		StatusOffset:          0x0e24,
		StatusMask:            2,
		AcknowledgeOffset:     0x0e28,
		AcknowledgeWidth:      Width16,
		AcknowledgeMask:       0xffff,
		InterruptSource:       35,
		UseVectoredController: true,
	}
	newConfiguredDevice := func(probe *interruptLineProbe) (*QualcommBootControl, error) {
		vectored, err := NewQualcommVectoredInterruptController(
			QualcommVectoredInterruptConfig{SourceCount: 49, Bank0Sources: 25},
			probe,
		)
		if err != nil {
			return nil, err
		}
		return NewQualcommBootControl(QualcommBootControlConfig{
			HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
			EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
			WritableOffsets:             []uint32{0x0e04},
			HalfwordOffsets:             []uint32{0x0e28},
			CompletionEvents:            []QualcommCompletionEventConfig{event},
			NANDReady:                   NewStatusSignal(),
			VectoredInterruptController: vectored,
		})
	}

	probe := &interruptLineProbe{}
	device, err := newConfiguredDevice(probe)
	check(t, err)
	check(t, device.Write(0x0434, Width32, 1<<10))
	check(t, device.Write(0x0e04, Width32, 0))
	if status, _ := device.Read(0x0e24, Width32); status != 0 || probe.irq {
		t.Fatalf("inactive completion status = %#x IRQ=%v", status, probe.irq)
	}
	check(t, device.Write(0x0e04, Width32, 1))
	if status, readErr := device.Read(0x0e24, Width32); readErr != nil || status != 2 {
		t.Fatalf("completion status = %#x error %v", status, readErr)
	}
	if !probe.irq {
		t.Fatal("enabled completion event did not drive the vectored IRQ")
	}
	if banks := device.vectoredInterruptController.PendingStatusBanks(); banks[1] != 1<<10 {
		t.Fatalf("completion interrupt banks = %#v", banks)
	}
	if err := device.Write(0x0e24, Width32, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("completion status write error = %v", err)
	}
	if err := device.Write(0x0e28, Width32, 0xffff); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("wrong-width completion acknowledge error = %v", err)
	}
	if status, _ := device.Read(0x0e24, Width32); status != 2 {
		t.Fatalf("wrong-width acknowledge cleared status = %#x", status)
	}

	state, err := device.SaveState()
	check(t, err)
	restoredProbe := &interruptLineProbe{}
	restored, err := newConfiguredDevice(restoredProbe)
	check(t, err)
	check(t, restored.LoadState(state))
	if status, _ := restored.Read(0x0e24, Width32); status != 2 || !restoredProbe.irq {
		t.Fatalf("restored completion status = %#x IRQ=%v", status, restoredProbe.irq)
	}

	mismatchEvent := event
	mismatchEvent.StatusMask = 4
	mismatchVIC, _ := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{SourceCount: 49, Bank0Sources: 25},
		&interruptLineProbe{},
	)
	mismatch, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:             []uint32{0x0e04},
		HalfwordOffsets:             []uint32{0x0e28},
		CompletionEvents:            []QualcommCompletionEventConfig{mismatchEvent},
		NANDReady:                   NewStatusSignal(),
		VectoredInterruptController: mismatchVIC,
	})
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched completion profile state error = %v", err)
	}

	check(t, device.Write(0x0e28, Width16, 0xffff))
	if status, _ := device.Read(0x0e24, Width32); status != 0 {
		t.Fatalf("acknowledged completion status = %#x", status)
	}
	if !probe.irq {
		t.Fatal("device acknowledge unexpectedly cleared the separate VIC latch")
	}
	check(t, device.Write(0x0404, Width32, 1<<10))
	if probe.irq {
		t.Fatal("VIC acknowledge left completion IRQ asserted")
	}
}

func TestQualcommBootControlDispatchesCompletionHandlersOutsideWrite(t *testing.T) {
	event := QualcommCompletionEventConfig{
		StartOffset: 0x0e04, StartMask: 1,
		StatusOffset: 0x0e24, StatusMask: 2,
		AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff,
		InterruptSource: 5,
	}
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:  []uint32{0x0e04, 0x0e08},
		HalfwordOffsets:  []uint32{0x0e28},
		CompletionEvents: []QualcommCompletionEventConfig{event},
		NANDReady:        NewStatusSignal(),
	})
	check(t, err)
	handler := &qualcommCompletionHandlerProbe{}
	check(t, device.AttachCompletionHandler(event.StartOffset, handler))
	if handler.resets != 1 {
		t.Fatalf("attach resets = %d", handler.resets)
	}
	check(t, device.Write(0x0e08, Width32, 0x12345678))
	check(t, device.Write(0x0e04, Width32, 1))
	if handler.queued != 1 || handler.pointer != 0x12345678 || handler.advances != 0 {
		t.Fatalf("handler after kickoff = %+v", handler)
	}
	check(t, device.Advance(0))
	if handler.advances != 1 {
		t.Fatalf("handler advances = %d", handler.advances)
	}
	check(t, device.Reset())
	if handler.resets != 2 {
		t.Fatalf("handler resets = %d", handler.resets)
	}
}

func TestQualcommBootControlRejectsInvalidCompletionHandlers(t *testing.T) {
	event := QualcommCompletionEventConfig{
		StartOffset: 0x0e04, StartMask: 1,
		StatusOffset: 0x0e24, StatusMask: 2,
		AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff,
		InterruptSource: 5,
	}
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:  []uint32{0x0e04, 0x0e08},
		HalfwordOffsets:  []uint32{0x0e28},
		CompletionEvents: []QualcommCompletionEventConfig{event},
		NANDReady:        NewStatusSignal(),
	})
	check(t, err)
	if err := device.AttachCompletionHandler(event.StartOffset, nil); err == nil {
		t.Fatal("accepted nil completion handler")
	}
	if err := device.AttachCompletionHandler(0x0e08, &qualcommCompletionHandlerProbe{}); err == nil {
		t.Fatal("accepted handler for unprofiled completion event")
	}
	handler := &qualcommCompletionHandlerProbe{}
	check(t, device.AttachCompletionHandler(event.StartOffset, handler))
	if err := device.AttachCompletionHandler(event.StartOffset, &qualcommCompletionHandlerProbe{}); err == nil {
		t.Fatal("accepted duplicate completion handler")
	}
}

func TestQualcommBootControlRollsBackRejectedCompletion(t *testing.T) {
	event := QualcommCompletionEventConfig{
		StartOffset: 0x0e04, StartMask: 1,
		StatusOffset: 0x0e24, StatusMask: 2,
		AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff,
		InterruptSource: 5,
	}
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:  []uint32{0x0e04, 0x0e08},
		HalfwordOffsets:  []uint32{0x0e28},
		CompletionEvents: []QualcommCompletionEventConfig{event},
		NANDReady:        NewStatusSignal(),
	})
	check(t, err)
	rejected := errors.New("rejected command list")
	handler := &qualcommCompletionHandlerProbe{queueErr: rejected}
	check(t, device.AttachCompletionHandler(event.StartOffset, handler))
	if err := device.Write(event.StartOffset, Width32, 1); !errors.Is(err, rejected) {
		t.Fatalf("kickoff error = %v", err)
	}
	if start, _ := device.Read(event.StartOffset, Width32); start != 0 {
		t.Fatalf("rejected start register = %#x", start)
	}
	if status, _ := device.Read(event.StatusOffset, Width32); status != 0 {
		t.Fatalf("rejected completion status = %#x", status)
	}
}

type qualcommCompletionHandlerProbe struct {
	queued   int
	pointer  uint32
	advances int
	resets   int
	queueErr error
}

func (p *qualcommCompletionHandlerProbe) QueueCompletion(
	registerValue func(offset uint32) (uint32, bool),
) error {
	p.queued++
	p.pointer, _ = registerValue(0x0e08)
	return p.queueErr
}

func (p *qualcommCompletionHandlerProbe) Advance(uint64) error {
	p.advances++
	return nil
}

func (p *qualcommCompletionHandlerProbe) Reset() error {
	p.resets++
	return nil
}

func TestQualcommBootControlRejectsInvalidCompletionEvents(t *testing.T) {
	validEvent := QualcommCompletionEventConfig{
		StartOffset: 0x0e04, StartMask: 1,
		StatusOffset: 0x0e24, StatusMask: 2,
		AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff,
		InterruptSource: 35, UseVectoredController: true,
	}
	newVIC := func() *QualcommVectoredInterruptController {
		device, _ := NewQualcommVectoredInterruptController(
			QualcommVectoredInterruptConfig{SourceCount: 49, Bank0Sources: 25},
			nil,
		)
		return device
	}
	base := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:             []uint32{0x0e04},
		HalfwordOffsets:             []uint32{0x0e28},
		CompletionEvents:            []QualcommCompletionEventConfig{validEvent},
		NANDReady:                   NewStatusSignal(),
		VectoredInterruptController: newVIC(),
	}
	invalidEvents := []QualcommCompletionEventConfig{
		{StartOffset: 0x0e08, StartMask: 1, StatusOffset: 0x0e24, StatusMask: 2, AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff, InterruptSource: 35, UseVectoredController: true},
		{StartOffset: 0x0e04, StartMask: 1, StatusOffset: 0x0e04, StatusMask: 2, AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff, InterruptSource: 35, UseVectoredController: true},
		{StartOffset: 0x0e04, StartMask: 1, StatusOffset: 0x0e24, StatusMask: 2, AcknowledgeOffset: 0x0e04, AcknowledgeWidth: Width32, AcknowledgeMask: 1, InterruptSource: 35, UseVectoredController: true},
		{StartOffset: 0x0e04, StartMask: 1, StatusOffset: QualcommBootControlWindowSize, StatusMask: 2, AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff, InterruptSource: 35, UseVectoredController: true},
		{StartOffset: 0x0e04, StartMask: 1, StatusOffset: 0x0e24, StatusMask: 2, AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff, InterruptSource: 49, UseVectoredController: true},
	}
	for _, event := range invalidEvents {
		config := base
		config.CompletionEvents = []QualcommCompletionEventConfig{event}
		if _, err := NewQualcommBootControl(config); err == nil {
			t.Fatalf("accepted invalid completion event %+v", event)
		}
	}
	withoutVIC := base
	withoutVIC.VectoredInterruptController = nil
	if _, err := NewQualcommBootControl(withoutVIC); err == nil {
		t.Fatal("accepted vectored completion event without a vectored controller")
	}
}

func TestQualcommBootControlProfilesReadOnlyRegisters(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		ReadOnlyRegisters: []QualcommBootReadOnlyRegister{{Offset: 0x00bc, Value: 0x12345678}},
		NANDReady:         NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	value, err := device.Read(0x00bc, Width32)
	if err != nil || value != 0x12345678 {
		t.Fatalf("profiled read-only register = %#x error %v", value, err)
	}
	if err := device.Write(0x00bc, Width32, 0); !errors.Is(err, ErrQualcommBootControlMMIO) {
		t.Fatalf("read-only register write error = %v", err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewQualcommBootControl(config)
	check(t, restored.LoadState(state))
	mismatchConfig := config
	mismatchConfig.ReadOnlyRegisters = []QualcommBootReadOnlyRegister{{Offset: 0x00bc, Value: 1}}
	mismatch, _ := NewQualcommBootControl(mismatchConfig)
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched read-only profile state error = %v", err)
	}

	for _, registers := range [][]QualcommBootReadOnlyRegister{
		{{Offset: 0x00bc}, {Offset: 0x00bc}},
		{{Offset: 2}},
		{{Offset: QualcommBootControlWindowSize}},
		{{Offset: 0x0000}},
		{{Offset: 0x0900}},
		{{Offset: 0x0a40}},
	} {
		invalid := config
		invalid.ReadOnlyRegisters = registers
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid boot-control read-only registers %#v", registers)
		}
	}
	overlappingExtra := config
	overlappingExtra.WritableOffsets = []uint32{0x00bc}
	if _, err := NewQualcommBootControl(overlappingExtra); err == nil {
		t.Fatal("accepted overlapping writable and read-only registers")
	}
}

func TestQualcommBootControlSubsetStateAddsReadOnlyRegister(t *testing.T) {
	baseConfig := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets: []uint32{0x0e04},
		NANDReady:       NewStatusSignal(),
	}
	base, err := NewQualcommBootControl(baseConfig)
	check(t, err)
	check(t, base.Write(0x0e04, Width32, 0x12345678))
	state, err := base.SaveState()
	check(t, err)

	extendedConfig := baseConfig
	extendedConfig.NANDReady = NewStatusSignal()
	extendedConfig.ReadOnlyRegisters = []QualcommBootReadOnlyRegister{{Offset: 0x0e14, Value: 7}}
	extended, err := NewQualcommBootControl(extendedConfig)
	check(t, err)
	if err := extended.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("exact load with added read-only register error = %v", err)
	}
	check(t, extended.LoadStateSubset(state))
	if value, err := extended.Read(0x0e04, Width32); err != nil || value != 0x12345678 {
		t.Fatalf("restored writable register = %#x error %v", value, err)
	}
	if value, err := extended.Read(0x0e14, Width32); err != nil || value != 7 {
		t.Fatalf("added read-only register = %#x error %v", value, err)
	}

	writableConfig := baseConfig
	writableConfig.NANDReady = NewStatusSignal()
	writableConfig.WritableOffsets = []uint32{0x0e04, 0x0e14}
	writable, err := NewQualcommBootControl(writableConfig)
	check(t, err)
	if err := writable.LoadStateSubset(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("subset load with added writable register error = %v", err)
	}

	resetWritableConfig := baseConfig
	resetWritableConfig.NANDReady = NewStatusSignal()
	resetWritableConfig.WritableOffsets = []uint32{0x0e04, 0x0e14}
	resetWritableConfig.RegisterResets = []QualcommBootRegisterReset{{Offset: 0x0e14, Value: 7}}
	resetWritable, err := NewQualcommBootControl(resetWritableConfig)
	check(t, err)
	if err := resetWritable.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("exact load with added reset writable register error = %v", err)
	}
	check(t, resetWritable.LoadStateSubset(state))
	if value, err := resetWritable.Read(0x0e14, Width32); err != nil || value != 7 {
		t.Fatalf("added reset writable register = %#x error %v", value, err)
	}
}

func TestQualcommBootControlProfiledSBICompletesAndClearsStatus(t *testing.T) {
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		SBIControllers: []uint32{0x5000},
		SBIReadResponses: []QualcommSBIReadResponse{{
			Controller: 0x5000, Address: 0x02, Value: 0xa5,
		}},
		SBICompletionStatus: 0x0494,
		NANDReady:           NewStatusSignal(),
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	status, err := device.Read(0x5014, Width32)
	if err != nil || status != 0 {
		t.Fatalf("reset SBI status = %#x error %v", status, err)
	}
	check(t, device.Write(0x5008, Width32, 0x01020000))
	state, err := device.SaveState()
	check(t, err)
	// Version 17 continued with the writable-register block immediately after
	// the empty register-reset list. Keep accepting those snapshots while newer
	// versions insert SBI response, UART, and grouped-status identities there and
	// append the periodic-tick state at the end.
	const legacySBIResponseBlockOffset = 75
	stateWithoutPeriodicTick := state[:len(state)-16]
	legacyState := append([]byte(nil), stateWithoutPeriodicTick[:legacySBIResponseBlockOffset]...)
	const newerIdentityBlocks = 10 + 4 + 4 // one SBI response, empty UART/group lists
	legacyState = append(legacyState, stateWithoutPeriodicTick[legacySBIResponseBlockOffset+newerIdentityBlocks:]...)
	binary.LittleEndian.PutUint32(legacyState[4:8], 17)
	legacyRestored, _ := NewQualcommBootControl(config)
	if err := legacyRestored.LoadState(legacyState); err != nil {
		t.Fatalf("load version 17 SBI state: %v", err)
	}
	restored, _ := NewQualcommBootControl(config)
	check(t, restored.LoadState(state))
	status, err = restored.Read(0x0494, Width32)
	if err != nil || status != qualcommBootSBICompleteStatus {
		t.Fatalf("completed SBI status = %#x error %v", status, err)
	}
	status, _ = restored.Read(0x0494, Width32)
	if status != 0 {
		t.Fatalf("cleared SBI status = %#x", status)
	}
	result, err := restored.Read(0x5010, Width32)
	if err != nil || result != 0xa5 {
		t.Fatalf("SBI result = %#x error %v", result, err)
	}
	check(t, restored.Write(0x5008, Width32, 0x01030000))
	if result, err = restored.Read(0x5010, Width32); err != nil || result != 0 {
		t.Fatalf("unprofiled SBI read result = %#x error %v", result, err)
	}
	unprofiled := config
	unprofiled.SBIControllers = nil
	unprofiled.SBIReadResponses = nil
	unprofiled.SBICompletionStatus = 0
	withoutSBI, _ := NewQualcommBootControl(unprofiled)
	if err := withoutSBI.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched SBI profile state error = %v", err)
	}
	for _, controllers := range [][]uint32{
		{0x5000, 0x5000}, {2}, {QualcommBootControlWindowSize - 4},
	} {
		invalid := config
		invalid.SBIControllers = controllers
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid SBI controllers %#v", controllers)
		}
	}
	collision := config
	collision.WritableOffsets = []uint32{0x5008}
	if _, err := NewQualcommBootControl(collision); err == nil {
		t.Fatal("accepted overlapping SBI and compatibility register")
	}
	missingCompletion := config
	missingCompletion.SBICompletionStatus = 0
	if _, err := NewQualcommBootControl(missingCompletion); err == nil {
		t.Fatal("accepted SBI controllers without completion status")
	}
	for _, responses := range [][]QualcommSBIReadResponse{
		{{Controller: 0x5100, Address: 0x02, Value: 1}},
		{
			{Controller: 0x5000, Address: 0x02, Value: 1},
			{Controller: 0x5000, Address: 0x02, Value: 2},
		},
	} {
		invalid := config
		invalid.SBIReadResponses = responses
		if _, err := NewQualcommBootControl(invalid); err == nil {
			t.Fatalf("accepted invalid SBI read responses %#v", responses)
		}
	}
	mismatched := config
	mismatched.SBIReadResponses = []QualcommSBIReadResponse{{
		Controller: 0x5000, Address: 0x02, Value: 0xa4,
	}}
	mismatchedDevice, err := NewQualcommBootControl(mismatched)
	check(t, err)
	if err := mismatchedDevice.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched SBI response profile state error = %v", err)
	}
}

func TestQualcommBootControlRoutesSBICompletionThroughVectoredGroup(t *testing.T) {
	vectored, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
			GroupCount: 1,
			Groups: [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig{{
				Source: 7, EnableOffset: 0x20, StatusOffset: 0x94, ValidMask: 0x0f,
			}},
		},
		&interruptLineProbe{},
	)
	check(t, err)
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		SBIControllers:              []uint32{0x5000},
		SBICompletionStatus:         0x0494,
		NANDReady:                   NewStatusSignal(),
		VectoredInterruptController: vectored,
	})
	check(t, err)

	// Preserve an unrelated child while the SBI transaction asserts and then
	// read-clears only completion bit zero in the overlapping group-status word.
	check(t, vectored.SetGroupedSource(0x94, 0x02, true))
	check(t, device.Write(0x5008, Width32, 0x01010000))
	if status, err := device.Read(0x0494, Width32); err != nil || status != 0x03 {
		t.Fatalf("SBI grouped completion status = %#x error %v", status, err)
	}
	if status, err := device.Read(0x0494, Width32); err != nil || status != 0x02 {
		t.Fatalf("cleared SBI grouped completion status = %#x error %v", status, err)
	}
}

func TestQualcommBootControlAdvancesClockedTimeTickAndPulsesProfileSource(t *testing.T) {
	probe := &interruptLineProbe{}
	controller := NewQualcommInterruptController(probe)
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady: NewStatusSignal(), InterruptController: controller,
		TimeTickClock: &QualcommTimeTickClockConfig{
			InstructionsPerSecond: 10, TimeTickHz: 3, InterruptSource: 5,
		},
	})
	check(t, err)
	check(t, controller.Write(qualcommIRQEnable0Offset, Width32, 1<<5))
	first, _ := device.Read(0x5408, Width32)
	stable, _ := device.Read(0x5408, Width32)
	if first != 0 || stable != first {
		t.Fatalf("clocked timetick reads = %#x/%#x", first, stable)
	}
	check(t, device.Write(0x54c4, Width32, 2))
	if ready, _ := device.Read(0x54c0, Width32); ready != 1 {
		t.Fatalf("new match ready status = %#x, want accepted", ready)
	}
	check(t, device.Advance(3))
	if ready, _ := device.Read(0x54c0, Width32); ready != 1 {
		t.Fatalf("advanced match ready status = %#x", ready)
	}
	if tick, _ := device.Read(0x5408, Width32); tick != 0 {
		t.Fatalf("fractional tick = %#x", tick)
	}
	check(t, device.Advance(1))
	if tick, _ := device.Read(0x5408, Width32); tick != 1 || probe.irq {
		t.Fatalf("pre-match tick/IRQ = %#x/%v", tick, probe.irq)
	}
	check(t, device.Advance(4))
	if tick, _ := device.Read(0x5408, Width32); tick != 2 || !probe.irq || probe.fiq {
		t.Fatalf("matched tick outputs = %#x IRQ=%v FIQ=%v", tick, probe.irq, probe.fiq)
	}
	state, err := device.SaveState()
	check(t, err)
	restoredController := NewQualcommInterruptController(&interruptLineProbe{})
	restored, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady: NewStatusSignal(), InterruptController: restoredController,
		TimeTickClock: &QualcommTimeTickClockConfig{
			InstructionsPerSecond: 10, TimeTickHz: 3, InterruptSource: 5,
		},
	})
	check(t, err)
	check(t, restored.LoadState(state))
	if tick, _ := restored.Read(0x5408, Width32); tick != 2 {
		t.Fatalf("restored clocked tick = %#x", tick)
	}
	mismatch, _ := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady: NewStatusSignal(), InterruptController: NewQualcommInterruptController(nil),
		TimeTickClock: &QualcommTimeTickClockConfig{
			InstructionsPerSecond: 10, TimeTickHz: 2, InterruptSource: 5,
		},
	})
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched timetick clock state error = %v", err)
	}
}

func TestQualcommBootControlRoutesClockedTimeTickThroughVectoredSource(t *testing.T) {
	probe := &interruptLineProbe{}
	config := QualcommVectoredInterruptConfig{
		SourceCount: 49, Bank0Sources: 25,
		ReverseSourceOrder: true,
	}
	vectored, err := NewQualcommVectoredInterruptController(config, probe)
	check(t, err)
	device, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady:                   NewStatusSignal(),
		VectoredInterruptController: vectored,
		TimeTickClock: &QualcommTimeTickClockConfig{
			InstructionsPerSecond: 1,
			TimeTickHz:            1,
			InterruptSource:       21,
			UseVectoredController: true,
		},
	})
	check(t, err)
	// Source 21 is packed as raw bit 27: second bank bit 2. Its hardware
	// vector is 27, which the SCH-W830 firmware maps back to logical source
	// 48-27 = 21.
	check(t, vectored.Write(qualcommVICEnable1Offset, Width32, 1<<2))
	check(t, device.Write(0x54c4, Width32, 1))
	check(t, device.Advance(1))
	if !probe.irq || probe.fiq {
		t.Fatalf("vectored timetick outputs IRQ=%v FIQ=%v", probe.irq, probe.fiq)
	}
	if status, readErr := vectored.Read(qualcommVICStatus1Offset, Width32); readErr != nil || status != 1<<2 {
		t.Fatalf("vectored timetick status = %#x error %v", status, readErr)
	}
	if vector, readErr := vectored.Read(qualcommVICVectorReadOffset, Width32); readErr != nil || vector != 27 {
		t.Fatalf("vectored timetick vector = %#x error %v", vector, readErr)
	}
	state, err := device.SaveState()
	check(t, err)
	mismatchVIC, _ := NewQualcommVectoredInterruptController(config, nil)
	mismatch, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady:                   NewStatusSignal(),
		VectoredInterruptController: mismatchVIC,
		TimeTickClock: &QualcommTimeTickClockConfig{
			InstructionsPerSecond: 1, TimeTickHz: 1, InterruptSource: 21,
		},
	})
	check(t, err)
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched timetick route state error = %v", err)
	}
}

func TestQualcommBootControlRaisesPeriodicLegacyTimeTickWithoutMatch(t *testing.T) {
	probe := &interruptLineProbe{}
	controller := NewQualcommInterruptController(probe)
	config := QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady: NewStatusSignal(), InterruptController: controller,
		TimeTickClock: &QualcommTimeTickClockConfig{
			InstructionsPerSecond: 1_000,
			TimeTickHz:            100,
			PeriodicInterruptHz:   10,
			InterruptSource:       2,
		},
	}
	device, err := NewQualcommBootControl(config)
	check(t, err)
	check(t, controller.Write(qualcommIRQEnable0Offset, Width32, 1<<2))
	check(t, device.Advance(99))
	if probe.irq {
		t.Fatal("periodic timetick fired before its first period")
	}
	check(t, device.Advance(1))
	if !probe.irq || probe.fiq {
		t.Fatalf("periodic timetick outputs IRQ=%v FIQ=%v", probe.irq, probe.fiq)
	}
	if tick, readErr := device.Read(0x5408, Width32); readErr != nil || tick != 10 {
		t.Fatalf("periodic timetick counter = %#x error %v", tick, readErr)
	}
	if status, readErr := controller.Read(qualcommInterruptStatus0Offset, Width32); readErr != nil || status != 1<<2 {
		t.Fatalf("periodic timetick status = %#x error %v", status, readErr)
	}

	state, err := device.SaveState()
	check(t, err)
	restoredController := NewQualcommInterruptController(&interruptLineProbe{})
	restoredConfig := config
	restoredConfig.InterruptController = restoredController
	restored, err := NewQualcommBootControl(restoredConfig)
	check(t, err)
	check(t, restored.LoadState(state))
	check(t, restoredController.Write(qualcommInterruptClear0Offset, Width32, 1<<2))
	check(t, restored.Advance(99))
	if status, _ := restoredController.Read(qualcommInterruptStatus0Offset, Width32); status != 0 {
		t.Fatalf("restored periodic timetick fired early: %#x", status)
	}
	if tick, readErr := restored.Read(0x5408, Width32); readErr != nil || tick != 19 {
		t.Fatalf("restored periodic timetick counter = %#x error %v", tick, readErr)
	}
	check(t, restored.Advance(1))
	if status, _ := restoredController.Read(qualcommInterruptStatus0Offset, Width32); status != 1<<2 {
		t.Fatalf("restored periodic timetick status = %#x", status)
	}
	if tick, readErr := restored.Read(0x5408, Width32); readErr != nil || tick != 20 {
		t.Fatalf("restored completed periodic timetick counter = %#x error %v", tick, readErr)
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
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0x5680, NANDReady: NewStatusSignal(), TimeTickClock: &QualcommTimeTickClockConfig{}},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0x5680, NANDReady: NewStatusSignal(), TimeTickClock: &QualcommTimeTickClockConfig{InstructionsPerSecond: 10, TimeTickHz: 11}},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0x5680, NANDReady: NewStatusSignal(), TimeTickClock: &QualcommTimeTickClockConfig{InstructionsPerSecond: 10, TimeTickHz: 1, InterruptSource: 64}},
		{HardwareRevision: 0x10000000, NANDInterfaceMode: 2, EBIMemoryConfiguration: 0x5680, NANDReady: NewStatusSignal(), TimeTickClock: &QualcommTimeTickClockConfig{InstructionsPerSecond: 10, TimeTickHz: 1, InterruptSource: 5, UseVectoredController: true}},
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
	check(t, err)
	check(t, device.Write(0x0380, Width32, 2))
	check(t, device.Reset())
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
	check(t, device.Write(0x0430, Width32, 0x2d))
	check(t, device.Write(0x0434, Width32, 4))
	check(t, device.Write(0x0408, Width32, 0x11223344))
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
	check(t, err)
	restored := NewQualcommSecondaryClockControl()
	check(t, restored.LoadState(state))
	value, _ = restored.Read(0x0434, Width32)
	if value != 4 {
		t.Fatalf("restored secondary data = %#x", value)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated secondary clock state error = %v", err)
	}
}

func TestQualcommSecondaryClockControlProfilesAdditionalWritableOffsets(t *testing.T) {
	device, err := NewQualcommSecondaryClockControlWithWritableOffsets([]uint32{0x040c})
	check(t, err)
	check(t, device.Write(0x040c, Width32, 0x12345678))
	value, err := device.Read(0x040c, Width32)
	if err != nil || value != 0x12345678 {
		t.Fatalf("profiled secondary-clock latch = %#x error %v", value, err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, err := NewQualcommSecondaryClockControlWithWritableOffsets([]uint32{0x040c})
	check(t, err)
	check(t, restored.LoadState(state))
	value, err = restored.Read(0x040c, Width32)
	if err != nil || value != 0x12345678 {
		t.Fatalf("restored profiled secondary-clock latch = %#x error %v", value, err)
	}
	if err := NewQualcommSecondaryClockControl().LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched secondary-clock profile state error = %v", err)
	}

	for _, offsets := range [][]uint32{
		{0x0400},
		{0x040c, 0x040c},
		{0x040e},
		{qualcommSecondaryClockDisabledStatusOffset},
		{QualcommSecondaryClockWindowSize},
	} {
		if _, err := NewQualcommSecondaryClockControlWithWritableOffsets(offsets); err == nil {
			t.Fatalf("accepted invalid secondary-clock writable offsets %#v", offsets)
		}
	}
}

func TestQualcommSecondaryClockControlProfilesReadOnlyInput(t *testing.T) {
	device, err := NewQualcommSecondaryClockControlWithConfig(QualcommSecondaryClockConfig{
		WritableOffsets: []uint32{0x040c},
		ReadOnlyRegisters: []QualcommSecondaryClockReadOnlyRegister{
			{Offset: qualcommSecondaryClockDisabledStatusOffset, Value: 0x00000004},
			{Offset: 0x0444, Value: 0x00000400},
		},
	})
	check(t, err)
	value, err := device.Read(0x0444, Width32)
	if err != nil || value != 0x00000400 {
		t.Fatalf("secondary clock input = %#x error %v", value, err)
	}
	if err := device.Write(0x0444, Width32, 0); !errors.Is(err, ErrQualcommSecondaryClockMMIO) {
		t.Fatalf("secondary clock input write error = %v", err)
	}
	value, err = device.Read(qualcommSecondaryClockDisabledStatusOffset, Width32)
	if err != nil || value != 0x00000004 {
		t.Fatalf("profiled secondary status = %#x error %v", value, err)
	}
}

func TestQualcommSecondaryClockControlRejectsInvalidReadOnlyInputs(t *testing.T) {
	for _, registers := range [][]QualcommSecondaryClockReadOnlyRegister{
		{{Offset: 0x0441}},
		{{Offset: QualcommSecondaryClockWindowSize}},
		{{Offset: 0x0400}},
		{{Offset: 0x0444}, {Offset: 0x0444}},
	} {
		if _, err := NewQualcommSecondaryClockControlWithConfig(QualcommSecondaryClockConfig{
			ReadOnlyRegisters: registers,
		}); err == nil {
			t.Fatalf("accepted secondary-clock read-only registers %+v", registers)
		}
	}
	if _, err := NewQualcommSecondaryClockControlWithConfig(QualcommSecondaryClockConfig{
		WritableOffsets: []uint32{0x0444},
		ReadOnlyRegisters: []QualcommSecondaryClockReadOnlyRegister{{
			Offset: 0x0444,
		}},
	}); err == nil {
		t.Fatal("accepted overlapping secondary-clock writable and read-only registers")
	}
}
