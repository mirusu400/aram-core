package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func newTestQualcommSFlash(t *testing.T, data []byte) (*QualcommSFlashController, *OneNAND) {
	t.Helper()
	target, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		Capacity:       uint64(len(data)),
		Storage:        byteStorage{data: data},
	})
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewQualcommSFlashController(target)
	if err != nil {
		t.Fatal(err)
	}
	return controller, target
}

func writeQualcommSFlashOneNANDRegister(
	t *testing.T,
	controller *QualcommSFlashController,
	address uint16,
	value uint16,
) {
	t.Helper()
	for _, write := range []struct {
		offset uint32
		value  uint32
	}{
		{0x0004, uint32(address)},
		{qualcommSFlashGenP0Offset, uint32(value)},
		{qualcommSFlashCommandOffset, 1<<20 | qualcommSFlashCommandRegWrite},
		{qualcommSFlashExecuteOffset, 1},
	} {
		if err := controller.Write(write.offset, Width32, write.value); err != nil {
			t.Fatal(err)
		}
	}
}

func readQualcommSFlashOneNANDRegister(
	t *testing.T,
	controller *QualcommSFlashController,
	address uint16,
) uint16 {
	t.Helper()
	if err := controller.Write(0x0004, Width32, uint32(address)); err != nil {
		t.Fatal(err)
	}
	if err := controller.Write(
		qualcommSFlashCommandOffset,
		Width32,
		1<<20|qualcommSFlashCommandRegRead,
	); err != nil {
		t.Fatal(err)
	}
	if err := controller.Write(qualcommSFlashExecuteOffset, Width32, 1); err != nil {
		t.Fatal(err)
	}
	value, err := controller.Read(qualcommSFlashGenP0Offset, Width32)
	if err != nil {
		t.Fatal(err)
	}
	return uint16(value)
}

func TestQualcommSFlashBridgesOneNANDRegistersAndData(t *testing.T) {
	data := make([]byte, 2*oneNANDEraseBlockSize)
	for index := range data {
		data[index] = byte(index*29 + 7)
	}
	controller, _ := newTestQualcommSFlash(t, data)

	if manufacturer := readQualcommSFlashOneNANDRegister(
		t,
		controller,
		uint16(oneNANDManufacturerIDOffset/2),
	); manufacturer != 0x00ec {
		t.Fatalf("OneNAND manufacturer = %#x", manufacturer)
	}

	writeQualcommSFlashOneNANDRegister(t, controller, uint16(oneNANDInterruptStatusOffset/2), 0)
	writeQualcommSFlashOneNANDRegister(t, controller, uint16(oneNANDStartAddress1Offset/2), 0)
	writeQualcommSFlashOneNANDRegister(t, controller, uint16(oneNANDStartAddress8Offset/2), 0)
	writeQualcommSFlashOneNANDRegister(t, controller, uint16(oneNANDStartBufferOffset/2), 0x0801)
	writeQualcommSFlashOneNANDRegister(t, controller, uint16(oneNANDCommandOffset/2), oneNANDCommandRead)
	if err := controller.Write(qualcommSFlashMacro1Offset, Width32, 0x0200); err != nil {
		t.Fatal(err)
	}
	if err := controller.Write(
		qualcommSFlashCommandOffset,
		Width32,
		256<<20|qualcommSFlashCommandDataRead,
	); err != nil {
		t.Fatal(err)
	}
	if err := controller.Write(qualcommSFlashExecuteOffset, Width32, 1); err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < qualcommSFlashBufferSize; offset += 4 {
		value, err := controller.Read(qualcommSFlashBufferOffset+offset, Width32)
		if err != nil {
			t.Fatal(err)
		}
		if want := binary.LittleEndian.Uint32(data[offset:]); value != want {
			t.Fatalf("SFlash buffer word %#x = %#x, want %#x", offset, value, want)
		}
	}
}

func TestQualcommSFlashReportsCommandReady(t *testing.T) {
	controller, _ := newTestQualcommSFlash(
		t,
		bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize),
	)
	if status, err := controller.Read(qualcommSFlashStatusOffset, Width32); err != nil {
		t.Fatal(err)
	} else if status != qualcommSFlashStatusCommandReady {
		t.Fatalf("reset SFlash status = %#x, want %#x", status, qualcommSFlashStatusCommandReady)
	}

	if err := controller.Write(0x0004, Width32, uint32(oneNANDManufacturerIDOffset/2)); err != nil {
		t.Fatal(err)
	}
	if err := controller.Write(
		qualcommSFlashCommandOffset,
		Width32,
		1<<20|qualcommSFlashCommandRegRead,
	); err != nil {
		t.Fatal(err)
	}
	if err := controller.Write(qualcommSFlashExecuteOffset, Width32, 1); err != nil {
		t.Fatal(err)
	}
	if status, err := controller.Read(qualcommSFlashStatusOffset, Width32); err != nil {
		t.Fatal(err)
	} else if status != qualcommSFlashStatusCommandReady {
		t.Fatalf("completed SFlash status = %#x, want %#x", status, qualcommSFlashStatusCommandReady)
	}
}

func TestQualcommSFlashStateRoundTripIncludesTarget(t *testing.T) {
	controller, _ := newTestQualcommSFlash(
		t,
		bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize),
	)
	writeQualcommSFlashOneNANDRegister(t, controller, uint16(oneNANDSystemConfig1Offset/2), 0x1234)
	if err := controller.Write(qualcommSFlashBufferOffset+4, Width32, 0x44332211); err != nil {
		t.Fatal(err)
	}
	state, err := controller.SaveState()
	if err != nil {
		t.Fatal(err)
	}

	writeQualcommSFlashOneNANDRegister(t, controller, uint16(oneNANDSystemConfig1Offset/2), 0x5678)
	if err := controller.Write(qualcommSFlashBufferOffset+4, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := controller.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if value := readQualcommSFlashOneNANDRegister(
		t,
		controller,
		uint16(oneNANDSystemConfig1Offset/2),
	); value != 0x1234&0xffe0 {
		t.Fatalf("restored OneNAND system config = %#x", value)
	}
	if value, err := controller.Read(qualcommSFlashBufferOffset+4, Width32); err != nil || value != 0x44332211 {
		t.Fatalf("restored SFlash buffer = %#x error %v", value, err)
	}

	corrupt := append([]byte(nil), state...)
	binary.LittleEndian.PutUint32(corrupt[8:12], uint32(len(corrupt)))
	if err := controller.LoadState(corrupt); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("corrupt state error = %v", err)
	}
}

func TestQualcommSFlashRejectsInvalidAccesses(t *testing.T) {
	if _, err := NewQualcommSFlashController(nil); !errors.Is(err, ErrInvalidQualcommSFlash) {
		t.Fatalf("nil target error = %v", err)
	}
	controller, _ := newTestQualcommSFlash(
		t,
		bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize),
	)
	if _, err := controller.Read(0x002c, Width32); !errors.Is(err, ErrQualcommSFlashMMIO) {
		t.Fatalf("unknown read error = %v", err)
	}
	if err := controller.Write(qualcommSFlashCommandOffset, Width32, 1<<20|0xf); err != nil {
		t.Fatal(err)
	}
	if err := controller.Write(qualcommSFlashExecuteOffset, Width32, 1); !errors.Is(err, ErrQualcommSFlashMMIO) {
		t.Fatalf("unknown command error = %v", err)
	}
	if err := controller.Write(qualcommSFlashExecuteOffset, Width32, 2); !errors.Is(err, ErrQualcommSFlashMMIO) {
		t.Fatalf("invalid execute error = %v", err)
	}
}
