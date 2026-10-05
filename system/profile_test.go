package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestSCHW830BoardProfileAppliesEvidenceBackedIRAM(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	check(t, profile.Validate())
	if len(profile.HLECalls) != 0 {
		t.Fatalf("SCH-W830 unexpectedly enables failure-path HLE calls: %+v", profile.HLECalls)
	}
	if profile.NANDReadID != 0xecba {
		t.Fatalf("SCH-W830 NAND read ID = %#x", profile.NANDReadID)
	}
	if profile.NANDSize != 0x10000000 {
		t.Fatalf("SCH-W830 NAND size = %#x", profile.NANDSize)
	}
	if profile.NANDPageSize != 0x800 || profile.NANDEraseBlockSize != 0x20000 {
		t.Fatalf("SCH-W830 NAND geometry = %#x/%#x", profile.NANDPageSize, profile.NANDEraseBlockSize)
	}
	if len(profile.NANDFactoryBadBlocks) != 0 {
		t.Fatalf("SCH-W830 factory bad blocks = %#v", profile.NANDFactoryBadBlocks)
	}
	if want := []FlashSeed{{
		Offset: 0x097c0000,
		Data:   []byte{0xff, 0xfe, 0xaf, 0xbe},
	}}; !reflect.DeepEqual(profile.NANDInitialData, want) {
		t.Fatalf("SCH-W830 NAND initial data = %#v", profile.NANDInitialData)
	}
	if profile.PrimaryClockStatus != 0x1f || profile.PrimaryClockInputMask != 0x1f {
		t.Fatalf("SCH-W830 primary clock status = %#x", profile.PrimaryClockStatus)
	}
	if want := []QualcommPrimaryClockKeyProfile{{
		ID: "end", InputLine: 4, ActiveLow: true,
	}}; !reflect.DeepEqual(profile.PrimaryClockKeys, want) {
		t.Fatalf("SCH-W830 primary-clock keys = %#v, want %#v", profile.PrimaryClockKeys, want)
	}
	if profile.BootClockModeStatus != 1 {
		t.Fatalf("SCH-W830 boot clock mode status = %#x", profile.BootClockModeStatus)
	}
	if want := []uint32{
		0x0008,
		0x00bc, 0x00c0,
		0x0204,
		0x058c, 0x0590, 0x059c,
		0x05a0, 0x05a4, 0x05b0, 0x05b4, 0x05b8,
		0x05c4, 0x05c8, 0x05cc, 0x05d8,
		0x0a34,
		0x0a54, 0x0a58,
		0x0b34,
		0x0c00, 0x0c04, 0x0c08, 0x0c0c, 0x0c2c, 0x0c38, 0x0c3c, 0x0c40,
		0x0e00, 0x0e04, 0x0e08, 0x0e10, 0x0e1c, 0x0e20, 0x0e38, 0x0e3c, 0x0e40,
		0x200c,
		0x2840,
		0x4100, 0x4104, 0x4108,
		0x4110, 0x4114, 0x4118, 0x411c, 0x4120,
		0x4128, 0x412c, 0x4130, 0x4134, 0x4138, 0x413c,
		0x423c,
		0x4600, 0x4604, 0x4614,
		0x533c,
	}; !reflect.DeepEqual(profile.BootControlWritableOffsets, want) {
		t.Fatalf("SCH-W830 boot-control writable offsets = %#v", profile.BootControlWritableOffsets)
	}
	if want := []QualcommBootRegisterReset{
		{Offset: 0x0204, Value: 0},
		{Offset: 0x0a00, Value: 1},
		{Offset: 0x0c00, Value: 1},
	}; !reflect.DeepEqual(profile.BootControlRegisterResets, want) {
		t.Fatalf("SCH-W830 boot-control register resets = %#v", profile.BootControlRegisterResets)
	}
	if want := []QualcommBootReadOnlyRegister{
		{Offset: 0x048c, Value: 0},
		{Offset: 0x0c34, Value: 0},
		{Offset: 0x0e14, Value: 0},
	}; !reflect.DeepEqual(profile.BootControlReadOnlyRegisters, want) {
		t.Fatalf(
			"SCH-W830 boot-control read-only registers = %#v",
			profile.BootControlReadOnlyRegisters,
		)
	}
	if want := []uint32{0x5000, 0x5100, 0x5200}; !reflect.DeepEqual(profile.BootControlSBIControllers, want) {
		t.Fatalf("SCH-W830 boot-control SBI controllers = %#v", profile.BootControlSBIControllers)
	}
	if want := []QualcommSBIReadResponse{
		{Controller: 0x5100, Address: 0x4f, Value: 0xc1},
		{Controller: 0x5100, Address: 0x53, Value: 0xff},
		{Controller: 0x5100, Address: 0x54, Value: 0x01},
	}; !reflect.DeepEqual(profile.BootControlSBIReadResponses, want) {
		t.Fatalf("SCH-W830 boot-control SBI read responses = %#v", profile.BootControlSBIReadResponses)
	}
	if profile.BootControlSBICompletionStatus != 0x0494 {
		t.Fatalf(
			"SCH-W830 boot-control SBI completion status = %#x",
			profile.BootControlSBICompletionStatus,
		)
	}
	if want := []uint32{
		0x0e0c, 0x0e28,
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
	if want := []uint32{0x0e20}; !reflect.DeepEqual(profile.BootControlMixedWidthOffsets, want) {
		t.Fatalf("SCH-W830 boot-control mixed-width offsets = %#v", profile.BootControlMixedWidthOffsets)
	}
	if want := []uint32{0x4000, 0x4200}; !reflect.DeepEqual(profile.BootControlLegacyUARTControllers, want) {
		t.Fatalf(
			"SCH-W830 boot-control legacy UART controllers = %#v",
			profile.BootControlLegacyUARTControllers,
		)
	}
	if want := []QualcommCompletionEventConfig{{
		StartOffset:           0x0e04,
		StartMask:             1,
		StatusOffset:          0x0e24,
		StatusMask:            2,
		AcknowledgeOffset:     0x0e28,
		AcknowledgeWidth:      Width16,
		AcknowledgeMask:       0xffff,
		InterruptSource:       13,
		UseVectoredController: true,
	}}; !reflect.DeepEqual(profile.BootControlCompletionEvents, want) {
		t.Fatalf("SCH-W830 boot-control completion events = %#v", profile.BootControlCompletionEvents)
	}
	if want := (&QualcommMDPProfile{
		CompletionStartOffset: 0x0e04,
		ScriptPointerOffset:   0x0e08,
		RGB565SourceFormat:    0x20,
	}); !reflect.DeepEqual(profile.MDP, want) {
		t.Fatalf("SCH-W830 MDP profile = %#v", profile.MDP)
	}
	if want := []uint32{
		0x0594, 0x0598, 0x05a8, 0x05ac,
		0x05bc, 0x05c0, 0x05d0, 0x05d4,
	}; !reflect.DeepEqual(profile.PrimaryClockWritableOffsets, want) {
		t.Fatalf("SCH-W830 primary-clock writable offsets = %#v", profile.PrimaryClockWritableOffsets)
	}
	if want := []uint32{0x040c}; !reflect.DeepEqual(profile.SecondaryClockWritableOffsets, want) {
		t.Fatalf("SCH-W830 secondary-clock writable offsets = %#v", profile.SecondaryClockWritableOffsets)
	}
	if len(profile.ClockRegimeSleepControllers) != 2 ||
		profile.ClockRegimeSleepControllers[0] != 0x5200 ||
		profile.ClockRegimeSleepControllers[1] != 0x5244 {
		t.Fatalf(
			"SCH-W830 clock-regime sleep controllers = %#v",
			profile.ClockRegimeSleepControllers,
		)
	}
	if want := []QualcommClockRegimeCounterConfig{{
		Offset: 0x6000, InstructionsPerSecond: 60_000_000,
		CounterHz: 9_830_400, Bits: 18,
	}}; !reflect.DeepEqual(profile.ClockRegimeCounters, want) {
		t.Fatalf("SCH-W830 clock-regime counters = %#v", profile.ClockRegimeCounters)
	}
	if want := []QualcommClockRegimeComparatorConfig{{
		CounterOffset: 0x480c, CounterMask: 0x0000ff00,
		InstructionsPerSecond: 60_000_000, CounterHz: 150, CounterModulus: 150,
		MatchBaseOffset: 0x48c4, MatchStride: 4, MatchMask: 0x0000ff00,
		EnableOffset: 0x487c, StatusOffset: 0x4864, AcknowledgeOffset: 0x4870,
		EventMask: 0x000000ff, InterruptSource: 46, UseVectoredController: true,
	}}; !reflect.DeepEqual(profile.ClockRegimeComparators, want) {
		t.Fatalf("SCH-W830 clock-regime comparators = %#v", profile.ClockRegimeComparators)
	}
	// DL21 polls its raw-NAND ready bit through the flat boot-control alias at
	// CHIP_BASE+0x488. Group apertures would shadow that register, so the
	// shared W830 profile keeps the ungrouped compact VIC.
	if profile.VectoredInterrupt == nil ||
		*profile.VectoredInterrupt != (QualcommVectoredInterruptConfig{
			SourceCount:        49,
			Bank0Sources:       25,
			ReverseSourceOrder: true,
		}) {
		t.Fatalf("SCH-W830 vectored interrupt profile = %+v", profile.VectoredInterrupt)
	}
	if profile.TimeTickClock == nil ||
		*profile.TimeTickClock != (QualcommTimeTickClockConfig{
			InstructionsPerSecond: 60_000_000,
			TimeTickHz:            32_768,
			InterruptSource:       21,
			UseVectoredController: true,
		}) {
		t.Fatalf("SCH-W830 timetick profile = %+v", profile.TimeTickClock)
	}
	if profile.Keypad == nil {
		t.Fatal("SCH-W830 keypad profile is nil")
	}
	wantKeys := map[string][2]uint8{
		"soft-left":   {0, 0},
		"soft-right":  {1, 0},
		"ok":          {5, 0},
		"back":        {3, 0},
		"send":        {2, 0},
		"up":          {4, 0},
		"down":        {4, 1},
		"left":        {4, 2},
		"right":       {4, 3},
		"volume-up":   {6, 0},
		"volume-down": {6, 1},
	}
	for _, key := range profile.Keypad.Keys {
		if coordinates, ok := wantKeys[key.ID]; ok {
			if key.Row != coordinates[0] || key.Column != coordinates[1] {
				t.Fatalf("SCH-W830 %s key = row %d column %d, want row %d column %d",
					key.ID, key.Row, key.Column, coordinates[0], coordinates[1])
			}
			delete(wantKeys, key.ID)
		}
	}
	if len(wantKeys) != 0 {
		t.Fatalf("SCH-W830 keypad profile is missing controls: %v", wantKeys)
	}
	if profile.Panel != (DCSPanelConfig{Width: 240, Height: 320, NativeAddressMode: 0x48}) {
		t.Fatalf("SCH-W830 panel profile = %+v", profile.Panel)
	}
	if profile.LegacyTopIdentification != 0 {
		t.Fatalf("SCH-W830 legacy top identification = %#x", profile.LegacyTopIdentification)
	}
	if profile.LegacyTopVersion != 0 {
		t.Fatalf("SCH-W830 legacy top version = %#x", profile.LegacyTopVersion)
	}
	if len(profile.LatchedRegisters) != 0 {
		t.Fatalf("SCH-W830 latched registers = %#v", profile.LatchedRegisters)
	}
	wantLatchedWindows := []LatchedRegisterWindowProfile{
		{ID: "external-16bit-bank-0", Address: 0x91000000, Size: 0x00010000, Width: Width16},
		{ID: "external-16bit-bank-1", Address: 0x91200000, Size: 0x00010000, Width: Width16},
		{ID: "external-32bit-bank-2", Address: 0x91400000, Size: 0x00010000, Width: Width32},
		{ID: "external-32bit-bank-4", Address: 0x91800000, Size: 0x00014000, Width: Width32},
	}
	if !reflect.DeepEqual(profile.LatchedRegisterWindows, wantLatchedWindows) {
		t.Fatalf("SCH-W830 latched register windows = %#v", profile.LatchedRegisterWindows)
	}
	wantADSPMailbox := &QualcommADSPMailboxProfile{
		ID:                 "external-32bit-control",
		Address:            0x91c00000,
		Size:               0x00000100,
		WriteControlOffset: 0x00000008,
		ControlRules: []QualcommADSPControlRuleProfile{
			{
				Offset: 4, Value: 1, ResponseDelayInstructions: 1,
				Writes: []QualcommADSPMemoryWriteProfile{
					{
						WindowID: "external-16bit-bank-1", Offset: 0x00004d1e,
						Width: Width16, Value: 0,
					},
					{
						WindowID: "external-16bit-bank-1", Offset: 0x000051a4,
						Width: Width16, Value: 1,
					},
				},
				Interrupt: &QualcommADSPInterruptProfile{
					Source: 33, UseVectoredController: true,
				},
			},
			{
				Offset: 0, Value: 2,
				Writes: []QualcommADSPMemoryWriteProfile{{
					WindowID: "external-16bit-bank-1", Offset: 0x00000b6c,
					Width: Width16, Value: 1,
				}},
			},
			{
				Offset: 0, Value: 3,
				Writes: []QualcommADSPMemoryWriteProfile{{
					WindowID: "external-16bit-bank-1", Offset: 0x00000b6c,
					Width: Width16, Value: 0,
				}},
			},
		},
		HostCommand: &QualcommADSPHostCommandProfile{
			SelectorWindowID: "external-16bit-bank-1",
			SelectorOffset:   0x00000bc8,
			SelectorWidth:    Width16,
			Rules: []QualcommADSPHostCommandRuleProfile{
				{
					Command: 1,
					Copies: []QualcommADSPMemoryCopyProfile{{
						SourceWindowID: "external-32bit-bank-2", SourceOffset: 0x00000570,
						DestinationWindowID: "external-32bit-bank-2", DestinationOffset: 0x0000056c,
						Width: Width32,
					}},
				},
				{Command: 4},
			},
		},
	}
	if !reflect.DeepEqual(profile.ADSPMailbox, wantADSPMailbox) {
		t.Fatalf("SCH-W830 ADSP mailbox = %#v", profile.ADSPMailbox)
	}
	bus := NewBus()
	check(t, profile.ApplyMemory(bus))
	if err := bus.MapRAM("ebi-overlap-check", 0x07fff000, 0x1000); err == nil {
		t.Fatal("board profile did not map 128 MiB EBI RAM")
	}
	if err := bus.MapRAM("adsp-overlap-check", 0x77fff000, 0x1000); err == nil {
		t.Fatal("board profile did not map the complete ADSP address space")
	}
	var adspWord [4]byte
	binary.LittleEndian.PutUint32(adspWord[:], 0x11223344)
	check(t, bus.Write(0x70001338, adspWord[:], cpu.PermissionWrite))
	clear(adspWord[:])
	if err := bus.Read(0x70001338, adspWord[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint32(adspWord[:]) != 0x11223344 {
		t.Fatalf("profiled ADSP address space = %x error %v", adspWord, err)
	}
	if err := bus.MapRAM("overlap-check", 0x7800f000, 0x1000); err == nil {
		t.Fatal("board profile did not map PBL IRAM")
	}
	if err := bus.MapRAM("high-vector-overlap-check", 0xffff5000, 0x1000); err == nil {
		t.Fatal("board profile did not map high-vector IRAM")
	}
}

func TestSCHW860DA06BoardProfileKeepsAdjacentBoardFactsSeparate(t *testing.T) {
	profile := SCHW860DA06BoardProfile()
	check(t, profile.Validate())
	if profile.ID != "samsung.sch-w860" || profile.FirmwareBuildID != "samsung.sch-w860.da06" ||
		profile.NANDSize != 0x10000000 || profile.NANDReadID != 0xecba {
		t.Fatalf("SCH-W860 identity/NAND = %q/%q %#x/%#x",
			profile.ID, profile.FirmwareBuildID, profile.NANDSize, profile.NANDReadID)
	}
	if len(profile.LegacyTopWritableOffsets) != 2 || profile.Keypad != nil ||
		len(profile.BootControlSBIReadResponses) != 0 {
		t.Fatalf("SCH-W860 top-page/keypad/SBI response profile = %#v/%#v/%#v",
			profile.LegacyTopWritableOffsets, profile.Keypad, profile.BootControlSBIReadResponses)
	}
	wantInitialData := []FlashSeed{{Offset: 0x097c0000, Data: make([]byte, 12)}}
	if !reflect.DeepEqual(profile.NANDInitialData, wantInitialData) {
		t.Fatalf("SCH-W860 downloader baseline = %#v", profile.NANDInitialData)
	}
	if w830 := SCHW830DL21BoardProfile(); len(w830.LegacyTopWritableOffsets) != 0 || w830.Keypad == nil {
		t.Fatalf("SCH-W860 profile construction mutated SCH-W830")
	}
}

func TestSCHW770DA05BoardProfileKeepsVersionOneNANDSeparate(t *testing.T) {
	profile := SCHW770DA05BoardProfile()
	check(t, profile.Validate())
	if profile.ID != "samsung.sch-w770" || profile.FirmwareBuildID != "samsung.sch-w770.da05" ||
		profile.NANDSize != 0x20000000 || profile.NANDReadID != 0xecdc {
		t.Fatalf("SCH-W770 identity/NAND = %q/%q %#x/%#x",
			profile.ID, profile.FirmwareBuildID, profile.NANDSize, profile.NANDReadID)
	}
	wantSeed := []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0}
	if want := []FlashSeed{{Offset: 0x11200000, Data: wantSeed}}; !reflect.DeepEqual(profile.NANDInitialData, want) || profile.Keypad == nil {
		t.Fatalf("SCH-W770 initial data/keypad = %#v/%#v", profile.NANDInitialData, profile.Keypad)
	}
	if want := (QualcommGPIOKeypadProfile{
		Columns: []uint8{0, 1, 2},
		Rows: []QualcommGPIOKeypadRowProfile{
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000400},
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00000800},
			{OutputBank: QualcommGPIOOutputSecondaryClock, OutputOffset: 0x0400, OutputMask: 0x00001000},
		},
		Keys: []QualcommGPIOKeyProfile{{ID: "hold", Row: 0, Column: 2}},
		InterruptGroups: []QualcommGPIOInterruptGroupProfile{
			{
				ClearOffset: 0x0594, EnableOffset: 0x05a8,
				DetectOffset: 0x05bc, PolarityOffset: 0x05d0, StatusOffset: 0x05e4,
				InterruptSource: 5, UseVectoredController: true,
			},
			{
				ClearOffset: 0x0598, EnableOffset: 0x05ac,
				DetectOffset: 0x05c0, PolarityOffset: 0x05d4, StatusOffset: 0x05e8,
				InterruptSource: 5, UseVectoredController: true,
			},
		},
		ColumnInterrupts: []QualcommGPIOKeypadColumnInterruptProfile{
			{Column: 0, Group: 1, Mask: 1 << 7},
			{Column: 1, Group: 1, Mask: 1 << 8},
			{Column: 2, Group: 0, Mask: 1 << 7},
		},
	}); !reflect.DeepEqual(*profile.Keypad, want) {
		t.Fatalf("SCH-W770 keypad = %#v, want %#v", *profile.Keypad, want)
	}
	if profile.PBLLegacyFeatureDataAddress != 0xffff6044 {
		t.Fatalf("SCH-W770 legacy PBL feature data = %#x", profile.PBLLegacyFeatureDataAddress)
	}
	if profile.PrimaryClockKeys != nil {
		t.Fatalf("SCH-W770 inherited unevidenced primary-clock keys %#v", profile.PrimaryClockKeys)
	}
	if !reflect.DeepEqual(profile.LegacyTopWritableOffsets, []uint32{
		qualcommLegacyTopIDOffset,
		qualcommLegacyTopIDOffset + 4,
	}) {
		t.Fatalf("SCH-W770 legacy top-page scratch = %#v", profile.LegacyTopWritableOffsets)
	}
	if profile.OneNAND == nil || profile.OneNAND.Address != 0x40000000 ||
		profile.OneNAND.ManufacturerID != 0xec || profile.OneNAND.DeviceID != 0x5c ||
		profile.OneNAND.DieBlockOffset != 0x800 || profile.OneNAND.Capacity != 0x18000000 {
		t.Fatalf("SCH-W770 OneNAND profile = %+v", profile.OneNAND)
	}
	foundEBIRAM := false
	for _, region := range profile.Memory {
		if region.ID == "ebi-ram" {
			foundEBIRAM = region.Address == 0 && region.Size == 0x0c000000
		}
	}
	if !foundEBIRAM {
		t.Fatal("SCH-W770 profile lacks its traced 192 MiB EBI RAM")
	}
	if profile.Panel.Width != 240 || profile.Panel.Height != 400 ||
		profile.Panel.NativeAddressMode != 0x88 || profile.PanelPorts == nil ||
		profile.PanelPorts.CommandAddress != 0x20000000 || profile.PanelPorts.DataAddress != 0x20020000 {
		t.Fatalf("SCH-W770 primary panel = %+v / %+v", profile.Panel, profile.PanelPorts)
	}
	wantAuxiliaryPanelWindows := []LatchedRegisterWindowProfile{
		{ID: "auxiliary-16bit-panel-command", Address: 0x30000000, Size: 2, Width: Width16},
		{ID: "auxiliary-16bit-panel-data", Address: 0x30020000, Size: 2, Width: Width16},
	}
	for _, want := range wantAuxiliaryPanelWindows {
		found := false
		for _, window := range profile.LatchedRegisterWindows {
			found = found || window == want
		}
		if !found {
			t.Fatalf("SCH-W770 profile lacks auxiliary panel latch %+v", want)
		}
	}
	if len(profile.BootControlGPIOInputs) != 1 ||
		profile.BootControlGPIOInputs[0] != (QualcommGPIOInputRegister{Offset: 0x40, Value: 0}) {
		t.Fatalf("SCH-W770 GPIO inputs = %+v", profile.BootControlGPIOInputs)
	}
	if !reflect.DeepEqual(profile.SecondaryClockReadOnlyRegisters,
		[]QualcommSecondaryClockReadOnlyRegister{{Offset: 0x0444, Value: 0}}) {
		t.Fatalf("SCH-W770 secondary GPIO inputs = %+v", profile.SecondaryClockReadOnlyRegisters)
	}
	foundAMSSPeripheralStatus := false
	foundAMSSPeripheralResult := false
	foundAMSSRawInput := false
	for _, register := range profile.BootControlReadOnlyRegisters {
		if register == (QualcommBootReadOnlyRegister{Offset: 0x0a4c, Value: 2}) {
			foundAMSSPeripheralStatus = true
		}
		if register == (QualcommBootReadOnlyRegister{Offset: 0x0a50, Value: 0}) {
			foundAMSSPeripheralResult = true
		}
		if register == (QualcommBootReadOnlyRegister{Offset: 0x05ec, Value: 0}) {
			foundAMSSRawInput = true
		}
	}
	if !foundAMSSPeripheralStatus || !foundAMSSPeripheralResult || !foundAMSSRawInput {
		t.Fatal("SCH-W770 profile lacks its AMSS peripheral status registers")
	}
	foundOEMSBLClockLatch := false
	foundStackExitLatch := false
	foundSavedChipConfigLatch := false
	foundAMSSCalibrationLatch := false
	foundAMSSClockPlanLatch := false
	foundAMSSClockProgramLatch := false
	foundAMSSClockSourceLatches := 0
	foundAMSSClockLatch := false
	foundAMSSClockBankLatch := false
	foundAMSSPeripheralControl := false
	for _, offset := range profile.BootControlWritableOffsets {
		if offset == 0x0080 {
			foundAMSSCalibrationLatch = true
		}
		if offset == 0x00a0 {
			foundSavedChipConfigLatch = true
		}
		if offset == 0x0148 {
			foundAMSSClockLatch = true
		}
		if offset == 0x010c {
			foundAMSSClockPlanLatch = true
		}
		if offset == 0x0130 || offset == 0x0134 {
			foundAMSSClockSourceLatches++
		}
		if offset == 0x0120 {
			foundAMSSClockProgramLatch = true
		}
		if offset == 0x0248 {
			foundAMSSClockBankLatch = true
		}
		if offset == 0x0a44 {
			foundAMSSPeripheralControl = true
		}
		if offset == 0x53a8 {
			foundOEMSBLClockLatch = true
		}
		if offset == 0x53e0 {
			foundStackExitLatch = true
		}
	}
	if !foundSavedChipConfigLatch || !foundAMSSCalibrationLatch ||
		!foundAMSSClockPlanLatch || !foundAMSSClockProgramLatch ||
		foundAMSSClockSourceLatches != 2 || !foundAMSSClockLatch ||
		!foundAMSSClockBankLatch || !foundAMSSPeripheralControl ||
		!foundOEMSBLClockLatch || !foundStackExitLatch {
		t.Fatal("SCH-W770 profile lacks traced QCSBL/OEMSBL control latches")
	}
	if w830 := SCHW830DL21BoardProfile(); w830.NANDSize != 0x10000000 || w830.Keypad == nil {
		t.Fatalf("SCH-W770 profile construction mutated SCH-W830")
	}
}

func TestSCHW850CF11BoardProfileUsesMSM7600SFlashFlexOneNAND(t *testing.T) {
	profile := SCHW850CF11BoardProfile()
	check(t, profile.Validate())
	if profile.ID != "samsung.sch-w850" ||
		profile.PlatformID != "qualcomm.msm7600-modem-arm9" ||
		profile.FirmwareBuildID != "samsung.sch-w850.cf11" ||
		profile.NANDSize != 0x20000000 || profile.OneNAND != nil {
		t.Fatalf("SCH-W850 identity/storage profile = %+v", profile)
	}
	wantSFlash := &QualcommSFlashOneNANDProfile{
		Address:        0xa0a00000,
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		TechnologyID:   1,
		Capacity:       0x20000000,
		FlexGeometry: &OneNANDFlexGeometry{
			PageSize: 0x1000, BlockCount: 0x400, SLCBoundary: 0x0f,
			SLCBlockSize: 0x40000, MLCBlockSize: 0x80000,
		},
		SpareInitialData: []FlashSeed{
			{Offset: 0x00c74000, Data: []byte{0xff, 0xff, 0xa5, 0xa5}},
			{Offset: 0x00c74080, Data: []byte{0xff, 0xff, 0xa5, 0xa5}},
			{Offset: 0x00c75f80, Data: []byte{0xff, 0xff, 0xa5, 0xa5}},
			{Offset: 0x00c78000, Data: []byte{0xff, 0xff, 0xa5, 0xa5}},
			{Offset: 0x00c78080, Data: []byte{0xff, 0xff, 0xa5, 0xa5}},
			{Offset: 0x00c79f80, Data: []byte{0xff, 0xff, 0xa5, 0xa5}},
		},
	}
	if !reflect.DeepEqual(profile.SFlashOneNAND, wantSFlash) {
		t.Fatalf("SCH-W850 SFlash OneNAND = %+v, want %+v", profile.SFlashOneNAND, wantSFlash)
	}
	if want := []FlashSeed{
		{
			Offset: 0x18e80000,
			Data: []byte{
				'U', 'P', 'C', 'H', 0, 0, 0, 0, 1, 0, 0xff, 0xff,
				1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0,
				0, 4, 0, 0, 0, 4, 0, 0,
			},
		},
		{Offset: 0x18e81000, Data: []byte{0x00}},
		{Offset: 0x18ebc20c, Data: []byte{
			0x01, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x04,
		}},
		{Offset: 0x18ebf000, Data: make([]byte, 0x1000)},
		{
			Offset: 0x18f00000,
			Data: []byte{
				'L', 'P', 'C', 'H', 0, 0, 0, 0, 1, 0, 0xff, 0xff,
				1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0,
				0, 4, 0, 0, 0, 4, 0, 0,
			},
		},
		{Offset: 0x18f01000, Data: []byte{0x00}},
		{Offset: 0x18f3c20c, Data: []byte{
			0x01, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x04,
		}},
		{Offset: 0x18f3f000, Data: make([]byte, 0x1000)},
	}; !reflect.DeepEqual(profile.NANDInitialData, want) {
		t.Fatalf("SCH-W850 generated LPCH data = %#v, want %#v", profile.NANDInitialData, want)
	}
	if profile.PBLSharedDataAddress != 0xffffe000 || profile.PBLSharedDataSize != 0x68 {
		t.Fatalf("SCH-W850 PBL shared data = %#x/%#x", profile.PBLSharedDataAddress, profile.PBLSharedDataSize)
	}
	if profile.PBLServiceTableAddress != 0xffffe100 || profile.PBLServiceTableHeaderSize != 0x30 ||
		profile.PBLHeaderFeatureDataAddress != 0xffffe0a0 || !reflect.DeepEqual(
		profile.PBLHeaderFeatures,
		[]QualcommPBLHeaderFeature{
			{Selector: qualcommPBLHeaderFlashBlockCount, Value: 0x0400},
			{Selector: qualcommPBLHeaderSLCBlockCount, Value: 0x0010},
			{Selector: qualcommPBLHeaderBadBlockLimit, Value: 0x0014},
		},
	) {
		t.Fatalf(
			"SCH-W850 PBL service table = %#x/%#x features %#x/%+v",
			profile.PBLServiceTableAddress,
			profile.PBLServiceTableHeaderSize,
			profile.PBLHeaderFeatureDataAddress,
			profile.PBLHeaderFeatures,
		)
	}
	foundIRAM := false
	foundHardwareRevision := false
	foundMSM7600StatusInputs := map[uint32]bool{0x40: false, 0x44: false}
	for _, region := range profile.Memory {
		if region.ID == "msm7600-qcsbl-iram-page" {
			foundIRAM = region.Kind == MemoryRAM && region.Address == 0xfffef000 && region.Size == 0x1000
		}
	}
	for _, register := range profile.LatchedRegisters {
		if register.ID == "msm7600-hardware-revision" {
			foundHardwareRevision = register.Address == 0xa9000270 &&
				register.Width == Width32 && register.ResetValue == 0x10000000
		}
	}
	for _, register := range profile.BootControlGPIOInputs {
		if _, ok := foundMSM7600StatusInputs[register.Offset]; ok && register.Value == 0 {
			foundMSM7600StatusInputs[register.Offset] = true
		}
	}
	if !foundIRAM {
		t.Fatal("SCH-W850 profile lacks the observed terminal MSM7600 IRAM page")
	}
	if !foundHardwareRevision {
		t.Fatal("SCH-W850 profile lacks the revision-1 MSM7600 PBL service-table selector")
	}
	for offset, found := range foundMSM7600StatusInputs {
		if !found {
			t.Fatalf("SCH-W850 profile lacks reset-zero MSM7600 status input 0x%x", offset)
		}
	}
	foundClockInterruptLatch := false
	for _, offset := range profile.BootControlWritableOffsets {
		foundClockInterruptLatch = foundClockInterruptLatch || offset == 0x0840
	}
	if !foundClockInterruptLatch {
		t.Fatal("SCH-W850 profile lacks its MSM7600 clock/interrupt latch")
	}
	if want := []uint32{0x0904}; !reflect.DeepEqual(
		profile.BootControlInterruptWindowWritableOffsets,
		want,
	) {
		t.Fatalf(
			"SCH-W850 interrupt-window writable offsets = %#v, want %#v",
			profile.BootControlInterruptWindowWritableOffsets,
			want,
		)
	}
}

func TestRawSamsungBoardProfilesKeepExactIdentityAndPackagedEnd(t *testing.T) {
	tests := []struct {
		profile          BoardProfile
		id, build        string
		packagedEnd      uint64
		page, erase      uint32
		reportErasedECC  bool
		extraInitialData []FlashSeed
	}{
		{
			profile: SCHW320DC18BoardProfile(), id: "samsung.sch-w320",
			build: "samsung.sch-w320.dc18", packagedEnd: 0x0a760000,
			page: 0x800, erase: 0x20000, reportErasedECC: true,
		},
		{
			profile: SCHW340DC18BoardProfile(), id: "samsung.sch-w340",
			build: "samsung.sch-w340.dc18", packagedEnd: 0x08800000,
			page: 0x800, erase: 0x20000, reportErasedECC: true,
		},
		{
			profile: SCHW350CK06BoardProfile(), id: "samsung.sch-w350",
			build: "samsung.sch-w350.ck06", packagedEnd: 0x08f80000,
			page: 0x800, erase: 0x20000,
			extraInitialData: []FlashSeed{{Offset: 0x019a0004, Data: []byte{0}}},
		},
		{
			profile: SCHW410CL10BoardProfile(), id: "samsung.sch-w410",
			build: "samsung.sch-w410.cl10", packagedEnd: 0x09100000,
			page: 0x800, erase: 0x20000, reportErasedECC: true,
		},
		{
			profile: SCHW300DA04BoardProfile(), id: "samsung.sch-w300",
			build: "samsung.sch-w300.da04", packagedEnd: 0x085c0000,
			page: 0x800, erase: 0x20000, reportErasedECC: true,
		},
		{
			profile: SCHW420CD16BoardProfile(), id: "samsung.sch-w420",
			build: "samsung.sch-w420.cd16", packagedEnd: 0x0d5c0000,
			page: 0x800, erase: 0x20000, reportErasedECC: true,
		},
		{
			profile: SPHW4200DC17BoardProfile(), id: "samsung.sph-w4200",
			build: "samsung.sph-w4200.dc17", packagedEnd: 0x0e600000,
			page: 0x800, erase: 0x20000, reportErasedECC: true,
		},
		{
			profile: SCHW270CL28BoardProfile(), id: "samsung.sch-w270",
			build: "samsung.sch-w270.cl28", packagedEnd: 0x08c80000,
			page: 0x200, erase: 0x4000, reportErasedECC: true,
		},
	}
	for _, test := range tests {
		if err := test.profile.Validate(); err != nil {
			t.Fatalf("%s: %v", test.id, err)
		}
		wantReadID, wantNANDSize := uint32(0x000098ca), uint64(0x10000000)
		if test.id == "samsung.sph-w4200" {
			wantReadID, wantNANDSize = 0x0000ecdc, 0x20000000
		}
		oneNANDMismatch := test.profile.OneNAND != nil
		if test.id == "samsung.sph-w4200" {
			oneNANDMismatch = !reflect.DeepEqual(test.profile.OneNAND, &OneNANDProfile{
				Address: 0x40000000, ManufacturerID: 0x00ec, DeviceID: 0x005c,
				DieBlockOffset: 0x0800, Capacity: 0x20000000, InitialImageFromFirmware: true,
			})
		}
		if test.profile.ID != test.id || test.profile.FirmwareBuildID != test.build ||
			test.profile.PlatformID != "qualcomm.arm9-sch-raw-v1" ||
			test.profile.NANDReadID != wantReadID || test.profile.NANDSize != wantNANDSize ||
			test.profile.NANDPageSize != test.page || test.profile.NANDEraseBlockSize != test.erase ||
			test.profile.NANDReportsErasedECCCodewords != test.reportErasedECC ||
			oneNANDMismatch || test.profile.PBLLegacyFeatureDataAddress != 0xffff6044 {
			t.Fatalf("raw Samsung board profile = %+v", test.profile)
		}
		wantInitialData := append([]FlashSeed{{
			Offset: test.packagedEnd,
			Data:   []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0},
		}}, test.extraInitialData...)
		if test.id == "samsung.sch-w340" {
			footer := make([]byte, 0x13ecc)
			copy(footer, []byte{0xff, 0xfe, 0xaf, 0xbe, 1, 0, 0, 0, 0, 0, 0, 0})
			copy(footer[12:], "nvm/preload_ver")
			footer[12+0x80] = 8
			footer[12+0x84] = 4
			wantInitialData[0].Data = footer
		}
		if !reflect.DeepEqual(test.profile.NANDInitialData, wantInitialData) {
			t.Fatalf("%s initial NAND data = %+v", test.id, test.profile.NANDInitialData)
		}
		foundExternalCommandData, foundExternalMemoryControl := false, false
		for _, window := range test.profile.LatchedRegisterWindows {
			if window.ID == "external-8bit-command-data" && window.Address == 0x30000000 &&
				window.Size == 4 && window.Width == Width8 {
				foundExternalCommandData = true
			}
			if window.ID == "qcsbl-external-memory-control" && window.Address == 0xa0000000 &&
				window.Size == 0x78 && window.Width == Width32 {
				foundExternalMemoryControl = true
			}
		}
		if !foundExternalCommandData || !foundExternalMemoryControl {
			t.Fatalf("%s lacks raw external-bus windows", test.id)
		}
		foundSparseBusZero, foundSparseBusStatus := false, false
		foundRawClockControl, foundPeripheralControl := false, false
		for _, offset := range test.profile.BootControlWritableOffsets {
			foundRawClockControl = foundRawClockControl || offset == 0x000c
			foundPeripheralControl = foundPeripheralControl || offset == 0x2078
		}
		if !foundRawClockControl || !foundPeripheralControl {
			t.Fatalf("%s lacks raw boot-control latches", test.id)
		}
		foundColdPeripheralStatus := false
		for _, register := range test.profile.BootControlReadOnlyRegisters {
			foundColdPeripheralStatus = foundColdPeripheralStatus ||
				register == (QualcommBootReadOnlyRegister{Offset: 0x0d64, Value: 0})
		}
		if !foundColdPeripheralStatus {
			t.Fatalf("%s lacks cold peripheral status", test.id)
		}
		for _, offset := range test.profile.SparseBusRegisterOffsets {
			foundSparseBusZero = foundSparseBusZero || offset == 0
			foundSparseBusStatus = foundSparseBusStatus || offset == 0x40
		}
		if !foundSparseBusZero || !foundSparseBusStatus ||
			len(test.profile.SparseBusRegisterResets) != 1 ||
			test.profile.SparseBusRegisterResets[0] != (SparseWordRegisterReset{
				Offset: 0x40, Value: 0x80000000,
			}) {
			t.Fatalf("%s sparse-bus profile = %v / %v", test.id,
				test.profile.SparseBusRegisterOffsets, test.profile.SparseBusRegisterResets)
		}
	}
}

func TestCompactVICGroupsRequireGroupedStatusWiring(t *testing.T) {
	// A second-level group status word shadows the flat boot-control register
	// at the same CHIP_BASE offset. Boards that never wire grouped sources must
	// keep the legacy registers, such as the NAND-ready alias at +0x488 that
	// the W410/W830/W860 OEMSBL raw-NAND probes poll.
	grouped := map[string]bool{
		"samsung.sch-w320": true, "samsung.sch-w340": true,
		"samsung.sch-w350": true, "samsung.sph-w4200": true,
	}
	for _, profile := range []BoardProfile{
		SCHW830DL21BoardProfile(), SCHW860DA06BoardProfile(), SCHW770DA05BoardProfile(),
		SCHW210CK12BoardProfile(), SCHW240CL28BoardProfile(), SCHW270CL28BoardProfile(),
		SCHW290CK10BoardProfile(), SCHW300DA04BoardProfile(), SCHW320DC18BoardProfile(),
		SCHW330CK06BoardProfile(), SCHW340DC18BoardProfile(), SCHW350CK06BoardProfile(),
		SCHW390CK11BoardProfile(), SCHW410CL10BoardProfile(), SCHW420CD16BoardProfile(),
		SCHW450CK10BoardProfile(), SCHW460CC26BoardProfile(), SCHW599BE30BoardProfile(),
		SCHW850CF11BoardProfile(), SPHW4200DC17BoardProfile(),
	} {
		if profile.VectoredInterrupt == nil {
			continue
		}
		groups := profile.VectoredInterrupt.GroupCount
		if grouped[profile.ID] {
			if groups != 6 || len(profile.BootControlGroupedStatusResponses) == 0 {
				t.Fatalf("%s grouped compact VIC = %d groups, %d responses",
					profile.ID, groups, len(profile.BootControlGroupedStatusResponses))
			}
			continue
		}
		if groups != 0 {
			t.Fatalf("%s exposes %d unwired compact-VIC groups", profile.ID, groups)
		}
	}
}

func TestLegacyFlatBoardProfilesDeclareExactResetDevices(t *testing.T) {
	w450 := SCHW450CK10BoardProfile()
	check(t, w450.Validate())
	if w450.ID != "samsung.sch-w450" || w450.FirmwareBuildID != "samsung.sch-w450.ck10" ||
		w450.PlatformID != "qualcomm.arm7-samsung-flat-v1" ||
		!w450.CPUCompatibility.UserSystemSPSRReadAsCPSR ||
		w450.NANDReadID != 0x00002079 ||
		w450.Panel != (DCSPanelConfig{
			Width: 176, Height: 220, Protocol: ParallelPanelProtocolIndexedRGB565Window4445,
		}) ||
		!reflect.DeepEqual(w450.BootControlByteWritableOffsets, []uint32{0x3404}) {
		t.Fatalf("SCH-W450 reset profile = %+v", w450)
	}
	foundResetStatus, foundMixedControl, foundBusWindow, foundClockReady := false, false, false, false
	for _, register := range w450.BootControlReadOnlyRegisters {
		foundResetStatus = foundResetStatus || register == (QualcommBootReadOnlyRegister{Offset: 0x3400})
	}
	for _, offset := range w450.BootControlMixedWidthOffsets {
		foundMixedControl = foundMixedControl || offset == 0x3404
	}
	for _, window := range w450.LatchedRegisterWindows {
		foundBusWindow = foundBusWindow || window == (LatchedRegisterWindowProfile{
			ID: "w450-external-bus-control", Address: 0x63800000, Size: 0x400, Width: Width32,
		})
	}
	for _, register := range w450.PrimaryClockReadOnlyRegisters {
		foundClockReady = foundClockReady || register == (QualcommPrimaryClockReadOnlyRegister{
			Offset: 0x0168, Value: 0x01000000,
		})
	}
	wantClockInterrupts := []QualcommPrimaryClockInterruptRegister{{
		StatusOffset: 0x0244,
		ClearOffset:  0x024c,
		Bits: []QualcommPrimaryClockInterruptBit{
			{Bit: 1, Source: 45},
			{Bit: 2, Source: 46},
		},
	}}
	if !foundResetStatus || !foundMixedControl || !foundBusWindow || !foundClockReady ||
		!reflect.DeepEqual(w450.PrimaryClockInterruptRegisters, wantClockInterrupts) {
		t.Fatalf("SCH-W450 reset devices = status:%t mixed:%t bus:%t clock-ready:%t interrupts:%+v",
			foundResetStatus, foundMixedControl, foundBusWindow, foundClockReady,
			w450.PrimaryClockInterruptRegisters)
	}

	w599 := SCHW599BE30BoardProfile()
	check(t, w599.Validate())
	if w599.ID != "samsung.sch-w599" || w599.FirmwareBuildID != "samsung.sch-w599.be30" ||
		w599.PlatformID != "intel.pxa27x-samsung-flat-v1" ||
		w599.NANDReadID != 0x00009879 ||
		w599.Panel != (DCSPanelConfig{
			Width: 240, Height: 320, Protocol: ParallelPanelProtocolIndexedRGB565Window36373839,
		}) ||
		w599.PanelSelectorPorts == nil ||
		*w599.PanelSelectorPorts != (ParallelPanelSelectorPortProfile{
			SelectorAddress: 0x20000000, TransferAddress: 0x20000002,
			CommandSelect: 0, DataSelect: 1,
		}) ||
		!reflect.DeepEqual(w599.NANDRegisterResets, []QualcommNANDRegisterReset{
			{Offset: 0x0240, Value: 0},
			{Offset: 0x0260, Value: 0},
		}) ||
		!w599.CPUCompatibility.UserSystemSPSRReadAsCPSR {
		t.Fatalf("SCH-W599 reset profile = %+v", w599)
	}
	wantStorage := AddressedStorageWindowProfile{
		ID: "w599-bootstrap-page", Address: 0x64000000, Size: 0x200,
		CommandID: "w599-bootstrap-page-command", CommandAddress: 0x64000304,
		CommandWidth: Width32, AddressMask: 0xfffffe00,
	}
	if !reflect.DeepEqual(w599.AddressedStorageWindows, []AddressedStorageWindowProfile{wantStorage}) {
		t.Fatalf("SCH-W599 addressed storage = %+v", w599.AddressedStorageWindows)
	}
	foundRAM, foundSelectors, foundResult, foundStatusAlias := false, false, false, false
	for _, memory := range w599.Memory {
		foundRAM = foundRAM || memory == (MemoryRegionProfile{
			ID: "ebi-ram", Kind: MemoryRAM, Address: 0, Size: 0x08000000,
		})
	}
	for _, register := range w599.ReadOnlyRegisters {
		foundSelectors = foundSelectors || register == (ReadOnlyRegisterProfile{
			ID: "w599-bootstrap-selectors", Address: 0x64000308, Width: Width32, Value: 0x4c3c8000,
		})
		foundResult = foundResult || register == (ReadOnlyRegisterProfile{
			ID: "w599-bootstrap-result", Address: 0x64000320, Width: Width32, Value: 0xff,
		})
	}
	for _, alias := range w599.BootControlInterruptStatusAliases {
		foundStatusAlias = foundStatusAlias || alias == (QualcommInterruptStatusAlias{Offset: 0x50, Bank: 1})
	}
	if !foundRAM || !foundSelectors || !foundResult || !foundStatusAlias {
		t.Fatalf("SCH-W599 reset devices = ram:%t selectors:%t result:%t irq:%t",
			foundRAM, foundSelectors, foundResult, foundStatusAlias)
	}
}

func TestSPHW4200DC17BoardDeclaresSecondRAMBankAndR61509Panel(t *testing.T) {
	profile := SPHW4200DC17BoardProfile()
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	foundFixedSDCCStatus := false
	for _, register := range profile.BootControlReadOnlyRegisters {
		foundFixedSDCCStatus = foundFixedSDCCStatus || register.Offset == 0x0c34
	}
	if foundFixedSDCCStatus || !reflect.DeepEqual(
		profile.BootControlSDCCControllers,
		[]QualcommSDCCControllerConfig{{
			Base: 0x0c00, CardPresent: true, GroupStatusOffset: 0x90, GroupMask: 0x01,
		}},
	) {
		t.Fatalf(
			"SPH-W4200 SDCC absent-card profile = fixed:%t controllers:%+v",
			foundFixedSDCCStatus,
			profile.BootControlSDCCControllers,
		)
	}
	if profile.Keypad != nil || profile.Touchscreen == nil ||
		profile.Touchscreen.Width != 240 || profile.Touchscreen.Height != 432 ||
		profile.Touchscreen.PenInputOffset != 0x0440 || profile.Touchscreen.PenInputMask != 1 ||
		profile.Touchscreen.InterruptGroup.StatusOffset != 0x05e4 ||
		profile.Touchscreen.InterruptGroup.InterruptSource != 5 ||
		profile.Touchscreen.InterruptMask != 0x01 {
		t.Fatalf("SPH-W4200 input profile = keypad:%#v touchscreen:%#v", profile.Keypad, profile.Touchscreen)
	}
	if !slices.Contains(profile.PrimaryClockReadOnlyRegisters,
		(QualcommPrimaryClockReadOnlyRegister{Offset: 0x05e8, Value: 0})) {
		t.Fatalf("SPH-W4200 passive GPIO group status = %#v", profile.PrimaryClockReadOnlyRegisters)
	}
	if profile.BootClockModeStatus != 0 {
		t.Fatalf("SPH-W4200 cold-boot strap = %#08x", profile.BootClockModeStatus)
	}
	if want := []QualcommSBIReadResponse{
		{Controller: 0x5100, Address: 0x4f, Value: 0xc1},
		{Controller: 0x5100, Address: 0x53, Value: 0xff},
		{Controller: 0x5100, Address: 0x54, Value: 0x01},
	}; !reflect.DeepEqual(profile.BootControlSBIReadResponses, want) {
		t.Fatalf("SPH-W4200 PMIC ADC responses = %+v", profile.BootControlSBIReadResponses)
	}
	if !slices.Contains(profile.LegacyTopWritableOffsets, uint32(0x03a8)) {
		t.Fatalf("SPH-W4200 PBL top-page scratch = %#v", profile.LegacyTopWritableOffsets)
	}
	if !slices.Contains(profile.BootControlLegacyUARTControllers, uint32(0x4100)) {
		t.Fatalf("SPH-W4200 UIM UART controllers = %#v", profile.BootControlLegacyUARTControllers)
	}
	if want := []QualcommLegacyUARTReceiveData{{
		Controller:         0x4100,
		InterruptSource:    55,
		DelayInstructions:  65_536,
		EchoTransmit:       true,
		TransmitFrameBytes: 5,
		TransmitResponse:   []byte{0x6d, 0x00},
		Data:               []byte{0x3b, 0x00},
	}}; !reflect.DeepEqual(profile.BootControlLegacyUARTReceiveData, want) {
		t.Fatalf("SPH-W4200 UIM UART receive data = %#v", profile.BootControlLegacyUARTReceiveData)
	}
	if profile.LegacyInterruptCascade == nil ||
		*profile.LegacyInterruptCascade != (QualcommInterruptCascadeProfile{
			VectoredSource: 17, GroupStatusOffset: 0x8c, GroupMask: 0x02,
		}) {
		t.Fatalf("SPH-W4200 legacy interrupt cascade = %+v", profile.LegacyInterruptCascade)
	}
	if want := []QualcommBootGroupedStatusResponse{{
		Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
		GroupStatusOffset: 0x88, GroupMask: 0x02,
	}, {
		Offset: 0x0380, NANDReadyMask: 0x01,
		GroupStatusOffset: 0x88, GroupMask: 0x01,
	}}; !reflect.DeepEqual(profile.BootControlGroupedStatusResponses, want) {
		t.Fatalf(
			"SPH-W4200 raw-NAND grouped-status responses = %+v",
			profile.BootControlGroupedStatusResponses,
		)
	}
	for _, relative := range append(
		append([]uint32(nil), qualcommLegacyUARTHalfwordRegisterOffsets[:]...),
		qualcommLegacyUARTFIFOOffset,
	) {
		offset := uint32(0x4100) + relative
		if !slices.Contains(profile.BootControlWritableOffsets, offset) ||
			!slices.Contains(profile.BootControlMixedWidthOffsets, offset) {
			t.Fatalf(
				"SPH-W4200 UIM UART register %#x missing word/mixed-width aperture",
				offset,
			)
		}
	}
	if profile.Panel != (DCSPanelConfig{
		Width: 240, Height: 432, Protocol: ParallelPanelProtocolIndexedRGB565Window210213,
	}) || profile.PanelPorts == nil ||
		*profile.PanelPorts != (ParallelPanelPortProfile{
			CommandAddress: 0x30005000,
			DataAddress:    0x30005004,
		}) {
		t.Fatalf("SPH-W4200 panel = %+v / %+v", profile.Panel, profile.PanelPorts)
	}
	foundSecondRAM, foundSharedConfig := false, false
	foundADSPDownloadResponse, foundADSPBIOSResponse, foundADSPCommandResponse := false, false, false
	foundBusReady, foundBusControl, foundBusTiming, foundBusChipSelect, foundBusControl14, foundBusClock, foundBusMask, foundBusControl28, foundBusMode, foundBusControl30 := false, false, false, false, false, false, false, false, false, false
	foundIndirectBus := [8]bool{}
	foundExternalBytePorts := [2]bool{}
	foundExternalGPIO := [8]bool{}
	foundAMSSInterruptRegisters := [5]bool{}
	for _, memory := range profile.Memory {
		foundSecondRAM = foundSecondRAM || memory == (MemoryRegionProfile{
			ID: "w4200-ebi-ram-bank-1", Kind: MemorySparseRAM,
			Address: 0x08000000, Size: 0x08000000,
		})
		foundSharedConfig = foundSharedConfig || memory == (MemoryRegionProfile{
			ID: "w4200-shared-config-segment", Kind: MemorySparseRAM,
			Address: 0x18000000, Size: 0x02600000,
		})
	}
	for _, response := range profile.MemoryWriteResponses {
		foundADSPDownloadResponse = foundADSPDownloadResponse || reflect.DeepEqual(response, MemoryWriteResponseProfile{
			MemoryID: "adsp-address-space", Offset: 0x00202f3a,
			Width: Width16, Request: 2,
			Writes: []MemoryResponseWriteProfile{{
				Offset: 0x00202f3a, Width: Width16, Value: 0,
			}},
		})
		foundADSPBIOSResponse = foundADSPBIOSResponse || reflect.DeepEqual(response, MemoryWriteResponseProfile{
			MemoryID: "adsp-address-space", Offset: 0x00202f30,
			Width: Width16, Request: 0x0100,
			Writes: []MemoryResponseWriteProfile{
				{Offset: 0x00202f30, Width: Width16, Value: 0},
				{Offset: 0x00202f3a, Width: Width16, Value: 1},
			},
		})
	}
	for _, rule := range profile.ADSPMailbox.ControlRules {
		foundADSPCommandResponse = foundADSPCommandResponse || reflect.DeepEqual(rule, QualcommADSPControlRuleProfile{
			Offset: 0x08, Value: 0x00020000, ResponseDelayInstructions: 1,
			Copies: []QualcommADSPMemoryCopyProfile{{
				SourceWindowID:      "external-32bit-bank-2",
				SourceOffset:        0x00000570,
				DestinationWindowID: "external-32bit-bank-2",
				DestinationOffset:   0x0000056c,
				Width:               Width32,
			}},
			Writes: []QualcommADSPMemoryWriteProfile{
				{
					WindowID: "external-16bit-bank-1", Offset: 0x00000bfc,
					Width: Width16, Value: 0,
				},
				{
					WindowID: "external-16bit-bank-1", Offset: 0x00003e4a,
					Width: Width16, Value: 1,
				},
			},
			Interrupt: &QualcommADSPInterruptProfile{
				Source: 33, UseVectoredController: true,
			},
			StartPeriodicInterrupt: true,
		})
	}
	if want := (&QualcommADSPPeriodicInterruptProfile{
		InstructionsPerSecond: 60_000_000,
		InterruptHz:           217,
		Interrupt: QualcommADSPInterruptProfile{
			Source: 29, UseVectoredController: true,
		},
	}); !reflect.DeepEqual(profile.ADSPMailbox.PeriodicInterrupt, want) {
		t.Fatalf("SPH-W4200 ADSP periodic interrupt = %+v", profile.ADSPMailbox.PeriodicInterrupt)
	}
	for _, register := range profile.ReadOnlyRegisters {
		foundBusReady = foundBusReady || reflect.DeepEqual(register, ReadOnlyRegisterProfile{
			ID: "w4200-external-bus-ready", Address: 0x30002000,
			Width: Width32, Value: 0x00000002,
		})
	}
	for _, register := range profile.LatchedRegisters {
		for index, want := range [...]LatchedRegisterProfile{
			{ID: "w4200-external-byte-port", Address: 0x38000000, Width: Width8},
			{ID: "w4200-external-byte-control", Address: 0x38010000, Width: Width8},
		} {
			foundExternalBytePorts[index] = foundExternalBytePorts[index] || reflect.DeepEqual(register, want)
		}
		for index := range foundExternalGPIO {
			foundExternalGPIO[index] = foundExternalGPIO[index] || reflect.DeepEqual(register, LatchedRegisterProfile{
				ID:      fmt.Sprintf("w4200-external-gpio-group-%d", index),
				Address: 0x38020000 + uint32(index)*2,
				Width:   Width8,
			})
		}
		foundBusControl = foundBusControl || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-control", Address: 0x30002004,
			Width: Width32, ResetValue: 0,
		})
		foundBusTiming = foundBusTiming || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-timing", Address: 0x30002008,
			Width: Width32, ResetValue: 0,
		})
		foundBusChipSelect = foundBusChipSelect || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-chip-select", Address: 0x30002010,
			Width: Width32, ResetValue: 0,
		})
		foundBusControl14 = foundBusControl14 || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-control-14", Address: 0x30002014,
			Width: Width32, ResetValue: 0,
		})
		foundBusClock = foundBusClock || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-clock", Address: 0x3000201c,
			Width: Width32, ResetValue: 0,
		})
		foundBusMask = foundBusMask || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-mask", Address: 0x30002020,
			Width: Width32, ResetValue: 0,
		})
		foundBusControl28 = foundBusControl28 || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-control-28", Address: 0x30002028,
			Width: Width32, ResetValue: 0,
		})
		foundBusMode = foundBusMode || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-mode", Address: 0x3000202c,
			Width: Width32, AdditionalWidths: []Width{Width16}, ResetValue: 0,
		})
		foundBusControl30 = foundBusControl30 || reflect.DeepEqual(register, LatchedRegisterProfile{
			ID: "w4200-external-bus-control-30", Address: 0x30002030,
			Width: Width32, ResetValue: 0,
		})
		for index, want := range [...]LatchedRegisterProfile{
			{ID: "w4200-indirect-bus-control", Address: 0x30006000, Width: Width16},
			{ID: "w4200-indirect-bus-address-low", Address: 0x30006008, Width: Width16},
			{ID: "w4200-indirect-bus-address-high", Address: 0x3000600a, Width: Width16},
			{ID: "w4200-indirect-bus-data", Address: 0x3000600c, Width: Width32, AdditionalWidths: []Width{Width16}, AllowSubwordOffsets: true},
			{ID: "w4200-indirect-bus-2-control", Address: 0x30007000, Width: Width16},
			{ID: "w4200-indirect-bus-2-address-low", Address: 0x30007008, Width: Width16},
			{ID: "w4200-indirect-bus-2-address-high", Address: 0x3000700a, Width: Width16},
			{ID: "w4200-indirect-bus-2-data", Address: 0x30007800, Width: Width32, AdditionalWidths: []Width{Width16}},
		} {
			foundIndirectBus[index] = foundIndirectBus[index] || reflect.DeepEqual(register, want)
		}
	}
	for _, offset := range profile.BootControlWritableOffsets {
		for index, want := range [...]uint32{0x0d04, 0x0d10, 0x0d14, 0x0d18, 0x0d1c} {
			foundAMSSInterruptRegisters[index] = foundAMSSInterruptRegisters[index] || offset == want
		}
	}
	if !foundSecondRAM || !foundSharedConfig || !foundADSPDownloadResponse || !foundADSPBIOSResponse || !foundADSPCommandResponse || !foundBusReady || !foundBusControl || !foundBusTiming || !foundBusChipSelect || !foundBusControl14 || !foundBusClock || !foundBusMask || !foundBusControl28 || !foundBusMode || !foundBusControl30 ||
		!foundIndirectBus[0] || !foundIndirectBus[1] || !foundIndirectBus[2] || !foundIndirectBus[3] ||
		!foundIndirectBus[4] || !foundIndirectBus[5] || !foundIndirectBus[6] || !foundIndirectBus[7] ||
		!foundExternalBytePorts[0] || !foundExternalBytePorts[1] ||
		!foundExternalGPIO[0] || !foundExternalGPIO[1] || !foundExternalGPIO[2] || !foundExternalGPIO[3] ||
		!foundExternalGPIO[4] || !foundExternalGPIO[5] || !foundExternalGPIO[6] || !foundExternalGPIO[7] ||
		!foundAMSSInterruptRegisters[0] || !foundAMSSInterruptRegisters[1] ||
		!foundAMSSInterruptRegisters[2] || !foundAMSSInterruptRegisters[3] ||
		!foundAMSSInterruptRegisters[4] {
		t.Fatalf(
			"SPH-W4200 boot devices = ram:%t shared:%t bus-ready:%t bus-control:%t bus-timing:%t bus-chip-select:%t bus-control-14:%t bus-clock:%t bus-mask:%t bus-control-28:%t bus-mode:%t bus-control-30:%t indirect:%v irq:%v",
			foundSecondRAM, foundSharedConfig, foundBusReady, foundBusControl, foundBusTiming, foundBusChipSelect, foundBusControl14, foundBusClock, foundBusMask, foundBusControl28, foundBusMode, foundBusControl30, foundIndirectBus, foundAMSSInterruptRegisters,
		)
	}
}

func TestAdditionalSmallPageRawBoardProfilesKeepExactGeometry(t *testing.T) {
	tests := []struct {
		profile                       BoardProfile
		id, build                     string
		packagedEnd, nandSize         uint64
		legacyFeatures, fixedFeatures uint32
	}{
		{
			profile: SCHW210CK12BoardProfile(), id: "samsung.sch-w210",
			build: "samsung.sch-w210.ck12", packagedEnd: 0x093c0000,
			nandSize: 0x10000000, legacyFeatures: 0xffff6044,
		},
		{
			profile: SCHW240CL28BoardProfile(), id: "samsung.sch-w240",
			build: "samsung.sch-w240.cl28", packagedEnd: 0x0bac0000,
			nandSize: 0x10000000, fixedFeatures: 0xffff601c,
		},
		{
			profile: SCHW290CK10BoardProfile(), id: "samsung.sch-w290",
			build: "samsung.sch-w290.ck10", packagedEnd: 0x05d00000,
			nandSize: 0x08000000, fixedFeatures: 0xffff601c,
		},
		{
			profile: SCHW330CK06BoardProfile(), id: "samsung.sch-w330",
			build: "samsung.sch-w330.ck06", packagedEnd: 0x05700000,
			nandSize: 0x08000000, legacyFeatures: 0xffff6044,
		},
		{
			profile: SCHW390CK11BoardProfile(), id: "samsung.sch-w390",
			build: "samsung.sch-w390.ck11", packagedEnd: 0x05700000,
			nandSize: 0x08000000, legacyFeatures: 0xffff6044,
		},
		{
			profile: SCHW460CC26BoardProfile(), id: "samsung.sch-w460",
			build: "samsung.sch-w460.cc26", packagedEnd: 0x05870000,
			nandSize: 0x08000000, legacyFeatures: 0xffff6044,
		},
	}
	for _, test := range tests {
		if err := test.profile.Validate(); err != nil {
			t.Fatalf("%s: %v", test.id, err)
		}
		if test.profile.ID != test.id || test.profile.FirmwareBuildID != test.build ||
			test.profile.PlatformID != "qualcomm.arm9-sch-raw-v1" ||
			test.profile.NANDReadID != 0x000098ca || test.profile.NANDSize != test.nandSize ||
			test.profile.NANDPageSize != 0x200 || test.profile.NANDEraseBlockSize != 0x4000 ||
			test.profile.PBLLegacyFeatureDataAddress != test.legacyFeatures ||
			test.fixedFeatures != 0 && test.profile.PBLFixedFeatureDataAddress != test.fixedFeatures ||
			test.profile.OneNAND != nil {
			t.Fatalf("small-page raw Samsung board profile = %+v", test.profile)
		}
		wantInitialData := []FlashSeed{{
			Offset: test.packagedEnd,
			Data:   []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0},
		}}
		if !reflect.DeepEqual(test.profile.NANDInitialData, wantInitialData) {
			t.Fatalf("%s initial NAND data = %+v", test.id, test.profile.NANDInitialData)
		}
	}

	wantFixedFeatures := []QualcommPBLFixedFeature{
		{Selector: 0x0ff, Value: 0x0020},
		{Selector: 0x100, Value: 0x4000},
		{Selector: 0x102, Value: 0x0200},
		{Selector: 0x103, Value: 0x004a},
		{Selector: 0x119, Value: 0x0014},
	}
	for _, profile := range []BoardProfile{SCHW240CL28BoardProfile(), SCHW290CK10BoardProfile()} {
		if profile.PBLFixedFeatureFirst != 0x0ff || profile.PBLFixedFeatureSlotCount != 0x13f ||
			!reflect.DeepEqual(profile.PBLFixedFeatures, wantFixedFeatures) {
			t.Fatalf("%s retained fixed PBL features = %#x/%#x/%+v", profile.ID,
				profile.PBLFixedFeatureFirst, profile.PBLFixedFeatureSlotCount, profile.PBLFixedFeatures)
		}
	}
}

func TestRawSamsungAddressBitSevenPanelsStayProfiled(t *testing.T) {
	for _, profile := range []BoardProfile{
		SCHW300DA04BoardProfile(),
		SCHW340DC18BoardProfile(),
	} {
		if profile.Panel.Protocol != ParallelPanelProtocolIndexedRGB565Window454647 ||
			profile.PanelPorts == nil ||
			profile.PanelPorts.CommandAddress != 0x20000000 ||
			profile.PanelPorts.DataAddress != 0x20000080 ||
			profile.PanelPorts.AliasSpan != 0x80 {
			t.Fatalf("%s panel = %+v / %+v", profile.ID, profile.Panel, profile.PanelPorts)
		}
	}
	w320 := SCHW320DC18BoardProfile()
	if w320.Panel.Protocol != ParallelPanelProtocolPackedRGB565Window424A ||
		w320.PanelPorts == nil ||
		w320.PanelPorts.CommandAddress != 0x20000000 ||
		w320.PanelPorts.DataAddress != 0x20000080 ||
		w320.PanelPorts.AliasSpan != 0x80 {
		t.Fatalf("%s packed panel = %+v / %+v", w320.ID, w320.Panel, w320.PanelPorts)
	}
}

func TestSCHW270SelectsMappedQCSBLStackRevision(t *testing.T) {
	profile := SCHW270CL28BoardProfile()
	if profile.PBLStackPointer != 0x03f40000 {
		t.Fatalf("SCH-W270 PBL stack = %#x", profile.PBLStackPointer)
	}
	wantFixedFeatures := []QualcommPBLFixedFeature{
		{Selector: 0xf9, Value: 0x20},
		{Selector: 0xfa, Value: 0x200},
		{Selector: 0xfc, Value: 0x4000},
		{Selector: 0xfd, Value: 0x14},
	}
	if profile.PBLFixedFeatureDataAddress != 0x78002000 ||
		profile.PBLFixedFeatureFirst != 0xf9 || profile.PBLFixedFeatureSlotCount != 5 ||
		!reflect.DeepEqual(profile.PBLFixedFeatures, wantFixedFeatures) {
		t.Fatalf(
			"SCH-W270 fixed PBL features = %#x/%#x/%#x/%+v",
			profile.PBLFixedFeatureDataAddress,
			profile.PBLFixedFeatureFirst,
			profile.PBLFixedFeatureSlotCount,
			profile.PBLFixedFeatures,
		)
	}
	wantHLECalls := []HLECallProfile{
		{
			ID: "w270-pbl-nand-read", Contract: HLEContractQualcommPBLNANDRead,
			Address: 0x03d4b000, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w270-pbl-fatal", Contract: HLEContractQualcommPBLFatal,
			Address: 0x03d4b004, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w270-pbl-nand-bad-block", Contract: HLEContractQualcommPBLNANDBadBlock,
			Address: 0x03d4b008, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	}
	if !reflect.DeepEqual(profile.HLECalls, wantHLECalls) {
		t.Fatalf("SCH-W270 retained PBL HLE calls = %+v", profile.HLECalls)
	}
	found := false
	pblFuseStatus := make(map[uint32]bool)
	stackLatches := make(map[uint32]bool)
	pblClockLatches := make(map[uint32]bool)
	foundRuntimeData := false
	for _, register := range profile.BootControlReadOnlyRegisters {
		found = found || register == (QualcommBootReadOnlyRegister{Offset: 0x0270, Value: 1})
		if register.Offset >= 0x53b8 && register.Offset <= 0x53d0 &&
			(register.Offset == 0x53b8 && register.Value == 2 ||
				register.Offset != 0x53b8 && register.Value == 0) {
			pblFuseStatus[register.Offset] = true
		}
	}
	for _, offset := range profile.BootControlWritableOffsets {
		if offset >= 0x0018 && offset <= 0x0024 {
			stackLatches[offset] = true
		}
		if offset == 0x0a1c || offset == 0x0a2c {
			pblClockLatches[offset] = true
		}
	}
	for _, region := range profile.Memory {
		foundRuntimeData = foundRuntimeData || region == (MemoryRegionProfile{
			ID: "w270-pbl-runtime-data", Kind: MemoryRAM,
			Address: 0x7f000000, Size: 0x1000,
		})
	}
	if !found {
		t.Fatal("SCH-W270 profile lacks QCSBL stack-selection revision")
	}
	if len(pblFuseStatus) != 7 {
		t.Fatalf("SCH-W270 PBL fuse/status words = %#v", pblFuseStatus)
	}
	if len(stackLatches) != 4 {
		t.Fatalf("SCH-W270 QCSBL stack latches = %#v", stackLatches)
	}
	if len(pblClockLatches) != 2 {
		t.Fatalf("SCH-W270 PBL clock latches = %#v", pblClockLatches)
	}
	if !foundRuntimeData {
		t.Fatal("SCH-W270 profile lacks PBL runtime-data memory")
	}
}

func TestSCHW410UsesDedicatedActiveLowEndKey(t *testing.T) {
	profile := SCHW410CL10BoardProfile()
	want := []QualcommPrimaryClockKeyProfile{{
		ID: "end", InputLine: 4, ActiveLow: true,
	}}
	if !reflect.DeepEqual(profile.PrimaryClockKeys, want) {
		t.Fatalf("SCH-W410 primary keys = %+v, want %+v", profile.PrimaryClockKeys, want)
	}
}

func TestSCHW350UsesItsOEMSBLStartupInput(t *testing.T) {
	profile := SCHW350CK06BoardProfile()
	if want := []uint32{0x5000, 0x5100, 0x5200}; !reflect.DeepEqual(profile.BootControlSBIControllers, want) ||
		profile.BootControlSBICompletionStatus != 0x0494 {
		t.Fatalf(
			"SCH-W350 SBI profile = controllers %#v completion %#x, want %#v/0x494",
			profile.BootControlSBIControllers,
			profile.BootControlSBICompletionStatus,
			want,
		)
	}
	wantResponse := QualcommSBIReadResponse{Controller: 0x5000, Address: 0x01, Value: 0x38}
	foundResponse := false
	for _, response := range profile.BootControlSBIReadResponses {
		foundResponse = foundResponse || response == wantResponse
	}
	if !foundResponse {
		t.Fatalf("SCH-W350 PMIC ID response = %#v, want %+v", profile.BootControlSBIReadResponses, wantResponse)
	}
	if !profile.BootControlWatchdogReadable {
		t.Fatal("SCH-W350 watchdog service latch remains write-only")
	}
	if want := []QualcommGPIOInputRegister{{Offset: 0x40, Value: 0}}; !reflect.DeepEqual(
		profile.BootControlGPIOInputs,
		want,
	) {
		t.Fatalf("SCH-W350 legacy GPIO inputs = %+v", profile.BootControlGPIOInputs)
	}
	wantGroupedStatus := []QualcommBootGroupedStatusResponse{
		{
			Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
			GroupStatusOffset: 0x88, GroupMask: 0x02,
		},
		{
			Offset: 0x0380, NANDReadyMask: 0x01,
			GroupStatusOffset: 0x88, GroupMask: 0x01,
		},
	}
	if !reflect.DeepEqual(profile.BootControlGroupedStatusResponses, wantGroupedStatus) {
		t.Fatalf(
			"SCH-W350 grouped NAND status = %+v, want %+v",
			profile.BootControlGroupedStatusResponses,
			wantGroupedStatus,
		)
	}
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wantOffset := uint32(0x4200) + relative
		found := false
		for _, offset := range profile.BootControlMixedWidthOffsets {
			found = found || offset == wantOffset
		}
		if !found {
			t.Fatalf("SCH-W350 second UART register 0x%x is not mixed-width", wantOffset)
		}
	}
	if !slices.Contains(profile.BootControlLegacyUARTControllers, uint32(0x4100)) {
		t.Fatalf("SCH-W350 UIM UART controllers = %#v", profile.BootControlLegacyUARTControllers)
	}
	if want := []QualcommLegacyUARTReceiveData{{
		Controller:        0x4100,
		InterruptSource:   55,
		DelayInstructions: 0,
		EchoTransmit:      true,
		T0Card:            true,
		Data:              []byte{0x3b, 0x00},
	}}; !reflect.DeepEqual(profile.BootControlLegacyUARTReceiveData, want) {
		t.Fatalf("SCH-W350 UIM UART receive data = %#v", profile.BootControlLegacyUARTReceiveData)
	}
	if profile.LegacyInterruptCascade == nil ||
		*profile.LegacyInterruptCascade != (QualcommInterruptCascadeProfile{
			VectoredSource: 17, GroupStatusOffset: 0x8c, GroupMask: 0x02,
		}) {
		t.Fatalf("SCH-W350 legacy interrupt cascade = %+v", profile.LegacyInterruptCascade)
	}
	foundHardwareConfiguration := false
	for _, offset := range profile.BootControlWritableOffsets {
		foundHardwareConfiguration = foundHardwareConfiguration || offset == 0x039c
	}
	if !foundHardwareConfiguration {
		t.Fatal("SCH-W350 profile lacks its late hardware-configuration latch")
	}
	if wantKeys := []QualcommPrimaryClockKeyProfile{{
		ID: "download", InputLine: 2, ActiveLow: true,
	}}; !reflect.DeepEqual(profile.PrimaryClockKeys, wantKeys) {
		t.Fatalf("SCH-W350 primary keys = %+v", profile.PrimaryClockKeys)
	}
	want := QualcommSecondaryClockReadOnlyRegister{
		Offset: qualcommSecondaryClockDisabledStatusOffset,
		Value:  0x00000004,
	}
	found := false
	for _, register := range profile.SecondaryClockReadOnlyRegisters {
		found = found || register == want
	}
	if !found {
		t.Fatalf("SCH-W350 secondary startup inputs = %+v", profile.SecondaryClockReadOnlyRegisters)
	}
	if profile.PanelPorts == nil ||
		*profile.PanelPorts != (ParallelPanelPortProfile{
			CommandAddress: 0x38000000,
			DataAddress:    0x38000004,
		}) || profile.Panel.Protocol != ParallelPanelProtocolPackedRGB565Window424A {
		t.Fatalf("SCH-W350 panel ports = %+v", profile.PanelPorts)
	}
	if want := []IndexedHalfwordRegisterPortProfile{{
		ID:             "w350-indexed-external-registers",
		CommandAddress: 0x20000000,
		DataAddress:    0x20040000,
	}}; !reflect.DeepEqual(profile.IndexedHalfwordRegisterPorts, want) {
		t.Fatalf("SCH-W350 indexed external registers = %+v", profile.IndexedHalfwordRegisterPorts)
	}
	foundSecondaryPanel := false
	for _, window := range profile.LatchedRegisterWindows {
		foundSecondaryPanel = foundSecondaryPanel || window == (LatchedRegisterWindowProfile{
			ID: "w350-secondary-panel-output", Address: 0x40000000,
			Size: 6, Width: Width16,
		})
	}
	if !foundSecondaryPanel {
		t.Fatalf("SCH-W350 secondary panel output = %+v", profile.LatchedRegisterWindows)
	}
	wantHLE := []HLECallProfile{
		{
			ID:       "w350-static-bss-zero",
			Contract: HLEContractSamsungW350StaticBSSZero,
			Address:  0x000a0040, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w350-operator-provisioning",
			Contract: HLEContractSamsungW350OperatorProvisioning,
			Address:  0x00578d0e, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID:       "w350-bootstrap-verified-firmware",
			Contract: HLEContractQualcommBootstrapVerifiedFirmware,
			Address:  0x00113d30, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w350-resident-thumb-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x001129a8, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w350-resident-boot-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x001478c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w350-resident-registration-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x00147968, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w350-resident-registration-finalize",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x00147970, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w350-nv-rebuild-power-cycle",
			Contract: HLEContractSamsungPowerCycle,
			Address:  0x0131cb38, Mode: cpu.ModeThumb, Return: HLEReturnPowerCycle,
		},
	}
	if !reflect.DeepEqual(profile.HLECalls, wantHLE) {
		t.Fatalf("SCH-W350 bootstrap HLE calls = %+v", profile.HLECalls)
	}
}

func TestSCHW320RestoresVerifiedPBLLoaderState(t *testing.T) {
	profile := SCHW320DC18BoardProfile()
	if want := []QualcommPrimaryClockKeyProfile{
		{ID: "download", InputLine: 1, ActiveLow: true},
	}; !reflect.DeepEqual(profile.PrimaryClockKeys, want) {
		t.Fatalf("SCH-W320 primary keys = %+v, want %+v", profile.PrimaryClockKeys, want)
	}
	if want := []QualcommGPIOInputRegister{{Offset: 0x40, Value: 0}}; !reflect.DeepEqual(
		profile.BootControlGPIOInputs,
		want,
	) {
		t.Fatalf("SCH-W320 legacy GPIO inputs = %+v", profile.BootControlGPIOInputs)
	}
	foundRAM := false
	foundMGP := false
	foundMGPData := false
	for _, region := range profile.Memory {
		foundRAM = foundRAM || region == (MemoryRegionProfile{
			ID: "w320-ebi1-ram", Kind: MemorySparseRAM,
			Address: 0x08000000, Size: 0x04000000,
		})
		foundMGP = foundMGP || region == (MemoryRegionProfile{
			ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
			Address: 0x90108000, Size: 0x00008000,
		})
		foundMGPData = foundMGPData || region == (MemoryRegionProfile{
			ID: "w320-mgp-data-ram", Kind: MemorySparseRAM,
			Address: 0x90110000, Size: 0x0000f140,
		})
	}
	if !foundRAM {
		t.Fatal("SCH-W320 profile lacks its second 64 MiB EBI RAM bank")
	}
	if !foundMGP {
		t.Fatal("SCH-W320 profile lacks MGP code RAM")
	}
	if !foundMGPData {
		t.Fatal("SCH-W320 profile lacks MGP data RAM")
	}
	wantMGP := &SamsungMGPProfile{
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0xa0,
		ReleaseOffset: 0x0c, SharedMemoryID: "samsung-mgp-code-ram",
		ReadyOffset: 0x29e0, ReadyValue: 1, ResponseDelayInstructions: 1,
	}
	if !reflect.DeepEqual(profile.SamsungMGP, wantMGP) {
		t.Fatalf("SCH-W320 MGP profile = %+v, want %+v", profile.SamsungMGP, wantMGP)
	}
	foundMGPInterface := false
	foundExternal8Bit := false
	for _, window := range profile.LatchedRegisterWindows {
		foundMGPInterface = foundMGPInterface || window == (LatchedRegisterWindowProfile{
			ID: "samsung-mgp-interface-registers", Address: 0x9011f140,
			Size: 0x60, Width: Width16,
		})
		foundExternal8Bit = foundExternal8Bit || window == (LatchedRegisterWindowProfile{
			ID: "w320-external-8bit-command-data", Address: 0x38000000,
			Size: 4, Width: Width8,
		})
	}
	if !foundMGPInterface {
		t.Fatal("SCH-W320 profile lacks MGP interface registers")
	}
	if !foundExternal8Bit {
		t.Fatal("SCH-W320 profile lacks its second external byte-wide control aperture")
	}
	if !slices.Contains(profile.BootControlLegacyUARTControllers, uint32(0x4100)) {
		t.Fatalf("SCH-W320 UIM UART controllers = %#v", profile.BootControlLegacyUARTControllers)
	}
	if want := []QualcommLegacyUARTReceiveData{{
		Controller:        0x4100,
		InterruptSource:   55,
		DelayInstructions: 0,
		EchoTransmit:      true,
		T0Card:            true,
		Data:              []byte{0x3b, 0x00},
	}}; !reflect.DeepEqual(profile.BootControlLegacyUARTReceiveData, want) {
		t.Fatalf("SCH-W320 UIM UART receive data = %#v", profile.BootControlLegacyUARTReceiveData)
	}
	if profile.LegacyInterruptCascade == nil ||
		*profile.LegacyInterruptCascade != (QualcommInterruptCascadeProfile{
			VectoredSource: 17, GroupStatusOffset: 0x8c, GroupMask: 0x02,
		}) {
		t.Fatalf("SCH-W320 legacy interrupt cascade = %+v", profile.LegacyInterruptCascade)
	}
	for _, offset := range profile.BootControlHalfwordOffsets {
		if offset >= 0x4200 && offset < 0x423c {
			t.Fatalf("SCH-W320 second UART register 0x%x remains halfword-only", offset)
		}
	}
	for _, relative := range qualcommLegacyUARTHalfwordRegisterOffsets {
		wantOffset := uint32(0x4200) + relative
		found := false
		for _, offset := range profile.BootControlMixedWidthOffsets {
			found = found || offset == wantOffset
		}
		if !found {
			t.Fatalf("SCH-W320 second UART register 0x%x is not mixed-width", wantOffset)
		}
	}
	want := []HLECallProfile{
		{
			ID: "w320-pbl-verified-loader-state", Contract: HLEContractQualcommPBLVerifiedLoaderState,
			Address: 0x0010214e, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w320-pbl-cache-maintenance-callback", Contract: HLEContractQualcommResidentBootCallback,
			Address: 0x00102fb2, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w320-resident-boot-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x001138c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w320-late-resident-boot-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x00113ea8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w320-optional-phonebook-preload",
			Contract: HLEContractSamsungOptionalPreloadFile,
			Address:  0x01402864, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w320-optional-multimedia-preload",
			Contract: HLEContractSamsungOptionalPreloadFile,
			Address:  0x014082ba, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
	}
	if !reflect.DeepEqual(profile.HLECalls, want) {
		t.Fatalf("SCH-W320 boot HLE calls = %+v, want %+v", profile.HLECalls, want)
	}
}

func TestSCHW340MapsBoundedMGPRegisters(t *testing.T) {
	profile := SCHW340DC18BoardProfile()
	foundFixedSDCCStatus := false
	for _, register := range profile.BootControlReadOnlyRegisters {
		foundFixedSDCCStatus = foundFixedSDCCStatus || register.Offset == 0x0c34
	}
	if want := []QualcommSDCCControllerConfig{{
		Base: 0x0c00, CardPresent: true,
		GroupStatusOffset: 0x90, GroupMask: 0x01,
	}}; foundFixedSDCCStatus || !reflect.DeepEqual(profile.BootControlSDCCControllers, want) {
		t.Fatalf(
			"SCH-W340 SDCC profile = fixed:%t controllers:%+v, want %+v",
			foundFixedSDCCStatus,
			profile.BootControlSDCCControllers,
			want,
		)
	}
	if !slices.Contains(profile.BootControlLegacyUARTControllers, uint32(0x4100)) {
		t.Fatalf("SCH-W340 UIM UART controllers = %#v", profile.BootControlLegacyUARTControllers)
	}
	if !slices.Contains(profile.BootControlHalfwordOffsets, uint32(0x0480)) {
		t.Fatalf("SCH-W340 late GPIO halfword offsets = %#v", profile.BootControlHalfwordOffsets)
	}
	if want := []QualcommLegacyUARTReceiveData{{
		Controller:                0x4100,
		InterruptSource:           17,
		UseVectoredController:     true,
		VectoredGroupStatusOffset: 0x8c,
		VectoredGroupMask:         0x02,
		DelayInstructions:         4_000_000,
		ReceiveCommand:            0x04,
		ActivationCommand:         0x60,
		PulseReceiveInterrupt:     true,
		EchoTransmit:              true,
		T0Card:                    true,
		Data:                      []byte{0x3b, 0x00},
	}}; !reflect.DeepEqual(profile.BootControlLegacyUARTReceiveData, want) {
		t.Fatalf("SCH-W340 UIM UART receive data = %#v", profile.BootControlLegacyUARTReceiveData)
	}
	if profile.LegacyInterruptCascade != nil {
		t.Fatalf("SCH-W340 legacy interrupt cascade = %+v", profile.LegacyInterruptCascade)
	}
	if profile.VectoredInterrupt == nil ||
		profile.VectoredInterrupt.Groups[2].Source != 17 ||
		profile.VectoredInterrupt.ResetEnabledSources[1] != (1<<10)|(1<<2) ||
		profile.TimeTickClock == nil || profile.TimeTickClock.InterruptSource != 21 {
		t.Fatalf(
			"SCH-W340 compact-VIC profile = vic:%+v tick:%+v",
			profile.VectoredInterrupt,
			profile.TimeTickClock,
		)
	}
	if want := []QualcommBootGroupedStatusResponse{{
		Offset: 0x0380, RequestMask: 0x08, NANDReadyMask: 0x02,
		GroupStatusOffset: 0x88, GroupMask: 0x02,
	}, {
		Offset: 0x0380, NANDReadyMask: 0x01,
		GroupStatusOffset: 0x88, GroupMask: 0x01,
	}}; !reflect.DeepEqual(profile.BootControlGroupedStatusResponses, want) {
		t.Fatalf(
			"SCH-W340 raw-NAND grouped-status response = %+v",
			profile.BootControlGroupedStatusResponses,
		)
	}
	wantHLE := []HLECallProfile{
		{
			ID:       "w340-pbl-fatal",
			Contract: HLEContractQualcommPBLFatal,
			Address:  0x000fff84, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-amss-flash-environment", Contract: HLEContractSamsungAMSSFlashEnvironment,
			Address: 0x000a1514, Mode: cpu.ModeARM, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-amss-bulk-zero", Contract: HLEContractSamsungAMSSBulkZero,
			Address: 0x0142c3d0, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-sbi-transaction", Contract: HLEContractSamsungW340SBITransaction,
			Address: 0x005d39aa, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-sbi-transaction-buffered", Contract: HLEContractSamsungW340SBITransaction,
			Address: 0x005d3940, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-sbi-transaction-late", Contract: HLEContractSamsungW340SBITransaction,
			Address: 0x01d900c0, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-pmic-adc-conversion", Contract: HLEContractSamsungW340PMICADCConversion,
			Address: 0x00505a7a, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-rf-settled-deferred", Contract: HLEContractSamsungW340RFSettledDeferred,
			Address: 0x01a2e598, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-dog-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d70e, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-gsdi-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d1d8, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-gstk-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d1f2, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-callback-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d4c0, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-qvp-app-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d65e, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-qvppl-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d6a8, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-qtv-render-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d780, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-qtv-audio-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d7a4, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-qtv-worker-start-ack", Contract: HLEContractSamsungW340DOGStartAcknowledgement,
			Address: 0x01d8d84e, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-optional-phonebook-preload", Contract: HLEContractSamsungOptionalPreloadFile,
			Address: 0x00fa88e8, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-optional-multimedia-preload", Contract: HLEContractSamsungOptionalPreloadFile,
			Address: 0x00fa9b82, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-optional-module-preload", Contract: HLEContractSamsungOptionalPreloadFile,
			Address: 0x01d291c4, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-bcx-firmware-identity", Contract: HLEContractSamsungW340BCXFirmwareIdentity,
			Address: 0x01d8de4a, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-uim-clock-configuration", Contract: HLEContractSamsungW340UIMClockConfiguration,
			Address: 0x010427ba, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-image-resource-offset", Contract: HLEContractSamsungW340ImageResourceOffset,
			Address: 0x01041ae8, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-display-color", Contract: HLEContractSamsungW340DisplayColor,
			Address: 0x00572fdc, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-font-metrics", Contract: HLEContractSamsungW340FontMetrics,
			Address: 0x00058e34, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-font-draw", Contract: HLEContractSamsungW340FontDraw,
			Address: 0x07fd1380, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-font-measure", Contract: HLEContractSamsungW340FontMeasure,
			Address: 0x07fd1384, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-font-info", Contract: HLEContractSamsungW340FontInfo,
			Address: 0x07fd1388, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-pointer-access-policy", Contract: HLEContractSamsungW340PointerAccessPolicy,
			Address: 0x00042b8c, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-connection-manager", Contract: HLEContractSamsungW340ConnectionManager,
			Address: 0x0054b0d8, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID: "w340-main-applet-lifecycle-prestart", Contract: HLEContractSamsungW340MainAppletLifecycle,
			Address: 0x012b3692, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-main-applet-lifecycle-start", Contract: HLEContractSamsungW340MainAppletLifecycle,
			Address: 0x012b7cd6, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-main-applet-lifecycle-update", Contract: HLEContractSamsungW340MainAppletLifecycle,
			Address: 0x012b7e7c, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-idle-carousel-lifecycle", Contract: HLEContractSamsungW340IdleCarouselLifecycle,
			Address: 0x01959ec4, Mode: cpu.ModeThumb, Return: HLEReturnProgramCounter,
		},
		{
			ID: "w340-idle-applet-dependency", Contract: HLEContractSamsungW340IdleAppletDependency,
			Address: 0x01349f68, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-idle-sim-main-target", Contract: HLEContractSamsungW340IdleSimMainTarget,
			Address: 0x01351376, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-idle-sim-main-activation", Contract: HLEContractSamsungW340IdleSimMainActivation,
			Address: 0x01351670, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-annunciator-start", Contract: HLEContractSamsungW340AnnunciatorStart,
			Address: 0x004d4b18, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-idle-extended-provider", Contract: HLEContractSamsungW340IdleExtendedProvider,
			Address: 0x0134edac, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-idle-primary-notification", Contract: HLEContractSamsungW340IdlePrimaryNotification,
			Address: 0x00c67ae2, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-startup-primary-interface", Contract: HLEContractSamsungW340StartupPrimaryInterface,
			Address: 0x01a22064, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-startup-secondary-interface", Contract: HLEContractSamsungW340StartupSecondaryInterface,
			Address: 0x01a220ba, Mode: cpu.ModeThumb, Return: HLEReturnNextInstruction,
		},
		{
			ID: "w340-mgp-frame-counter", Contract: HLEContractSamsungW340MGPFrameCounter,
			Address: 0x00d58ed0, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	}
	if !reflect.DeepEqual(profile.HLECalls, wantHLE) {
		t.Fatalf("SCH-W340 AMSS environment HLE calls = %+v, want %+v", profile.HLECalls, wantHLE)
	}
	foundRAM := false
	foundDataRAM := false
	foundBREWHighRAM := false
	for _, region := range profile.Memory {
		foundRAM = foundRAM || region == (MemoryRegionProfile{
			ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
			Address: 0x90108000, Size: 0x00008000,
		})
		foundDataRAM = foundDataRAM || region == (MemoryRegionProfile{
			ID: "w340-mgp-data-ram", Kind: MemorySparseRAM,
			Address: 0x90110000, Size: 0x0000f140,
		})
		foundBREWHighRAM = foundBREWHighRAM || region == (MemoryRegionProfile{
			ID: "w340-brew-high-ram", Kind: MemorySparseRAM,
			Address: 0x80010000, Size: 0x03ff0000,
		})
	}
	if !foundRAM {
		t.Fatal("SCH-W340 profile lacks MGP code RAM")
	}
	if !foundDataRAM {
		t.Fatal("SCH-W340 profile lacks MGP data RAM")
	}
	if !foundBREWHighRAM {
		t.Fatal("SCH-W340 profile lacks the identity-mapped BREW high RAM arena")
	}
	want := &SamsungMGPProfile{
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0xa0,
		ReleaseOffset: 0x0c, SharedMemoryID: "samsung-mgp-code-ram",
		ReadyOffset: 0x29e0, ReadyValue: 1, ResponseDelayInstructions: 1,
	}
	if !reflect.DeepEqual(profile.SamsungMGP, want) {
		t.Fatalf("SCH-W340 MGP profile = %+v, want %+v", profile.SamsungMGP, want)
	}
	foundInterface := false
	for _, window := range profile.LatchedRegisterWindows {
		if window.ID == want.ID {
			t.Fatalf("SCH-W340 still maps MGP as a passive register window: %+v", window)
		}
		foundInterface = foundInterface || window == (LatchedRegisterWindowProfile{
			ID: "samsung-mgp-interface-registers", Address: 0x9011f140,
			Size: 0x60, Width: Width16,
		})
	}
	if !foundInterface {
		t.Fatal("SCH-W340 profile lacks its bounded MGP host-interface registers")
	}

	bus := NewBus()
	check(t, profile.ApplyMemory(bus))
	device, err := profile.AttachSamsungMGP(bus)
	check(t, err)
	writeSamsungMGPRegister(t, bus, 0x9011f1ac, 1)
	writeSamsungMGPRegister(t, bus, 0x9011f1ac, 0)
	check(t, device.Advance(1))
	if got := readSamsungMGPByte(t, bus, 0x9010a9e0); got != 1 {
		t.Fatalf("SCH-W340 MGP ready byte = %#x", got)
	}
}

func TestBoardProfileRejectsInvalidSamsungMGP(t *testing.T) {
	for _, mutate := range []func(*BoardProfile){
		func(profile *BoardProfile) { profile.SamsungMGP.ID = "" },
		func(profile *BoardProfile) { profile.SamsungMGP.SharedMemoryID = "missing" },
		func(profile *BoardProfile) { profile.SamsungMGP.ReadyOffset = 0x8000 },
		func(profile *BoardProfile) { profile.SamsungMGP.ReleaseOffset = 1 },
		func(profile *BoardProfile) { profile.SamsungMGP.ReadyValue = 0 },
		func(profile *BoardProfile) { profile.SamsungMGP.ResponseDelayInstructions = 0 },
		func(profile *BoardProfile) { profile.SamsungMGP.Address = 0x90108000 },
		func(profile *BoardProfile) {
			profile.LatchedRegisterWindows = append(
				profile.LatchedRegisterWindows,
				LatchedRegisterWindowProfile{
					ID: "overlap", Address: 0x9011f1a0, Size: 2, Width: Width16,
				},
			)
		},
	} {
		profile := SCHW340DC18BoardProfile()
		mutate(&profile)
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid Samsung MGP: %+v", profile.SamsungMGP)
		}
	}
}

func TestBoardProfileRejectsInvalidMDPProfile(t *testing.T) {
	for _, mutate := range []func(*BoardProfile){
		func(profile *BoardProfile) { profile.MDP.CompletionStartOffset = 0x0e08 },
		func(profile *BoardProfile) { profile.MDP.ScriptPointerOffset = 0x0e0c },
		func(profile *BoardProfile) { profile.MDP.RGB565SourceFormat = 0 },
		func(profile *BoardProfile) { profile.Panel = DCSPanelConfig{} },
	} {
		profile := SCHW830DL21BoardProfile()
		mutate(&profile)
		if err := profile.Validate(); err == nil {
			t.Fatalf("accepted invalid MDP profile %#v", profile.MDP)
		}
	}
}

func TestBoardProfileRejectsInvalidNANDInitialData(t *testing.T) {
	for _, initialData := range [][]FlashSeed{
		{{Offset: 0, Data: []byte{0}}},
		{{Offset: 0x10000000, Data: []byte{0}}},
		{{Offset: 0x0fffffff, Data: []byte{0, 0}}},
		{{Offset: 0x1000, Data: nil}},
		{{Offset: 0x1000, Data: []byte{0, 0}}, {Offset: 0x1001, Data: []byte{0}}},
	} {
		profile := SCHW830DL21BoardProfile()
		profile.NANDInitialData = initialData
		if initialData[0].Offset == 0 {
			profile.NANDSize = 0
		}
		if err := profile.Validate(); err == nil {
			t.Fatalf("accepted invalid NAND initial data %#v", initialData)
		}
	}
}

func TestBoardProfileRejectsInvalidNANDGeometry(t *testing.T) {
	for _, mutate := range []func(*BoardProfile){
		func(profile *BoardProfile) { profile.NANDPageSize = 0 },
		func(profile *BoardProfile) { profile.NANDEraseBlockSize = 0 },
		func(profile *BoardProfile) { profile.NANDPageSize = 0x300 },
		func(profile *BoardProfile) { profile.NANDEraseBlockSize = 0x400 },
		func(profile *BoardProfile) { profile.NANDEraseBlockSize = 0x21000 },
		func(profile *BoardProfile) { profile.NANDSize++ },
		func(profile *BoardProfile) { profile.NANDSize = 0 },
	} {
		profile := SCHW830DL21BoardProfile()
		mutate(&profile)
		if err := profile.Validate(); err == nil {
			t.Fatalf(
				"accepted invalid NAND geometry size=%#x page=%#x erase=%#x",
				profile.NANDSize, profile.NANDPageSize, profile.NANDEraseBlockSize,
			)
		}
	}
}

func TestBoardProfileRejectsUnalignedPBLStackPointer(t *testing.T) {
	profile := SCHW270CL28BoardProfile()
	profile.PBLStackPointer++
	if err := profile.Validate(); err == nil {
		t.Fatalf("accepted unaligned PBL stack pointer %#x", profile.PBLStackPointer)
	}
}

func TestBoardProfileAttachesProfileSelectedMDP(t *testing.T) {
	event := QualcommCompletionEventConfig{
		StartOffset: 0x0e04, StartMask: 1,
		StatusOffset: 0x0e24, StatusMask: 2,
		AcknowledgeOffset: 0x0e28, AcknowledgeWidth: Width16, AcknowledgeMask: 0xffff,
		InterruptSource: 5,
	}
	profile := BoardProfile{
		ID: "test.board", PlatformID: "test.platform", FirmwareBuildID: "test.firmware",
		BootControlWritableOffsets:  []uint32{0x0e04, 0x0e08},
		BootControlHalfwordOffsets:  []uint32{0x0e28},
		BootControlCompletionEvents: []QualcommCompletionEventConfig{event},
		Panel:                       DCSPanelConfig{Width: 2, Height: 2},
		MDP: &QualcommMDPProfile{
			CompletionStartOffset: 0x0e04,
			ScriptPointerOffset:   0x0e08,
			RGB565SourceFormat:    0x20,
		},
	}
	bootControl, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		WritableOffsets:  profile.BootControlWritableOffsets,
		HalfwordOffsets:  profile.BootControlHalfwordOffsets,
		CompletionEvents: profile.BootControlCompletionEvents,
		NANDReady:        NewStatusSignal(),
	})
	check(t, err)
	panel, err := NewDCSPanelController(profile.Panel)
	check(t, err)
	engine, err := profile.AttachMDP(NewBus(), panel, bootControl)
	check(t, err)
	if engine == nil || len(bootControl.orderedCompletionHandlers) != 1 {
		t.Fatalf("attached MDP = %p, handlers = %d", engine, len(bootControl.orderedCompletionHandlers))
	}

	withoutMDP := profile
	withoutMDP.MDP = nil
	engine, err = withoutMDP.AttachMDP(nil, nil, nil)
	if err != nil || engine != nil {
		t.Fatalf("profile without MDP returned engine %p error %v", engine, err)
	}
}

func TestBoardProfileAppliesAddressedStorageWindow(t *testing.T) {
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		AddressedStorageWindows: []AddressedStorageWindowProfile{{
			ID: "storage-data", Address: 0x91000000, Size: 0x20,
			CommandID: "storage-command", CommandAddress: 0x91000100,
			CommandWidth: Width32, AddressMask: 0xffffffe0,
		}},
	}
	image := make([]byte, 0x80)
	for index := range image {
		image[index] = byte(index)
	}
	bus := NewBus()
	check(t, profile.ApplyAddressedStorageWindows(bus, bytes.NewReader(image)))
	var command [4]byte
	binary.LittleEndian.PutUint32(command[:], 0x25)
	check(t, bus.Write(0x91000100, command[:], cpu.PermissionWrite))
	var data [4]byte
	if err := bus.Read(0x91000004, data[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint32(data[:]) != 0x27262524 {
		t.Fatalf("profiled storage word = %x error %v", data, err)
	}
	if err := bus.Write(0x91000000, data[:], cpu.PermissionWrite); err == nil {
		t.Fatal("profiled storage data window accepted a write")
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
	check(t, profile.ApplyLatchedRegisters(bus))
	var data [2]byte
	if err := bus.Read(0x91000002, data[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint16(data[:]) != 0x12 {
		t.Fatalf("profiled latched register = %x error %v", data, err)
	}
	binary.LittleEndian.PutUint16(data[:], 0x3456)
	check(t, bus.Write(0x91000002, data[:], cpu.PermissionWrite))
	clear(data[:])
	_ = bus.Read(0x91000002, data[:], cpu.PermissionRead)
	if binary.LittleEndian.Uint16(data[:]) != 0x3456 {
		t.Fatalf("updated profiled latched register = %x", data)
	}
}

func TestBoardProfileAppliesLatchedRegisterWindows(t *testing.T) {
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		LatchedRegisterWindows: []LatchedRegisterWindowProfile{
			{ID: "external-controls", Address: 0x91000000, Size: 0x10000, Width: Width16},
		},
	}
	bus := NewBus()
	check(t, profile.ApplyLatchedRegisters(bus))
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], 0x3456)
	check(t, bus.Write(0x9100552a, data[:], cpu.PermissionWrite))
	clear(data[:])
	if err := bus.Read(0x9100552a, data[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint16(data[:]) != 0x3456 {
		t.Fatalf("profiled register-window value = %x error %v", data, err)
	}
	var wrongWidth [4]byte
	if err := bus.Read(0x91005528, wrongWidth[:], cpu.PermissionRead); !errors.Is(err, ErrLatchedRegisterWindowMMIO) {
		t.Fatalf("register-window wrong-width error = %v", err)
	}
}

func TestBoardProfileAppliesQualcommADSPMailbox(t *testing.T) {
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		VectoredInterrupt: &QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
		},
		LatchedRegisterWindows: []LatchedRegisterWindowProfile{
			{ID: "shared", Address: 0x91200000, Size: 0x20, Width: Width16},
			{ID: "payload", Address: 0x91400000, Size: 0x20, Width: Width32},
		},
		ADSPMailbox: &QualcommADSPMailboxProfile{
			ID: "dsp-control", Address: 0x91c00000, Size: 0x100, WriteControlOffset: 0x08,
			ControlRules: []QualcommADSPControlRuleProfile{{
				Offset: 0, Value: 2,
				Writes: []QualcommADSPMemoryWriteProfile{{
					WindowID: "shared", Offset: 0x0c, Width: Width16, Value: 1,
				}},
			}, {
				Offset: 4, Value: 1,
				Writes: []QualcommADSPMemoryWriteProfile{{
					WindowID: "shared", Offset: 0x0e, Width: Width16, Value: 1,
				}},
				Interrupt: &QualcommADSPInterruptProfile{
					Source: 33, UseVectoredController: true,
				},
			}},
			HostCommand: &QualcommADSPHostCommandProfile{
				SelectorWindowID: "shared", SelectorOffset: 0x08, SelectorWidth: Width16,
				Rules: []QualcommADSPHostCommandRuleProfile{{
					Command: 1,
					Copies: []QualcommADSPMemoryCopyProfile{{
						SourceWindowID: "payload", SourceOffset: 0x0c,
						DestinationWindowID: "payload", DestinationOffset: 0x08, Width: Width32,
					}},
				}},
			},
		},
	}
	bus := NewBus()
	vic, err := NewQualcommVectoredInterruptController(*profile.VectoredInterrupt, nil)
	check(t, err)
	check(t, profile.ApplyLatchedRegistersWithInterrupts(bus, nil, vic))
	var data [4]byte
	var selector [2]byte
	binary.LittleEndian.PutUint32(data[:], 2)
	check(t, bus.Write(0x91c00000, data[:], cpu.PermissionWrite))
	if err := bus.Read(0x9120000c, selector[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint16(selector[:]) != 1 {
		t.Fatalf("profiled ADSP control response = %x error %v", selector, err)
	}
	binary.LittleEndian.PutUint16(selector[:], 1)
	check(t, bus.Write(0x91200008, selector[:], cpu.PermissionWrite))
	binary.LittleEndian.PutUint32(data[:], 0x11223344)
	check(t, bus.Write(0x9140000c, data[:], cpu.PermissionWrite))
	binary.LittleEndian.PutUint32(data[:], 0x80020000)
	check(t, bus.Write(0x91c00008, data[:], cpu.PermissionWrite))
	clear(data[:])
	if err := bus.Read(0x91c00008, data[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint32(data[:]) != 0x00020000 {
		t.Fatalf("profiled ADSP acknowledgement = %x error %v", data, err)
	}
	clear(selector[:])
	if err := bus.Read(0x91200008, selector[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint16(selector[:]) != 0 {
		t.Fatalf("profiled ADSP selector = %x error %v", selector, err)
	}
	clear(data[:])
	if err := bus.Read(0x91400008, data[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint32(data[:]) != 0x11223344 {
		t.Fatalf("profiled ADSP response = %x error %v", data, err)
	}
	binary.LittleEndian.PutUint32(data[:], 1)
	check(t, bus.Write(0x91c00004, data[:], cpu.PermissionWrite))
	clear(selector[:])
	if err := bus.Read(0x9120000e, selector[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint16(selector[:]) != 1 {
		t.Fatalf("profiled ADSP interrupt response = %x error %v", selector, err)
	}
	if pending := vic.PendingStatusBanks(); pending != [2]uint32{0x00008000, 0} {
		t.Fatalf("profiled ADSP interrupt banks = %#v", pending)
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

func TestBoardProfileRejectsInvalidLatchedRegisterWindows(t *testing.T) {
	for _, windows := range [][]LatchedRegisterWindowProfile{
		{{ID: "bad", Address: 1, Size: 2, Width: Width16}},
		{{ID: "bad", Address: 0x1000, Size: 3, Width: Width16}},
		{{ID: "bad", Address: 0x1000, Size: 4, Width: 0}},
		{{ID: "bad", Address: 0xfffffffe, Size: 4, Width: Width16}},
		{
			{ID: "one", Address: 0x1000, Size: 0x100, Width: Width16},
			{ID: "two", Address: 0x1080, Size: 0x100, Width: Width16},
		},
	} {
		profile := BoardProfile{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			LatchedRegisterWindows: windows,
		}
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid latched register windows: %+v", windows)
		}
	}
	for _, profile := range []BoardProfile{
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			ReadOnlyRegisters: []ReadOnlyRegisterProfile{
				{ID: "read-only", Address: 0x1080, Width: Width32},
			},
			LatchedRegisterWindows: []LatchedRegisterWindowProfile{
				{ID: "window", Address: 0x1000, Size: 0x100, Width: Width16},
			},
		},
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			LatchedRegisters: []LatchedRegisterProfile{
				{ID: "latched", Address: 0x1080, Width: Width16},
			},
			LatchedRegisterWindows: []LatchedRegisterWindowProfile{
				{ID: "window", Address: 0x1000, Size: 0x100, Width: Width16},
			},
		},
	} {
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted overlapping register window: %+v", profile)
		}
	}
}

func TestBoardProfileRejectsInvalidQualcommADSPMailbox(t *testing.T) {
	for _, mailbox := range []QualcommADSPMailboxProfile{
		{},
		{ID: "mailbox", Address: 1, Size: 0x100, WriteControlOffset: 8},
		{ID: "mailbox", Address: 0x1000, Size: 3, WriteControlOffset: 0},
		{ID: "mailbox", Address: 0x1000, Size: 4, WriteControlOffset: 4},
		{ID: "mailbox", Address: 0xfffffffc, Size: 8, WriteControlOffset: 0},
	} {
		profile := BoardProfile{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			ADSPMailbox: &mailbox,
		}
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid ADSP mailbox: %+v", mailbox)
		}
	}
	for _, profile := range []BoardProfile{
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			Memory:      []MemoryRegionProfile{{ID: "ram", Kind: MemoryRAM, Address: 0x1000, Size: 0x100}},
			ADSPMailbox: &QualcommADSPMailboxProfile{ID: "mailbox", Address: 0x1080, Size: 0x100},
		},
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			LatchedRegisterWindows: []LatchedRegisterWindowProfile{
				{ID: "window", Address: 0x1000, Size: 0x100, Width: Width32},
			},
			ADSPMailbox: &QualcommADSPMailboxProfile{ID: "mailbox", Address: 0x1080, Size: 0x100},
		},
	} {
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted overlapping ADSP mailbox: %+v", profile)
		}
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
			BootControlInterruptWindowWritableOffsets: []uint32{0x0904, 0x0904},
		},
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			PrimaryClockWritableOffsets: []uint32{qualcommPrimaryGPIOInputOffset},
		},
		{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			SecondaryClockWritableOffsets: []uint32{qualcommSecondaryClockDisabledStatusOffset},
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

func TestBoardProfileRejectsIncompletePanelDimensions(t *testing.T) {
	for _, panel := range []DCSPanelConfig{{Width: 240}, {Height: 320}} {
		profile := BoardProfile{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build", Panel: panel,
		}
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid panel profile: %+v", panel)
		}
	}
}

func TestBoardProfileRejectsInvalidIndexedHalfwordRegisterPorts(t *testing.T) {
	for _, ports := range [][]IndexedHalfwordRegisterPortProfile{
		{{ID: "", CommandAddress: 0x1000, DataAddress: 0x2000}},
		{{ID: "ports", CommandAddress: 0x1001, DataAddress: 0x2000}},
		{{ID: "ports", CommandAddress: 0x1000, DataAddress: 0x2001}},
		{{ID: "ports", CommandAddress: 0x1000, DataAddress: 0x1000}},
		{
			{ID: "ports", CommandAddress: 0x1000, DataAddress: 0x2000},
			{ID: "ports", CommandAddress: 0x3000, DataAddress: 0x4000},
		},
		{
			{ID: "one", CommandAddress: 0x1000, DataAddress: 0x2000},
			{ID: "two", CommandAddress: 0x3000, DataAddress: 0x2000},
		},
	} {
		profile := BoardProfile{
			ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
			IndexedHalfwordRegisterPorts: ports,
		}
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid indexed halfword ports: %+v", ports)
		}
	}
}

func TestBoardProfileRejectsInvalidPrimaryClockKeys(t *testing.T) {
	mutations := []func(*BoardProfile){
		func(profile *BoardProfile) { profile.PrimaryClockKeys[0].ID = "" },
		func(profile *BoardProfile) { profile.PrimaryClockKeys[0].InputLine = 5 },
		func(profile *BoardProfile) { profile.PrimaryClockKeys[0].ActiveLow = false },
		func(profile *BoardProfile) {
			profile.PrimaryClockKeys = append(profile.PrimaryClockKeys,
				QualcommPrimaryClockKeyProfile{ID: "end", InputLine: 0, ActiveLow: true})
		},
		func(profile *BoardProfile) {
			profile.PrimaryClockKeys = append(profile.PrimaryClockKeys,
				QualcommPrimaryClockKeyProfile{ID: "power", InputLine: 4, ActiveLow: true})
		},
		func(profile *BoardProfile) {
			profile.PrimaryClockKeys[0] = QualcommPrimaryClockKeyProfile{
				ID: "send", InputLine: 4, ActiveLow: true,
			}
		},
		func(profile *BoardProfile) {
			profile.PrimaryClockKeys[0].InputLine = 0
		},
	}
	for index, mutate := range mutations {
		profile := SCHW830DL21BoardProfile()
		mutate(&profile)
		if err := profile.Validate(); err == nil {
			t.Fatalf("BoardProfile accepted invalid primary-clock key case %d: %+v", index, profile.PrimaryClockKeys)
		}
	}
}

func TestBoardProfileRejectsCompletionEventOutsideVectoredController(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	profile.BootControlCompletionEvents[0].InterruptSource = profile.VectoredInterrupt.SourceCount
	if err := profile.Validate(); err == nil {
		t.Fatal("BoardProfile accepted a completion event outside the vectored controller")
	}
}

func TestBoardProfileRejectsTimeTickOutsideVectoredController(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	profile.TimeTickClock.InterruptSource = profile.VectoredInterrupt.SourceCount
	if err := profile.Validate(); err == nil {
		t.Fatal("BoardProfile accepted timetick outside the vectored controller")
	}
}

func TestBoardProfileRejectsInvalidClockRegimeCounter(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	profile.ClockRegimeCounters[0].CounterHz =
		profile.ClockRegimeCounters[0].InstructionsPerSecond + 1
	if err := profile.Validate(); err == nil {
		t.Fatal("BoardProfile accepted invalid clock-regime counter")
	}
}

func TestBoardProfileRejectsClockRegimeComparatorOutsideVectoredController(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	profile.ClockRegimeComparators[0].InterruptSource = profile.VectoredInterrupt.SourceCount
	if err := profile.Validate(); err == nil {
		t.Fatal("BoardProfile accepted clock-regime comparator outside the vectored controller")
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
