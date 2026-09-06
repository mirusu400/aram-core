package system

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestLatchedRegisterEnforcesWidthAndPersists(t *testing.T) {
	device, err := NewLatchedRegister(Width16, 0x1234)
	check(t, err)
	value, err := device.Read(0, Width16)
	if err != nil || value != 0x1234 {
		t.Fatalf("reset latch = %#x error %v", value, err)
	}
	check(t, device.Write(0, Width16, 0xabcd))
	if _, err := device.Read(0, Width32); !errors.Is(err, ErrLatchedRegisterMMIO) {
		t.Fatalf("wrong-width read error = %v", err)
	}
	if err := device.Write(2, Width16, 0); !errors.Is(err, ErrLatchedRegisterMMIO) {
		t.Fatalf("wrong-offset write error = %v", err)
	}
	state, err := device.SaveState()
	check(t, err)
	restored, _ := NewLatchedRegister(Width16, 0x1234)
	check(t, restored.LoadState(state))
	value, _ = restored.Read(0, Width16)
	if value != 0xabcd {
		t.Fatalf("restored latch = %#x", value)
	}
	wrongReset, _ := NewLatchedRegister(Width16, 0)
	if err := wrongReset.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched latch state error = %v", err)
	}
	check(t, restored.Reset())
	value, _ = restored.Read(0, Width16)
	if value != 0x1234 {
		t.Fatalf("reset restored latch = %#x", value)
	}
}

func TestLatchedRegisterRejectsInvalidConfiguration(t *testing.T) {
	for _, fixture := range []struct {
		width Width
		value uint32
	}{{0, 0}, {Width16, 0x10000}, {Width8, 0x100}} {
		if _, err := NewLatchedRegister(fixture.width, fixture.value); err == nil {
			t.Fatalf("accepted latched register width/value %d/%#x", fixture.width, fixture.value)
		}
	}
}

func TestLatchedRegisterProfilesInterruptPulses(t *testing.T) {
	interrupts, err := NewQualcommInterruptControllerWithConfig(
		QualcommInterruptControllerConfig{StatusAliases: []QualcommInterruptStatusAlias{{
			Offset: 0x50, Bank: 1,
		}}},
		nil,
	)
	check(t, err)
	device, _ := NewLatchedRegister(Width32, 0)
	check(t, device.AttachWritePulse(1, 1, []uint8{45, 46}, interrupts))
	check(t, device.Write(0, Width32, 0))
	if status, err := interrupts.Read(0x50, Width32); err != nil || status != 0 {
		t.Fatalf("inactive command status = %#x error %v", status, err)
	}
	check(t, device.Write(0, Width32, 7))
	if status, err := interrupts.Read(0x50, Width32); err != nil || status != 0x00006000 {
		t.Fatalf("command completion status = %#x error %v", status, err)
	}
	state, err := device.SaveState()
	check(t, err)
	if version := binary.LittleEndian.Uint32(state[4:8]); version != 2 {
		t.Fatalf("pulsed latch state version = %d, want 2", version)
	}
	restored, _ := NewLatchedRegister(Width32, 0)
	check(t, restored.AttachWritePulse(1, 1, []uint8{45, 46}, interrupts))
	check(t, restored.LoadState(state))
	if value, err := restored.Read(0, Width32); err != nil || value != 7 {
		t.Fatalf("restored pulsed latch = %#x error %v", value, err)
	}
	mismatch, _ := NewLatchedRegister(Width32, 0)
	check(t, mismatch.AttachWritePulse(2, 2, []uint8{45, 46}, interrupts))
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched pulse state error = %v", err)
	}
	for _, invalid := range []struct {
		mask    uint32
		value   uint32
		sources []uint8
		pulser  qualcommInterruptSourcePulser
	}{
		{sources: []uint8{1}, pulser: interrupts},
		{mask: 1, value: 2, sources: []uint8{1}, pulser: interrupts},
		{mask: 1, value: 1, pulser: interrupts},
		{mask: 1, value: 1, sources: []uint8{1}},
		{mask: 1, value: 1, sources: []uint8{64}, pulser: interrupts},
		{mask: 1, value: 1, sources: []uint8{1, 1}, pulser: interrupts},
	} {
		candidate, _ := NewLatchedRegister(Width32, 0)
		if err := candidate.AttachWritePulse(invalid.mask, invalid.value, invalid.sources, invalid.pulser); err == nil {
			t.Fatalf("accepted invalid pulse mask=%#x sources=%v", invalid.mask, invalid.sources)
		}
	}
}

func TestLatchedRegisterPulseRequiresAttachedInterruptController(t *testing.T) {
	// A profiled write pulse without an interrupt controller must be refused
	// while the board is being wired, not dereferenced on a guest MMIO write.
	profile := SCHW599BE30BoardProfile()
	profile.ADSPMailbox = nil
	bus := NewBus()
	err := profile.ApplyLatchedRegisters(bus)
	if err == nil {
		t.Fatal("latched-register pulse accepted without an interrupt controller")
	}
	if !strings.Contains(err.Error(), "w599-bootstrap-control") ||
		!strings.Contains(err.Error(), "no attached interrupt controller") {
		t.Fatalf("unexpected wiring error = %v", err)
	}
}
