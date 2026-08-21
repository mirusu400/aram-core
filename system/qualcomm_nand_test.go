package system

import (
	"bytes"
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
	device, err := NewQualcommNAND(byteStorage{data: data}, Qualcomm2K8BitNANDConfig(0xecaa, NewLevelSignal()))
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
	ready := NewLevelSignal()
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
	if ready.Asserted() {
		t.Fatal("NAND reset command left the shared ready line asserted")
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, 7); err != nil {
		t.Fatalf("NAND controller mode command error = %v", err)
	}
	if !ready.Asserted() {
		t.Fatal("NAND mode command did not assert the shared ready line")
	}
	for _, command := range []uint32{5, 6} {
		if err := device.Write(qualcommNANDCommandOffset, Width32, command); err != nil {
			t.Fatalf("NAND identification command %d error = %v", command, err)
		}
	}
}

func TestQualcommNANDStateRoundTrip(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0x5a}, 0x800)}
	device, err := NewQualcommNAND(base, Qualcomm2K8BitNANDConfig(0xecaa, NewLevelSignal()))
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
	restored, err := NewQualcommNAND(base, Qualcomm2K8BitNANDConfig(0xecaa, NewLevelSignal()))
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
}

func TestQualcommNANDReadsControllerWordAtLatchedAddress(t *testing.T) {
	data := make([]byte, 0x800)
	data[0x202] = 0x34
	data[0x203] = 0x12
	ready := NewLevelSignal()
	device, err := NewQualcommNAND(byteStorage{data: data}, Qualcomm2K8BitNANDConfig(0xecaa, ready))
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDAddressOffset, Width32, 0x202); err != nil {
		t.Fatal(err)
	}
	if err := device.Write(qualcommNANDCommandOffset, Width32, 2); err != nil {
		t.Fatal(err)
	}
	value, err := device.Read(qualcommNANDReadDataOffset, Width32)
	if err != nil || value != 0x1234 || !ready.Asserted() {
		t.Fatalf("controller word = %#x ready %v error %v", value, ready.Asserted(), err)
	}
}

func TestQualcommNANDExposesAndResetsExplicitControllerConfiguration(t *testing.T) {
	config := Qualcomm2K8BitNANDConfig(0xecaa, NewLevelSignal())
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
