package brewrt

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	maxDBRecords = 4096
	maxDBBytes   = 8 << 20
)

// Database content belongs to one runtime, just like the writable BREW file map.
// An absent database is different from a newly created, empty database.
type brewDatabase struct {
	path      string
	records   []*brewDBRecord
	nextID    uint16
	usedBytes uint32
}

type brewDatabaseHandle struct {
	database *brewDatabase
	refs     uint32
	iterator int
}

type brewDBField struct {
	fieldType byte
	name      byte
	data      []byte
}

type brewDBRecord struct {
	id      uint16
	fields  []brewDBField
	removed bool
}

type brewDBRecordHandle struct {
	database    *brewDatabase
	record      *brewDBRecord
	refs        uint32
	field       int
	dataAddress uint32
}

func (r *Runtime) mapDatabaseServices() error {
	if r.dbServicesMapped {
		return nil
	}
	if err := r.cpu.Map(dbServiceBase, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW database services: %w", err)
	}
	var service [0x1000]byte
	binary.LittleEndian.PutUint32(service[dbMgrObject-dbServiceBase:], dbMgrVTable)
	for slot := uint32(0); slot < dbMgrMethodCount; slot++ {
		trap := dbMgrTrapBase + slot*2
		binary.LittleEndian.PutUint16(service[trap-dbServiceBase:], 0xbe17)
		binary.LittleEndian.PutUint32(service[dbMgrVTable-dbServiceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(service[dbMgrVTable-dbServiceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(service[dbMgrVTable-dbServiceBase+4:], releaseTrap|1)
	for slot := uint32(0); slot < databaseMethodCount; slot++ {
		trap := databaseTrapBase + slot*2
		binary.LittleEndian.PutUint16(service[trap-dbServiceBase:], 0xbe18)
		binary.LittleEndian.PutUint32(service[databaseVTable-dbServiceBase+slot*4:], trap|1)
	}
	for slot := uint32(0); slot < dbRecordMethodCount; slot++ {
		trap := dbRecordTrapBase + slot*2
		binary.LittleEndian.PutUint16(service[trap-dbServiceBase:], 0xbe19)
		binary.LittleEndian.PutUint32(service[dbRecordVTable-dbServiceBase+slot*4:], trap|1)
	}
	if err := r.cpu.WriteMemory(dbServiceBase, service[:]); err != nil {
		return fmt.Errorf("write BREW database services: %w", err)
	}
	r.dbServicesMapped = true
	return nil
}

func (r *Runtime) openDatabase() error {
	namePointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW database name pointer: %w", err)
	}
	if namePointer == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	name, err := r.readCString(namePointer)
	if err != nil {
		return fmt.Errorf("read BREW database name: %w", err)
	}
	path := normalizeGuestPath(name)
	create, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW database create flag: %w", err)
	}
	database := r.databases[path]
	if database == nil && path != "" {
		if _, _, packaged := r.lookupGuestFile(path); packaged {
			return fmt.Errorf("BREW execution boundary: parsing packaged database %q is not implemented", path)
		}
	}
	if database == nil && path != "" && create != 0 {
		database = &brewDatabase{path: path, nextID: 1}
		r.databases[path] = database
	}
	if database == nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	object, err := r.allocateGuest(4)
	if err != nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], databaseVTable)
	if err := r.cpu.WriteMemory(object, encoded[:]); err != nil {
		r.releaseGuest(object)
		return fmt.Errorf("initialize BREW database object: %w", err)
	}
	r.databaseHandles[object] = &brewDatabaseHandle{database: database, refs: 1}
	return r.cpu.WriteRegister(cpu.RegisterR0, object)
}

func (r *Runtime) removeDatabase() error {
	namePointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW database removal name pointer: %w", err)
	}
	if namePointer == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	}
	name, err := r.readCString(namePointer)
	if err != nil {
		return fmt.Errorf("read BREW database removal name: %w", err)
	}
	path := normalizeGuestPath(name)
	if r.databases[path] == nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	}
	for _, handle := range r.databaseHandles {
		if handle.database.path == path {
			return r.cpu.WriteRegister(cpu.RegisterR0, 1)
		}
	}
	delete(r.databases, path)
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}

func (r *Runtime) handleDatabase(slot uint32) error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW database object: %w", err)
	}
	handle := r.databaseHandles[object]
	if handle == nil {
		return fmt.Errorf("BREW database object 0x%08x is not open", object)
	}
	switch slot {
	case 0: // AddRef
		handle.refs++
		return r.cpu.WriteRegister(cpu.RegisterR0, handle.refs)
	case 1: // Release
		handle.refs--
		if handle.refs == 0 {
			r.releaseInterfaceObject(object)
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, handle.refs)
	case 2: // GetRecordCount
		return r.cpu.WriteRegister(cpu.RegisterR0, uint32(len(handle.database.records)))
	case 3: // Reset
		handle.iterator = 0
		return nil
	case 4: // GetNextRecord
		if handle.iterator >= len(handle.database.records) {
			return r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		record := handle.database.records[handle.iterator]
		object, err := r.openDBRecord(handle.database, record)
		if err != nil {
			return err
		}
		if object != 0 {
			handle.iterator++
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, object)
	case 5: // GetRecordByID
		id, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return err
		}
		for _, record := range handle.database.records {
			if record.id == uint16(id) {
				object, err := r.openDBRecord(handle.database, record)
				if err != nil {
					return err
				}
				return r.cpu.WriteRegister(cpu.RegisterR0, object)
			}
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 6: // CreateRecord
		fieldsPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return err
		}
		count, err := r.cpu.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return err
		}
		fields, err := r.readDBFields(fieldsPointer, count)
		bytes := dbFieldsSize(fields)
		if err != nil || handle.database.nextID == 0xffff || len(handle.database.records) >= maxDBRecords ||
			bytes > maxDBBytes-handle.database.usedBytes {
			return r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		record := &brewDBRecord{id: handle.database.nextID, fields: fields}
		object, err := r.openDBRecord(handle.database, record)
		if err != nil {
			return err
		}
		if object == 0 {
			return r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		handle.database.nextID++
		handle.database.usedBytes += bytes
		handle.database.records = append(handle.database.records, record)
		return r.cpu.WriteRegister(cpu.RegisterR0, object)
	default:
		return fmt.Errorf("BREW database vtable slot %d is out of range", slot)
	}
}

func dbFieldsSize(fields []brewDBField) uint32 {
	var size uint32
	for _, field := range fields {
		size += uint32(len(field.data))
	}
	return size
}

func (r *Runtime) readDBFields(pointer, count uint32) ([]brewDBField, error) {
	if count > 64 || (count != 0 && pointer == 0) {
		return nil, fmt.Errorf("invalid BREW database field count or pointer")
	}
	fields := make([]brewDBField, 0, count)
	for index := uint32(0); index < count; index++ {
		var encoded [8]byte
		if err := r.cpu.ReadMemory(pointer+index*8, encoded[:]); err != nil {
			return nil, err
		}
		length := binary.LittleEndian.Uint16(encoded[2:4])
		if length > 4096 {
			return nil, fmt.Errorf("BREW database field exceeds 4096 bytes")
		}
		data := make([]byte, length)
		if length != 0 {
			address := binary.LittleEndian.Uint32(encoded[4:8])
			if address == 0 {
				return nil, fmt.Errorf("BREW database field data is null")
			}
			if err := r.cpu.ReadMemory(address, data); err != nil {
				return nil, err
			}
		}
		fields = append(fields, brewDBField{fieldType: encoded[0], name: encoded[1], data: data})
	}
	return fields, nil
}

func (r *Runtime) openDBRecord(database *brewDatabase, record *brewDBRecord) (uint32, error) {
	object, err := r.allocateGuest(4)
	if err != nil {
		return 0, nil
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], dbRecordVTable)
	if err := r.cpu.WriteMemory(object, encoded[:]); err != nil {
		r.releaseGuest(object)
		return 0, err
	}
	r.dbRecordHandles[object] = &brewDBRecordHandle{database: database, record: record, refs: 1, field: -1}
	return object, nil
}

func (r *Runtime) handleDBRecord(slot uint32) error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return err
	}
	handle := r.dbRecordHandles[object]
	if handle == nil {
		return fmt.Errorf("BREW database record 0x%08x is not open", object)
	}
	switch slot {
	case 0: // AddRef
		handle.refs++
		return r.cpu.WriteRegister(cpu.RegisterR0, handle.refs)
	case 1: // Release
		handle.refs--
		if handle.refs == 0 {
			r.releaseInterfaceObject(object)
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, handle.refs)
	case 2: // Reset
		handle.field = -1
		return nil
	case 3: // Update
		if handle.record.removed {
			return r.cpu.WriteRegister(cpu.RegisterR0, 1)
		}
		pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return err
		}
		count, err := r.cpu.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return err
		}
		fields, err := r.readDBFields(pointer, count)
		oldBytes := dbFieldsSize(handle.record.fields)
		newBytes := dbFieldsSize(fields)
		if err != nil || newBytes > maxDBBytes-(handle.database.usedBytes-oldBytes) {
			return r.cpu.WriteRegister(cpu.RegisterR0, 1)
		}
		handle.record.fields = fields
		handle.database.usedBytes = handle.database.usedBytes - oldBytes + newBytes
		handle.field = -1
		r.releaseGuest(handle.dataAddress)
		handle.dataAddress = 0
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 4: // Remove
		for index, record := range handle.database.records {
			if record == handle.record {
				handle.database.usedBytes -= dbFieldsSize(record.fields)
				record.removed = true
				handle.database.records = append(handle.database.records[:index], handle.database.records[index+1:]...)
				return r.cpu.WriteRegister(cpu.RegisterR0, 0)
			}
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	case 5: // GetID
		return r.cpu.WriteRegister(cpu.RegisterR0, uint32(handle.record.id))
	case 6: // NextField
		handle.field++
		if handle.field >= len(handle.record.fields) {
			return r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		field := handle.record.fields[handle.field]
		if err := r.writeDBFieldMetadata(field, false); err != nil {
			return err
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, uint32(field.fieldType))
	case 7: // GetField
		field, ok := handle.currentField()
		if !ok {
			return r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		if err := r.writeDBFieldMetadata(field, true); err != nil {
			return err
		}
		return r.returnDBFieldData(handle, field)
	case 8: // GetFieldWord
		return r.copyDBScalar(handle, 2)
	case 9: // GetFieldDWord
		return r.copyDBScalar(handle, 4)
	case 10: // GetFieldString
		field, ok := handle.currentField()
		if !ok || field.fieldType != 4 {
			return r.cpu.WriteRegister(cpu.RegisterR0, 0)
		}
		return r.returnDBFieldData(handle, field)
	default:
		return fmt.Errorf("BREW database record vtable slot %d is out of range", slot)
	}
}

func (handle *brewDBRecordHandle) currentField() (brewDBField, bool) {
	if handle.field < 0 || handle.field >= len(handle.record.fields) {
		return brewDBField{}, false
	}
	return handle.record.fields[handle.field], true
}

func (r *Runtime) writeDBFieldMetadata(field brewDBField, withType bool) error {
	namePointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	if namePointer != 0 {
		if err := r.cpu.WriteMemory(namePointer, []byte{field.name}); err != nil {
			return err
		}
	}
	lengthRegister := cpu.RegisterR2
	if withType {
		typePointer, err := r.cpu.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return err
		}
		if typePointer != 0 {
			if err := r.cpu.WriteMemory(typePointer, []byte{field.fieldType}); err != nil {
				return err
			}
		}
		lengthRegister = cpu.RegisterR3
	}
	lengthPointer, err := r.cpu.ReadRegister(lengthRegister)
	if err != nil {
		return err
	}
	if lengthPointer != 0 {
		var encoded [2]byte
		binary.LittleEndian.PutUint16(encoded[:], uint16(len(field.data)))
		return r.cpu.WriteMemory(lengthPointer, encoded[:])
	}
	return nil
}

func (r *Runtime) returnDBFieldData(handle *brewDBRecordHandle, field brewDBField) error {
	r.releaseGuest(handle.dataAddress)
	handle.dataAddress = 0
	if len(field.data) == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	address, err := r.allocateGuest(uint32(len(field.data)))
	if err != nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if err := r.cpu.WriteMemory(address, field.data); err != nil {
		r.releaseGuest(address)
		return err
	}
	handle.dataAddress = address
	return r.cpu.WriteRegister(cpu.RegisterR0, address)
}

func (r *Runtime) copyDBScalar(handle *brewDBRecordHandle, length int) error {
	field, ok := handle.currentField()
	if !ok || len(field.data) < length {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	if pointer == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if err := r.cpu.WriteMemory(pointer, field.data[:length]); err != nil {
		return err
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 1)
}
