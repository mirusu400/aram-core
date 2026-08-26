package application

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

func newSyntheticKTFMachine(t *testing.T) *Machine {
	t.Helper()
	client := syntheticKTFClient()
	jar := testZIP(t, map[string][]byte{"client.bin4096": client})
	archive := testZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__": []byte(
			"PID:PD000001\nAID:01020304\nMClass:GameMain\n",
		),
	})
	created, err := NewFactory().Create(
		context.Background(),
		machinecore.Source{
			Name: "savedata.zip", ReaderAt: bytes.NewReader(archive),
			Size: int64(len(archive)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	return machine
}

// A KTF title that creates a record store on its first run must find that
// store through the Java DataBase adapter on the next launch. Restoring save
// data only into the storage service leaves the adapter believing the store is
// missing, so the title reopens it with create=true and the service rejects the
// name as already taken, faulting the machine.
func TestImportSaveDataRebindsKTFDatabases(t *testing.T) {
	source := newSyntheticKTFMachine(t)
	store := &ktfrt.Database{
		Name:       "dg100conf",
		RecordSize: 4,
		Records:    [][]byte{{1, 2, 3, 4}, {5, 6, 7, 8}},
	}
	serviceID, err := source.ktf.Services.Storage.CreateRecordStore(
		source.ktf.ServiceOwner,
		store.Name,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.ktf.Services.Storage.ReplaceRecords(
		source.ktf.ServiceOwner,
		serviceID,
		2,
		map[uint32][]byte{0: {1, 2, 3, 4}, 1: {5, 6, 7, 8}},
	); err != nil {
		t.Fatal(err)
	}
	source.ktf.DatabaseStores[store.Name] = store
	source.ktf.DatabaseServices[store.Name] = serviceID

	saved, err := source.ExportSaveData()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) == 0 {
		t.Fatal("exported KTF save data is empty")
	}

	restored := newSyntheticKTFMachine(t)
	if err := restored.ImportSaveData(saved); err != nil {
		t.Fatal(err)
	}
	adopted := restored.ktf.DatabaseStores[store.Name]
	if adopted == nil {
		t.Fatalf(
			"restored KTF databases = %v, want %q",
			restored.ktf.DatabaseStores,
			store.Name,
		)
	}
	if len(adopted.Records) != 2 ||
		!bytes.Equal(adopted.Records[0], []byte{1, 2, 3, 4}) ||
		!bytes.Equal(adopted.Records[1], []byte{5, 6, 7, 8}) {
		t.Fatalf("restored KTF records = %v", adopted.Records)
	}
	if adopted.RecordSize != 4 {
		t.Fatalf("restored KTF record size = %d, want 4", adopted.RecordSize)
	}
	adoptedService := restored.ktf.DatabaseServices[store.Name]
	if adoptedService == 0 {
		t.Fatal("restored KTF database has no storage service")
	}
	if _, err := restored.ktf.Services.Storage.RecordCount(
		restored.ktf.ServiceOwner,
		adoptedService,
	); err != nil {
		t.Fatalf("restored KTF database service is unusable: %v", err)
	}
}

// The public WIPI adapter keys record stores by mode and name. A restored save
// must land back under the same key, otherwise MC_dbOpenDataBase silently fails
// to create the store and the title loses its save.
func TestImportSaveDataRebindsWIPIDatabases(t *testing.T) {
	source := newSyntheticMachine(t)
	const key = "0:scores"
	database := &wipirt.Database{
		Name:       "scores",
		RecordSize: 4,
		Mode:       0,
		NextRecord: 2,
		Records:    map[int32][]byte{0: {9, 9, 9, 9}, 1: {8, 8, 8, 8}},
	}
	serviceID, err := source.wipi.Services.Storage.CreateRecordStore(
		source.wipi.ServiceOwner,
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.wipi.Services.Storage.ReplaceRecords(
		source.wipi.ServiceOwner,
		serviceID,
		2,
		map[uint32][]byte{0: {9, 9, 9, 9}, 1: {8, 8, 8, 8}},
	); err != nil {
		t.Fatal(err)
	}
	source.wipi.Databases[key] = database
	source.wipi.DatabaseServices[key] = serviceID

	saved, err := source.ExportSaveData()
	if err != nil {
		t.Fatal(err)
	}
	restored := newSyntheticMachine(t)
	if err := restored.ImportSaveData(saved); err != nil {
		t.Fatal(err)
	}
	adopted := restored.wipi.Databases[key]
	if adopted == nil {
		t.Fatalf(
			"restored WIPI databases = %v, want %q",
			restored.wipi.Databases,
			key,
		)
	}
	if adopted.Name != "scores" || adopted.Mode != 0 ||
		adopted.NextRecord != 2 || adopted.RecordSize != 4 {
		t.Fatalf("restored WIPI database = %+v", adopted)
	}
	if !bytes.Equal(adopted.Records[0], []byte{9, 9, 9, 9}) ||
		!bytes.Equal(adopted.Records[1], []byte{8, 8, 8, 8}) {
		t.Fatalf("restored WIPI records = %v", adopted.Records)
	}
	if restored.wipi.DatabaseServices[key] == 0 {
		t.Fatal("restored WIPI database has no storage service")
	}
}

// Save data that no longer carries a store the package shipped must also drop
// the adapter's stale service handle, so the next create call sees the same
// world the storage service does.
func TestImportSaveDataDropsUnsavedKTFDatabases(t *testing.T) {
	machine := newSyntheticKTFMachine(t)
	serviceID, err := machine.ktf.Services.Storage.CreateRecordStore(
		machine.ktf.ServiceOwner,
		"stale",
	)
	if err != nil {
		t.Fatal(err)
	}
	machine.ktf.DatabaseStores["stale"] = &ktfrt.Database{Name: "stale"}
	machine.ktf.DatabaseServices["stale"] = serviceID

	envelope := saveDataEnvelope{
		Magic: saveDataMagic,
		Storage: shared.StoragePersistenceState{
			Schema: shared.StoragePersistenceSchemaVersion,
		},
	}
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(envelope); err != nil {
		t.Fatal(err)
	}
	if err := machine.ImportSaveData(buffer.Bytes()); err != nil {
		t.Fatal(err)
	}
	if len(machine.ktf.DatabaseStores) != 0 ||
		len(machine.ktf.DatabaseServices) != 0 {
		t.Fatalf(
			"KTF databases after import = %v / %v, want empty",
			machine.ktf.DatabaseStores,
			machine.ktf.DatabaseServices,
		)
	}
}
