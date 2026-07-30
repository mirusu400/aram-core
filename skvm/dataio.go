package skvm

import (
	"context"
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

type dataInputState struct {
	stream uint32
}

type dataOutputState struct {
	stream uint32
}

func (vm *VM) installDataIONatives() {
	vm.RegisterNative(
		"java/io/ByteArrayInputStream",
		"<init>",
		"([B)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.ByteArray(reference)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(
				receiver,
				&inputStreamState{data: append([]byte(nil), data...)},
			)
		},
	)
	vm.RegisterNative(
		"java/io/DataInputStream",
		"<init>",
		"(Ljava/io/InputStream;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			stream, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &dataInputState{stream: stream})
		},
	)
	vm.RegisterNative(
		"java/io/DataOutputStream",
		"<init>",
		"(Ljava/io/OutputStream;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			stream, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &dataOutputState{stream: stream})
		},
	)
	vm.RegisterNative(
		"java/io/DataOutputStream",
		"write",
		"([B)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.ByteArray(reference)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.dataOutputWrite(receiver, data)
		},
	)
	vm.RegisterNative(
		"java/io/DataOutputStream",
		"write",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.dataOutputWrite(receiver, []byte{byte(value)})
		},
	)

	vm.RegisterNative(
		"java/io/DataInputStream",
		"available",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			stream, err := vm.dataInput(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(stream.data) - stream.offset)), true, nil
		},
	)
	vm.RegisterNative("java/io/DataInputStream", "close", "()V", nativeVoid)
	vm.RegisterNative("java/io/DataOutputStream", "close", "()V", nativeVoid)
	vm.RegisterNative("java/io/DataOutputStream", "flush", "()V", nativeVoid)

	for _, method := range []struct {
		name       string
		descriptor string
		size       int
		result     func([]byte) Value
	}{
		{"readBoolean", "()Z", 1, func(data []byte) Value { return boolValue(data[0] != 0) }},
		{"readByte", "()B", 1, func(data []byte) Value { return IntValue(int32(int8(data[0]))) }},
		{"readShort", "()S", 2, func(data []byte) Value {
			return IntValue(int32(int16(binary.BigEndian.Uint16(data))))
		}},
		{"readInt", "()I", 4, func(data []byte) Value {
			return IntValue(int32(binary.BigEndian.Uint32(data)))
		}},
		{"readLong", "()J", 8, func(data []byte) Value {
			return LongValue(int64(binary.BigEndian.Uint64(data)))
		}},
	} {
		spec := method
		vm.RegisterNative(
			"java/io/DataInputStream",
			spec.name,
			spec.descriptor,
			func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
				stream, err := vm.dataInput(receiver)
				if err != nil {
					return Value{}, false, err
				}
				data, err := readStreamBytes(stream, spec.size)
				if err != nil {
					return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
				}
				return spec.result(data), true, nil
			},
		)
	}
	vm.RegisterNative(
		"java/io/DataInputStream",
		"read",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			stream, err := vm.dataInput(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if stream.offset >= len(stream.data) {
				return IntValue(-1), true, nil
			}
			value := stream.data[stream.offset]
			stream.offset++
			return IntValue(int32(value)), true, nil
		},
	)
	for _, descriptor := range []string{"([B)I", "([BII)I"} {
		methodDescriptor := descriptor
		vm.RegisterNative(
			"java/io/DataInputStream",
			"read",
			methodDescriptor,
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				return vm.dataInputRead(receiver, args, false)
			},
		)
	}
	for _, descriptor := range []string{"([B)V", "([BII)V"} {
		methodDescriptor := descriptor
		vm.RegisterNative(
			"java/io/DataInputStream",
			"readFully",
			methodDescriptor,
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				_, _, err := vm.dataInputRead(receiver, args, true)
				return Value{}, false, err
			},
		)
	}
	vm.RegisterNative(
		"java/io/DataInputStream",
		"skipBytes",
		"(I)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			stream, err := vm.dataInput(receiver)
			if err != nil {
				return Value{}, false, err
			}
			count, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			skipped := min(max(0, int(count)), len(stream.data)-stream.offset)
			stream.offset += skipped
			return IntValue(int32(skipped)), true, nil
		},
	)
	vm.RegisterNative(
		"java/io/DataInputStream",
		"skip",
		"(J)J",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			stream, err := vm.dataInput(receiver)
			if err != nil {
				return Value{}, false, err
			}
			count, err := args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
			skipped := min(max(int64(0), count), int64(len(stream.data)-stream.offset))
			stream.offset += int(skipped)
			return LongValue(skipped), true, nil
		},
	)
	vm.RegisterNative(
		"java/io/DataInputStream",
		"readUTF",
		"()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			stream, err := vm.dataInput(receiver)
			if err != nil {
				return Value{}, false, err
			}
			lengthBytes, err := readStreamBytes(stream, 2)
			if err != nil {
				return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
			}
			data, err := readStreamBytes(stream, int(binary.BigEndian.Uint16(lengthBytes)))
			if err != nil {
				return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
			}
			return ReferenceValue(vm.NewString(string(data))), true, nil
		},
	)

	for _, method := range []struct {
		name       string
		descriptor string
		encode     func([]Value) ([]byte, error)
	}{
		{"writeBoolean", "(Z)V", func(args []Value) ([]byte, error) {
			value, err := intArgument(args, 0)
			return []byte{byte(value)}, err
		}},
		{"writeByte", "(I)V", func(args []Value) ([]byte, error) {
			value, err := intArgument(args, 0)
			return []byte{byte(value)}, err
		}},
		{"writeShort", "(I)V", func(args []Value) ([]byte, error) {
			value, err := intArgument(args, 0)
			data := make([]byte, 2)
			binary.BigEndian.PutUint16(data, uint16(value))
			return data, err
		}},
		{"writeInt", "(I)V", func(args []Value) ([]byte, error) {
			value, err := intArgument(args, 0)
			data := make([]byte, 4)
			binary.BigEndian.PutUint32(data, uint32(value))
			return data, err
		}},
		{"writeLong", "(J)V", func(args []Value) ([]byte, error) {
			value, err := args[0].Long()
			data := make([]byte, 8)
			binary.BigEndian.PutUint64(data, uint64(value))
			return data, err
		}},
	} {
		spec := method
		vm.RegisterNative(
			"java/io/DataOutputStream",
			spec.name,
			spec.descriptor,
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				data, err := spec.encode(args)
				if err != nil {
					return Value{}, false, err
				}
				err = vm.dataOutputWrite(receiver, data)
				return Value{}, false, err
			},
		)
	}
	vm.RegisterNative(
		"java/io/DataOutputStream",
		"write",
		"([BII)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.dataOutputWrite(receiver, data)
		},
	)
	vm.RegisterNative(
		"java/io/DataOutputStream",
		"writeUTF",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data := []byte(value)
			if len(data) > 0xffff {
				return Value{}, false, vm.newThrowable("java/io/IOException", "UTF string too long")
			}
			encoded := make([]byte, 2+len(data))
			binary.BigEndian.PutUint16(encoded, uint16(len(data)))
			copy(encoded[2:], data)
			return Value{}, false, vm.dataOutputWrite(receiver, encoded)
		},
	)
}

func (vm *VM) dataInput(reference uint32) (*inputStreamState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid DataInputStream reference")
	}
	state, ok := object.Native.(*dataInputState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a DataInputStream", reference)
	}
	return vm.inputStream(state.stream)
}

func (vm *VM) dataInputRead(
	receiver uint32,
	args []Value,
	fully bool,
) (Value, bool, error) {
	stream, err := vm.dataInput(receiver)
	if err != nil {
		return Value{}, false, err
	}
	reference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	destination, ok := vm.Object(reference)
	if !ok || destination.Array == nil || destination.Array.Descriptor != "[B" {
		return Value{}, false, fmt.Errorf("DataInput destination is not byte[]")
	}
	offset := int32(0)
	length := int32(len(destination.Array.Elements))
	if len(args) == 3 {
		offset, err = intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		length, err = intArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
	}
	if offset < 0 || length < 0 ||
		int64(offset)+int64(length) > int64(len(destination.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	available := len(stream.data) - stream.offset
	if available == 0 && !fully {
		return IntValue(-1), true, nil
	}
	if fully && available < int(length) {
		return Value{}, false, vm.newThrowable("java/io/IOException", "unexpected EOF")
	}
	count := min(int(length), available)
	for index := range count {
		destination.Array.Elements[int(offset)+index] =
			IntValue(int32(int8(stream.data[stream.offset+index])))
	}
	stream.offset += count
	return IntValue(int32(count)), true, nil
}

func (vm *VM) dataOutputWrite(reference uint32, data []byte) error {
	object, ok := vm.Object(reference)
	if !ok {
		return fmt.Errorf("invalid DataOutputStream reference")
	}
	state, ok := object.Native.(*dataOutputState)
	if !ok {
		return fmt.Errorf("object %d is not a DataOutputStream", reference)
	}
	output, err := vm.outputStream(state.stream)
	if err != nil {
		return err
	}
	if output.file != nil {
		end := output.file.offset + len(data)
		if end > len(output.file.data) {
			output.file.data = append(
				output.file.data,
				make([]byte, end-len(output.file.data))...,
			)
		}
		copy(output.file.data[output.file.offset:end], data)
		output.file.offset = end
	} else {
		output.data = append(output.data, data...)
	}
	return nil
}

func readStreamBytes(stream *inputStreamState, count int) ([]byte, error) {
	if count < 0 || stream.offset > len(stream.data)-count {
		return nil, fmt.Errorf("unexpected EOF")
	}
	data := stream.data[stream.offset : stream.offset+count]
	stream.offset += count
	return data, nil
}

func utf16Bytes(value string) []byte {
	units := utf16.Encode([]rune(value))
	data := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.BigEndian.PutUint16(data[index*2:], unit)
	}
	return data
}
