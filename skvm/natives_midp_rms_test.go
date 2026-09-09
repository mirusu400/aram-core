package skvm

import (
	"context"
	"testing"
)

func TestMIDPRecordStoreMetadataAndEnumeration(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	value := invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "openRecordStore", "(Ljava/lang/String;Z)Ljavax/microedition/rms/RecordStore;", 0, ReferenceValue(vm.NewString("scores")), IntValue(1))
	store, err := value.Reference()
	check(t, err)
	data := vm.NewByteArray([]byte{9, 1, 2, 3, 8})
	for range 2 {
		invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "addRecord", "([BII)I", store, ReferenceValue(data), IntValue(1), IntValue(3))
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "getVersion", "()I", store)); got != 2 {
		t.Fatalf("RecordStore version = %d", got)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "getSize", "()I", store)); got != 6 {
		t.Fatalf("RecordStore size = %d", got)
	}
	enumerationValue := invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "enumerateRecords", "(Ljavax/microedition/rms/RecordFilter;Ljavax/microedition/rms/RecordComparator;Z)Ljavax/microedition/rms/RecordEnumeration;", store, ReferenceValue(0), ReferenceValue(0), IntValue(1))
	enumeration, err := enumerationValue.Reference()
	check(t, err)
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/rms/RecordEnumeration", "nextRecordId", "()I", enumeration)); got != 1 {
		t.Fatalf("first record ID = %d", got)
	}
	invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "addRecord", "([BII)I", store, ReferenceValue(data), IntValue(1), IntValue(3))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/rms/RecordEnumeration", "numRecords", "()I", enumeration)); got != 3 {
		t.Fatalf("updated enumeration size = %d", got)
	}
}

func TestMIDPRecordStoreCloseIsEnforced(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	value := invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "openRecordStore", "(Ljava/lang/String;Z)Ljavax/microedition/rms/RecordStore;", 0, ReferenceValue(vm.NewString("closed")), IntValue(1))
	store, err := value.Reference()
	check(t, err)
	invokeTestNative(t, vm, "javax/microedition/rms/RecordStore", "closeRecordStore", "()V", store)
	native := vm.natives[nativeKey{"javax/microedition/rms/RecordStore", "getNumRecords", "()I"}]
	_, _, err = native(context.Background(), vm, store, nil)
	thrown, ok := err.(*thrown)
	if !ok || thrown.class != "javax/microedition/rms/RecordStoreNotOpenException" {
		t.Fatalf("closed store error = %v", err)
	}
}
