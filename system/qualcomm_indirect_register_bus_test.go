package system

import (
	"errors"
	"testing"
)

func selectQualcommIndirectRegister(t *testing.T, device *QualcommIndirectRegisterBus, address uint32) {
	t.Helper()
	if err := device.Write(8, Width16, address&0xffff); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(10, Width16, address>>16); err != nil {
		t.Fatal(err)
	}
}

func TestQualcommIndirectRegisterBusKeepsTargetWordsIndependent(t *testing.T) {
	device, err := NewQualcommIndirectRegisterBus(0x30000608)
	if err != nil {
		t.Fatal(err)
	}
	selectQualcommIndirectRegister(t, device, 0x30006110)
	if err := device.Write(12, Width32, 0x0000000f); err != nil {
		t.Fatal(err)
	}
	selectQualcommIndirectRegister(t, device, 0x30000600)
	if err := device.Write(12, Width16, 0x5678); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(14, Width16, 0x1234); err != nil {
		t.Fatal(err)
	}
	if got, err := device.Read(12, Width32); err != nil || got != 0x12345678 {
		t.Fatalf("first target = %#x, %v", got, err)
	}
	selectQualcommIndirectRegister(t, device, 0x30006110)
	if got, err := device.Read(12, Width32); err != nil || got != 0x0000000f {
		t.Fatalf("second target = %#x, %v", got, err)
	}
}

func TestQualcommIndirectRegisterBusOwnsStatusWords(t *testing.T) {
	device, _ := NewQualcommIndirectRegisterBus(0x30000608)
	selectQualcommIndirectRegister(t, device, 0x30000608)
	if err := device.Write(12, Width32, 0x30000608); err != nil {
		t.Fatal(err)
	}
	if got, _ := device.Read(12, Width32); got != 0 {
		t.Fatalf("guest write changed status to %#x", got)
	}
	if err := device.AssertStatus(0x30000608, 1<<7); err != nil {
		t.Fatal(err)
	}
	if got, _ := device.Read(12, Width32); got != 1<<7 {
		t.Fatalf("asserted status = %#x", got)
	}
	if err := device.ClearStatus(0x30000608, 1<<7); err != nil {
		t.Fatal(err)
	}
	if got, _ := device.Read(12, Width32); got != 0 {
		t.Fatalf("cleared status = %#x", got)
	}
	if err := device.AssertStatus(0x3000060c, 1); !errors.Is(err, ErrQualcommIndirectRegisterBus) {
		t.Fatalf("unprofiled status error = %v", err)
	}
}

func TestQualcommIndirectRegisterBusStateRoundTrip(t *testing.T) {
	device, _ := NewQualcommIndirectRegisterBus(0x30000608)
	selectQualcommIndirectRegister(t, device, 0x30006110)
	_ = device.Write(12, Width32, 0xabcdef01)
	_ = device.Write(0, Width16, 2)
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewQualcommIndirectRegisterBus(0x30000608)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got, _ := restored.Read(12, Width32); got != 0xabcdef01 {
		t.Fatalf("restored data = %#x", got)
	}
	if got, _ := restored.Read(0, Width16); got != 2 {
		t.Fatalf("restored control = %#x", got)
	}
	wrongProfile, _ := NewQualcommIndirectRegisterBus(0x3000060c)
	if err := wrongProfile.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong-profile state error = %v", err)
	}
}

func TestQualcommIndirectRegisterBusRejectsInvalidAccess(t *testing.T) {
	if _, err := NewQualcommIndirectRegisterBus(0x30000609); !errors.Is(err, ErrQualcommIndirectRegisterBus) {
		t.Fatalf("unaligned status error = %v", err)
	}
	device, _ := NewQualcommIndirectRegisterBus()
	if _, err := device.Read(4, Width32); !errors.Is(err, ErrQualcommIndirectRegisterBus) {
		t.Fatalf("invalid read error = %v", err)
	}
	if err := device.Write(12, Width8, 1); !errors.Is(err, ErrQualcommIndirectRegisterBus) {
		t.Fatalf("invalid write error = %v", err)
	}
}
