package system

import (
	"errors"
	"testing"
)

func TestLatchedRegisterEnforcesWidthAndPersists(t *testing.T) {
	device, err := NewLatchedRegister(Width16, 0x1234)
	if err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0, Width16)
	if err != nil || value != 0x1234 {
		t.Fatalf("reset latch = %#x error %v", value, err)
	}
	if err := device.Write(0, Width16, 0xabcd); err != nil {
		t.Fatal(err)
	}
	if _, err := device.Read(0, Width32); !errors.Is(err, ErrLatchedRegisterMMIO) {
		t.Fatalf("wrong-width read error = %v", err)
	}
	if err := device.Write(2, Width16, 0); !errors.Is(err, ErrLatchedRegisterMMIO) {
		t.Fatalf("wrong-offset write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewLatchedRegister(Width16, 0x1234)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	value, _ = restored.Read(0, Width16)
	if value != 0xabcd {
		t.Fatalf("restored latch = %#x", value)
	}
	wrongReset, _ := NewLatchedRegister(Width16, 0)
	if err := wrongReset.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched latch state error = %v", err)
	}
	if err := restored.Reset(); err != nil {
		t.Fatal(err)
	}
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
