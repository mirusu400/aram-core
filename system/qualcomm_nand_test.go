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
	device, err := NewQualcommNAND(byteStorage{data: data}, 0x800)
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
	device, err := NewQualcommNAND(byteStorage{data: bytes.Repeat([]byte{0xff}, 0x800)}, 0x800)
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
	if err := device.Write(qualcommNANDCommandOffset, Width32, 6); !errors.Is(err, ErrQualcommNANDMMIO) {
		t.Fatalf("unknown command error = %v", err)
	}
}

func TestQualcommNANDStateRoundTrip(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0x5a}, 0x800)}
	device, err := NewQualcommNAND(base, 0x800)
	if err != nil {
		t.Fatal(err)
	}
	_ = device.Write(qualcommNANDAddressOffset, Width32, 0)
	_ = device.Write(qualcommNANDCommandOffset, Width32, qualcommNANDCommandRead)
	state, err := device.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewQualcommNAND(base, 0x800)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if restored.nextChunk != 0x200 || !bytes.Equal(restored.data[:], device.data[:]) {
		t.Fatal("NAND state did not round trip")
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated NAND state error = %v", err)
	}
}
