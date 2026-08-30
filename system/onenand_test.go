package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func newTestOneNAND(t *testing.T, data []byte) (*OneNAND, *COWFlash) {
	t.Helper()
	flash, err := NewCOWFlash(byteStorage{data: data}, oneNANDEraseBlockSize, "onenand-test")
	if err != nil {
		t.Fatal(err)
	}
	spareConfig := Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal())
	spareConfig.Capacity = uint64(len(data))
	spare, err := NewQualcommNAND(flash, spareConfig)
	if err != nil {
		t.Fatal(err)
	}
	device, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec,
		DeviceID:       0x005c,
		Capacity:       uint64(len(data)),
		Storage:        flash,
		Spare:          spare,
	})
	if err != nil {
		t.Fatal(err)
	}
	return device, flash
}

func TestOneNANDIdentifiesAndLoadsMainData(t *testing.T) {
	data := make([]byte, 2*oneNANDEraseBlockSize)
	for index := range data {
		data[index] = byte(index*17 + 3)
	}
	device, _ := newTestOneNAND(t, data)
	if manufacturer, err := device.Read(oneNANDManufacturerIDOffset, Width16); err != nil || manufacturer != 0xec {
		t.Fatalf("manufacturer = %#x error %v", manufacturer, err)
	}
	if deviceID, err := device.Read(oneNANDDeviceIDOffset, Width16); err != nil || deviceID != 0x5c {
		t.Fatalf("device ID = %#x error %v", deviceID, err)
	}
	if interrupt, err := device.Read(oneNANDInterruptStatusOffset, Width16); err != nil || interrupt != 0x8080 {
		t.Fatalf("cold interrupt = %#x error %v", interrupt, err)
	}
	if err := device.Write(oneNANDInterruptStatusOffset, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDStartAddress1Offset, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDStartAddress8Offset, Width16, 4); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDStartBufferOffset, Width16, 0x0800); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < 16; offset += 4 {
		value, err := device.Read(0x400+offset, Width32)
		if err != nil || value != binary.LittleEndian.Uint32(data[oneNANDPageSize+offset:]) {
			t.Fatalf("loaded word %#x = %#x error %v", offset, value, err)
		}
	}
	if interrupt, _ := device.Read(oneNANDInterruptStatusOffset, Width16); interrupt != 0x8080 {
		t.Fatalf("read completion interrupt = %#x", interrupt)
	}
}

func TestOneNANDDecodesSecondDieBlockAddresses(t *testing.T) {
	const capacity = uint64(0x20000000)
	const targetOffset = uint64(0x11200000)
	flash, err := NewCOWFlashWithCapacityAndSeeds(
		byteStorage{data: bytes.Repeat([]byte{0xff}, oneNANDEraseBlockSize)},
		capacity,
		oneNANDEraseBlockSize,
		"onenand-ddp-test",
		[]FlashSeed{{Offset: targetOffset, Data: []byte{0x11, 0x22, 0x33, 0x44}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	device, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec,
		DeviceID:       0x00dc,
		DieBlockOffset: 0x0800,
		Capacity:       capacity,
		Storage:        flash,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []uint32{
		0x8090, // Standard DFS plus die-relative FBA.
		0x8890, // DA05's DFS plus global FBA encoding.
	} {
		if err := device.Write(oneNANDInterruptStatusOffset, Width16, 0); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(oneNANDStartAddress1Offset, Width16, address); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(oneNANDStartAddress8Offset, Width16, 0); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(oneNANDStartBufferOffset, Width16, 0x0801); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandRead); err != nil {
			t.Fatal(err)
		}
		if value, readErr := device.Read(0x400, Width32); readErr != nil || value != 0x44332211 {
			t.Fatalf("SA1 %#x loaded %#x error %v", address, value, readErr)
		}
	}
}

func TestOneNANDProgramsAndErasesSharedFlash(t *testing.T) {
	device, flash := newTestOneNAND(t, bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize))
	if err := device.Write(0x400, Width32, 0x44332211); err != nil {
		t.Fatal(err)
	}
	for _, write := range []struct {
		offset uint32
		value  uint32
	}{
		{oneNANDInterruptStatusOffset, 0},
		{oneNANDStartAddress1Offset, 0},
		{oneNANDStartAddress8Offset, 0},
		{oneNANDStartBufferOffset, 0x0801},
		{oneNANDCommandOffset, oneNANDCommandProgram},
	} {
		if err := device.Write(write.offset, Width16, write.value); err != nil {
			t.Fatal(err)
		}
	}
	programmed := make([]byte, 4)
	if _, err := flash.ReadAt(programmed, 0); err != nil || !bytes.Equal(programmed, []byte{0x11, 0x22, 0x33, 0x44}) {
		t.Fatalf("programmed bytes = %x error %v", programmed, err)
	}
	if err := device.Write(oneNANDInterruptStatusOffset, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandErase); err != nil {
		t.Fatal(err)
	}
	if _, err := flash.ReadAt(programmed, 0); err != nil || !bytes.Equal(programmed, bytes.Repeat([]byte{0xff}, 4)) {
		t.Fatalf("erased bytes = %x error %v", programmed, err)
	}
}

func TestOneNANDTransfersAndErasesSpareWithMainData(t *testing.T) {
	device, _ := newTestOneNAND(t, bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize))
	if err := device.Write(0x400, Width32, 0x44332211); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x10020, Width32, 0x88776655); err != nil {
		t.Fatal(err)
	}
	for _, write := range []struct {
		offset uint32
		value  uint32
	}{
		{oneNANDInterruptStatusOffset, 0},
		{oneNANDStartAddress1Offset, 0},
		{oneNANDStartAddress8Offset, 0},
		{oneNANDStartBufferOffset, 0x0801},
		{oneNANDCommandOffset, oneNANDCommandProgram},
		{oneNANDInterruptStatusOffset, 0},
	} {
		if err := device.Write(write.offset, Width16, write.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := device.Write(0x400, Width32, 0xffffffff); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x10020, Width32, 0xffffffff); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	if value, err := device.Read(0x400, Width32); err != nil || value != 0x44332211 {
		t.Fatalf("loaded main = %#x error %v", value, err)
	}
	if value, err := device.Read(0x10020, Width32); err != nil || value != 0x88776655 {
		t.Fatalf("loaded spare = %#x error %v", value, err)
	}

	if err := device.Write(oneNANDInterruptStatusOffset, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x10020, Width32, 0xffff0050); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandProgramSpare); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDInterruptStatusOffset, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x10020, Width32, 0xffffffff); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandReadSpare); err != nil {
		t.Fatal(err)
	}
	if value, err := device.Read(0x10020, Width32); err != nil || value != 0x88770050 {
		t.Fatalf("programmed spare = %#x error %v", value, err)
	}

	if err := device.Write(oneNANDInterruptStatusOffset, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandErase); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDInterruptStatusOffset, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	if main, _ := device.Read(0x400, Width32); main != 0xffffffff {
		t.Fatalf("erased main = %#x", main)
	}
	if spare, _ := device.Read(0x10020, Width32); spare != 0xffffffff {
		t.Fatalf("erased spare = %#x", spare)
	}
}

func TestOneNANDStateRoundTripKeepsVolatileBuffers(t *testing.T) {
	device, _ := newTestOneNAND(t, bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize))
	if err := device.Write(oneNANDSystemConfig1Offset, Width16, 0x1230); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(0x400, Width32, 0x87654321); err != nil {
		t.Fatal(err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := newTestOneNAND(t, bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize))
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if config, _ := restored.Read(oneNANDSystemConfig1Offset, Width16); config != 0x1220 {
		t.Fatalf("restored config = %#x", config)
	}
	if value, _ := restored.Read(0x400, Width32); value != 0x87654321 {
		t.Fatalf("restored buffer = %#x", value)
	}
	wrong, _ := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0xec, DeviceID: 0x44,
		Capacity: 2 * oneNANDEraseBlockSize,
		Storage:  byteStorage{data: bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize)},
	})
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched state error = %v", err)
	}
}

func TestOneNANDRejectsInvalidGeometryAndAccesses(t *testing.T) {
	if _, err := NewOneNAND(OneNANDConfig{}); !errors.Is(err, ErrInvalidOneNAND) {
		t.Fatalf("empty geometry error = %v", err)
	}
	device, _ := newTestOneNAND(t, bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize))
	if _, err := device.Read(oneNANDManufacturerIDOffset, Width32); !errors.Is(err, ErrOneNANDMMIO) {
		t.Fatalf("wide register read error = %v", err)
	}
	if err := device.Write(oneNANDControllerStatusOffset, Width16, 1); !errors.Is(err, ErrOneNANDMMIO) {
		t.Fatalf("read-only status write error = %v", err)
	}
}
