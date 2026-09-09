package skvm

import (
	"context"
	"errors"
	"fmt"
	"sort"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	rmsVersionField       = "$rms.version"
	rmsModifiedField      = "$rms.modified"
	rmsListenersField     = "$rms.listeners"
	rmsModeField          = "$rms.mode"
	rmsWritableField      = "$rms.writable"
	rmsEnumStoreField     = "$rms.enumeration.store"
	rmsEnumIDsField       = "$rms.enumeration.ids"
	rmsEnumCursorField    = "$rms.enumeration.cursor"
	rmsEnumFilterField    = "$rms.enumeration.filter"
	rmsEnumComparator     = "$rms.enumeration.comparator"
	rmsEnumKeepField      = "$rms.enumeration.keepUpdated"
	rmsEnumDestroyedField = "$rms.enumeration.destroyed"
	rmsEnumVersionField   = "$rms.enumeration.version"
)

func (vm *VM) installRecordStoreExtras() {
	vm.installRecordStoreOpenNatives()
	vm.installRecordStoreMetadataNatives()
	vm.installRecordStoreMutationNatives()
	vm.installRecordEnumerationNatives()
	vm.installRMSExceptionNatives()
	vm.RegisterStaticField("javax/microedition/rms/RecordStore", "AUTHMODE_PRIVATE", "I", IntValue(0))
	vm.RegisterStaticField("javax/microedition/rms/RecordStore", "AUTHMODE_ANY", "I", IntValue(1))
}

func (vm *VM) installRecordStoreOpenNatives() {
	for _, descriptor := range []string{
		"(Ljava/lang/String;Z)Ljavax/microedition/rms/RecordStore;",
		"(Ljava/lang/String;ZIZ)Ljavax/microedition/rms/RecordStore;",
	} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/rms/RecordStore", "openRecordStore", descriptor, func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			create, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			if len(args) == 4 {
				mode, _ := intArgument(args, 2)
				if mode != 0 && mode != 1 {
					return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
				}
			}
			id, openErr := vm.services.Storage.OpenRecordStore(vm.serviceOwner, name)
			if openErr != nil && create != 0 {
				id, openErr = vm.services.Storage.CreateRecordStore(vm.serviceOwner, name)
			}
			if openErr != nil {
				return Value{}, false, vm.rmsStoreThrowable(openErr)
			}
			return ReferenceValue(vm.newRecordStoreHandle(name, id)), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/rms/RecordStore", "openRecordStore", "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)Ljavax/microedition/rms/RecordStore;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		name, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if _, err = vm.stringArgument(args, 1); err != nil {
			return Value{}, false, err
		}
		if _, err = vm.stringArgument(args, 2); err != nil {
			return Value{}, false, err
		}
		id, err := vm.services.Storage.OpenRecordStore(vm.serviceOwner, name)
		if err != nil {
			return Value{}, false, vm.rmsStoreThrowable(err)
		}
		return ReferenceValue(vm.newRecordStoreHandle(name, id)), true, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "closeRecordStore", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state.id = 0
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "listRecordStores", "()[Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		names := make([]string, 0)
		for _, store := range vm.services.Storage.Snapshot().RecordStores {
			if store.Owner == vm.serviceOwner {
				names = append(names, store.Name)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return ReferenceValue(0), true, nil
		}
		return ReferenceValue(vm.mediaStringArray(names)), true, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "setMode", "(IZ)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		if _, err := vm.recordStore(receiver); err != nil {
			return Value{}, false, err
		}
		mode, _ := intArgument(args, 0)
		if mode != 0 && mode != 1 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		_ = setObjectField(vm, receiver, rmsModeField, args[0])
		return Value{}, false, setObjectField(vm, receiver, rmsWritableField, args[1])
	})
}

func (vm *VM) newRecordStoreHandle(name string, id shared.ServiceID) uint32 {
	reference := vm.NewObject("javax/microedition/rms/RecordStore", &recordStoreState{name: name, id: id})
	object, _ := vm.Object(reference)
	version, modified := int32(0), vm.services.Clock.WallMillis()
	for otherReference, other := range vm.heap {
		if otherReference == reference {
			continue
		}
		state, ok := other.Native.(*recordStoreState)
		if !ok || state.id != id {
			continue
		}
		version, _ = other.Fields[rmsVersionField].Int()
		modified, _ = other.Fields[rmsModifiedField].Long()
		break
	}
	object.Fields[rmsVersionField] = IntValue(version)
	object.Fields[rmsModifiedField] = LongValue(modified)
	object.Fields[rmsListenersField] = ReferenceValue(vm.newArray("[Ljavax/microedition/rms/RecordListener;", nil))
	object.Fields[rmsModeField] = IntValue(0)
	object.Fields[rmsWritableField] = IntValue(0)
	return reference
}

func (vm *VM) installRecordStoreMetadataNatives() {
	vm.RegisterNative("javax/microedition/rms/RecordStore", "getName", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(state.name)), true, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "getVersion", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if _, err := vm.recordStore(receiver); err != nil {
			return Value{}, false, err
		}
		value, err := objectField(vm, receiver, rmsVersionField)
		return value, true, err
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "getLastModified", "()J", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if _, err := vm.recordStore(receiver); err != nil {
			return Value{}, false, err
		}
		value, err := objectField(vm, receiver, rmsModifiedField)
		return value, true, err
	})
	for _, method := range []struct {
		name      string
		available bool
	}{{"getSize", false}, {"getSizeAvailable", true}} {
		method := method
		vm.RegisterNative("javax/microedition/rms/RecordStore", method.name, "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.recordStore(receiver)
			if err != nil {
				return Value{}, false, err
			}
			size, err := vm.recordStoreSize(state.id)
			if err != nil {
				return Value{}, false, vm.rmsThrowable(err)
			}
			if method.available {
				limit := vm.services.Storage.Snapshot().Limits.MaxRecordBytes
				if size < limit {
					size = limit - size
				} else {
					size = 0
				}
			}
			return IntValue(int32(min(size, uint64(1<<31-1)))), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/rms/RecordStore", "getRecordSize", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		id, _ := intArgument(args, 0)
		data, err := vm.rmsRecord(state.id, id)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(int32(len(data))), true, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "getRecord", "(I)[B", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		id, _ := intArgument(args, 0)
		data, err := vm.rmsRecord(state.id, id)
		if err != nil {
			return Value{}, false, err
		}
		if len(data) == 0 {
			return ReferenceValue(0), true, nil
		}
		return ReferenceValue(vm.NewByteArray(data)), true, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "getRecord", "(I[BI)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		id, _ := intArgument(args, 0)
		data, err := vm.rmsRecord(state.id, id)
		if err != nil {
			return Value{}, false, err
		}
		destination, _ := referenceArgument(args, 1)
		object, ok := vm.Object(destination)
		if !ok || object.Array == nil {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		offset, _ := intArgument(args, 2)
		if offset < 0 || int64(offset)+int64(len(data)) > int64(len(object.Array.Elements)) {
			return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
		}
		for index, value := range data {
			object.Array.Elements[int(offset)+index] = IntValue(int32(int8(value)))
		}
		return IntValue(int32(len(data))), true, nil
	})
	for _, name := range []string{"addRecordListener", "removeRecordListener"} {
		name := name
		vm.RegisterNative("javax/microedition/rms/RecordStore", name, "(Ljavax/microedition/rms/RecordListener;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			if _, err := vm.recordStore(receiver); err != nil {
				return Value{}, false, err
			}
			listener, _ := referenceArgument(args, 0)
			if listener == 0 {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			listeners, _ := vm.objectArray(receiver, rmsListenersField, "[Ljavax/microedition/rms/RecordListener;")
			for index, value := range listeners.Elements {
				current, _ := value.Reference()
				if current == listener {
					if name == "removeRecordListener" {
						listeners.Elements = append(listeners.Elements[:index], listeners.Elements[index+1:]...)
					}
					return Value{}, false, nil
				}
			}
			if name == "addRecordListener" {
				listeners.Elements = append(listeners.Elements, ReferenceValue(listener))
			}
			return Value{}, false, nil
		})
	}
}

func (vm *VM) recordStoreSize(id shared.ServiceID) (uint64, error) {
	ids, err := vm.services.Storage.RecordIDs(vm.serviceOwner, id)
	if err != nil {
		return 0, err
	}
	var size uint64
	for _, recordID := range ids {
		data, err := vm.services.Storage.Record(vm.serviceOwner, id, recordID)
		if err != nil {
			return 0, err
		}
		size += uint64(len(data))
	}
	return size, nil
}

func (vm *VM) rmsRecord(store shared.ServiceID, id int32) ([]byte, error) {
	if id <= 0 {
		return nil, vm.newThrowable("javax/microedition/rms/InvalidRecordIDException", "invalid record ID")
	}
	data, err := vm.services.Storage.Record(vm.serviceOwner, store, uint32(id))
	if err != nil {
		return nil, vm.rmsRecordThrowable(err)
	}
	return data, nil
}

func (vm *VM) installRecordStoreMutationNatives() {
	vm.RegisterNative("javax/microedition/rms/RecordStore", "addRecord", "([BII)I", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		data, err := vm.rmsDataArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		id, err := vm.services.Storage.AddRecord(vm.serviceOwner, state.id, data)
		if err != nil {
			return Value{}, false, vm.rmsMutationThrowable(err)
		}
		vm.bumpRecordStore(state.id)
		if err := vm.notifyRMSListeners(ctx, receiver, "recordAdded", int32(id)); err != nil {
			return Value{}, false, err
		}
		return IntValue(int32(id)), true, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "setRecord", "(I[BII)V", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		id, _ := intArgument(args, 0)
		if _, err := vm.rmsRecord(state.id, id); err != nil {
			return Value{}, false, err
		}
		data, err := vm.rmsDataArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if err := vm.services.Storage.SetRecord(vm.serviceOwner, state.id, uint32(id), data); err != nil {
			return Value{}, false, vm.rmsMutationThrowable(err)
		}
		vm.bumpRecordStore(state.id)
		return Value{}, false, vm.notifyRMSListeners(ctx, receiver, "recordChanged", id)
	})
	vm.RegisterNative("javax/microedition/rms/RecordStore", "deleteRecord", "(I)V", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.recordStore(receiver)
		if err != nil {
			return Value{}, false, err
		}
		id, _ := intArgument(args, 0)
		if _, err := vm.rmsRecord(state.id, id); err != nil {
			return Value{}, false, err
		}
		if err := vm.services.Storage.DeleteRecord(vm.serviceOwner, state.id, uint32(id)); err != nil {
			return Value{}, false, vm.rmsRecordThrowable(err)
		}
		vm.bumpRecordStore(state.id)
		return Value{}, false, vm.notifyRMSListeners(ctx, receiver, "recordDeleted", id)
	})
}

func (vm *VM) rmsDataArgument(args []Value, start int) ([]byte, error) {
	reference, err := referenceArgument(args, start)
	if err != nil {
		return nil, err
	}
	offset, _ := intArgument(args, start+1)
	length, _ := intArgument(args, start+2)
	if reference == 0 {
		if offset == 0 && length == 0 {
			return nil, nil
		}
		return nil, vm.newThrowable("java/lang/NullPointerException", "")
	}
	data, err := vm.ByteArray(reference)
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(data)) {
		return nil, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
	}
	return append([]byte(nil), data[offset:offset+length]...), nil
}

func (vm *VM) bumpRecordStore(id shared.ServiceID) {
	for _, object := range vm.heap {
		state, ok := object.Native.(*recordStoreState)
		if !ok || state.id != id {
			continue
		}
		version, _ := object.Fields[rmsVersionField].Int()
		object.Fields[rmsVersionField] = IntValue(version + 1)
		object.Fields[rmsModifiedField] = LongValue(vm.services.Clock.WallMillis())
	}
}

func (vm *VM) notifyRMSListeners(ctx context.Context, storeReference uint32, method string, id int32) error {
	listeners, _ := vm.objectArray(storeReference, rmsListenersField, "[Ljavax/microedition/rms/RecordListener;")
	for _, value := range append([]Value(nil), listeners.Elements...) {
		listener, _ := value.Reference()
		if listener == 0 {
			continue
		}
		_, _, err := vm.InvokeVirtual(ctx, listener, method, "(Ljavax/microedition/rms/RecordStore;I)V", ReferenceValue(storeReference), IntValue(id))
		if err != nil && !errors.Is(err, ErrMethodNotFound) {
			return err
		}
	}
	return nil
}

func (vm *VM) installRecordEnumerationNatives() {
	vm.RegisterNative("javax/microedition/rms/RecordStore", "enumerateRecords", "(Ljavax/microedition/rms/RecordFilter;Ljavax/microedition/rms/RecordComparator;Z)Ljavax/microedition/rms/RecordEnumeration;", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		if _, err := vm.recordStore(receiver); err != nil {
			return Value{}, false, err
		}
		reference := vm.NewObject("javax/microedition/rms/RecordEnumeration", nil)
		object, _ := vm.Object(reference)
		object.Fields[rmsEnumStoreField] = ReferenceValue(receiver)
		object.Fields[rmsEnumFilterField] = args[0]
		object.Fields[rmsEnumComparator] = args[1]
		object.Fields[rmsEnumKeepField] = args[2]
		object.Fields[rmsEnumDestroyedField] = IntValue(0)
		if err := vm.rebuildRecordEnumeration(ctx, reference); err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(reference), true, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordEnumeration", "destroy", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		_ = setObjectField(vm, receiver, rmsEnumDestroyedField, IntValue(1))
		_ = setObjectField(vm, receiver, rmsEnumStoreField, ReferenceValue(0))
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/rms/RecordEnumeration", "rebuild", "()V", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, vm.rebuildRecordEnumeration(ctx, receiver)
	})
	vm.RegisterNative("javax/microedition/rms/RecordEnumeration", "reset", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if err := vm.ensureRecordEnumeration(receiver); err != nil {
			return Value{}, false, err
		}
		return Value{}, false, setObjectField(vm, receiver, rmsEnumCursorField, IntValue(0))
	})
	vm.RegisterNative("javax/microedition/rms/RecordEnumeration", "numRecords", "()I", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if err := vm.refreshRecordEnumeration(ctx, receiver); err != nil {
			return Value{}, false, err
		}
		ids, _ := vm.objectArray(receiver, rmsEnumIDsField, "[I")
		return IntValue(int32(len(ids.Elements))), true, nil
	})
	for _, method := range []struct {
		name string
		next bool
	}{{"hasNextElement", true}, {"hasPreviousElement", false}} {
		method := method
		vm.RegisterNative("javax/microedition/rms/RecordEnumeration", method.name, "()Z", func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if err := vm.refreshRecordEnumeration(ctx, receiver); err != nil {
				return Value{}, false, err
			}
			ids, _ := vm.objectArray(receiver, rmsEnumIDsField, "[I")
			cursor, _ := vm.gameInt(receiver, rmsEnumCursorField)
			if method.next {
				return IntValue(boolInt(int(cursor) < len(ids.Elements))), true, nil
			}
			return IntValue(boolInt(cursor > 0)), true, nil
		})
	}
	for _, method := range []struct {
		name       string
		next, data bool
	}{{"nextRecordId", true, false}, {"previousRecordId", false, false}, {"nextRecord", true, true}, {"previousRecord", false, true}} {
		method := method
		descriptor := "()I"
		if method.data {
			descriptor = "()[B"
		}
		vm.RegisterNative("javax/microedition/rms/RecordEnumeration", method.name, descriptor, func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			id, err := vm.recordEnumerationStep(ctx, receiver, method.next)
			if err != nil {
				return Value{}, false, err
			}
			if !method.data {
				return IntValue(id), true, nil
			}
			storeRefValue, _ := objectField(vm, receiver, rmsEnumStoreField)
			storeRef, _ := storeRefValue.Reference()
			store, err := vm.recordStore(storeRef)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.rmsRecord(store.id, id)
			if err != nil {
				return Value{}, false, err
			}
			if len(data) == 0 {
				return ReferenceValue(0), true, nil
			}
			return ReferenceValue(vm.NewByteArray(data)), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/rms/RecordEnumeration", "isKeptUpdated", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if err := vm.ensureRecordEnumeration(receiver); err != nil {
			return Value{}, false, err
		}
		value, err := objectField(vm, receiver, rmsEnumKeepField)
		return value, true, err
	})
	vm.RegisterNative("javax/microedition/rms/RecordEnumeration", "keepUpdated", "(Z)V", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		if err := vm.ensureRecordEnumeration(receiver); err != nil {
			return Value{}, false, err
		}
		_ = setObjectField(vm, receiver, rmsEnumKeepField, args[0])
		keep, _ := intArgument(args, 0)
		if keep != 0 {
			return Value{}, false, vm.rebuildRecordEnumeration(ctx, receiver)
		}
		return Value{}, false, nil
	})
}

func (vm *VM) ensureRecordEnumeration(reference uint32) error {
	object, ok := vm.Object(reference)
	if !ok || object.Class != "javax/microedition/rms/RecordEnumeration" {
		return fmt.Errorf("invalid RecordEnumeration")
	}
	destroyed, _ := object.Fields[rmsEnumDestroyedField].Int()
	if destroyed != 0 {
		return vm.newThrowable("java/lang/IllegalStateException", "enumeration destroyed")
	}
	return nil
}
func (vm *VM) refreshRecordEnumeration(ctx context.Context, reference uint32) error {
	if err := vm.ensureRecordEnumeration(reference); err != nil {
		return err
	}
	keepValue, _ := objectField(vm, reference, rmsEnumKeepField)
	keep, _ := keepValue.Int()
	if keep != 0 {
		storeValue, _ := objectField(vm, reference, rmsEnumStoreField)
		storeReference, _ := storeValue.Reference()
		storeObject, _ := vm.Object(storeReference)
		currentVersion, _ := storeObject.Fields[rmsVersionField].Int()
		enumeratedVersion, _ := vm.gameInt(reference, rmsEnumVersionField)
		if currentVersion != enumeratedVersion {
			return vm.rebuildRecordEnumeration(ctx, reference)
		}
	}
	return nil
}
func (vm *VM) recordEnumerationStep(ctx context.Context, reference uint32, next bool) (int32, error) {
	if err := vm.refreshRecordEnumeration(ctx, reference); err != nil {
		return 0, err
	}
	ids, _ := vm.objectArray(reference, rmsEnumIDsField, "[I")
	cursor, _ := vm.gameInt(reference, rmsEnumCursorField)
	if next {
		if int(cursor) >= len(ids.Elements) {
			return 0, vm.newThrowable("javax/microedition/rms/InvalidRecordIDException", "no next record")
		}
		id, _ := ids.Elements[cursor].Int()
		_ = setObjectField(vm, reference, rmsEnumCursorField, IntValue(cursor+1))
		return id, nil
	}
	if cursor <= 0 {
		return 0, vm.newThrowable("javax/microedition/rms/InvalidRecordIDException", "no previous record")
	}
	cursor--
	id, _ := ids.Elements[cursor].Int()
	_ = setObjectField(vm, reference, rmsEnumCursorField, IntValue(cursor))
	return id, nil
}

func (vm *VM) rebuildRecordEnumeration(ctx context.Context, reference uint32) error {
	if err := vm.ensureRecordEnumeration(reference); err != nil {
		return err
	}
	storeValue, _ := objectField(vm, reference, rmsEnumStoreField)
	storeReference, _ := storeValue.Reference()
	store, err := vm.recordStore(storeReference)
	if err != nil {
		return err
	}
	ids, err := vm.services.Storage.RecordIDs(vm.serviceOwner, store.id)
	if err != nil {
		return vm.rmsThrowable(err)
	}
	filterValue, _ := objectField(vm, reference, rmsEnumFilterField)
	filter, _ := filterValue.Reference()
	filtered := make([]uint32, 0, len(ids))
	for _, id := range ids {
		data, err := vm.services.Storage.Record(vm.serviceOwner, store.id, id)
		if err != nil {
			return vm.rmsThrowable(err)
		}
		if filter != 0 {
			matched, _, invokeErr := vm.InvokeVirtual(ctx, filter, "matches", "([B)Z", ReferenceValue(vm.NewByteArray(data)))
			if invokeErr != nil {
				return invokeErr
			}
			yes, _ := matched.Int()
			if yes == 0 {
				continue
			}
		}
		filtered = append(filtered, id)
	}
	comparatorValue, _ := objectField(vm, reference, rmsEnumComparator)
	comparator, _ := comparatorValue.Reference()
	if comparator != 0 {
		var compareErr error
		sort.SliceStable(filtered, func(i, j int) bool {
			if compareErr != nil {
				return false
			}
			a, _ := vm.services.Storage.Record(vm.serviceOwner, store.id, filtered[i])
			b, _ := vm.services.Storage.Record(vm.serviceOwner, store.id, filtered[j])
			result, _, err := vm.InvokeVirtual(ctx, comparator, "compare", "([B[B)I", ReferenceValue(vm.NewByteArray(a)), ReferenceValue(vm.NewByteArray(b)))
			if err != nil {
				compareErr = err
				return false
			}
			value, _ := result.Int()
			return value < 0
		})
		if compareErr != nil {
			return compareErr
		}
	}
	values := make([]Value, len(filtered))
	for index, id := range filtered {
		values[index] = IntValue(int32(id))
	}
	_ = setObjectField(vm, reference, rmsEnumIDsField, ReferenceValue(vm.newArray("[I", values)))
	storeObject, _ := vm.Object(storeReference)
	version, _ := storeObject.Fields[rmsVersionField].Int()
	_ = setObjectField(vm, reference, rmsEnumVersionField, IntValue(version))
	return setObjectField(vm, reference, rmsEnumCursorField, IntValue(0))
}

func (vm *VM) installRMSExceptionNatives() {
	for _, class := range []string{"javax/microedition/rms/InvalidRecordIDException", "javax/microedition/rms/RecordStoreException", "javax/microedition/rms/RecordStoreFullException", "javax/microedition/rms/RecordStoreNotFoundException", "javax/microedition/rms/RecordStoreNotOpenException"} {
		class := class
		vm.RegisterNative(class, "<init>", "()V", nativeVoid)
		vm.RegisterNative(class, "<init>", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			message, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, message)
		})
	}
}

func (vm *VM) rmsStoreThrowable(err error) error {
	class := "javax/microedition/rms/RecordStoreException"
	if errors.Is(err, shared.ErrNotFound) {
		class = "javax/microedition/rms/RecordStoreNotFoundException"
	}
	return vm.newThrowable(class, err.Error())
}
func (vm *VM) rmsRecordThrowable(err error) error {
	if errors.Is(err, shared.ErrNotFound) || errors.Is(err, shared.ErrInvalidArgument) {
		return vm.newThrowable("javax/microedition/rms/InvalidRecordIDException", err.Error())
	}
	return vm.newThrowable("javax/microedition/rms/RecordStoreException", err.Error())
}
func (vm *VM) rmsMutationThrowable(err error) error {
	if errors.Is(err, shared.ErrLimitExceeded) {
		return vm.newThrowable("javax/microedition/rms/RecordStoreFullException", err.Error())
	}
	return vm.rmsRecordThrowable(err)
}
