package wipi

import (
	"bytes"
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"sort"
)

const (
	wipiDBError       int32 = -1
	wipiDBBadFD       int32 = -7
	wipiDBInvalid     int32 = -8
	wipiDBShortBuffer int32 = -9
)

type wipiDatabaseSortItem struct {
	recordID int32
	Data     []byte
	address  uint32
}

func databaseReturn(value int32) guest.WIPIReturn {
	return guest.WIPIReturn{Low: uint32(value)}
}

func (r *Runtime) dispatchDatabase(name string) (guest.WIPIReturn, bool, error) {
	count := databaseArgumentCount(name)
	args, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	arg := func(index int) uint32 {
		if index >= len(args) {
			return 0
		}
		return args[index]
	}
	switch name {
	case "MC_dbOpenDataBase":
		return r.openDatabase(arg(0), int32(arg(1)), arg(2) != 0, int32(arg(3)))
	case "MC_dbCloseDataBase":
		handle := int32(arg(0))
		if _, ok := r.DatabaseHandles[handle]; !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		delete(r.DatabaseHandles, handle)
		return guest.WIPIReturn{}, true, nil
	case "MC_dbDeleteDataBase":
		key, err := r.databaseKey(arg(0), int32(arg(1)))
		if err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		for _, openKey := range r.DatabaseHandles {
			if openKey == key {
				return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
			}
		}
		if _, ok := r.Databases[key]; !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if serviceID := r.DatabaseServices[key]; serviceID != 0 {
			if err := r.Services.Storage.DeleteRecordStore(
				r.ServiceOwner,
				serviceID,
			); err != nil {
				return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
			}
			delete(r.DatabaseServices, key)
		}
		delete(r.Databases, key)
		return guest.WIPIReturn{}, true, nil
	case "MC_dbInsertRecord":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok || int32(arg(2)) < 0 || arg(2) > database.RecordSize {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		record := make([]byte, database.RecordSize)
		if err := r.CPU.ReadMemory(arg(1), record[:arg(2)]); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		recordID := database.NextRecord
		key := r.DatabaseHandles[int32(arg(0))]
		serviceID := r.DatabaseServices[key]
		sharedID, serviceErr := r.Services.Storage.AddRecord(
			r.ServiceOwner,
			serviceID,
			record,
		)
		if serviceErr != nil || sharedID != uint32(recordID) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		database.NextRecord++
		database.Records[recordID] = record
		return guest.WIPIReturn{Low: uint32(recordID)}, true, nil
	case "MC_dbSelectRecord":
		return r.selectDatabaseRecord(int32(arg(0)), int32(arg(1)), arg(2), int32(arg(3)))
	case "MC_dbUpdateRecord":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		record, exists := databaseRecord(database, int32(arg(1)))
		if !ok || !exists || int32(arg(3)) < 0 || arg(3) > database.RecordSize {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		clear(record)
		if err := r.CPU.ReadMemory(arg(2), record[:arg(3)]); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		key := r.DatabaseHandles[int32(arg(0))]
		if err := r.Services.Storage.SetRecord(
			r.ServiceOwner,
			r.DatabaseServices[key],
			uint32(int32(arg(1))),
			record,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_dbDeleteRecord":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		recordID := int32(arg(1))
		if _, exists := database.Records[recordID]; !exists {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		key := r.DatabaseHandles[int32(arg(0))]
		if err := r.Services.Storage.DeleteRecord(
			r.ServiceOwner,
			r.DatabaseServices[key],
			uint32(recordID),
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		delete(database.Records, recordID)
		return guest.WIPIReturn{}, true, nil
	case "MC_dbListRecords":
		return r.listDatabaseRecords(int32(arg(0)), arg(1), int32(arg(2)))
	case "MC_dbSortRecords":
		return r.sortDatabaseRecords(
			int32(arg(0)),
			arg(1),
			int32(arg(2)),
			arg(3),
			arg(4),
		)
	case "MC_dbGetAccessMode":
		raw, err := r.ReadCString(arg(0))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		for _, database := range r.Databases {
			if database.Name == string(raw) {
				return guest.WIPIReturn{Low: uint32(database.Mode)}, true, nil
			}
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_dbGetNumberOfRecords":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{Low: uint32(len(database.Records))}, true, nil
	case "MC_dbGetRecordSize":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{Low: database.RecordSize}, true, nil
	case "MC_dbListDataBases":
		return r.listDatabases(arg(0), int32(arg(1)))
	default:
		return guest.WIPIReturn{}, false, nil
	}
}

func databaseArgumentCount(name string) int {
	switch name {
	case "MC_dbCloseDataBase", "MC_dbGetAccessMode", "MC_dbGetNumberOfRecords",
		"MC_dbGetRecordSize":
		return 1
	case "MC_dbDeleteDataBase", "MC_dbDeleteRecord", "MC_dbListDataBases":
		return 2
	case "MC_dbInsertRecord", "MC_dbListRecords":
		return 3
	case "MC_dbOpenDataBase", "MC_dbSelectRecord", "MC_dbUpdateRecord":
		return 4
	case "MC_dbSortRecords":
		return 5
	default:
		return 0
	}
}

func (r *Runtime) databaseKey(address uint32, mode int32) (string, error) {
	name, err := r.ReadCString(address)
	if err != nil {
		return "", err
	}
	if len(name) == 0 || len(name) > 255 {
		return "", errInvalidDatabaseName
	}
	return string(rune('0'+mode)) + ":" + string(name), nil
}

func (r *Runtime) openDatabase(nameAddress uint32, recordSize int32, create bool, mode int32) (guest.WIPIReturn, bool, error) {
	key, err := r.databaseKey(nameAddress, mode)
	if err != nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	database, exists := r.Databases[key]
	if !exists {
		if !create || recordSize <= 0 || recordSize > int32(maxWIPIString) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		raw, err := r.ReadCString(nameAddress)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		database = &Database{
			Name:       string(raw),
			RecordSize: uint32(recordSize),
			Mode:       mode,
			NextRecord: 1,
			Records:    make(map[int32][]byte),
		}
		serviceID, serviceErr := r.Services.Storage.CreateRecordStore(
			r.ServiceOwner,
			key,
		)
		if serviceErr != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		r.DatabaseServices[key] = serviceID
		r.Databases[key] = database
	}
	handle := r.NextDatabase
	r.NextDatabase++
	r.DatabaseHandles[handle] = key
	return guest.WIPIReturn{Low: uint32(handle)}, true, nil
}

func (r *Runtime) openDatabaseByHandle(handle int32) (*Database, bool) {
	key, ok := r.DatabaseHandles[handle]
	if !ok {
		return nil, false
	}
	database, ok := r.Databases[key]
	return database, ok
}

func databaseRecord(database *Database, recordID int32) ([]byte, bool) {
	if database == nil {
		return nil, false
	}
	record, ok := database.Records[recordID]
	return record, ok
}

func (r *Runtime) selectDatabaseRecord(handle, recordID int32, output uint32, length int32) (guest.WIPIReturn, bool, error) {
	database, ok := r.openDatabaseByHandle(handle)
	record, exists := databaseRecord(database, recordID)
	if !ok || !exists || length < int32(database.RecordSize) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	key := r.DatabaseHandles[handle]
	serviceRecord, serviceErr := r.Services.Storage.Record(
		r.ServiceOwner,
		r.DatabaseServices[key],
		uint32(recordID),
	)
	if serviceErr != nil || !bytes.Equal(serviceRecord, record) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	if err := r.CPU.WriteMemory(output, serviceRecord); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	return guest.WIPIReturn{}, true, nil
}

func (r *Runtime) listDatabaseRecords(handle int32, output uint32, length int32) (guest.WIPIReturn, bool, error) {
	database, ok := r.openDatabaseByHandle(handle)
	if !ok {
		return databaseReturn(wipiDBBadFD), true, nil
	}
	if output == 0 || length <= 0 {
		return databaseReturn(wipiDBInvalid), true, nil
	}
	recordIDs := make([]int32, 0, len(database.Records))
	for recordID := range database.Records {
		recordIDs = append(recordIDs, recordID)
	}
	sort.Slice(recordIDs, func(i, j int) bool { return recordIDs[i] < recordIDs[j] })
	if int64(length) < int64(len(recordIDs))*4 {
		return databaseReturn(wipiDBShortBuffer), true, nil
	}
	encoded := make([]byte, len(recordIDs)*4)
	for index, recordID := range recordIDs {
		binary.LittleEndian.PutUint32(encoded[index*4:], uint32(recordID))
	}
	if err := r.CPU.WriteMemory(output, encoded); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	return guest.WIPIReturn{Low: uint32(len(recordIDs))}, true, nil
}

func (r *Runtime) sortDatabaseRecords(
	handle int32,
	output uint32,
	length int32,
	compareProcedure uint32,
	filterProcedure uint32,
) (guest.WIPIReturn, bool, error) {
	database, ok := r.openDatabaseByHandle(handle)
	if !ok {
		return databaseReturn(wipiDBBadFD), true, nil
	}
	if output == 0 || length <= 0 {
		return databaseReturn(wipiDBInvalid), true, nil
	}

	recordIDs := make([]int32, 0, len(database.Records))
	for recordID := range database.Records {
		recordIDs = append(recordIDs, recordID)
	}
	sort.Slice(recordIDs, func(i, j int) bool { return recordIDs[i] < recordIDs[j] })

	items := make([]wipiDatabaseSortItem, 0, len(recordIDs))
	guestData := compareProcedure != 0 || filterProcedure != 0
	allocated := make([]uint32, 0, len(recordIDs))
	defer func() {
		for _, address := range allocated {
			r.Heap.Release(address)
		}
	}()
	for _, recordID := range recordIDs {
		item := wipiDatabaseSortItem{
			recordID: recordID,
			Data:     database.Records[recordID],
		}
		if guestData {
			address, err := r.Heap.Allocate(database.RecordSize, false)
			if err != nil || address == 0 {
				return databaseReturn(wipiDBError), true, nil
			}
			allocated = append(allocated, address)
			item.address = address
			if err := r.CPU.WriteMemory(address, item.Data); err != nil {
				return guest.WIPIReturn{}, true, err
			}
		}
		if filterProcedure != 0 {
			accepted, err := r.CallGuestFunction(filterProcedure, item.address)
			if err != nil {
				return guest.WIPIReturn{}, true, err
			}
			if int32(accepted) <= 0 {
				continue
			}
		}
		items = append(items, item)
	}

	if int64(length) < int64(len(items))*4 {
		return databaseReturn(wipiDBShortBuffer), true, nil
	}
	for index := 1; index < len(items); index++ {
		for position := index; position > 0; position-- {
			var comparison int32
			if compareProcedure != 0 {
				value, err := r.CallGuestFunction(
					compareProcedure,
					items[position-1].address,
					items[position].address,
				)
				if err != nil {
					return guest.WIPIReturn{}, true, err
				}
				comparison = int32(value)
			} else {
				comparison = int32(bytes.Compare(
					items[position-1].Data,
					items[position].Data,
				))
			}
			if comparison <= 0 {
				break
			}
			items[position-1], items[position] = items[position], items[position-1]
		}
	}

	encoded := make([]byte, len(items)*4)
	for index, item := range items {
		binary.LittleEndian.PutUint32(encoded[index*4:], uint32(item.recordID))
	}
	if err := r.CPU.WriteMemory(output, encoded); err != nil {
		return guest.WIPIReturn{}, true, err
	}
	return guest.WIPIReturn{Low: uint32(len(items))}, true, nil
}

func (r *Runtime) listDatabases(output uint32, length int32) (guest.WIPIReturn, bool, error) {
	if length < 2 {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	names := make([]string, 0, len(r.Databases))
	for _, database := range r.Databases {
		names = append(names, database.Name)
	}
	sort.Strings(names)
	var encoded []byte
	for _, name := range names {
		encoded = append(encoded, []byte(name)...)
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	if len(encoded) > int(length) {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	return guest.WIPIReturn{}, true, r.CPU.WriteMemory(output, encoded)
}

var errInvalidDatabaseName = &databaseError{"invalid database name"}

type databaseError struct {
	message string
}

func (e *databaseError) Error() string {
	return e.message
}
