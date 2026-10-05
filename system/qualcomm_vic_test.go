package system

import (
	"errors"
	"testing"
)

func TestQualcommVectoredInterruptControllerPacksW830SourcesAndVectors(t *testing.T) {
	probe := &interruptLineProbe{}
	device, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25,
			ReverseSourceOrder: true,
		},
		probe,
	)
	check(t, err)
	for _, offset := range []uint32{
		qualcommVICVectorReadOffset,
		qualcommVICPendingReadOffset,
	} {
		value, readErr := device.Read(offset, Width32)
		if readErr != nil || value != qualcommVICNoPendingVector {
			t.Fatalf("idle vector at 0x%x = %#x error %v", offset, value, readErr)
		}
	}
	if value, readErr := device.Read(qualcommVICVectorWriteOffset, Width32); readErr != nil || value != 0 {
		t.Fatalf("idle vector-completion latch = %#x error %v", value, readErr)
	}
	if value, readErr := device.Read(qualcommVICInServiceOffset, Width32); readErr != nil || value != qualcommVICNoInServiceVector {
		t.Fatalf("idle in-service vector = %#x error %v", value, readErr)
	}
	check(t, device.Write(qualcommVICEnable1Offset, Width32, 1<<2))
	check(t, device.PulseSource(21))
	if !probe.irq || probe.fiq {
		t.Fatalf("vectored outputs IRQ=%v FIQ=%v", probe.irq, probe.fiq)
	}
	if status, readErr := device.Read(qualcommVICStatus1Offset, Width32); readErr != nil || status != 1<<2 {
		t.Fatalf("second-bank status = %#x error %v", status, readErr)
	}
	if vector, readErr := device.Read(qualcommVICVectorReadOffset, Width32); readErr != nil || vector != 27 {
		t.Fatalf("claimed vector = %#x error %v", vector, readErr)
	}
	if probe.irq {
		t.Fatal("claimed vector left CPU IRQ asserted while in service")
	}
	if vector, readErr := device.Read(qualcommVICInServiceOffset, Width32); readErr != nil || vector != 27 {
		t.Fatalf("in-service vector = %#x error %v", vector, readErr)
	}
	check(t, device.Write(qualcommVICAcknowledge1Offset, Width32, 1<<2))
	if probe.irq {
		t.Fatal("acknowledged pulse left vectored IRQ asserted")
	}
	if vector, _ := device.Read(qualcommVICInServiceOffset, Width32); vector != 27 {
		t.Fatalf("acknowledged source completed service early: %#x", vector)
	}
	check(t, device.Write(qualcommVICVectorWriteOffset, Width32, 0))
	if vector, _ := device.Read(qualcommVICInServiceOffset, Width32); vector != qualcommVICNoInServiceVector {
		t.Fatalf("completed in-service vector = %#x", vector)
	}

	check(t, device.Write(qualcommVICEnable0Offset, Width32, 1<<12))
	for _, source := range []uint8{21, 36} {
		check(t, device.PulseSource(source))
	}
	if vector, _ := device.Read(qualcommVICPendingReadOffset, Width32); vector != 12 {
		t.Fatalf("fixed-priority vector = %d, want 12", vector)
	}
}

func TestQualcommVectoredInterruptControllerDrainsPendingSourcesInOneService(t *testing.T) {
	probe := &interruptLineProbe{}
	device, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25,
			ReverseSourceOrder: true,
		},
		probe,
	)
	check(t, err)
	check(t, device.Write(qualcommVICEnable0Offset, Width32, 1<<12))
	check(t, device.Write(qualcommVICEnable1Offset, Width32, 1<<2))
	check(t, device.PulseSource(21))
	check(t, device.PulseSource(36))

	if vector, _ := device.Read(qualcommVICVectorReadOffset, Width32); vector != 12 {
		t.Fatalf("first drained vector = %d, want 12", vector)
	}
	check(t, device.Write(qualcommVICAcknowledge0Offset, Width32, 1<<12))
	if probe.irq {
		t.Fatal("second pending source reasserted IRQ during dispatch")
	}
	if vector, _ := device.Read(qualcommVICVectorReadOffset, Width32); vector != 27 {
		t.Fatalf("second drained vector = %d, want 27", vector)
	}
	check(t, device.Write(qualcommVICAcknowledge1Offset, Width32, 1<<2))
	if vector, _ := device.Read(qualcommVICVectorReadOffset, Width32); vector != qualcommVICNoPendingVector {
		t.Fatalf("drained idle vector = %#x", vector)
	}
	if vector, _ := device.Read(qualcommVICInServiceOffset, Width32); vector != qualcommVICNoInServiceVector {
		t.Fatalf("idle-vector read left service active: %#x", vector)
	}
}

func TestQualcommVectoredInterruptControllerPendingReadDoesNotReplaceCurrentVector(t *testing.T) {
	probe := &interruptLineProbe{}
	device, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25,
			ReverseSourceOrder: true,
		},
		probe,
	)
	check(t, err)
	check(t, device.Write(qualcommVICEnable0Offset, Width32, 1<<16))
	check(t, device.Write(qualcommVICEnable1Offset, Width32, 1<<2))
	check(t, device.PulseSource(32))
	check(t, device.PulseSource(21))

	if vector, readErr := device.Read(qualcommVICVectorReadOffset, Width32); readErr != nil || vector != 16 {
		t.Fatalf("claimed current vector = %d error %v", vector, readErr)
	}
	check(t, device.Write(qualcommVICAcknowledge0Offset, Width32, 1<<16))
	if vector, readErr := device.Read(qualcommVICPendingReadOffset, Width32); readErr != nil || vector != 27 {
		t.Fatalf("pending look-ahead vector = %d error %v", vector, readErr)
	}
	if vector, readErr := device.Read(qualcommVICInServiceOffset, Width32); readErr != nil || vector != 16 {
		t.Fatalf("pending look-ahead replaced current vector = %d error %v", vector, readErr)
	}
	check(t, device.Write(qualcommVICAcknowledge1Offset, Width32, 1<<2))
	if vector, readErr := device.Read(qualcommVICPendingReadOffset, Width32); readErr != nil || vector != qualcommVICNoPendingVector {
		t.Fatalf("drained pending vector = %d error %v", vector, readErr)
	}
	if _, valid := device.InServiceSource(); valid {
		t.Fatal("idle pending read did not complete current service")
	}
}

func TestQualcommVectoredInterruptControllerCoalescesRelatchedInServiceSource(t *testing.T) {
	probe := &interruptLineProbe{}
	device, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25,
			ReverseSourceOrder: true,
		},
		probe,
	)
	check(t, err)
	check(t, device.Write(qualcommVICEnable1Offset, Width32, (1<<3)|(1<<7)))
	check(t, device.PulseSource(20))
	if vector, readErr := device.Read(qualcommVICVectorReadOffset, Width32); readErr != nil || vector != 28 {
		t.Fatalf("first periodic vector = %d error %v", vector, readErr)
	}
	check(t, device.Write(qualcommVICAcknowledge1Offset, Width32, 1<<3))
	check(t, device.PulseSource(20))
	check(t, device.PulseSource(16))
	if vector, readErr := device.Read(qualcommVICPendingReadOffset, Width32); readErr != nil || vector != 32 {
		t.Fatalf("lower-priority vector behind relatched service = %d error %v", vector, readErr)
	}
	check(t, device.Write(qualcommVICAcknowledge1Offset, Width32, 1<<7))
	if vector, readErr := device.Read(qualcommVICPendingReadOffset, Width32); readErr != nil || vector != qualcommVICNoPendingVector {
		t.Fatalf("coalesced periodic vector = %d error %v", vector, readErr)
	}
}

func TestQualcommVectoredInterruptControllerPreservesLevelAndState(t *testing.T) {
	config := QualcommVectoredInterruptConfig{
		SourceCount: 49, Bank0Sources: 25,
		ReverseSourceOrder: true,
	}
	device, err := NewQualcommVectoredInterruptController(config, &interruptLineProbe{})
	check(t, err)
	check(t, device.Write(qualcommVICEnable1Offset, Width32, ^uint32(0)))
	if enabled, _ := device.Read(qualcommVICEnable1Offset, Width32); enabled != 0x00ffffff {
		t.Fatalf("masked second-bank enables = %#x", enabled)
	}
	check(t, device.SetSource(0, true))
	check(t, device.Write(qualcommVICAcknowledge1Offset, Width32, 1<<23))
	if status, _ := device.Read(qualcommVICStatus1Offset, Width32); status != 1<<23 {
		t.Fatalf("asserted level status = %#x", status)
	}
	check(t, device.SetSource(0, false))
	check(t, device.Write(qualcommVICAcknowledge1Offset, Width32, 1<<23))
	state, err := device.SaveState()
	check(t, err)
	restoredProbe := &interruptLineProbe{}
	restored, _ := NewQualcommVectoredInterruptController(config, restoredProbe)
	check(t, restored.LoadState(state))
	if restoredProbe.irq || restoredProbe.fiq {
		t.Fatalf("restored empty outputs IRQ=%v FIQ=%v", restoredProbe.irq, restoredProbe.fiq)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated state error = %v", err)
	}
	if err := device.PulseSource(49); err == nil {
		t.Fatal("accepted out-of-range vectored interrupt source")
	}
	if _, err := device.Read(0x08, Width32); !errors.Is(
		err,
		ErrQualcommVectoredInterruptControllerMMIO,
	) {
		t.Fatalf("reserved read error = %v", err)
	}
}

func TestQualcommVectoredInterruptControllerGroupedSourceTracksChildLevel(t *testing.T) {
	probe := &interruptLineProbe{}
	device, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
			GroupCount: 1,
			Groups: [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig{{
				Source: 14, EnableOffset: 0x14, StatusOffset: 0x88, ValidMask: 0x03,
			}},
		},
		probe,
	)
	check(t, err)
	check(t, device.Write(0x14, Width32, 0x01))
	check(t, device.Write(0x14, Width32, 0x02))
	if enabled, readErr := device.Read(0x14, Width32); readErr != nil || enabled != 0x03 {
		t.Fatalf("write-one-to-set group enables = %#x error %v", enabled, readErr)
	}
	check(t, device.Write(qualcommVICEnable1Offset, Width32, 1<<9))
	check(t, device.SetGroupedSource(0x88, 0x02, true))
	if status, readErr := device.Read(qualcommVICStatus1Offset, Width32); readErr != nil || status != 1<<9 {
		t.Fatalf("asserted aggregate status = %#x error %v", status, readErr)
	}
	if !probe.irq {
		t.Fatal("asserted aggregate did not raise IRQ")
	}
	check(t, device.SetGroupedSource(0x88, 0x02, false))
	if status, readErr := device.Read(qualcommVICStatus1Offset, Width32); readErr != nil || status != 0 {
		t.Fatalf("deasserted aggregate status = %#x error %v", status, readErr)
	}
	if probe.irq {
		t.Fatal("deasserted aggregate left IRQ asserted")
	}
}

func TestQualcommVectoredInterruptControllerCompletesDeassertedClaimedGroup(t *testing.T) {
	probe := &interruptLineProbe{}
	device, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
			GroupCount: 1,
			Groups: [qualcommVICMaximumGroups]QualcommVectoredInterruptGroupConfig{{
				Source: 19, EnableOffset: 0x1c, StatusOffset: 0x90, ValidMask: 0x0f,
			}},
		},
		probe,
	)
	check(t, err)
	check(t, device.Write(0x1c, Width32, 1))
	check(t, device.Write(qualcommVICEnable1Offset, Width32, (1<<4)|(1<<18)))
	check(t, device.SetGroupedSource(0x90, 1, true))
	if vector, readErr := device.Read(qualcommVICVectorReadOffset, Width32); readErr != nil || vector != 29 {
		t.Fatalf("claimed grouped vector = %d error %v", vector, readErr)
	}
	if _, valid := device.InServiceSource(); !valid {
		t.Fatal("claimed group did not enter service")
	}
	check(t, device.SetGroupedSource(0x90, 1, false))
	if source, valid := device.InServiceSource(); valid {
		t.Fatalf("deasserted group left source %d in service", source)
	}
	check(t, device.PulseSource(5))
	if !probe.irq {
		t.Fatal("deasserted group blocked following source")
	}
}

func TestQualcommVectoredInterruptControllerValidatesPacking(t *testing.T) {
	for _, config := range []QualcommVectoredInterruptConfig{
		{},
		{SourceCount: 49},
		{SourceCount: 49, Bank0Sources: 49},
		{SourceCount: 64, Bank0Sources: 31},
		{SourceCount: 49, Bank0Sources: 25, VectorOffset: 15},
		{SourceCount: 49, Bank0Sources: 25, ResetEnabledSources: [2]uint32{1 << 25, 0}},
		{SourceCount: 49, Bank0Sources: 25, ResetEnabledSources: [2]uint32{0, 1 << 24}},
	} {
		if _, err := NewQualcommVectoredInterruptController(config, nil); err == nil {
			t.Fatalf("accepted invalid vectored interrupt config %+v", config)
		}
	}
}

func TestQualcommVectoredInterruptControllerRestoresResetEnabledSources(t *testing.T) {
	device, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{
			SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
			ResetEnabledSources: [2]uint32{1 << 7, 1 << 2},
		},
		&interruptLineProbe{},
	)
	check(t, err)
	if got := device.EnabledSourceBanks(); got != [2]uint32{1 << 7, 1 << 2} {
		t.Fatalf("reset enabled sources = %#v", got)
	}
	check(t, device.Write(qualcommVICEnable0Offset, Width32, 1<<8))
	check(t, device.Write(qualcommVICEnable1Offset, Width32, 1<<3))
	if got := device.EnabledSourceBanks(); got != [2]uint32{(1 << 7) | (1 << 8), (1 << 2) | (1 << 3)} {
		t.Fatalf("write-one-to-set enabled sources = %#v", got)
	}
	check(t, device.Reset())
	if got := device.EnabledSourceBanks(); got != [2]uint32{1 << 7, 1 << 2} {
		t.Fatalf("restored reset enabled sources = %#v", got)
	}
}
