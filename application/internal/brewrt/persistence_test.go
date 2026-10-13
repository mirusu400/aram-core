package brewrt

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func persistentGuestCall(t *testing.T, r *Runtime, method func() error, args ...uint32) uint32 {
	t.Helper()
	for i, value := range args {
		if err := r.cpu.WriteRegister(cpu.RegisterR0+uint32(i), value); err != nil {
			t.Fatal(err)
		}
	}
	if err := method(); err != nil {
		t.Fatal(err)
	}
	result, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPersistentStorageRestoresFilesDirectoriesAndDatabaseIDs(t *testing.T) {
	packageFiles := map[string][]byte{
		"bin/default.opt": []byte("default"),
		"bin/removed.opt": []byte("obsolete"),
		"bin/art.bin":     []byte("immutable resource"),
	}
	r := newSyntheticRuntimeWithFiles(t, packageFiles)
	path, buffer := heapBase+0x100, heapBase+0x200
	put := func(address uint32, data []byte) {
		t.Helper()
		if err := r.cpu.WriteMemory(address, data); err != nil {
			t.Fatal(err)
		}
	}
	// Use the guest API so modified packaged files keep their resolved path.
	put(path, []byte("fs:/~/default.opt\x00"))
	file := persistentGuestCall(t, r, r.openGuestFile, fileMgrObject, path, 3)
	if file == 0 {
		t.Fatal("failed to open packaged defaults")
	}
	put(buffer, []byte("changed"))
	if got := persistentGuestCall(t, r, r.writeGuestFile, file, buffer, 7); got != 7 {
		t.Fatalf("write count = %d", got)
	}
	r.releaseInterfaceObject(file)
	put(path, []byte("fs:/~/removed.opt\x00"))
	if got := persistentGuestCall(t, r, r.removeGuestFile, fileMgrObject, path); got != 0 {
		t.Fatalf("remove status = %d", got)
	}
	put(path, []byte("fs:/~/empty.sav\x00"))
	file = persistentGuestCall(t, r, r.openGuestFile, fileMgrObject, path, 4)
	if file == 0 {
		t.Fatal("failed to create empty save")
	}
	r.releaseInterfaceObject(file)
	put(path, []byte("fs:/~/saves\x00"))
	if got := persistentGuestCall(t, r, r.makeGuestDirectory, fileMgrObject, path); got != 0 {
		t.Fatalf("mkdir status = %d", got)
	}
	// Deleted IDs must not be reused, including when no records remain.
	r.databases["score.db"] = &brewDatabase{path: "score.db", nextID: 5, usedBytes: 4,
		records: []*brewDBRecord{{id: 3, fields: []brewDBField{{fieldType: 3, name: 7, data: []byte{0xd2, 4, 0, 0}}}}}}
	r.databases["empty.db"] = &brewDatabase{path: "empty.db", nextID: 9}
	state := r.CapturePersistentState(packageFiles)
	if len(state.Files) != 2 || len(state.RemovedFiles) != 1 || len(state.Directories) != 1 {
		t.Fatalf("file deltas = %+v", state)
	}
	if again := r.CapturePersistentState(packageFiles); !reflect.DeepEqual(state, again) {
		t.Fatal("snapshot order is nondeterministic")
	}
	restored := newSyntheticRuntimeWithFiles(t, packageFiles)
	if err := restored.RestorePersistentState(state, packageFiles); err != nil {
		t.Fatal(err)
	}
	if string(restored.files["bin/default.opt"]) != "changed" || string(packageFiles["bin/default.opt"]) != "default" {
		t.Fatal("changed defaults were lost or modified the source package")
	}
	if _, _, exists := restored.lookupGuestFile("removed.opt"); exists {
		t.Fatal("removed packaged file returned after a relaunch")
	}
	if data, exists := restored.files["empty.sav"]; !exists || len(data) != 0 {
		t.Fatal("empty guest file was lost")
	}
	if _, exists := restored.directories["saves"]; !exists {
		t.Fatal("guest directory was lost")
	}
	if string(restored.files["bin/art.bin"]) != "immutable resource" {
		t.Fatal("unchanged package resource was lost")
	}
	if err := restored.cpu.WriteMemory(path, []byte("score.db\x00")); err != nil {
		t.Fatal(err)
	}
	db := persistentGuestCall(t, restored, restored.openDatabase, dbMgrObject, path, 0)
	if db == 0 || persistentGuestCall(t, restored, func() error { return restored.handleDatabase(2) }, db) != 1 {
		t.Fatal("persisted database did not reopen with one record")
	}
	record := persistentGuestCall(t, restored, func() error { return restored.handleDatabase(5) }, db, 3)
	if record == 0 {
		t.Fatal("sparse record ID was lost")
	}
	persistentGuestCall(t, restored, func() error { return restored.handleDBRecord(6) }, record, 0, 0)
	if got := persistentGuestCall(t, restored, func() error { return restored.handleDBRecord(9) }, record, buffer); got != 1 {
		t.Fatalf("restored DWORD read status = %d", got)
	}
	var value [4]byte
	if err := restored.cpu.ReadMemory(buffer, value[:]); err != nil || binary.LittleEndian.Uint32(value[:]) != 1234 {
		t.Fatalf("restored field = %v, %v", value, err)
	}
	next := persistentGuestCall(t, restored, func() error { return restored.handleDatabase(6) }, db, 0, 0)
	if next == 0 || persistentGuestCall(t, restored, func() error { return restored.handleDBRecord(5) }, next) != 5 {
		t.Fatal("next record ID was reused after relaunch")
	}
	if restored.databases["empty.db"].nextID != 9 || restored.databases["score.db"].usedBytes != 4 {
		t.Fatal("database metadata was lost")
	}
	state.Files[0].Data[0] = 'X'
	state.Databases[1].Records[0].Fields[0].Data[0] = 0
	if !bytes.Equal(restored.files["bin/default.opt"], []byte("changed")) || restored.databases["score.db"].records[0].fields[0].data[0] != 0xd2 {
		t.Fatal("import retained aliases to caller-owned save data")
	}
}

func TestPersistentStorageRejectsInvalidCandidatesAtomically(t *testing.T) {
	r := newSyntheticRuntime(t)
	r.files["keep.sav"] = []byte("progress")
	original := r.CapturePersistentState(nil)
	for _, state := range []PersistentState{
		{Schema: PersistentStateSchema + 1},
		{Schema: PersistentStateSchema, Files: []PersistentFile{{Path: ""}}},
		{Schema: PersistentStateSchema, RemovedFiles: []string{"not-packaged.sav"}},
		{Schema: PersistentStateSchema, Files: []PersistentFile{{Path: "a"}, {Path: "a"}}},
		{Schema: PersistentStateSchema, Databases: []PersistentDatabase{{Path: "bad.db", NextID: 2, Records: []PersistentRecord{{ID: 2}}}}},
		{Schema: PersistentStateSchema, Databases: []PersistentDatabase{{Path: "bad.db", NextID: 2, Records: []PersistentRecord{{ID: 1}, {ID: 1}}}}},
		{Schema: PersistentStateSchema, Preferences: []PersistentPreference{{Data: make([]byte, (64<<10)+1)}}},
	} {
		if err := r.RestorePersistentState(state, nil); err == nil {
			t.Fatal("invalid storage was accepted")
		}
		if after := r.CapturePersistentState(nil); !reflect.DeepEqual(after, original) {
			t.Fatal("invalid import changed live storage")
		}
	}
	r.fileHandles[heapBase] = &brewFile{path: "keep.sav"}
	if err := r.RestorePersistentState(original, nil); err == nil {
		t.Fatal("replaced storage with an open handle")
	}
}
