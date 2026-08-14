package skvm

import (
	"context"
)

func (vm *VM) installInputStreamNatives() {
	vm.RegisterNative(
		"java/io/InputStream",
		"read",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.inputStream(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state.closed {
				return Value{}, false, vm.newThrowable("java/io/IOException", "stream closed")
			}
			if state.offset >= len(state.data) {
				return IntValue(-1), true, nil
			}
			value := state.data[state.offset]
			state.offset++
			return IntValue(int32(value)), true, nil
		},
	)
	vm.RegisterNative(
		"java/io/InputStream",
		"available",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.inputStream(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state.closed {
				return IntValue(0), true, nil
			}
			return IntValue(int32(len(state.data) - state.offset)), true, nil
		},
	)
	vm.RegisterNative("java/io/InputStream", "close", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		state, err := vm.inputStream(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state.closed = true
		return Value{}, false, nil
	})
	vm.RegisterNative("java/io/InputStream", "read", "([B)I", nativeStreamRead)
	vm.RegisterNative("java/io/InputStream", "read", "([BII)I", nativeStreamRead)
}

func (vm *VM) installOutputStreamNatives() {
	vm.RegisterNative("java/io/OutputStream", "<init>", "()V", nativeVoid)
	vm.RegisterNative("java/io/OutputStream", "close", "()V", nativeVoid)
	vm.RegisterNative("java/io/OutputStream", "flush", "()V", nativeVoid)
	vm.RegisterNative("java/io/OutputStream", "write", "(I)V", nativeStreamWrite)
	vm.RegisterNative("java/io/OutputStream", "write", "([B)V", nativeStreamWrite)
	vm.RegisterNative("java/io/OutputStream", "write", "([BII)V", nativeStreamWrite)
	for _, descriptor := range []string{
		"(I)V",
		"(Ljava/lang/String;)V",
		"(Ljava/lang/Object;)V",
	} {
		vm.RegisterNative("java/io/PrintStream", "println", descriptor, nativeVoid)
	}
	vm.RegisterNative("java/io/ByteArrayOutputStream", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &outputStreamState{})
	})
	vm.RegisterNative(
		"java/io/ByteArrayOutputStream",
		"size",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.outputStream(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(state.data))), true, nil
		},
	)
	vm.RegisterNative(
		"java/io/ByteArrayOutputStream",
		"toByteArray",
		"()[B",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.outputStream(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewByteArray(state.data)), true, nil
		},
	)
	vm.RegisterNative(
		"java/io/ByteArrayOutputStream",
		"reset",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.outputStream(receiver)
			if err != nil {
				return Value{}, false, err
			}
			state.data = nil
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"java/io/InputStreamReader",
		"<init>",
		"(Ljava/io/InputStream;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			stream, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(
				receiver,
				&inputStreamReaderState{stream: stream},
			)
		},
	)
	vm.RegisterNative(
		"java/io/InputStreamReader",
		"read",
		"([CII)I",
		nativeReaderRead,
	)
}
