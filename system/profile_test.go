package system

import (
	"encoding/binary"
	"errors"
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
	if profile.NANDReadID != 0xecba {
		t.Fatalf("SCH-W830 NAND read ID = %#x", profile.NANDReadID)
	}
	if profile.NANDSize != 0x10000000 {
		t.Fatalf("SCH-W830 NAND size = %#x", profile.NANDSize)
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
	if err := profile.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapRAM("ebi-overlap-check", 0x07fff000, 0x1000); err == nil {
		t.Fatal("board profile did not map 128 MiB EBI RAM")
	}
	if err := bus.MapRAM("adsp-overlap-check", 0x77fff000, 0x1000); err == nil {
		t.Fatal("board profile did not map the complete ADSP address space")
	}
	var adspWord [4]byte
	binary.LittleEndian.PutUint32(adspWord[:], 0x11223344)
	if err := bus.Write(0x70001338, adspWord[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
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
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
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
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
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
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
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
		reportErasedECC  bool
		extraInitialData []FlashSeed
	}{
		{
			profile: SCHW320DC18BoardProfile(), id: "samsung.sch-w320",
			build: "samsung.sch-w320.dc18", packagedEnd: 0x0a760000, reportErasedECC: true,
		},
		{
			profile: SCHW340DC18BoardProfile(), id: "samsung.sch-w340",
			build: "samsung.sch-w340.dc18", packagedEnd: 0x08800000, reportErasedECC: true,
		},
		{
			profile: SCHW350CK06BoardProfile(), id: "samsung.sch-w350",
			build: "samsung.sch-w350.ck06", packagedEnd: 0x08f80000,
			extraInitialData: []FlashSeed{{Offset: 0x019a0004, Data: []byte{0}}},
		},
		{
			profile: SCHW410CL10BoardProfile(), id: "samsung.sch-w410",
			build: "samsung.sch-w410.cl10", packagedEnd: 0x09100000, reportErasedECC: true,
		},
	}
	for _, test := range tests {
		if err := test.profile.Validate(); err != nil {
			t.Fatalf("%s: %v", test.id, err)
		}
		if test.profile.ID != test.id || test.profile.FirmwareBuildID != test.build ||
			test.profile.PlatformID != "qualcomm.arm9-sch-raw-v1" ||
			test.profile.NANDReadID != 0x000098ca || test.profile.NANDSize != 0x10000000 ||
			test.profile.NANDReportsErasedECCCodewords != test.reportErasedECC ||
			test.profile.OneNAND != nil || test.profile.PBLLegacyFeatureDataAddress != 0xffff6044 {
			t.Fatalf("raw Samsung board profile = %+v", test.profile)
		}
		wantInitialData := append([]FlashSeed{{
			Offset: test.packagedEnd,
			Data:   []byte{0xff, 0xfe, 0xaf, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0},
		}}, test.extraInitialData...)
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

func TestSCHW320AndW340UseAddressBitSevenPanel(t *testing.T) {
	for _, profile := range []BoardProfile{SCHW320DC18BoardProfile(), SCHW340DC18BoardProfile()} {
		if profile.Panel.Protocol != ParallelPanelProtocolIndexedRGB565Window454647 ||
			profile.PanelPorts == nil ||
			profile.PanelPorts.CommandAddress != 0x20000000 ||
			profile.PanelPorts.DataAddress != 0x20000080 {
			t.Fatalf("%s panel = %+v / %+v", profile.ID, profile.Panel, profile.PanelPorts)
		}
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
	if !profile.BootControlWatchdogReadable {
		t.Fatal("SCH-W350 watchdog service latch remains write-only")
	}
	if want := []QualcommGPIOInputRegister{{Offset: 0x40, Value: 0}}; !reflect.DeepEqual(
		profile.BootControlGPIOInputs,
		want,
	) {
		t.Fatalf("SCH-W350 legacy GPIO inputs = %+v", profile.BootControlGPIOInputs)
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
			ID:       "w350-bootstrap-verified-firmware",
			Contract: HLEContractQualcommBootstrapVerifiedFirmware,
			Address:  0x00113d30, Mode: cpu.ModeThumb, Return: HLEReturnLinkRegister,
		},
		{
			ID:       "w350-resident-boot-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x001478c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	}
	if !reflect.DeepEqual(profile.HLECalls, wantHLE) {
		t.Fatalf("SCH-W350 bootstrap HLE calls = %+v", profile.HLECalls)
	}
}

func TestSCHW320RestoresVerifiedPBLLoaderState(t *testing.T) {
	profile := SCHW320DC18BoardProfile()
	if want := []QualcommPrimaryClockKeyProfile{{
		ID: "download", InputLine: 1, ActiveLow: true,
	}}; !reflect.DeepEqual(profile.PrimaryClockKeys, want) {
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
	for _, region := range profile.Memory {
		foundRAM = foundRAM || region == (MemoryRegionProfile{
			ID: "w320-ebi1-ram", Kind: MemorySparseRAM,
			Address: 0x08000000, Size: 0x04000000,
		})
		foundMGP = foundMGP || region == (MemoryRegionProfile{
			ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
			Address: 0x90108000, Size: 0x00008000,
		})
	}
	if !foundRAM {
		t.Fatal("SCH-W320 profile lacks its second 64 MiB EBI RAM bank")
	}
	if !foundMGP {
		t.Fatal("SCH-W320 profile lacks MGP code RAM")
	}
	wantMGP := &SamsungMGPProfile{
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0x40,
		ReleaseOffset: 0x0c, SharedMemoryID: "samsung-mgp-code-ram",
		ReadyOffset: 0x29e0, ReadyValue: 1, ResponseDelayInstructions: 1,
	}
	if !reflect.DeepEqual(profile.SamsungMGP, wantMGP) {
		t.Fatalf("SCH-W320 MGP profile = %+v, want %+v", profile.SamsungMGP, wantMGP)
	}
	foundMGPInterface := false
	for _, window := range profile.LatchedRegisterWindows {
		foundMGPInterface = foundMGPInterface || window == (LatchedRegisterWindowProfile{
			ID: "samsung-mgp-interface-registers", Address: 0x9011f140,
			Size: 0x10, Width: Width16,
		})
	}
	if !foundMGPInterface {
		t.Fatal("SCH-W320 profile lacks MGP interface registers")
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
			ID:       "w320-resident-boot-callback",
			Contract: HLEContractQualcommResidentBootCallback,
			Address:  0x001138c8, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	}
	if !reflect.DeepEqual(profile.HLECalls, want) {
		t.Fatalf("SCH-W320 boot HLE calls = %+v, want %+v", profile.HLECalls, want)
	}
}

func TestSCHW340MapsBoundedMGPRegisters(t *testing.T) {
	profile := SCHW340DC18BoardProfile()
	foundRAM := false
	for _, region := range profile.Memory {
		foundRAM = foundRAM || region == (MemoryRegionProfile{
			ID: "samsung-mgp-code-ram", Kind: MemorySparseRAM,
			Address: 0x90108000, Size: 0x00008000,
		})
	}
	if !foundRAM {
		t.Fatal("SCH-W340 profile lacks MGP code RAM")
	}
	want := &SamsungMGPProfile{
		ID: "samsung-mgp-registers", Address: 0x9011f1a0, Size: 0x40,
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
			Size: 0x10, Width: Width16,
		})
	}
	if !foundInterface {
		t.Fatal("SCH-W340 profile lacks its bounded MGP host-interface registers")
	}

	bus := NewBus()
	if err := profile.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	device, err := profile.AttachSamsungMGP(bus)
	if err != nil {
		t.Fatal(err)
	}
	writeSamsungMGPRegister(t, bus, 0x9011f1ac, 1)
	writeSamsungMGPRegister(t, bus, 0x9011f1ac, 0)
	if err := device.Advance(1); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	panel, err := NewDCSPanelController(profile.Panel)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := profile.AttachMDP(NewBus(), panel, bootControl)
	if err != nil {
		t.Fatal(err)
	}
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

func TestBoardProfileAppliesLatchedRegisterWindows(t *testing.T) {
	profile := BoardProfile{
		ID: "board", PlatformID: "platform", FirmwareBuildID: "build",
		LatchedRegisterWindows: []LatchedRegisterWindowProfile{
			{ID: "external-controls", Address: 0x91000000, Size: 0x10000, Width: Width16},
		},
	}
	bus := NewBus()
	if err := profile.ApplyLatchedRegisters(bus); err != nil {
		t.Fatal(err)
	}
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], 0x3456)
	if err := bus.Write(0x9100552a, data[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ApplyLatchedRegistersWithInterrupts(bus, nil, vic); err != nil {
		t.Fatal(err)
	}
	var data [4]byte
	var selector [2]byte
	binary.LittleEndian.PutUint32(data[:], 2)
	if err := bus.Write(0x91c00000, data[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := bus.Read(0x9120000c, selector[:], cpu.PermissionRead); err != nil ||
		binary.LittleEndian.Uint16(selector[:]) != 1 {
		t.Fatalf("profiled ADSP control response = %x error %v", selector, err)
	}
	binary.LittleEndian.PutUint16(selector[:], 1)
	if err := bus.Write(0x91200008, selector[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(data[:], 0x11223344)
	if err := bus.Write(0x9140000c, data[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(data[:], 0x80020000)
	if err := bus.Write(0x91c00008, data[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
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
	if err := bus.Write(0x91c00004, data[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
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
