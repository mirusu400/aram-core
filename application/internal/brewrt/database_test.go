package brewrt

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestDatabaseCreateReopenAndRemove(t *testing.T) {
	r := newSyntheticRuntime(t)
	setArgs := func(values ...uint32) {
		t.Helper()
		for i, value := range values {
			if err := r.cpu.WriteRegister(cpu.RegisterR0+uint32(i), value); err != nil {
				t.Fatal(err)
			}
		}
	}
	result := func() uint32 {
		t.Helper()
		value, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if r.dbServicesMapped {
		t.Fatal("database services mapped before CreateInstance")
	}
	interfaceOutput, err := r.allocateGuest(4)
	if err != nil {
		t.Fatal(err)
	}
	setArgs(shellObject, DBMgrClassID, interfaceOutput)
	if err := r.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	if status := result(); status != 0 || !r.dbServicesMapped {
		t.Fatalf("DBMgr CreateInstance status=%d mapped=%v", status, r.dbServicesMapped)
	}
	var created [4]byte
	if err := r.cpu.ReadMemory(interfaceOutput, created[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(created[:]); got != dbMgrObject {
		t.Fatalf("DBMgr object=0x%08x, want 0x%08x", got, dbMgrObject)
	}
	name, err := r.allocateGuest(9)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.cpu.WriteMemory(name, []byte("score.db\x00")); err != nil {
		t.Fatal(err)
	}
	setArgs(dbMgrObject, name, 0)
	if err := r.openDatabase(); err != nil {
		t.Fatal(err)
	}
	if got := result(); got != 0 {
		t.Fatalf("opening an absent database without create returned 0x%08x", got)
	}
	setArgs(dbMgrObject, name, 1)
	if err := r.openDatabase(); err != nil {
		t.Fatal(err)
	}
	database := result()
	if database == 0 {
		t.Fatal("create did not return a database")
	}

	value, err := r.allocateGuest(4)
	if err != nil {
		t.Fatal(err)
	}
	var number [4]byte
	binary.LittleEndian.PutUint32(number[:], 1234)
	if err := r.cpu.WriteMemory(value, number[:]); err != nil {
		t.Fatal(err)
	}
	field, err := r.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor [8]byte
	descriptor[0] = 3  // AEEDB_FT_DWORD
	descriptor[1] = 16 // AEEDBFIELD_TITLE
	binary.LittleEndian.PutUint16(descriptor[2:4], 4)
	binary.LittleEndian.PutUint32(descriptor[4:8], value)
	if err := r.cpu.WriteMemory(field, descriptor[:]); err != nil {
		t.Fatal(err)
	}
	setArgs(database, field, 1)
	if err := r.handleDatabase(6); err != nil {
		t.Fatal(err)
	}
	record := result()
	if record == 0 {
		t.Fatal("CreateRecord returned null")
	}
	setArgs(record)
	if err := r.handleDBRecord(5); err != nil {
		t.Fatal(err)
	}
	if got := result(); got != 1 {
		t.Fatalf("record ID=%d, want 1", got)
	}
	setArgs(record)
	if err := r.handleDBRecord(1); err != nil {
		t.Fatal(err)
	}
	setArgs(database)
	if err := r.handleDatabase(1); err != nil {
		t.Fatal(err)
	}

	setArgs(dbMgrObject, name, 0)
	if err := r.openDatabase(); err != nil {
		t.Fatal(err)
	}
	database = result()
	if database == 0 {
		t.Fatal("created database did not reopen")
	}
	setArgs(database)
	if err := r.handleDatabase(2); err != nil {
		t.Fatal(err)
	}
	if got := result(); got != 1 {
		t.Fatalf("record count=%d, want 1", got)
	}
	setArgs(database)
	if err := r.handleDatabase(4); err != nil {
		t.Fatal(err)
	}
	record = result()
	if record == 0 {
		t.Fatal("GetNextRecord returned null")
	}
	output, err := r.allocateGuest(4)
	if err != nil {
		t.Fatal(err)
	}
	setArgs(record, 0, 0)
	if err := r.handleDBRecord(6); err != nil {
		t.Fatal(err)
	}
	if got := result(); got != 3 {
		t.Fatalf("field type=%d, want DWORD", got)
	}
	setArgs(record, output)
	if err := r.handleDBRecord(9); err != nil {
		t.Fatal(err)
	}
	if got := result(); got != 1 {
		t.Fatalf("GetFieldDWord status=%d, want success", got)
	}
	var actual [4]byte
	if err := r.cpu.ReadMemory(output, actual[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(actual[:]); got != 1234 {
		t.Fatalf("reopened field=%d, want 1234", got)
	}
	setArgs(record)
	if err := r.handleDBRecord(1); err != nil {
		t.Fatal(err)
	}
	setArgs(database)
	if err := r.handleDatabase(1); err != nil {
		t.Fatal(err)
	}
	setArgs(dbMgrObject, name)
	if err := r.removeDatabase(); err != nil {
		t.Fatal(err)
	}
	if got := result(); got != 0 {
		t.Fatalf("remove status=%d, want success", got)
	}
	setArgs(dbMgrObject, name, 0)
	if err := r.openDatabase(); err != nil {
		t.Fatal(err)
	}
	if got := result(); got != 0 {
		t.Fatalf("removed database reopened as 0x%08x", got)
	}
}
