package application

import (
	"bytes"
	"encoding/gob"
	"errors"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func TestGVMSaveDataSurvivesRelaunchAndOwnsBytes(t *testing.T) {
	source := &gvmMachine{state: machinecore.StateReady, persistent: new(gvmPersistentStore)}
	want := []byte{1, 2, 3, 0xff}
	if err := source.persistent.WriteGVMData(want); err != nil {
		t.Fatal(err)
	}
	saved, err := source.ExportSaveData()
	if err != nil {
		t.Fatal(err)
	}

	target := &gvmMachine{state: machinecore.StateReady, persistent: new(gvmPersistentStore)}
	if err := target.ImportSaveData(saved); err != nil {
		t.Fatal(err)
	}
	got, err := target.persistent.ReadGVMData(uint32(len(want)))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("restored GVM data = %x, %v; want %x", got, err, want)
	}
	want[0], saved[0], got[0] = 9, 9, 9
	owned, _ := target.persistent.ReadGVMData(4)
	if !bytes.Equal(owned, []byte{1, 2, 3, 0xff}) {
		t.Fatalf("GVM persistent data aliases caller storage: %x", owned)
	}
}

func TestGVMSaveDataRejectsInvalidImportAtomically(t *testing.T) {
	machine := &gvmMachine{state: machinecore.StateReady, persistent: &gvmPersistentStore{data: []byte("keep")}}
	encode := func(envelope gvmSaveDataEnvelope) []byte {
		var output bytes.Buffer
		if err := gob.NewEncoder(&output).Encode(envelope); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	valid := encode(gvmSaveDataEnvelope{Magic: gvmSaveDataMagic, Version: gvmSaveDataVersion})
	for name, data := range map[string][]byte{
		"malformed":     []byte("not a gob"),
		"oversized":     make([]byte, gvmMaxSaveDataSize+1),
		"wrong magic":   encode(gvmSaveDataEnvelope{Magic: "OTHER", Version: gvmSaveDataVersion}),
		"wrong version": encode(gvmSaveDataEnvelope{Magic: gvmSaveDataMagic, Version: gvmSaveDataVersion + 1}),
		"large payload": encode(gvmSaveDataEnvelope{Magic: gvmSaveDataMagic, Version: gvmSaveDataVersion, Data: make([]byte, maxGVMPersistentDataBytes+1)}),
		"trailing data": append(valid, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := machine.ImportSaveData(data); err == nil {
				t.Fatal("ImportSaveData accepted invalid data")
			}
			if got := machine.persistent.snapshot(); !bytes.Equal(got, []byte("keep")) {
				t.Fatalf("invalid import changed GVM data to %q", got)
			}
		})
	}
}

func TestGVMSaveDataHonorsLifecycleBoundaries(t *testing.T) {
	machine := &gvmMachine{state: machinecore.StateReady, persistent: new(gvmPersistentStore)}
	saved, err := machine.ExportSaveData()
	if err != nil {
		t.Fatal(err)
	}
	machine.state = machinecore.StateRunning
	if _, err := machine.ExportSaveData(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("running export error = %v", err)
	}
	if err := machine.ImportSaveData(saved); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("running import error = %v", err)
	}
	machine.state = machinecore.StateReady
	machine.closed = true
	if _, err := machine.ExportSaveData(); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("closed export error = %v", err)
	}
	if err := machine.ImportSaveData(saved); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("closed import error = %v", err)
	}
}

func TestGVMPersistentStoreRejectsOversizeWriteAtomically(t *testing.T) {
	store := &gvmPersistentStore{data: []byte("keep")}
	if err := store.WriteGVMData(make([]byte, maxGVMPersistentDataBytes+1)); err == nil {
		t.Fatal("oversized persistent write succeeded")
	}
	if got := store.snapshot(); !bytes.Equal(got, []byte("keep")) {
		t.Fatalf("oversized write changed persistent data to %q", got)
	}
}
