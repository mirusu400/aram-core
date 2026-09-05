package system

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

type interruptLineProbe struct {
	irq bool
	fiq bool
}

func (p *interruptLineProbe) SetInterruptLine(line cpu.InterruptLine, asserted bool) error {
	switch line {
	case cpu.InterruptIRQ:
		p.irq = asserted
	case cpu.InterruptFIQ:
		p.fiq = asserted
	default:
		return cpu.ErrInvalidAddress
	}
	return nil
}

func TestQualcommInterruptControllerMasksLatchesAndAcknowledgesSources(t *testing.T) {
	probe := &interruptLineProbe{}
	device := NewQualcommInterruptController(probe)
	if err := device.SetSource(2, true); err != nil {
		t.Fatal(err)
	}
	if probe.irq || probe.fiq {
		t.Fatalf("disabled source drove outputs IRQ=%v FIQ=%v", probe.irq, probe.fiq)
	}
	status, err := device.Read(qualcommInterruptStatus0Offset, Width32)
	if err != nil || status != 1<<2 {
		t.Fatalf("latched status = %#x error %v", status, err)
	}
	if err := device.Write(qualcommIRQEnable0Offset, Width32, 1<<2); err != nil {
		t.Fatal(err)
	}
	if !probe.irq || probe.fiq {
		t.Fatalf("IRQ enable outputs IRQ=%v FIQ=%v", probe.irq, probe.fiq)
	}
	if err := device.Write(qualcommInterruptClear0Offset, Width32, 1<<2); err != nil {
		t.Fatal(err)
	}
	if status, _ := device.Read(qualcommInterruptStatus0Offset, Width32); status != 1<<2 {
		t.Fatalf("asserted level was cleared: %#x", status)
	}
	if err := device.SetSource(2, false); err != nil {
		t.Fatal(err)
	}
	if !probe.irq {
		t.Fatal("deasserting source incorrectly cleared sticky status")
	}
	if err := device.Write(qualcommInterruptClear0Offset, Width32, 1<<2); err != nil {
		t.Fatal(err)
	}
	if probe.irq {
		t.Fatal("acknowledged source left IRQ asserted")
	}
}

func TestQualcommInterruptControllerRoutesFIQAndPulseSources(t *testing.T) {
	probe := &interruptLineProbe{}
	device := NewQualcommInterruptController(probe)
	if err := device.Write(qualcommFIQEnable1Offset, Width32, 1<<8); err != nil {
		t.Fatal(err)
	}
	if err := device.PulseSource(40); err != nil {
		t.Fatal(err)
	}
	if probe.irq || !probe.fiq {
		t.Fatalf("FIQ pulse outputs IRQ=%v FIQ=%v", probe.irq, probe.fiq)
	}
	if err := device.Write(qualcommInterruptClear1Offset, Width32, 1<<8); err != nil {
		t.Fatal(err)
	}
	if probe.fiq {
		t.Fatal("cleared pulse left FIQ asserted")
	}
	if err := device.PulseSource(64); err == nil {
		t.Fatal("accepted out-of-range pulse source")
	}
	if err := device.SetSource(64, true); err == nil {
		t.Fatal("accepted out-of-range level source")
	}
}

func TestQualcommInterruptControllerRejectsReservedAccessesAndRestoresState(t *testing.T) {
	probe := &interruptLineProbe{}
	device := NewQualcommInterruptController(probe)
	if _, err := device.Read(qualcommInterruptClear0Offset, Width32); !errors.Is(err, ErrQualcommInterruptControllerMMIO) {
		t.Fatalf("clear-register read error = %v", err)
	}
	if err := device.Write(qualcommInterruptStatus0Offset, Width32, 0); !errors.Is(err, ErrQualcommInterruptControllerMMIO) {
		t.Fatalf("status-register write error = %v", err)
	}
	if _, err := device.Read(qualcommIRQEnable0Offset, Width16); !errors.Is(err, ErrQualcommInterruptControllerMMIO) {
		t.Fatalf("wrong-width read error = %v", err)
	}
	if _, err := device.Read(0x44, Width32); !errors.Is(err, ErrQualcommInterruptControllerMMIO) {
		t.Fatalf("reserved read error = %v", err)
	}
	if err := device.Write(qualcommIRQEnable1Offset, Width32, 1<<3); err != nil {
		t.Fatal(err)
	}
	if err := device.SetSource(35, true); err != nil {
		t.Fatal(err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if version := binary.LittleEndian.Uint32(state[4:8]); version != 1 {
		t.Fatalf("legacy controller state version = %d, want 1", version)
	}
	restoredProbe := &interruptLineProbe{}
	restored := NewQualcommInterruptController(restoredProbe)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if !restoredProbe.irq || restoredProbe.fiq {
		t.Fatalf("restored outputs IRQ=%v FIQ=%v", restoredProbe.irq, restoredProbe.fiq)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated state error = %v", err)
	}
}

func TestQualcommInterruptControllerProfilesLegacyGPIOInputAlias(t *testing.T) {
	device, err := NewQualcommInterruptControllerWithConfig(QualcommInterruptControllerConfig{
		GPIOInputs: []QualcommGPIOInputRegister{
			{Offset: 0x40, Value: 0x20},
			{Offset: 0x44, Value: 0x20000},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0x40, Width32)
	if err != nil || value != 0x20 {
		t.Fatalf("GPIO input alias = %#x error %v", value, err)
	}
	if err := device.Write(0x40, Width32, 0); !errors.Is(err, ErrQualcommInterruptControllerMMIO) {
		t.Fatalf("GPIO input write error = %v", err)
	}
	if value, err = device.Read(0x44, Width32); err != nil || value != 0x20000 {
		t.Fatalf("extended GPIO input alias = %#x error %v", value, err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if version := binary.LittleEndian.Uint32(state[4:8]); version != 2 {
		t.Fatalf("profiled controller state version = %d, want 2", version)
	}
	restored, _ := NewQualcommInterruptControllerWithConfig(QualcommInterruptControllerConfig{
		GPIOInputs: []QualcommGPIOInputRegister{
			{Offset: 0x40, Value: 0x20},
			{Offset: 0x44, Value: 0x20000},
		},
	}, nil)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	mismatch, _ := NewQualcommInterruptControllerWithConfig(QualcommInterruptControllerConfig{
		GPIOInputs: []QualcommGPIOInputRegister{
			{Offset: 0x40, Value: 0},
			{Offset: 0x44, Value: 0x20000},
		},
	}, nil)
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched GPIO input state error = %v", err)
	}
	legacyState, _ := NewQualcommInterruptController(nil).SaveState()
	if err := restored.LoadState(legacyState); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("legacy state accepted by profiled controller: %v", err)
	}
	for _, config := range []QualcommInterruptControllerConfig{
		{GPIOInputs: []QualcommGPIOInputRegister{{Offset: 0x54}}},
		{GPIOInputs: []QualcommGPIOInputRegister{{Offset: 0x40}, {Offset: 0x40}}},
	} {
		if _, err := NewQualcommInterruptControllerWithConfig(config, nil); err == nil {
			t.Fatalf("accepted invalid GPIO input config %#v", config)
		}
	}
}

func TestQualcommInterruptControllerProfilesStatusAliasResetAndClear(t *testing.T) {
	config := QualcommInterruptControllerConfig{StatusAliases: []QualcommInterruptStatusAlias{{
		Offset: 0x50, Bank: 1, ResetValue: 0x00006000,
	}}}
	device, err := NewQualcommInterruptControllerWithConfig(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []uint32{0x50, qualcommInterruptStatus1Offset} {
		value, readErr := device.Read(offset, Width32)
		if readErr != nil || value != 0x00006000 {
			t.Fatalf("reset status at 0x%x = %#x error %v", offset, value, readErr)
		}
	}
	if err := device.Write(0x50, Width32, 0); !errors.Is(err, ErrQualcommInterruptControllerMMIO) {
		t.Fatalf("status alias write error = %v", err)
	}
	if err := device.Write(qualcommInterruptClear1Offset, Width32, 0x00002000); err != nil {
		t.Fatal(err)
	}
	if value, err := device.Read(0x50, Width32); err != nil || value != 0x00004000 {
		t.Fatalf("partially cleared status alias = %#x error %v", value, err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if version := binary.LittleEndian.Uint32(state[4:8]); version != 3 {
		t.Fatalf("status-alias state version = %d, want 3", version)
	}
	restored, _ := NewQualcommInterruptControllerWithConfig(config, nil)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if value, err := restored.Read(0x50, Width32); err != nil || value != 0x00004000 {
		t.Fatalf("restored status alias = %#x error %v", value, err)
	}
	if err := restored.Reset(); err != nil {
		t.Fatal(err)
	}
	if value, err := restored.Read(0x50, Width32); err != nil || value != 0x00006000 {
		t.Fatalf("re-reset status alias = %#x error %v", value, err)
	}
	mismatch, _ := NewQualcommInterruptControllerWithConfig(QualcommInterruptControllerConfig{
		StatusAliases: []QualcommInterruptStatusAlias{{Offset: 0x50, Bank: 1, ResetValue: 0}},
	}, nil)
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched status-alias state error = %v", err)
	}
	for _, invalid := range []QualcommInterruptControllerConfig{
		{StatusAliases: []QualcommInterruptStatusAlias{{Offset: 2, Bank: 1}}},
		{StatusAliases: []QualcommInterruptStatusAlias{{Offset: 0x50, Bank: 2}}},
		{StatusAliases: []QualcommInterruptStatusAlias{{Offset: qualcommInterruptStatus1Offset, Bank: 1}}},
		{StatusAliases: []QualcommInterruptStatusAlias{{Offset: 0x50}, {Offset: 0x50, Bank: 1}}},
		{GPIOInputs: []QualcommGPIOInputRegister{{Offset: 0x40}}, StatusAliases: []QualcommInterruptStatusAlias{{Offset: 0x40}}},
		{StatusAliases: []QualcommInterruptStatusAlias{{Offset: 0x50, Bank: 1, ResetValue: 1}, {Offset: 0x4c, Bank: 1, ResetValue: 2}}},
	} {
		if _, err := NewQualcommInterruptControllerWithConfig(invalid, nil); err == nil {
			t.Fatalf("accepted invalid status alias config %#v", invalid)
		}
	}
}

func TestQualcommBootControlRoutesInterruptWindowToARMCore(t *testing.T) {
	backend := interpreter.New()
	controller := NewQualcommInterruptController(nil)
	vectored, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{SourceCount: 49, Bank0Sources: 25},
		backend,
	)
	if err != nil {
		t.Fatal(err)
	}
	bootControl, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady: NewStatusSignal(), InterruptController: controller,
		VectoredInterruptController: vectored,
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	if err := bus.MapRAM("vectors", 0, 0x2000); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("chip-control", 0x80000000, QualcommBootControlWindowSize, bootControl); err != nil {
		t.Fatal(err)
	}
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	writeWord := func(address, value uint32) {
		t.Helper()
		var encoded [4]byte
		encoded[0] = byte(value)
		encoded[1] = byte(value >> 8)
		encoded[2] = byte(value >> 16)
		encoded[3] = byte(value >> 24)
		if err := bus.Write(address, encoded[:], cpu.PermissionWrite); err != nil {
			t.Fatal(err)
		}
	}
	writeWord(0x18, 0xe3a0002a) // MOV r0, #42
	writeWord(0x1000, 0xe3a00001)
	if err := backend.WriteRegister(cpu.RegisterCPSR, 0x1f); err != nil {
		t.Fatal(err)
	}
	writeWord(0x80000430, 1<<2)
	value, err := bootControl.Read(0x049c, Width32)
	if err != nil || value != 0x3f {
		t.Fatalf("idle IRQ vector = %#x error %v", value, err)
	}
	value, err = bootControl.Read(0x04a0, Width32)
	if err != nil || value != 0x3f {
		t.Fatalf("idle pending IRQ vector = %#x error %v", value, err)
	}
	value, err = bootControl.Read(0x04a8, Width32)
	if err != nil || value != 0xff {
		t.Fatalf("idle in-service IRQ vector = %#x error %v", value, err)
	}
	if err := vectored.SetSource(2, true); err != nil {
		t.Fatal(err)
	}
	value, err = bootControl.Read(0x0474, Width32)
	if err != nil || value != 1<<2 {
		t.Fatalf("vectored low status = %#x error %v", value, err)
	}
	value, err = bootControl.Read(0x049c, Width32)
	if err != nil || value != 2 {
		t.Fatalf("pending IRQ vector = %#x error %v", value, err)
	}
	value, err = bootControl.Read(0x04a8, Width32)
	if err != nil || value != 2 {
		t.Fatalf("in-service IRQ vector = %#x error %v", value, err)
	}
	if err := bootControl.Write(0x04a4, Width32, 0); err != nil {
		t.Fatal(err)
	}
	value, err = bootControl.Read(0x04a8, Width32)
	if err != nil || value != 0xff {
		t.Fatalf("completed in-service IRQ vector = %#x error %v", value, err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || result.PC != 0x1c {
		t.Fatalf("controller-driven IRQ result = %+v", result)
	}
	value, err = backend.ReadRegister(cpu.RegisterR0)
	if err != nil || value != 42 {
		t.Fatalf("IRQ vector r0 = %d error %v", value, err)
	}
}
