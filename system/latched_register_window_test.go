package system

import (
	"errors"
	"testing"
)

func TestLatchedRegisterWindowEnforcesRegisterShape(t *testing.T) {
	device, err := NewLatchedRegisterWindow(0x10000, Width16)
	if err != nil {
		t.Fatal(err)
	}
	for offset, value := range map[uint32]uint32{
		0x0000: 0x1234,
		0x0002: 0xabcd,
		0x552a: 0x5678,
		0xfffe: 0x9abc,
	} {
		if err := device.Write(offset, Width16, value); err != nil {
			t.Fatalf("write16 at %#x: %v", offset, err)
		}
		got, err := device.Read(offset, Width16)
		if err != nil || got != value {
			t.Fatalf("read16 at %#x = %#x, %v", offset, got, err)
		}
	}
	for _, access := range []struct {
		offset uint32
		width  Width
	}{
		{0, Width8},
		{0, Width32},
		{1, Width16},
		{0xffff, Width16},
		{0x10000, Width16},
	} {
		if _, err := device.Read(access.offset, access.width); !errors.Is(err, ErrLatchedRegisterWindowMMIO) {
			t.Fatalf("read%d at %#x error = %v", access.width*8, access.offset, err)
		}
	}
	if err := device.Write(0, Width16, 0x10000); !errors.Is(err, ErrLatchedRegisterWindowMMIO) {
		t.Fatalf("out-of-range write value error = %v", err)
	}
}

func TestLatchedRegisterWindowResetAndStateRoundTrip(t *testing.T) {
	device, _ := NewLatchedRegisterWindow(8, Width16)
	_ = device.Write(2, Width16, 0x1234)
	_ = device.Write(6, Width16, 0xabcd)
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewLatchedRegisterWindow(8, Width16)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got, _ := restored.Read(2, Width16); got != 0x1234 {
		t.Fatalf("restored register = %#x", got)
	}
	if err := restored.Reset(); err != nil {
		t.Fatal(err)
	}
	if got, _ := restored.Read(6, Width16); got != 0 {
		t.Fatalf("reset register = %#x", got)
	}
	wrongSize, _ := NewLatchedRegisterWindow(4, Width16)
	if err := wrongSize.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched-size state error = %v", err)
	}
	wrongWidth, _ := NewLatchedRegisterWindow(8, Width32)
	if err := wrongWidth.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched-width state error = %v", err)
	}
	corrupt := append([]byte(nil), state...)
	corrupt[9] = 1
	if err := device.LoadState(corrupt); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("corrupt state error = %v", err)
	}
}

func TestLatchedRegisterWindowRejectsInvalidConfiguration(t *testing.T) {
	for _, fixture := range []struct {
		size  uint32
		width Width
	}{{0, Width16}, {3, Width16}, {4, 0}} {
		if _, err := NewLatchedRegisterWindow(fixture.size, fixture.width); err == nil {
			t.Fatalf("accepted register window size/width %#x/%d", fixture.size, fixture.width)
		}
	}
}
