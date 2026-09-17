package skvmhost

import (
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestSKVMSaveDataRoundTripsRecordStoresAcrossOwners(t *testing.T) {
	source := newSKVMSaveDataTestMachine(t, false)
	store, err := source.services.Storage.CreateRecordStore(source.owner, "progress")
	if err != nil {
		t.Fatal(err)
	}
	recordID, err := source.services.Storage.AddRecord(
		source.owner,
		store,
		[]byte("stage=7"),
	)
	if err != nil {
		t.Fatal(err)
	}

	data, err := source.ExportSaveData()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("ExportSaveData returned no SKVM record-store data")
	}

	target := newSKVMSaveDataTestMachine(t, true)
	if source.owner == target.owner {
		t.Fatal("test setup did not create different runtime owners")
	}
	if err := target.ImportSaveData(data); err != nil {
		t.Fatal(err)
	}
	restored, err := target.services.Storage.OpenRecordStore(target.owner, "progress")
	if err != nil {
		t.Fatal(err)
	}
	got, err := target.services.Storage.Record(target.owner, restored, recordID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "stage=7" {
		t.Fatalf("restored record = %q, want %q", got, "stage=7")
	}
}

func TestSKVMSaveDataRejectsRunningMachine(t *testing.T) {
	machine := newSKVMSaveDataTestMachine(t, false)
	machine.state = machinecore.StateRunning
	if _, err := machine.ExportSaveData(); err == nil {
		t.Fatal("ExportSaveData succeeded while running")
	}
	if err := machine.ImportSaveData([]byte("not a save")); err == nil {
		t.Fatal("ImportSaveData succeeded while running")
	}
}

func newSKVMSaveDataTestMachine(t *testing.T, reserveOwner bool) *Machine {
	t.Helper()
	services, err := shared.NewServices(shared.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if reserveOwner {
		if _, err := services.Coordinator.Register("reserved", 1_000_000); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := services.Coordinator.Register("skvm-save-test", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	return &Machine{
		state:    machinecore.StatePaused,
		services: services,
		owner:    owner,
	}
}
