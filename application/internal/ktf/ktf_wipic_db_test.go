package ktf

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// writeKTFTestCString stores a NUL-terminated name in guest memory.
func writeKTFTestCString(t *testing.T, runtime *Runtime, text string) uint32 {
	t.Helper()
	address, err := runtime.Heap.Allocate(uint32(len(text)+1), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(address, []byte(text)); err != nil {
		t.Fatal(err)
	}
	return address
}

func callKTFWIPICDB(
	t *testing.T,
	runtime *Runtime,
	slot int,
	arguments []uint32,
) uint32 {
	t.Helper()
	setKTFWIPICCallArguments(t, runtime, arguments)
	value, err := ktfWIPICHandler(ktfWIPICMasterDatabase, slot)(
		context.Background(),
		runtime,
	)
	if err != nil {
		t.Fatalf("database slot %d: %v", slot, err)
	}
	return value
}

// 컴투스포춘골프3D reads its save through the WIPI-C database interface and
// quits to its "first run" notice when MC_dbOpenDataBase answers -12. With the
// table unimplemented every call answered zero, so the title never wrote its
// defaults and later dereferenced the record it thought it had (issue #86).
func TestKTFWIPICDatabaseRoundTripsRecords(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	name := writeKTFTestCString(t, runtime, "FG_102")

	missing := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotOpen,
		[]uint32{name, 128, 0, 1})
	if missing != ktfWIPICErrorNoEntry {
		t.Fatalf("open of a missing database = %d, want M_E_NOENT", int32(missing))
	}

	handle := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotOpen,
		[]uint32{name, 128, 1, 1})
	if handle == 0 || int32(handle) < 0 {
		t.Fatalf("create-open returned handle %d", int32(handle))
	}
	if runtime.DatabaseStores["FG_102"] == nil {
		t.Fatal("create-open did not register the database store")
	}

	payload, err := runtime.Heap.Allocate(8, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(
		payload,
		[]byte{1, 2, 3, 4, 5, 6, 7, 8},
	); err != nil {
		t.Fatal(err)
	}
	recordID := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotInsertRecord,
		[]uint32{handle, payload, 8})
	if recordID != 1 {
		t.Fatalf("first inserted record id = %d, want 1", int32(recordID))
	}

	ids, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	listed := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotListRecords,
		[]uint32{handle, ids, 16})
	if listed != 1 {
		t.Fatalf("listed %d records, want 1", int32(listed))
	}
	stored, err := runtime.ReadWords(ids, 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored[0] != recordID {
		t.Fatalf("listed record id = %d, want %d", stored[0], recordID)
	}

	output, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	read := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotSelectRecord,
		[]uint32{handle, recordID, output, 16})
	if read != 8 {
		t.Fatalf("select returned %d bytes, want 8", int32(read))
	}
	readBack := make([]byte, 8)
	if err := runtime.CPU.ReadMemory(output, readBack); err != nil {
		t.Fatal(err)
	}
	for index, want := range []byte{1, 2, 3, 4, 5, 6, 7, 8} {
		if readBack[index] != want {
			t.Fatalf("record byte %d = %d, want %d", index, readBack[index], want)
		}
	}

	if count := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotNumberOfRecords,
		[]uint32{handle}); count != 1 {
		t.Fatalf("record count = %d, want 1", int32(count))
	}
	if size := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotRecordSize,
		[]uint32{handle}); size != 128 {
		t.Fatalf("record size = %d, want 128", int32(size))
	}
	callKTFWIPICDB(t, runtime, ktfWIPICDBSlotClose, []uint32{handle})

	// Reopening without create has to find the database the title just wrote.
	reopened := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotOpen,
		[]uint32{name, 128, 0, 1})
	if reopened == 0 || int32(reopened) < 0 {
		t.Fatalf("reopen returned %d", int32(reopened))
	}
	if again := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotSelectRecord,
		[]uint32{reopened, recordID, output, 16}); again != 8 {
		t.Fatalf("reopened select returned %d bytes, want 8", int32(again))
	}
}

// MC_dbDeleteDataBase has to retire the backing record store as well: the
// title drops a stale save and immediately reopens the same name with create
// set, and that reopen fails if the store is still registered.
func TestKTFWIPICDatabaseDeleteAllowsRecreate(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	name := writeKTFTestCString(t, runtime, "FG_102")

	handle := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotOpen,
		[]uint32{name, 128, 1, 1})
	if int32(handle) <= 0 {
		t.Fatalf("create-open returned %d", int32(handle))
	}
	if deleted := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotDeleteDatabase,
		[]uint32{name, 1}); deleted != 0 {
		t.Fatalf("delete returned %d, want 0", int32(deleted))
	}
	if runtime.DatabaseStores["FG_102"] != nil {
		t.Fatal("delete left the database store behind")
	}
	recreated := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotOpen,
		[]uint32{name, 128, 1, 1})
	if int32(recreated) <= 0 {
		t.Fatalf("reopen after delete returned %d", int32(recreated))
	}
	if runtime.DatabaseStores["FG_102"] == nil {
		t.Fatal("reopen after delete did not recreate the store")
	}
}

// A handle the title never opened must not resolve to another database.
func TestKTFWIPICDatabaseRejectsUnknownHandles(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	for _, slot := range []int{
		ktfWIPICDBSlotClose,
		ktfWIPICDBSlotInsertRecord,
		ktfWIPICDBSlotSelectRecord,
		ktfWIPICDBSlotListRecords,
		ktfWIPICDBSlotNumberOfRecords,
	} {
		got := callKTFWIPICDB(t, runtime, slot, []uint32{9999, 0, 0, 0})
		if got != ktfWIPICErrorBadHandle {
			t.Fatalf(
				"slot %d on an unopened handle = %d, want a bad-handle error",
				slot,
				int32(got),
			)
		}
	}
}

// The record ids the interface hands out survive a save-state round trip so a
// title that reloads keeps reading the same profile.
func TestKTFWIPICDatabaseHandlesSurviveSnapshot(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	name := writeKTFTestCString(t, runtime, "FG_102")
	handle := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotOpen,
		[]uint32{name, 128, 1, 1})
	if int32(handle) <= 0 {
		t.Fatalf("create-open returned %d", int32(handle))
	}
	saved := ktfMetadataSnapshot{
		WIPICDatabases:    guest.CloneMap(runtime.wipicDatabases),
		NextWIPICDatabase: runtime.nextWIPICDatabase,
	}
	runtime.wipicDatabases = nil
	runtime.nextWIPICDatabase = 0
	runtime.wipicDatabases = guest.CloneMap(saved.WIPICDatabases)
	runtime.nextWIPICDatabase = max(uint32(1), saved.NextWIPICDatabase)
	if runtime.wipicDatabases[handle] != "FG_102" {
		t.Fatalf("restore lost the open handle: %v", runtime.wipicDatabases)
	}
	if again := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotNumberOfRecords,
		[]uint32{handle}); int32(again) < 0 {
		t.Fatalf("restored handle no longer resolves: %d", int32(again))
	}
}

// ktfWIPICDatabaseListEncodesIDs keeps the id array little-endian so a guest
// reading it as M_Int32[] sees the ids the interface reported.
func TestKTFWIPICDatabaseListWritesLittleEndianIDs(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	name := writeKTFTestCString(t, runtime, "LIST")
	handle := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotOpen,
		[]uint32{name, 4, 1, 1})
	payload, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		callKTFWIPICDB(t, runtime, ktfWIPICDBSlotInsertRecord,
			[]uint32{handle, payload, 4})
	}
	// Deleting the middle record must drop it from the listing while leaving
	// the ids around it untouched.
	callKTFWIPICDB(t, runtime, ktfWIPICDBSlotDeleteRecord,
		[]uint32{handle, 2})
	ids, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	listed := callKTFWIPICDB(t, runtime, ktfWIPICDBSlotListRecords,
		[]uint32{handle, ids, 12})
	if listed != 2 {
		t.Fatalf("listed %d records, want 2", int32(listed))
	}
	encoded := make([]byte, 8)
	if err := runtime.CPU.ReadMemory(ids, encoded); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(encoded) != 1 ||
		binary.LittleEndian.Uint32(encoded[4:]) != 3 {
		t.Fatalf("listed ids = %v, want 1 and 3", encoded)
	}
}
