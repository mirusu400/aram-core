package ktf

import (
	"context"
	"encoding/binary"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

// The WIPI-C database API is master-vector table 4. Its slots follow the
// published MC_DB ordering, which 컴투스포춘골프3D confirms from its own code:
// the title opens "FG_102" with a 128-byte record size, lists the record ids,
// selects one into a stack buffer, and closes the handle through the +0x00,
// +0x1c, +0x10 and +0x04 members in that order.
const (
	ktfWIPICDBSlotOpen = iota
	ktfWIPICDBSlotClose
	ktfWIPICDBSlotDeleteDatabase
	ktfWIPICDBSlotInsertRecord
	ktfWIPICDBSlotSelectRecord
	ktfWIPICDBSlotUpdateRecord
	ktfWIPICDBSlotDeleteRecord
	ktfWIPICDBSlotListRecords
	ktfWIPICDBSlotSortRecords
	ktfWIPICDBSlotGetAccessMode
	ktfWIPICDBSlotNumberOfRecords
	ktfWIPICDBSlotRecordSize
	ktfWIPICDBSlotListDatabases
)

// ktfWIPICDBMaxNameLength bounds the C string read for a database name. The
// published limit is far shorter; this only stops a corrupt pointer from
// scanning the whole address space.
const ktfWIPICDBMaxNameLength = 256

// ktfWIPICDBRecordLimit bounds the records one database may hold so a title
// looping on MC_dbInsertRecord cannot exhaust host memory.
const ktfWIPICDBRecordLimit = 65536

// A record id is the record's index plus one. Zero stays free so a title that
// treats it as "no record" still behaves, and the on-disk store keeps the
// index-based layout the Java org/kwis/msp/db/DataBase surface already writes.
func ktfWIPICDBRecordIndex(recordID uint32) (int, bool) {
	if recordID == 0 || recordID > ktfWIPICDBRecordLimit {
		return 0, false
	}
	return int(recordID - 1), true
}

func (r *Runtime) wipicDatabaseName(address uint32) (string, error) {
	if address == 0 {
		return "", nil
	}
	name, err := r.readCString(address, ktfWIPICDBMaxNameLength)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(name), nil
}

// wipicDatabaseParameter resolves the handle in parameter 0 to its store.
func (r *Runtime) wipicDatabaseParameter() (uint32, *Database, error) {
	handle, err := r.parameter(0)
	if err != nil {
		return 0, nil, err
	}
	name, ok := r.wipicDatabases[handle]
	if !ok {
		return handle, nil, nil
	}
	return handle, r.DatabaseStores[name], nil
}

func ktfWIPICDBOpenDataBase(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	recordSize, err := runtime.parameter(1)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	create, err := runtime.parameter(2)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	name, err := runtime.wipicDatabaseName(nameAddress)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if name == "" {
		return ktfWIPICErrorInvalid, nil
	}
	store := runtime.DatabaseStores[name]
	if store == nil {
		if create == 0 {
			runtime.tracef("wipic_db_open_missing:%s", name)
			return ktfWIPICErrorNoEntry, nil
		}
		store = &Database{Name: name, RecordSize: recordSize}
		serviceID, serviceErr := runtime.ensureWIPICRecordStore(name)
		if serviceErr != nil {
			return ktfWIPICError, nil
		}
		runtime.DatabaseStores[name] = store
		runtime.DatabaseServices[name] = serviceID
	}
	if store.RecordSize == 0 {
		store.RecordSize = recordSize
	}
	handle := runtime.nextWIPICDatabase
	for handle == 0 {
		handle++
	}
	if _, taken := runtime.wipicDatabases[handle]; taken {
		for {
			handle++
			if handle == 0 {
				handle = 1
			}
			if _, taken := runtime.wipicDatabases[handle]; !taken {
				break
			}
		}
	}
	runtime.nextWIPICDatabase = handle + 1
	runtime.wipicDatabases[handle] = name
	runtime.tracef(
		"wipic_db_open:%s:record_size=%d:records=%d:id=%d",
		name,
		store.RecordSize,
		len(store.Records),
		handle,
	)
	return handle, nil
}

func ktfWIPICDBCloseDataBase(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	handle, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	delete(runtime.wipicDatabases, handle)
	return 0, nil
}

func ktfWIPICDBDeleteDataBase(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	name, err := runtime.wipicDatabaseName(nameAddress)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	store := runtime.DatabaseStores[name]
	if name == "" || store == nil {
		return ktfWIPICErrorNoEntry, nil
	}
	// MC_dbDeleteDataBase removes the database itself, not only its records:
	// 컴투스포춘골프3D drops a stale save this way and immediately reopens the
	// same name with create set, so the backing record store has to go too or
	// that reopen fails and the title never writes its defaults.
	if serviceID := runtime.DatabaseServices[name]; serviceID != 0 {
		if err := runtime.Services.Storage.DeleteRecordStore(
			runtime.ServiceOwner,
			serviceID,
		); err != nil {
			return ktfWIPICError, nil
		}
	}
	delete(runtime.DatabaseServices, name)
	delete(runtime.DatabaseStores, name)
	for handle, open := range runtime.wipicDatabases {
		if open == name {
			delete(runtime.wipicDatabases, handle)
		}
	}
	runtime.tracef("wipic_db_delete:%s", name)
	return 0, nil
}

// ensureWIPICRecordStore returns the persistent record store for a database
// name, creating it when this is the first time the title names it and
// adopting the existing one when a previous session already did.
func (r *Runtime) ensureWIPICRecordStore(name string) (shared.ServiceID, error) {
	if serviceID := r.DatabaseServices[name]; serviceID != 0 {
		return serviceID, nil
	}
	serviceID, err := r.Services.Storage.CreateRecordStore(r.ServiceOwner, name)
	if err == nil {
		return serviceID, nil
	}
	return r.Services.Storage.OpenRecordStore(r.ServiceOwner, name)
}

func ktfWIPICDBInsert(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	if len(store.Records) >= ktfWIPICDBRecordLimit {
		return ktfWIPICError, nil
	}
	data, ok, err := runtime.wipicDatabaseInput(1, 2)
	if err != nil || !ok {
		return ktfWIPICErrorInvalid, err
	}
	store.Records = append(store.Records, data)
	if err := runtime.syncKTFDatabase(store); err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_db_insert:%s:record=%d:size=%d",
		store.Name,
		len(store.Records),
		len(data),
	)
	return uint32(len(store.Records)), nil
}

func ktfWIPICDBSelect(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	recordID, err := runtime.parameter(1)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	index, ok := ktfWIPICDBRecordIndex(recordID)
	if !ok || index >= len(store.Records) {
		return ktfWIPICErrorNoEntry, nil
	}
	output, err := runtime.parameter(2)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	capacity, err := runtime.parameter(3)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	record := store.Records[index]
	if output == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	if uint32(len(record)) > capacity {
		return ktfWIPICErrorShortBuf, nil
	}
	if len(record) == 0 {
		return 0, nil
	}
	if err := runtime.CPU.WriteMemory(output, record); err != nil {
		return 0, err
	}
	return uint32(len(record)), nil
}

func ktfWIPICDBUpdate(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	recordID, err := runtime.parameter(1)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	index, ok := ktfWIPICDBRecordIndex(recordID)
	if !ok || index >= len(store.Records) {
		return ktfWIPICErrorNoEntry, nil
	}
	data, present, err := runtime.wipicDatabaseInput(2, 3)
	if err != nil || !present {
		return ktfWIPICErrorInvalid, err
	}
	store.Records[index] = data
	if err := runtime.syncKTFDatabase(store); err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_db_update:%s:record=%d:size=%d",
		store.Name,
		recordID,
		len(data),
	)
	return uint32(len(data)), nil
}

func ktfWIPICDBDeleteRecord(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	recordID, err := runtime.parameter(1)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	index, ok := ktfWIPICDBRecordIndex(recordID)
	if !ok || index >= len(store.Records) {
		return ktfWIPICErrorNoEntry, nil
	}
	// A deleted record keeps its slot so every other record id a title is
	// still holding stays valid; MC_dbListRecords stops reporting it.
	store.Records[index] = nil
	if err := runtime.syncKTFDatabase(store); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfWIPICDBList(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	output, err := runtime.parameter(1)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	capacity, err := runtime.parameter(2)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	ids := make([]uint32, 0, len(store.Records))
	for index, record := range store.Records {
		if record == nil {
			continue
		}
		ids = append(ids, uint32(index)+1)
	}
	if output == 0 {
		return uint32(len(ids)), nil
	}
	// MC_dbListRecords fills as many ids as the caller's buffer holds and
	// answers with how many it wrote.
	written := min(uint32(len(ids)), capacity/4)
	if written != 0 {
		encoded := make([]byte, written*4)
		for index := uint32(0); index < written; index++ {
			binary.LittleEndian.PutUint32(encoded[index*4:], ids[index])
		}
		if err := runtime.CPU.WriteMemory(output, encoded); err != nil {
			return 0, err
		}
	}
	runtime.tracef(
		"wipic_db_list:%s:records=%d:written=%d",
		store.Name,
		len(ids),
		written,
	)
	return written, nil
}

func ktfWIPICDBAccessMode(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	name, err := runtime.wipicDatabaseName(nameAddress)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if name == "" || runtime.DatabaseStores[name] == nil {
		return ktfWIPICErrorNoEntry, nil
	}
	// Private storage is always readable and writable by its owner.
	return 3, nil
}

func ktfWIPICDBCount(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	count := uint32(0)
	for _, record := range store.Records {
		if record != nil {
			count++
		}
	}
	return count, nil
}

func ktfWIPICDBRecordSize(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	_, store, err := runtime.wipicDatabaseParameter()
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	if store == nil {
		return ktfWIPICErrorBadHandle, nil
	}
	return store.RecordSize, nil
}

func ktfWIPICDBListDatabases(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	output, err := runtime.parameter(0)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	capacity, err := runtime.parameter(1)
	if err != nil {
		return ktfWIPICErrorInvalid, err
	}
	names := guest.SortedStringKeys(runtime.DatabaseStores)
	// The buffer holds the names back to back, each NUL terminated.
	var encoded []byte
	for _, name := range names {
		encoded = append(encoded, name...)
		encoded = append(encoded, 0)
	}
	if output == 0 || uint32(len(encoded)) > capacity {
		return uint32(len(encoded)), nil
	}
	if len(encoded) != 0 {
		if err := runtime.CPU.WriteMemory(output, encoded); err != nil {
			return 0, err
		}
	}
	return uint32(len(names)), nil
}

// wipicDatabaseInput reads a (buffer, length) record payload argument pair.
func (r *Runtime) wipicDatabaseInput(
	bufferIndex, lengthIndex uint32,
) ([]byte, bool, error) {
	buffer, err := r.parameter(bufferIndex)
	if err != nil {
		return nil, false, err
	}
	length, err := r.parameter(lengthIndex)
	if err != nil {
		return nil, false, err
	}
	if length == 0 {
		return nil, true, nil
	}
	if buffer == 0 || length > ktfWIPICDBMaxRecordBytes {
		return nil, false, nil
	}
	data := make([]byte, length)
	if err := r.CPU.ReadMemory(buffer, data); err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// ktfWIPICDBMaxRecordBytes bounds one record so a corrupt length cannot make
// the host allocate without limit.
const ktfWIPICDBMaxRecordBytes = 4 << 20
