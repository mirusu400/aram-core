package system

import (
	"bytes"
	"errors"
	"testing"
)

func TestIndexedHalfwordRegisterPortsPreserveSelectedValues(t *testing.T) {
	registers := NewIndexedHalfwordRegisters(0x0008)
	command, err := NewIndexedHalfwordCommandPort(registers)
	if err != nil {
		t.Fatal(err)
	}
	data, err := NewIndexedHalfwordDataPort(registers)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := command.Read(0, Width16); err != nil || value != 0x0008 {
		t.Fatalf("command status = %#x, %v", value, err)
	}
	if value, err := data.Read(0, Width16); err != nil || value != 0 {
		t.Fatalf("unwritten register = %#x, %v", value, err)
	}
	for _, write := range []struct {
		register uint32
		value    uint32
	}{{0x21, 0x2010}, {0x22, 0x0745}} {
		if err := command.Write(0, Width16, write.register); err != nil {
			t.Fatal(err)
		}
		if err := data.Write(0, Width16, write.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := command.Write(0, Width16, 0x21); err != nil {
		t.Fatal(err)
	}
	if value, err := data.Read(0, Width16); err != nil || value != 0x2010 {
		t.Fatalf("register 0x21 = %#x, %v", value, err)
	}
}

func TestIndexedHalfwordRegisterPortsRejectUnsupportedAccess(t *testing.T) {
	registers := NewIndexedHalfwordRegisters(0)
	command, _ := NewIndexedHalfwordCommandPort(registers)
	data, _ := NewIndexedHalfwordDataPort(registers)
	for _, access := range []func() error{
		func() error { _, err := command.Read(1, Width16); return err },
		func() error { _, err := data.Read(0, Width32); return err },
		func() error { return command.Write(0, Width8, 1) },
		func() error { return data.Write(0, Width16, 0x10000) },
	} {
		if err := access(); !errors.Is(err, ErrIndexedHalfwordRegistersMMIO) {
			t.Fatalf("unsupported access error = %v", err)
		}
	}
	if _, err := NewIndexedHalfwordCommandPort(nil); err == nil {
		t.Fatal("accepted nil indexed register bank")
	}
	if _, err := NewIndexedHalfwordDataPort(nil); err == nil {
		t.Fatal("accepted nil indexed register bank")
	}
}

func TestIndexedHalfwordRegisterPortsRoundTripSharedStateOnce(t *testing.T) {
	registers := NewIndexedHalfwordRegisters(0)
	command, _ := NewIndexedHalfwordCommandPort(registers)
	data, _ := NewIndexedHalfwordDataPort(registers)
	for _, write := range []struct {
		register uint32
		value    uint32
	}{{0x23, 0x0286}, {0x21, 0x2010}} {
		_ = command.Write(0, Width16, write.register)
		_ = data.Write(0, Width16, write.value)
	}
	commandState, err := command.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	dataState, err := data.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if len(dataState) != 9 || len(commandState) <= len(dataState) {
		t.Fatalf("port state lengths = command %d, data %d", len(commandState), len(dataState))
	}

	restoredRegisters := NewIndexedHalfwordRegisters(0)
	restoredCommand, _ := NewIndexedHalfwordCommandPort(restoredRegisters)
	restoredData, _ := NewIndexedHalfwordDataPort(restoredRegisters)
	if err := restoredCommand.LoadState(commandState); err != nil {
		t.Fatal(err)
	}
	if value, err := restoredData.Read(0, Width16); err != nil || value != 0x2010 {
		t.Fatalf("restored selected register = %#x, %v", value, err)
	}
	if err := restoredCommand.Write(0, Width16, 0x23); err != nil {
		t.Fatal(err)
	}
	if value, err := restoredData.Read(0, Width16); err != nil || value != 0x0286 {
		t.Fatalf("restored register 0x23 = %#x, %v", value, err)
	}
	if err := restoredData.LoadState(dataState); err != nil {
		t.Fatal(err)
	}
	if err := restoredData.LoadState(commandState); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("cross-role state error = %v", err)
	}
	if err := restoredData.Reset(); err != nil {
		t.Fatal(err)
	}
	if value, _ := restoredData.Read(0, Width16); value != 0x0286 {
		t.Fatal("data-port reset cleared the shared register bank")
	}
	if err := restoredCommand.Reset(); err != nil {
		t.Fatal(err)
	}
	if value, _ := restoredData.Read(0, Width16); value != 0 {
		t.Fatalf("command-port reset retained register value %#x", value)
	}
	if err := restoredCommand.LoadState(commandState[:len(commandState)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated state error = %v", err)
	}

	otherStatus := NewIndexedHalfwordRegisters(1)
	otherCommand, _ := NewIndexedHalfwordCommandPort(otherStatus)
	if err := otherCommand.LoadState(commandState); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched status state error = %v", err)
	}
}

func TestIndexedHalfwordRegistersSerializeDeterministically(t *testing.T) {
	states := make([][]byte, 0, 2)
	for _, order := range [][]uint16{{0x21, 0x23}, {0x23, 0x21}} {
		registers := NewIndexedHalfwordRegisters(0)
		command, _ := NewIndexedHalfwordCommandPort(registers)
		data, _ := NewIndexedHalfwordDataPort(registers)
		for _, register := range order {
			_ = command.Write(0, Width16, uint32(register))
			_ = data.Write(0, Width16, uint32(register+1))
		}
		_ = command.Write(0, Width16, 0x21)
		state, err := command.SaveState()
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
	}
	if !bytes.Equal(states[0], states[1]) {
		t.Fatal("indexed register state depends on write order")
	}
}
