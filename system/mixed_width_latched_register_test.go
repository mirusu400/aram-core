package system

import (
	"errors"
	"testing"
)

func TestMixedWidthLatchedRegisterMergesNarrowWrites(t *testing.T) {
	device, err := NewMixedWidthLatchedRegister([]Width{Width32, Width16}, 0x12345678)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0, Width16, 0xabcd); err != nil {
		t.Fatal(err)
	}
	if got, err := device.Read(0, Width32); err != nil || got != 0x1234abcd {
		t.Fatalf("merged word = %#x error %v", got, err)
	}
	if _, err := device.Read(0, Width8); !errors.Is(err, ErrMixedWidthLatchedRegisterMMIO) {
		t.Fatalf("undeclared-width read error = %v", err)
	}
	if err := device.Write(2, Width16, 0); !errors.Is(err, ErrMixedWidthLatchedRegisterMMIO) {
		t.Fatalf("adjacent write error = %v", err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := NewMixedWidthLatchedRegister([]Width{Width16, Width32}, 0x12345678)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got, _ := restored.Read(0, Width32); got != 0x1234abcd {
		t.Fatalf("restored word = %#x", got)
	}
	wrongWidths, _ := NewMixedWidthLatchedRegister([]Width{Width8, Width32}, 0x12345678)
	if err := wrongWidths.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched-width state error = %v", err)
	}
}

func TestMixedWidthLatchedRegisterRejectsInvalidConfiguration(t *testing.T) {
	for _, widths := range [][]Width{
		nil,
		{Width32},
		{Width16, Width16},
		{Width16, 3},
	} {
		if _, err := NewMixedWidthLatchedRegister(widths, 0); err == nil {
			t.Fatalf("accepted widths %v", widths)
		}
	}
	if _, err := NewMixedWidthLatchedRegister([]Width{Width8, Width16}, 0x10000); err == nil {
		t.Fatal("accepted reset value beyond maximum width")
	}
}
