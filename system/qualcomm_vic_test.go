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

func TestQualcommVectoredInterruptControllerValidatesPacking(t *testing.T) {
	for _, config := range []QualcommVectoredInterruptConfig{
		{},
		{SourceCount: 49},
		{SourceCount: 49, Bank0Sources: 49},
		{SourceCount: 64, Bank0Sources: 31},
		{SourceCount: 49, Bank0Sources: 25, VectorOffset: 15},
	} {
		if _, err := NewQualcommVectoredInterruptController(config, nil); err == nil {
			t.Fatalf("accepted invalid vectored interrupt config %+v", config)
		}
	}
}
