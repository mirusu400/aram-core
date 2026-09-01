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

func TestOneNANDReportsTechnologyIdentity(t *testing.T) {
	data := bytes.Repeat([]byte{0xff}, oneNANDEraseBlockSize)
	device, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		TechnologyID:   1,
		Capacity:       uint64(len(data)),
		Storage:        byteStorage{data: data},
	})
	if err != nil {
		t.Fatal(err)
	}
	if technology, readErr := device.Read(oneNANDTechnologyOffset, Width16); readErr != nil || technology != 1 {
		t.Fatalf("technology ID = %#x error %v", technology, readErr)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		Capacity:       uint64(len(data)),
		Storage:        byteStorage{data: data},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("technology-mismatched state error = %v", err)
	}
}

func TestOneNANDResetCommandOverridesPendingInterrupt(t *testing.T) {
	device, _ := newTestOneNAND(t, bytes.Repeat([]byte{0xff}, oneNANDEraseBlockSize))
	if interrupt, err := device.Read(oneNANDInterruptStatusOffset, Width16); err != nil || interrupt != 0x8080 {
		t.Fatalf("cold interrupt = %#x error %v", interrupt, err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandResetCore); err != nil {
		t.Fatal(err)
	}
	if interrupt, err := device.Read(oneNANDInterruptStatusOffset, Width16); err != nil || interrupt != 0x8010 {
		t.Fatalf("reset completion interrupt = %#x error %v", interrupt, err)
	}
	for _, write := range []struct {
		offset uint32
		value  uint32
	}{
		{oneNANDStartAddress1Offset, 0},
		{oneNANDStartAddress8Offset, 0},
		{oneNANDStartBufferOffset, 0x0801},
		{oneNANDCommandOffset, oneNANDCommandRead},
	} {
		if err := device.Write(write.offset, Width16, write.value); err != nil {
			t.Fatal(err)
		}
	}
	if interrupt, err := device.Read(oneNANDInterruptStatusOffset, Width16); err != nil || interrupt != 0x8090 {
		t.Fatalf("read completion over pending reset interrupt = %#x error %v", interrupt, err)
	}
}

func TestOneNANDRejectsUnlockRangeOutsideGeometry(t *testing.T) {
	device, _ := newTestOneNAND(t, bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize))
	for _, write := range []struct {
		offset uint32
		value  uint32
	}{
		{oneNANDInterruptStatusOffset, 0},
		{oneNANDUnlockStartOffset, 2},
		{oneNANDUnlockEndOffset, 2},
		{oneNANDCommandOffset, oneNANDCommandUnlock},
	} {
		if err := device.Write(write.offset, Width16, write.value); err != nil {
			t.Fatal(err)
		}
	}
	if start, err := device.Read(oneNANDUnlockStartOffset, Width16); err != nil || start != 2 {
		t.Fatalf("unlock start = %#x error %v", start, err)
	}
	if end, err := device.Read(oneNANDUnlockEndOffset, Width16); err != nil || end != 2 {
		t.Fatalf("unlock end = %#x error %v", end, err)
	}
	if status, err := device.Read(oneNANDControllerStatusOffset, Width16); err != nil ||
		status&oneNANDStatusCommandError == 0 {
		t.Fatalf("out-of-range unlock status = %#x error %v", status, err)
	}
	if protection, err := device.Read(oneNANDWriteProtectOffset, Width16); err != nil ||
		protection != oneNANDWriteProtectLocked {
		t.Fatalf("out-of-range unlock protection = %#x error %v", protection, err)
	}

	device, _ = newTestOneNAND(t, bytes.Repeat([]byte{0xff}, 2*oneNANDEraseBlockSize))
	for _, write := range []struct {
		offset uint32
		value  uint32
	}{
		{oneNANDInterruptStatusOffset, 0},
		{oneNANDStartAddress1Offset, 0x8000},
		{oneNANDUnlockStartOffset, 0},
		{oneNANDCommandOffset, oneNANDCommandUnlock},
	} {
		if err := device.Write(write.offset, Width16, write.value); err != nil {
			t.Fatal(err)
		}
	}
	if status, err := device.Read(oneNANDControllerStatusOffset, Width16); err != nil ||
		status&oneNANDStatusCommandError == 0 {
		t.Fatalf("invalid-die unlock status = %#x error %v", status, err)
	}
}

func TestOneNANDOTPAccessUsesErasedViewUntilReset(t *testing.T) {
	data := bytes.Repeat([]byte{0x5a}, oneNANDEraseBlockSize)
	device, _ := newTestOneNAND(t, data)
	for _, write := range []struct {
		offset uint32
		value  uint32
	}{
		{oneNANDStartAddress1Offset, 0},
		{oneNANDStartAddress8Offset, 0},
		{oneNANDStartBufferOffset, 0x0801},
		{oneNANDCommandOffset, oneNANDCommandOTPAccess},
		{oneNANDCommandOffset, oneNANDCommandRead},
	} {
		if err := device.Write(write.offset, Width16, write.value); err != nil {
			t.Fatal(err)
		}
	}
	if status, err := device.Read(oneNANDControllerStatusOffset, Width16); err != nil || status != 0 {
		t.Fatalf("OTP controller status = %#x error %v", status, err)
	}
	if value, err := device.Read(0x400, Width32); err != nil || value != 0xffffffff {
		t.Fatalf("OTP data = %#x error %v", value, err)
	}

	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := newTestOneNAND(t, data)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if err := restored.Write(oneNANDCommandOffset, Width16, oneNANDCommandReset); err != nil {
		t.Fatal(err)
	}
	if err := restored.Write(oneNANDCommandOffset, Width16, oneNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	if value, err := restored.Read(0x400, Width32); err != nil || value != 0x5a5a5a5a {
		t.Fatalf("post-reset media data = %#x error %v", value, err)
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

func TestFlexOneNANDMapsRawSLCAndMLCBlockGeometry(t *testing.T) {
	const capacity = uint64(0x20000000)
	geometry := &OneNANDFlexGeometry{
		PageSize: 0x1000, BlockCount: 0x400, SLCBoundary: 0x0f,
		SLCBlockSize: 0x40000, MLCBlockSize: 0x80000,
	}
	rawOffset := func(block, page uint32) uint64 {
		if block <= geometry.SLCBoundary {
			return uint64(block)*uint64(geometry.SLCBlockSize) + uint64(page)*uint64(geometry.PageSize)
		}
		slcBlocks := uint64(geometry.SLCBoundary) + 1
		return slcBlocks*uint64(geometry.SLCBlockSize) +
			uint64(block-geometry.SLCBoundary-1)*uint64(geometry.MLCBlockSize) +
			uint64(page)*uint64(geometry.PageSize)
	}
	tests := []struct {
		block  uint32
		page   uint32
		marker []byte
	}{
		{0x0f, 0x3f, []byte{0x0f, 0x3f, 0x11, 0x22}},
		{0x10, 0x00, []byte{0x10, 0x00, 0x33, 0x44}},
		{0x10, 0x7f, []byte{0x10, 0x7f, 0x55, 0x66}},
		{0x326, 0x00, []byte{'L', 'P', 'C', 'H'}},
	}
	seeds := make([]FlashSeed, 0, len(tests))
	for _, test := range tests {
		seeds = append(seeds, FlashSeed{Offset: rawOffset(test.block, test.page), Data: test.marker})
	}
	flash, err := NewCOWFlashWithCapacityAndSeeds(
		byteStorage{data: bytes.Repeat([]byte{0xff}, oneNANDEraseBlockSize)},
		capacity,
		oneNANDEraseBlockSize,
		"flex-onenand-test",
		seeds,
	)
	if err != nil {
		t.Fatal(err)
	}
	device, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		TechnologyID:   1,
		Capacity:       capacity,
		FlexGeometry:   geometry,
		Storage:        flash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if size, readErr := device.Read(oneNANDDataBufferSizeOffset, Width16); readErr != nil || size != 0x1000 {
		t.Fatalf("Flex-OneNAND data buffer size = %#x error %v", size, readErr)
	}
	for _, test := range tests {
		for _, write := range []struct {
			offset uint32
			value  uint32
		}{
			{oneNANDInterruptStatusOffset, 0},
			{oneNANDStartAddress1Offset, test.block},
			{oneNANDStartAddress8Offset, test.page << 2},
			{oneNANDStartBufferOffset, 0x0800},
			{oneNANDCommandOffset, oneNANDCommandRead},
		} {
			if writeErr := device.Write(write.offset, Width16, write.value); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		loaded := make([]byte, len(test.marker))
		for index := range loaded {
			value, readErr := device.Read(0x400+uint32(index), Width8)
			if readErr != nil {
				t.Fatal(readErr)
			}
			loaded[index] = byte(value)
		}
		if !bytes.Equal(loaded, test.marker) {
			t.Fatalf("raw block/page %#x/%#x loaded %x, want %x", test.block, test.page, loaded, test.marker)
		}
		if status, readErr := device.Read(oneNANDControllerStatusOffset, Width16); readErr != nil || status != 0 {
			t.Fatalf("raw block/page %#x/%#x status = %#x error %v", test.block, test.page, status, readErr)
		}
	}

	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	wrongGeometry := *geometry
	wrongGeometry.SLCBoundary = 0x1f
	wrong, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec,
		DeviceID:       0x0250,
		TechnologyID:   1,
		Capacity:       capacity,
		FlexGeometry:   &wrongGeometry,
		Storage:        flash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("geometry-mismatched state error = %v", err)
	}
}

func TestFlexOneNANDErasesEveryUnderlyingCOWBlock(t *testing.T) {
	const capacity = uint64(0x20000000)
	geometry := &OneNANDFlexGeometry{
		PageSize: 0x1000, BlockCount: 0x400, SLCBoundary: 0x0f,
		SLCBlockSize: 0x40000, MLCBlockSize: 0x80000,
	}
	const mlcOffset = uint64(0x400000)
	flash, err := NewCOWFlashWithCapacityAndSeeds(
		byteStorage{data: bytes.Repeat([]byte{0xff}, oneNANDEraseBlockSize)},
		capacity,
		oneNANDEraseBlockSize,
		"flex-onenand-erase-test",
		[]FlashSeed{
			{Offset: mlcOffset, Data: []byte{0x00}},
			{Offset: mlcOffset + uint64(geometry.MLCBlockSize) - 1, Data: []byte{0x00}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	device, err := NewOneNAND(OneNANDConfig{
		ManufacturerID: 0x00ec, DeviceID: 0x0250, TechnologyID: 1,
		Capacity: capacity, FlexGeometry: geometry, Storage: flash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDStartAddress1Offset, Width16, 0x10); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(oneNANDCommandOffset, Width16, oneNANDCommandErase); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []uint64{mlcOffset, mlcOffset + uint64(geometry.MLCBlockSize) - 1} {
		value := []byte{0}
		if _, err := flash.ReadAt(value, int64(offset)); err != nil || value[0] != 0xff {
			t.Fatalf("erased byte at %#x = %#x error %v", offset, value[0], err)
		}
	}
	if dirty := flash.DirtyBlocks(); !bytes.Equal(
		uint32SliceBytes(dirty),
		uint32SliceBytes([]uint32{0x20, 0x21, 0x22, 0x23}),
	) {
		t.Fatalf("Flex-OneNAND erased COW blocks = %#v", dirty)
	}
}

func uint32SliceBytes(values []uint32) []byte {
	result := make([]byte, 4*len(values))
	for index, value := range values {
		binary.LittleEndian.PutUint32(result[index*4:], value)
	}
	return result
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
