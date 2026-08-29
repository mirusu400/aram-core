package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestQualcommNANDReadsPageThroughFourDataWindows(t *testing.T) {
	data := make([]byte, 0x1000)
	for page := 0; page < 2; page++ {
		for chunk := 0; chunk < 4; chunk++ {
			start := page*0x800 + chunk*0x200
			for index := 0; index < 0x200; index++ {
				data[start+index] = byte(0x10*page + chunk + 1)
			}
		}
	}
	device, err := NewQualcommNAND(byteStorage{data: data}, Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal()))
	if err != nil {
		t.Fatal(err)
	}
	for chunk := 0; chunk < 4; chunk++ {
		if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
			t.Fatal(err)
		}
		value, err := device.Read(0x100, Width32)
		if err != nil || value != uint32(chunk+1)*0x01010101 {
			t.Fatalf("chunk %d data = %#x error %v", chunk, value, err)
		}
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	value, _ := device.Read(0, Width8)
	if value != 1 {
		t.Fatalf("page sequence did not wrap: %#x", value)
	}
	for _, offset := range []uint32{0x200, 0x20c} {
		value, readErr := device.Read(offset, Width32)
		if readErr != nil || value != 0xffffffff {
			t.Fatalf("NAND spare/ECC buffer %#x = %#x error %v", offset, value, readErr)
		}
	}
	if _, err := device.Read(0x210, Width8); !errors.Is(err, ErrQualcommNANDMMIO) {
		t.Fatalf("read beyond NAND SRAM buffer error = %v", err)
	}

	if err := device.Write(qualcommNANDAddressOffset, Width32, 0x200); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	value, _ = device.Read(0, Width16)
	if value != 0x1111 {
		t.Fatalf("second page data = %#x", value)
	}
	status, _ := device.Read(qualcommNANDStatusOffset, Width32)
	if status != 0 {
		t.Fatalf("NAND status = %#x", status)
	}
}

func TestQualcommNANDReportsReadFailureAndRejectsUnknownCommand(t *testing.T) {
	ready := NewStatusSignal()
	device, err := NewQualcommNAND(
		byteStorage{data: bytes.Repeat([]byte{0xff}, 0x800)},
		Qualcomm2K8BitNANDConfig(0xecaa, ready),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0x200); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	status, _ := device.Read(qualcommNANDStatusOffset, Width32)
	if status != qualcommNANDStatusError {
		t.Fatalf("NAND failure status = %#x", status)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, 8); !errors.Is(err, ErrQualcommNANDMMIO) {
		t.Fatalf("unknown command error = %v", err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, 0); err != nil {
		t.Fatalf("NAND controller reset command error = %v", err)
	}
	if ready.Value() != 0 {
		t.Fatal("NAND reset command left the shared ready line asserted")
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, 7); err != nil {
		t.Fatalf("NAND controller mode command error = %v", err)
	}
	if ready.Value() != 2 {
		t.Fatal("NAND mode command did not assert the shared ready line")
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, 4); err != nil {
		t.Fatalf("NAND command 4 error = %v", err)
	}
	if ready.Value() != 1 {
		t.Fatalf("NAND command 4 status = %#x", ready.Value())
	}
	for _, command := range []uint32{5, 6} {
		if err := device.Write(qualcommNANDCommandOffset, Width32, command); err != nil {
			t.Fatalf("NAND identification command %d error = %v", command, err)
		}
	}
	status, err = device.Read(qualcommNANDStatusOffset, Width32)
	if err != nil || status != qualcommNANDStatusDeviceReady|qualcommNANDStatusReady {
		t.Fatalf("NAND status-check result = %#x error %v", status, err)
	}
}

func TestQualcommNANDErasesWritableStorageAndReportsWriteEnable(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0}, 2*qualcomm2K8BitNANDEraseBlockSize)}
	flash, err := NewCOWFlash(base, qualcomm2K8BitNANDEraseBlockSize, "nand-erase-test")
	if err != nil {
		t.Fatal(err)
	}
	ready := NewStatusSignal()
	device, err := NewQualcommNAND(flash, Qualcomm2K8BitNANDConfig(0xecaa, ready))
	if err != nil {
		t.Fatal(err)
	}
	address := uint32(qualcomm2K8BitNANDEraseBlockSize / 4)
	if err := device.Write(qualcommNANDAddressOffset, Width32, address); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandErase); err != nil {
		t.Fatal(err)
	}
	if got := flash.DirtyBlocks(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("erased blocks = %v, want [1]", got)
	}
	data := make([]byte, 32)
	if _, err := flash.ReadAt(data, qualcomm2K8BitNANDEraseBlockSize); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, bytes.Repeat([]byte{0xff}, len(data))) {
		t.Fatalf("erased data = %x", data)
	}
	if ready.Value() != 1 {
		t.Fatalf("erase completion signal = %#x", ready.Value())
	}
	status, err := device.Read(qualcommNANDStatusOffset, Width32)
	if err != nil || status != 0 {
		t.Fatalf("erase status = %#x error %v", status, err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandStatus); err != nil {
		t.Fatal(err)
	}
	status, err = device.Read(qualcommNANDStatusOffset, Width32)
	if err != nil || status != qualcommNANDStatusDeviceReady|
		qualcommNANDStatusReady|qualcommNANDStatusWriteEnabled {
		t.Fatalf("writable status-check result = %#x error %v", status, err)
	}
}

func TestQualcommNANDProgramsPageThroughFourDataWindows(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, qualcomm2K8BitNANDEraseBlockSize)}
	flash, err := NewCOWFlash(base, qualcomm2K8BitNANDEraseBlockSize, "nand-program-test")
	if err != nil {
		t.Fatal(err)
	}
	ready := NewStatusSignal()
	device, err := NewQualcommNAND(flash, Qualcomm2K8BitNANDConfig(0xecaa, ready))
	if err != nil {
		t.Fatal(err)
	}
	for chunk := uint32(0); chunk < 4; chunk++ {
		for offset := uint32(0); offset < qualcommNANDCodewordDataSize; offset += 4 {
			if err := device.Write(offset, Width32, 0x11111111*(chunk+1)); err != nil {
				t.Fatal(err)
			}
		}
		if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(
			qualcommNANDCommandOffset, Width32, qualcommNANDCommandProgram,
		); err != nil {
			t.Fatal(err)
		}
		wantReady := uint32(2)
		if chunk == 3 {
			wantReady = 3
		}
		if ready.Value() != wantReady {
			t.Fatalf("program chunk %d completion = %#x, want %#x", chunk, ready.Value(), wantReady)
		}
	}
	page := make([]byte, 0x800)
	if _, err := flash.ReadAt(page, 0); err != nil {
		t.Fatal(err)
	}
	for chunk := 0; chunk < 4; chunk++ {
		want := bytes.Repeat([]byte{byte(chunk+1) * 0x11}, qualcommNANDCodewordDataSize)
		if !bytes.Equal(page[chunk*0x200:(chunk+1)*0x200], want) {
			t.Fatalf("programmed chunk %d does not match", chunk)
		}
	}
	if ready.Value() != 3 || device.status != 0 || device.nextChunk != device.pageSize {
		t.Fatalf(
			"program completion ready=%#x status=%#x next=%#x",
			ready.Value(), device.status, device.nextChunk,
		)
	}
}

func TestQualcommNANDProgramsReadsAndErasesSparePerCodeword(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, qualcomm2K8BitNANDEraseBlockSize)}
	flash, err := NewCOWFlash(base, qualcomm2K8BitNANDEraseBlockSize, "nand-spare-program-test")
	if err != nil {
		t.Fatal(err)
	}
	device, err := NewQualcommNAND(
		flash,
		Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal()),
	)
	if err != nil {
		t.Fatal(err)
	}
	for chunk := uint32(0); chunk < 4; chunk++ {
		for offset := uint32(0); offset < 0x10; offset += 4 {
			if err := device.Write(
				qualcommNANDCodewordDataSize+offset,
				Width32,
				0x11111111*(chunk+1),
			); err != nil {
				t.Fatal(err)
			}
		}
		if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(
			qualcommNANDCommandOffset, Width32, qualcommNANDCommandProgram,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandStatus); err != nil {
		t.Fatal(err)
	}
	for chunk := uint32(0); chunk < 4; chunk++ {
		if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
			t.Fatal(err)
		}
		for offset := uint32(0); offset < 0x10; offset += 4 {
			value, readErr := device.Read(qualcommNANDCodewordDataSize+offset, Width32)
			if readErr != nil || value != 0x11111111*(chunk+1) {
				t.Fatalf("chunk %d spare %#x = %#x error %v", chunk, offset, value, readErr)
			}
		}
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandErase); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < 0x10; offset += 4 {
		value, readErr := device.Read(qualcommNANDCodewordDataSize+offset, Width32)
		if readErr != nil || value != 0xffffffff {
			t.Fatalf("erased spare %#x = %#x error %v", offset, value, readErr)
		}
	}
}

func TestQualcommNANDSpareMediaStateRoundTripAndProgramInhibit(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, qualcomm2K8BitNANDEraseBlockSize)}
	flash, err := NewCOWFlash(base, qualcomm2K8BitNANDEraseBlockSize, "nand-spare-state-test")
	if err != nil {
		t.Fatal(err)
	}
	config := Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal())
	device, err := NewQualcommNAND(flash, config)
	if err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < qualcommNANDCodewordSpareSize; offset += 4 {
		if err := device.Write(qualcommNANDCodewordDataSize+offset, Width32, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandProgram); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandStatus); err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < qualcommNANDCodewordSpareSize; offset += 4 {
		if err := device.Write(
			qualcommNANDCodewordDataSize+offset, Width32, 0xffffffff,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandProgram); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandStatus); err != nil {
		t.Fatal(err)
	}
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewQualcommNAND(flash, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if err := restored.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := restored.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < qualcommNANDCodewordSpareSize; offset += 4 {
		value, readErr := restored.Read(qualcommNANDCodewordDataSize+offset, Width32)
		if readErr != nil || value != 0 {
			t.Fatalf("restored inhibited spare %#x = %#x error %v", offset, value, readErr)
		}
	}
}

func TestQualcommNANDExposesSharedSpareStorage(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, 2*qualcomm2K8BitNANDEraseBlockSize)}
	flash, err := NewCOWFlash(base, qualcomm2K8BitNANDEraseBlockSize, "nand-shared-spare-test")
	if err != nil {
		t.Fatal(err)
	}
	device, err := NewQualcommNAND(
		flash,
		Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if device.SparePageSize() != 0x40 {
		t.Fatalf("spare page size = %#x", device.SparePageSize())
	}
	spare := make([]byte, device.SparePageSize())
	if err := device.ReadSparePage(spare, 1); err != nil || !allBytes(spare, 0xff) {
		t.Fatalf("erased spare = %x error %v", spare, err)
	}
	programmed := bytes.Repeat([]byte{0xff}, len(spare))
	programmed[3] = 0xa5
	programmed[37] = 0x5a
	if err := device.ProgramSparePage(programmed, 1); err != nil {
		t.Fatal(err)
	}
	programmed[3] = 0xf0
	programmed[37] = 0x0f
	if err := device.ProgramSparePage(programmed, 1); err != nil {
		t.Fatal(err)
	}
	if err := device.ReadSparePage(spare, 1); err != nil {
		t.Fatal(err)
	}
	if spare[3] != 0xa0 || spare[37] != 0x0a {
		t.Fatalf("program-inhibited spare bytes = %#x %#x", spare[3], spare[37])
	}
	if err := device.EraseSpareBlock(0); err != nil {
		t.Fatal(err)
	}
	if err := device.ReadSparePage(spare, 1); err != nil || !allBytes(spare, 0xff) {
		t.Fatalf("erased shared spare = %x error %v", spare, err)
	}
	if err := device.ReadSparePage(spare, 1<<20); !errors.Is(err, ErrFlashBounds) {
		t.Fatalf("out-of-range spare read error = %v", err)
	}
}

func TestQualcommNANDTreatsOneBitsAsProgramInhibitOnRepeatedCodeword(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, qualcomm2K8BitNANDEraseBlockSize)}
	flash, err := NewCOWFlash(base, qualcomm2K8BitNANDEraseBlockSize, "nand-program-inhibit-test")
	if err != nil {
		t.Fatal(err)
	}
	device, err := NewQualcommNAND(
		flash,
		Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal()),
	)
	if err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < qualcommNANDCodewordDataSize; offset += 4 {
		if err := device.Write(offset, Width32, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandProgram); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandStatus); err != nil {
		t.Fatal(err)
	}
	for offset := uint32(0); offset < qualcommNANDCodewordDataSize; offset += 4 {
		if err := device.Write(offset, Width32, 0xffffffff); err != nil {
			t.Fatal(err)
		}
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandProgram); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandStatus); err != nil {
		t.Fatal(err)
	}
	status, err := device.Read(qualcommNANDStatusOffset, Width32)
	if err != nil || status != qualcommNANDStatusDeviceReady|
		qualcommNANDStatusReady|qualcommNANDStatusWriteEnabled {
		t.Fatalf("repeated program status = %#x error %v", status, err)
	}
	programmed := make([]byte, qualcommNANDCodewordDataSize)
	if _, err := flash.ReadAt(programmed, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(programmed, make([]byte, len(programmed))) {
		t.Fatal("program-inhibited one bits changed the stored codeword")
	}
}

func TestQualcommNANDCompletesErasedCodewordReadWithoutControllerError(t *testing.T) {
	ready := NewStatusSignal()
	device, err := NewQualcommNAND(
		byteStorage{data: bytes.Repeat([]byte{0xff}, 0x800)},
		Qualcomm2K8BitNANDConfig(0xecaa, ready),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	status, err := device.Read(qualcommNANDStatusOffset, Width32)
	if err != nil || status != 0 {
		t.Fatalf("erased NAND status = %#x error %v", status, err)
	}
	if ready.Value() != 2 {
		t.Fatalf("erased NAND completion signal = %#x", ready.Value())
	}
	if device.nextChunk != qualcommNANDCodewordDataSize {
		t.Fatalf("erased NAND next chunk = %#x", device.nextChunk)
	}
	for index, value := range device.data {
		if value != 0xff {
			t.Fatalf("erased NAND buffer byte %#x = %#x", index, value)
		}
	}
}

func TestQualcommNANDDoesNotMisclassifyFFCodewordInProgrammedPage(t *testing.T) {
	data := bytes.Repeat([]byte{0xff}, 0x800)
	data[len(data)-1] = 0x5a
	ready := NewStatusSignal()
	device, err := NewQualcommNAND(
		byteStorage{data: data},
		Qualcomm2K8BitNANDConfig(0xecaa, ready),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	status, err := device.Read(qualcommNANDStatusOffset, Width32)
	if err != nil || status != 0 || ready.Value() != 2 {
		t.Fatalf("programmed NAND status = %#x ready %#x error %v", status, ready.Value(), err)
	}
}

func TestQualcommNANDStateRoundTrip(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0x5a}, 0x800)}
	device, err := NewQualcommNAND(base, Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal()))
	if err != nil {
		t.Fatal(err)
	}
	_ = device.Write(qualcommNANDAddressOffset, Width32, 0)
	_ = device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead)
	_ = device.Write(qualcommNANDDeviceConfig0Offset, Width32, 0x12345678)
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewQualcommNAND(base, Qualcomm2K8BitNANDConfig(0xecaa, NewStatusSignal()))
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if restored.nextChunk != 0x200 || !bytes.Equal(restored.data[:], device.data[:]) {
		t.Fatal("NAND state did not round trip")
	}
	if restored.deviceConfig0 != 0x12345678 {
		t.Fatalf("restored NAND device config 0 = %#x", restored.deviceConfig0)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated NAND state error = %v", err)
	}
	const scalarCount = 11
	fullBufferEnd := 8 + scalarCount*4 + len(device.data)
	legacyState := append([]byte(nil), state[:fullBufferEnd-0x10]...)
	legacyState[4] = qualcommNANDLegacyStateVersion
	for index := 5; index < 8; index++ {
		legacyState[index] = 0
	}
	if err := restored.LoadState(legacyState); err != nil {
		t.Fatalf("legacy NAND state migration error = %v", err)
	}
	for index, value := range restored.data[qualcommNANDCodewordDataSize:] {
		if value != 0xff {
			t.Fatalf("migrated NAND spare/ECC byte %d = %#x", index, value)
		}
	}
	previousState := append([]byte(nil), state[:fullBufferEnd]...)
	binary.LittleEndian.PutUint32(previousState[4:8], qualcommNANDPreviousStateVersion)
	binary.LittleEndian.PutUint32(previousState[8+6*4:], 0x80000000)
	if err := restored.LoadState(previousState); err != nil {
		t.Fatalf("previous NAND state migration error = %v", err)
	}
	if restored.status != qualcommNANDStatusOperationError {
		t.Fatalf("migrated NAND operation status = %#x", restored.status)
	}
}

func TestQualcommNANDSynthesizesErasedSpareForLogicalImage(t *testing.T) {
	data := make([]byte, 0x800)
	data[2] = 0x34
	data[3] = 0x12
	ready := NewStatusSignal()
	device, err := NewQualcommNAND(byteStorage{data: data}, Qualcomm2K8BitNANDConfig(0xecaa, ready))
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 2); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandReadSpare); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(qualcommNANDReadDataOffset, Width32)
	if err != nil || value != 0xffff || ready.Value() != 2 {
		t.Fatalf("logical-image spare word = %#x ready %#x error %v", value, ready.Value(), err)
	}
}

func TestQualcommNANDReadsProvidedSpareAtLatchedPageAndColumn(t *testing.T) {
	data := make([]byte, 2*0x800)
	spare := bytes.Repeat([]byte{0xff}, 2*0x10)
	spare[0x10+3] = 0x34
	spare[0x10+4] = 0x12
	ready := NewStatusSignal()
	config := Qualcomm2K8BitNANDConfig(0xecaa, ready)
	config.SpareSize = 0x10
	config.Spare = byteStorage{data: spare}
	device, err := NewQualcommNAND(byteStorage{data: data}, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0x203); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandReadSpare); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(qualcommNANDReadDataOffset, Width32)
	if err != nil || value != 0x1234 || ready.Value() != 2 {
		t.Fatalf("provided spare word = %#x ready %#x error %v", value, ready.Value(), err)
	}
}

func TestQualcommNANDSynthesizesFactoryBadBlockMarkers(t *testing.T) {
	storage := byteStorage{data: make([]byte, 3*0x20000)}
	ready := NewStatusSignal()
	config := Qualcomm2K8BitNANDConfig(0xecaa, ready)
	config.FactoryBadBlocks = []uint32{1}
	device, err := NewQualcommNAND(storage, config)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		page uint32
		want uint32
	}{
		{page: 0, want: 0xffff},
		{page: 0x40, want: 0x0000},
		{page: 0x41, want: 0x0000},
		{page: 0x42, want: 0xffff},
		{page: 0x80, want: 0xffff},
	} {
		if err := device.Write(
			qualcommNANDAddressOffset,
			Width32,
			test.page*qualcommNANDCodewordDataSize,
		); err != nil {
			t.Fatal(err)
		}
		if err := device.Write(
			qualcommNANDCommandOffset,
			Width32,
			qualcommNANDCommandReadSpare,
		); err != nil {
			t.Fatal(err)
		}
		got, err := device.Read(qualcommNANDReadDataOffset, Width32)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("page %#x marker = %#x, want %#x", test.page, got, test.want)
		}
	}
}

func TestQualcommNANDTreatsUnrepresentedDeviceTailAsErased(t *testing.T) {
	ready := NewStatusSignal()
	config := Qualcomm2K8BitNANDConfig(0xecaa, ready)
	config.Capacity = 2 * uint64(config.PageSize)
	device, err := NewQualcommNAND(
		byteStorage{data: bytes.Repeat([]byte{0x5a}, int(config.PageSize))},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, qualcommNANDCodewordDataSize); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(0, Width32)
	status, statusErr := device.Read(qualcommNANDStatusOffset, Width32)
	if err != nil || statusErr != nil || value != 0xffffffff ||
		status != 0 || ready.Value() != 2 {
		t.Fatalf(
			"unrepresented data tail = %#x status %#x ready %#x errors %v/%v",
			value, status, ready.Value(), err, statusErr,
		)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, qualcommNANDCodewordDataSize); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandReadSpare); err != nil {
		t.Fatal(err)
	}
	value, err = device.Read(qualcommNANDReadDataOffset, Width32)
	if err != nil || value != 0xffff || ready.Value() != 2 {
		t.Fatalf("unrepresented spare tail = %#x ready %#x error %v", value, ready.Value(), err)
	}
}

func TestQualcommNANDExposesAndResetsExplicitControllerConfiguration(t *testing.T) {
	config := Qualcomm2K8BitNANDConfig(0x1500aaec, NewStatusSignal())
	device, err := NewQualcommNAND(
		byteStorage{data: bytes.Repeat([]byte{0xff}, int(config.PageSize))},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]uint32{
		qualcommNANDDeviceConfig0Offset:   config.DeviceConfig0,
		qualcommNANDDeviceConfig1Offset:   config.DeviceConfig1,
		qualcommNANDCommandValidityOffset: config.CommandValidity,
		qualcommNANDReadIDOffset:          config.ReadID,
	}
	for offset, expected := range want {
		value, readErr := device.Read(offset, Width32)
		if readErr != nil || value != expected {
			t.Fatalf("NAND register %#x = %#x error %v", offset, value, readErr)
		}
	}
	if err := device.Write(qualcommNANDDeviceConfig1Offset, Width32, 0x10203040); err != nil {
		t.Fatal(err)
	}
	if err := device.Reset(); err != nil {
		t.Fatal(err)
	}
	value, _ := device.Read(qualcommNANDDeviceConfig1Offset, Width32)
	if value != config.DeviceConfig1 {
		t.Fatalf("reset NAND device config 1 = %#x", value)
	}
	invalid := config
	invalid.CommandValidity = 0x80
	if _, err := NewQualcommNAND(byteStorage{data: bytes.Repeat([]byte{0xff}, 0x800)}, invalid); !errors.Is(err, ErrInvalidQualcommNAND) {
		t.Fatalf("invalid NAND controller configuration error = %v", err)
	}
}
