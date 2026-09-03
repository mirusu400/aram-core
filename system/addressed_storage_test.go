package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestAddressedReadOnlyStorageWindowSelectsMaskedSourceRange(t *testing.T) {
	image := make([]byte, 0x80)
	for index := range image {
		image[index] = byte(index)
	}
	window, err := NewAddressedReadOnlyStorageWindow(bytes.NewReader(image), 0x20, 0xffffffe0, 0)
	if err != nil {
		t.Fatal(err)
	}
	command, err := NewAddressedStorageCommandRegister(window, Width32)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Write(0, Width32, 0x25); err != nil {
		t.Fatal(err)
	}
	if got, err := window.Read(4, Width32); err != nil || got != 0x27262524 {
		t.Fatalf("selected word = %#x error %v", got, err)
	}
	if _, err := window.Read(0x20, Width32); !errors.Is(err, ErrAddressedStorageWindowMMIO) {
		t.Fatalf("out-of-window read error = %v", err)
	}
	if err := window.Write(0, Width32, 0); !errors.Is(err, ErrAddressedStorageWindowMMIO) {
		t.Fatalf("data-window write error = %v", err)
	}
	if err := command.Write(0, Width16, 0); !errors.Is(err, ErrAddressedStorageWindowMMIO) {
		t.Fatalf("wrong-width command error = %v", err)
	}
	if err := command.Write(0, Width32, 0x85); !errors.Is(err, ErrAddressedStorageWindowMMIO) {
		t.Fatalf("out-of-storage command error = %v", err)
	}
	state, err := command.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if version := binary.LittleEndian.Uint32(state[4:8]); version != 1 {
		t.Fatalf("state version = %d", version)
	}
	if err := command.Write(0, Width32, 0x45); err != nil {
		t.Fatal(err)
	}
	if err := window.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got, err := command.Read(0, Width32); err != nil || got != 0x25 {
		t.Fatalf("restored command = %#x error %v", got, err)
	}
	if err := command.Reset(); err != nil {
		t.Fatal(err)
	}
	if got, err := command.Read(0, Width32); err != nil || got != 0 {
		t.Fatalf("reset command = %#x error %v", got, err)
	}
}

func TestAddressedReadOnlyStorageWindowRejectsInvalidConfiguration(t *testing.T) {
	storage := bytes.NewReader(make([]byte, 0x40))
	for _, fixture := range []struct {
		size  uint32
		mask  uint32
		reset uint32
	}{
		{size: 0},
		{size: 0x40, mask: 0xffffffe0, reset: 0x20},
	} {
		if _, err := NewAddressedReadOnlyStorageWindow(
			storage, fixture.size, fixture.mask, fixture.reset,
		); err == nil {
			t.Fatalf(
				"accepted size/mask/reset %#x/%#x/%#x",
				fixture.size,
				fixture.mask,
				fixture.reset,
			)
		}
	}
}
