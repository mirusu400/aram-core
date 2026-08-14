package skvm

import (
	"context"
	"fmt"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (vm *VM) installRecordStoreNatives() {
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"openRecordStore",
		"(Ljava/lang/String;Z)Ljavax/microedition/rms/RecordStore;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			create, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			id, openErr := vm.services.Storage.OpenRecordStore(vm.serviceOwner, name)
			if openErr != nil && create != 0 {
				id, openErr = vm.services.Storage.CreateRecordStore(vm.serviceOwner, name)
			}
			if openErr != nil {
				return Value{}, false, vm.rmsThrowable(openErr)
			}
			reference := vm.NewObject(
				"javax/microedition/rms/RecordStore",
				&recordStoreState{name: name, id: id},
			)
			return ReferenceValue(reference), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"deleteRecordStore",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.services.Storage.DeleteRecordStoreNamed(
				vm.serviceOwner,
				name,
			); err != nil {
				return Value{}, false, vm.rmsThrowable(err)
			}
			return Value{}, false, nil
		},
	)
	vm.RegisterNative("javax/microedition/rms/RecordStore", "closeRecordStore", "()V", nativeVoid)
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"getNextRecordID",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.recordStore(receiver)
			if err != nil {
				return Value{}, false, err
			}
			next, err := vm.services.Storage.NextRecordID(vm.serviceOwner, state.id)
			if err != nil {
				return Value{}, false, vm.rmsThrowable(err)
			}
			return IntValue(int32(next)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"addRecord",
		"([BII)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.recordStore(receiver)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			recordID, err := vm.services.Storage.AddRecord(
				vm.serviceOwner,
				state.id,
				data,
			)
			if err != nil {
				return Value{}, false, vm.rmsThrowable(err)
			}
			return IntValue(int32(recordID)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"setRecord",
		"(I[BII)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.recordStore(receiver)
			if err != nil {
				return Value{}, false, err
			}
			recordID, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.byteSliceArgument(args[1:])
			if err != nil {
				return Value{}, false, err
			}
			if recordID <= 0 {
				return Value{}, false, vm.rmsThrowable(shared.ErrInvalidArgument)
			}
			if err := vm.services.Storage.SetRecord(
				vm.serviceOwner,
				state.id,
				uint32(recordID),
				data,
			); err != nil {
				return Value{}, false, vm.rmsThrowable(err)
			}
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"getRecord",
		"(I[BI)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.recordStore(receiver)
			if err != nil {
				return Value{}, false, err
			}
			recordID, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if recordID <= 0 {
				return Value{}, false, vm.rmsThrowable(shared.ErrInvalidArgument)
			}
			record, err := vm.services.Storage.Record(
				vm.serviceOwner,
				state.id,
				uint32(recordID),
			)
			if err != nil {
				return Value{}, false, vm.rmsThrowable(err)
			}
			destination, err := referenceArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			object, ok := vm.Object(destination)
			if !ok || object.Array == nil {
				return Value{}, false, fmt.Errorf("getRecord destination is not an array")
			}
			destinationOffset, err := intArgument(args, 2)
			if err != nil {
				return Value{}, false, err
			}
			if destinationOffset < 0 ||
				int(destinationOffset) > len(object.Array.Elements) {
				return Value{}, false, vm.newThrowable(
					"java/lang/IndexOutOfBoundsException",
					"",
				)
			}
			count := min(
				len(record),
				len(object.Array.Elements)-int(destinationOffset),
			)
			for index := range count {
				object.Array.Elements[int(destinationOffset)+index] =
					IntValue(int32(int8(record[index])))
			}
			return IntValue(int32(count)), true, nil
		},
	)
}
