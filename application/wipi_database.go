package application

import (
	"bytes"
	"encoding/binary"
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
	data     []byte
	address  uint32
}

func databaseReturn(value int32) wipiReturn {
	return wipiReturn{low: uint32(value)}
}

func (r *wipiRuntime) dispatchDatabase(name string) (wipiReturn, bool, error) {
	count := databaseArgumentCount(name)
	args, err := r.args(count)
	if err != nil {
		return wipiReturn{}, true, err
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
		if _, ok := r.databaseHandles[handle]; !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(r.databaseHandles, handle)
		return wipiReturn{}, true, nil
	case "MC_dbDeleteDataBase":
		key, err := r.databaseKey(arg(0), int32(arg(1)))
		if err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		for _, openKey := range r.databaseHandles {
			if openKey == key {
				return wipiReturn{low: ^uint32(0)}, true, nil
			}
		}
		if _, ok := r.databases[key]; !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		if serviceID := r.databaseServices[key]; serviceID != 0 {
			if err := r.services.Storage.DeleteRecordStore(
				r.serviceOwner,
				serviceID,
			); err != nil {
				return wipiReturn{low: ^uint32(0)}, true, nil
			}
			delete(r.databaseServices, key)
		}
		delete(r.databases, key)
		return wipiReturn{}, true, nil
	case "MC_dbInsertRecord":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok || int32(arg(2)) < 0 || arg(2) > database.recordSize {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		record := make([]byte, database.recordSize)
		if err := r.cpu.ReadMemory(arg(1), record[:arg(2)]); err != nil {
			return wipiReturn{}, true, err
		}
		recordID := database.nextRecord
		key := r.databaseHandles[int32(arg(0))]
		serviceID := r.databaseServices[key]
		sharedID, serviceErr := r.services.Storage.AddRecord(
			r.serviceOwner,
			serviceID,
			record,
		)
		if serviceErr != nil || sharedID != uint32(recordID) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		database.nextRecord++
		database.records[recordID] = record
		return wipiReturn{low: uint32(recordID)}, true, nil
	case "MC_dbSelectRecord":
		return r.selectDatabaseRecord(int32(arg(0)), int32(arg(1)), arg(2), int32(arg(3)))
	case "MC_dbUpdateRecord":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		record, exists := databaseRecord(database, int32(arg(1)))
		if !ok || !exists || int32(arg(3)) < 0 || arg(3) > database.recordSize {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		clear(record)
		if err := r.cpu.ReadMemory(arg(2), record[:arg(3)]); err != nil {
			return wipiReturn{}, true, err
		}
		key := r.databaseHandles[int32(arg(0))]
		if err := r.services.Storage.SetRecord(
			r.serviceOwner,
			r.databaseServices[key],
			uint32(int32(arg(1))),
			record,
		); err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{}, true, nil
	case "MC_dbDeleteRecord":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		recordID := int32(arg(1))
		if _, exists := database.records[recordID]; !exists {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		key := r.databaseHandles[int32(arg(0))]
		if err := r.services.Storage.DeleteRecord(
			r.serviceOwner,
			r.databaseServices[key],
			uint32(recordID),
		); err != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(database.records, recordID)
		return wipiReturn{}, true, nil
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
		raw, err := r.readCString(arg(0))
		if err != nil {
			return wipiReturn{}, true, err
		}
		for _, database := range r.databases {
			if database.name == string(raw) {
				return wipiReturn{low: uint32(database.mode)}, true, nil
			}
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_dbGetNumberOfRecords":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{low: uint32(len(database.records))}, true, nil
	case "MC_dbGetRecordSize":
		database, ok := r.openDatabaseByHandle(int32(arg(0)))
		if !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{low: database.recordSize}, true, nil
	case "MC_dbListDataBases":
		return r.listDatabases(arg(0), int32(arg(1)))
	default:
		return wipiReturn{}, false, nil
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

func (r *wipiRuntime) databaseKey(address uint32, mode int32) (string, error) {
	name, err := r.readCString(address)
	if err != nil {
		return "", err
	}
	if len(name) == 0 || len(name) > 255 {
		return "", errInvalidDatabaseName
	}
	return string(rune('0'+mode)) + ":" + string(name), nil
}

func (r *wipiRuntime) openDatabase(nameAddress uint32, recordSize int32, create bool, mode int32) (wipiReturn, bool, error) {
	key, err := r.databaseKey(nameAddress, mode)
	if err != nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	database, exists := r.databases[key]
	if !exists {
		if !create || recordSize <= 0 || recordSize > int32(maxWIPIString) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		raw, err := r.readCString(nameAddress)
		if err != nil {
			return wipiReturn{}, true, err
		}
		database = &wipiDatabase{
			name:       string(raw),
			recordSize: uint32(recordSize),
			mode:       mode,
			nextRecord: 1,
			records:    make(map[int32][]byte),
		}
		serviceID, serviceErr := r.services.Storage.CreateRecordStore(
			r.serviceOwner,
			key,
		)
		if serviceErr != nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		r.databaseServices[key] = serviceID
		r.databases[key] = database
	}
	handle := r.nextDatabase
	r.nextDatabase++
	r.databaseHandles[handle] = key
	return wipiReturn{low: uint32(handle)}, true, nil
}

func (r *wipiRuntime) openDatabaseByHandle(handle int32) (*wipiDatabase, bool) {
	key, ok := r.databaseHandles[handle]
	if !ok {
		return nil, false
	}
	database, ok := r.databases[key]
	return database, ok
}

func databaseRecord(database *wipiDatabase, recordID int32) ([]byte, bool) {
	if database == nil {
		return nil, false
	}
	record, ok := database.records[recordID]
	return record, ok
}

func (r *wipiRuntime) selectDatabaseRecord(handle, recordID int32, output uint32, length int32) (wipiReturn, bool, error) {
	database, ok := r.openDatabaseByHandle(handle)
	record, exists := databaseRecord(database, recordID)
	if !ok || !exists || length < int32(database.recordSize) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	key := r.databaseHandles[handle]
	serviceRecord, serviceErr := r.services.Storage.Record(
		r.serviceOwner,
		r.databaseServices[key],
		uint32(recordID),
	)
	if serviceErr != nil || !bytes.Equal(serviceRecord, record) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	if err := r.cpu.WriteMemory(output, serviceRecord); err != nil {
		return wipiReturn{}, true, err
	}
	return wipiReturn{}, true, nil
}

func (r *wipiRuntime) listDatabaseRecords(handle int32, output uint32, length int32) (wipiReturn, bool, error) {
	database, ok := r.openDatabaseByHandle(handle)
	if !ok {
		return databaseReturn(wipiDBBadFD), true, nil
	}
	if output == 0 || length <= 0 {
		return databaseReturn(wipiDBInvalid), true, nil
	}
	recordIDs := make([]int32, 0, len(database.records))
	for recordID := range database.records {
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
	if err := r.cpu.WriteMemory(output, encoded); err != nil {
		return wipiReturn{}, true, err
	}
	return wipiReturn{low: uint32(len(recordIDs))}, true, nil
}

func (r *wipiRuntime) sortDatabaseRecords(
	handle int32,
	output uint32,
	length int32,
	compareProcedure uint32,
	filterProcedure uint32,
) (wipiReturn, bool, error) {
	database, ok := r.openDatabaseByHandle(handle)
	if !ok {
		return databaseReturn(wipiDBBadFD), true, nil
	}
	if output == 0 || length <= 0 {
		return databaseReturn(wipiDBInvalid), true, nil
	}

	recordIDs := make([]int32, 0, len(database.records))
	for recordID := range database.records {
		recordIDs = append(recordIDs, recordID)
	}
	sort.Slice(recordIDs, func(i, j int) bool { return recordIDs[i] < recordIDs[j] })

	items := make([]wipiDatabaseSortItem, 0, len(recordIDs))
	guestData := compareProcedure != 0 || filterProcedure != 0
	allocated := make([]uint32, 0, len(recordIDs))
	defer func() {
		for _, address := range allocated {
			r.heap.release(address)
		}
	}()
	for _, recordID := range recordIDs {
		item := wipiDatabaseSortItem{
			recordID: recordID,
			data:     database.records[recordID],
		}
		if guestData {
			address, err := r.heap.allocate(database.recordSize, false)
			if err != nil || address == 0 {
				return databaseReturn(wipiDBError), true, nil
			}
			allocated = append(allocated, address)
			item.address = address
			if err := r.cpu.WriteMemory(address, item.data); err != nil {
				return wipiReturn{}, true, err
			}
		}
		if filterProcedure != 0 {
			accepted, err := r.callGuestFunction(filterProcedure, item.address)
			if err != nil {
				return wipiReturn{}, true, err
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
				value, err := r.callGuestFunction(
					compareProcedure,
					items[position-1].address,
					items[position].address,
				)
				if err != nil {
					return wipiReturn{}, true, err
				}
				comparison = int32(value)
			} else {
				comparison = int32(bytes.Compare(
					items[position-1].data,
					items[position].data,
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
	if err := r.cpu.WriteMemory(output, encoded); err != nil {
		return wipiReturn{}, true, err
	}
	return wipiReturn{low: uint32(len(items))}, true, nil
}

func (r *wipiRuntime) listDatabases(output uint32, length int32) (wipiReturn, bool, error) {
	if length < 2 {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	names := make([]string, 0, len(r.databases))
	for _, database := range r.databases {
		names = append(names, database.name)
	}
	sort.Strings(names)
	var encoded []byte
	for _, name := range names {
		encoded = append(encoded, []byte(name)...)
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	if len(encoded) > int(length) {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	return wipiReturn{}, true, r.cpu.WriteMemory(output, encoded)
}

var errInvalidDatabaseName = &databaseError{"invalid database name"}

type databaseError struct {
	message string
}

func (e *databaseError) Error() string {
	return e.message
}
