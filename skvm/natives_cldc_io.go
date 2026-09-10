package skvm

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	writerStreamField   = "\x00aram-writer-stream"
	writerEncodingField = "\x00aram-writer-encoding"
	writerClosedField   = "\x00aram-writer-closed"
	printStreamField    = "\x00aram-print-stream"
	printErrorField     = "\x00aram-print-error"
)

func (vm *VM) installCLDCCharacterStreamNatives() {
	vm.installCLDCInputStreamBehavior()
	vm.installCLDCReaderNatives()
	vm.installCLDCWriterNatives()
	vm.installCLDCPrintStreamNatives()
}

func (vm *VM) installCLDCInputStreamBehavior() {
	vm.RegisterNative("java/io/InputStream", "mark", "(I)V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if _, err := vm.inputStream(receiver); err != nil {
			return Value{}, false, err
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("java/io/InputStream", "markSupported", "()Z", func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
		return boolValue(false), true, nil
	})
	vm.RegisterNative("java/io/InputStream", "reset", "()V", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, vm.newThrowable("java/io/IOException", "mark/reset not supported")
	})
	vm.RegisterNative("java/io/InputStream", "skip", "(J)J", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.inputStream(receiver)
		if err != nil {
			return Value{}, false, err
		}
		count, err := args[0].Long()
		if err != nil {
			return Value{}, false, err
		}
		if count <= 0 || state.closed {
			return LongValue(0), true, nil
		}
		skipped := min(count, int64(len(state.data)-state.offset))
		state.offset += int(skipped)
		return LongValue(skipped), true, nil
	})
	vm.RegisterNative("java/io/ByteArrayInputStream", "<init>", "([BII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		reference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		data, err := vm.ByteArray(reference)
		if err != nil {
			return Value{}, false, err
		}
		offset, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		length, err := intArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
		if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(data)) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		copyOfRange := append([]byte(nil), data[offset:offset+length]...)
		return Value{}, false, vm.setNative(receiver, &inputStreamState{data: copyOfRange})
	})
	vm.RegisterNative("java/io/ByteArrayInputStream", "mark", "(I)V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.inputStream(receiver)
		if err == nil {
			state.mark = state.offset
		}
		return Value{}, false, err
	})
	vm.RegisterNative("java/io/ByteArrayInputStream", "markSupported", "()Z", func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
		return boolValue(true), true, nil
	})
	vm.RegisterNative("java/io/ByteArrayInputStream", "reset", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.inputStream(receiver)
		if err == nil {
			state.offset = state.mark
		}
		return Value{}, false, err
	})
	// ByteArrayInputStream.close is specified to have no effect.
	vm.RegisterNative("java/io/ByteArrayInputStream", "close", "()V", nativeVoid)
	vm.RegisterNative("java/io/ByteArrayOutputStream", "<init>", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		size, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if size < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "negative initial size")
		}
		return Value{}, false, vm.setNative(receiver, &outputStreamState{data: make([]byte, 0, size)})
	})
	vm.RegisterNative("java/io/ByteArrayOutputStream", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.outputStream(receiver)
		if err != nil {
			return Value{}, false, err
		}
		text, err := vm.services.Text.Decode(state.data, shared.EncodingEUCKR)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(text)), true, nil
	})
}

func (vm *VM) installCLDCReaderNatives() {
	for _, descriptor := range []string{"()V", "(Ljava/lang/Object;)V"} {
		vm.RegisterNative("java/io/Reader", "<init>", descriptor, nativeVoid)
	}
	constructor := func(named bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			stream, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if stream == 0 {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "null input stream")
			}
			if _, err = vm.inputStream(stream); err != nil {
				return Value{}, false, err
			}
			encoding := shared.EncodingEUCKR
			if named {
				name, nameErr := vm.stringArgument(args, 1)
				if nameErr != nil {
					return Value{}, false, nameErr
				}
				encoding, err = vm.textEncoding(name)
				if err != nil {
					return Value{}, false, err
				}
			}
			return Value{}, false, vm.setNative(receiver, &inputStreamReaderState{stream: stream, encoding: encoding})
		}
	}
	vm.RegisterNative("java/io/InputStreamReader", "<init>", "(Ljava/io/InputStream;)V", constructor(false))
	vm.RegisterNative("java/io/InputStreamReader", "<init>", "(Ljava/io/InputStream;Ljava/lang/String;)V", constructor(true))
	readOne := func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.prepareReader(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if state.offset >= len(state.chars) {
			return IntValue(-1), true, nil
		}
		value := state.chars[state.offset]
		state.offset++
		return IntValue(int32(value)), true, nil
	}
	vm.RegisterNative("java/io/Reader", "read", "()I", readOne)
	vm.RegisterNative("java/io/InputStreamReader", "read", "()I", readOne)
	readArray := func(full bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			offset, length := int32(0), int32(0)
			object, ok := vm.Object(reference)
			if !ok || object.Array == nil || object.Array.Descriptor != "[C" {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "invalid char array")
			}
			length = int32(len(object.Array.Elements))
			if full {
				offset, err = intArgument(args, 1)
				if err == nil {
					length, err = intArgument(args, 2)
				}
				if err != nil {
					return Value{}, false, err
				}
			}
			return vm.readerRead(receiver, reference, offset, length)
		}
	}
	vm.RegisterNative("java/io/Reader", "read", "([C)I", readArray(false))
	vm.RegisterNative("java/io/Reader", "read", "([CII)I", readArray(true))
	vm.RegisterNative("java/io/InputStreamReader", "read", "([CII)I", readArray(true))
	vm.RegisterNative("java/io/Reader", "markSupported", "()Z", func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
		return boolValue(false), true, nil
	})
	vm.RegisterNative("java/io/Reader", "mark", "(I)V", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, vm.newThrowable("java/io/IOException", "mark not supported")
	})
	vm.RegisterNative("java/io/Reader", "reset", "()V", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, vm.newThrowable("java/io/IOException", "reset not supported")
	})
	vm.RegisterNative("java/io/Reader", "ready", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.prepareReader(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return boolValue(state.offset < len(state.chars)), true, nil
	})
	vm.RegisterNative("java/io/Reader", "skip", "(J)J", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.prepareReader(receiver)
		if err != nil {
			return Value{}, false, err
		}
		count, err := args[0].Long()
		if err != nil {
			return Value{}, false, err
		}
		if count < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "negative skip")
		}
		skipped := min(count, int64(len(state.chars)-state.offset))
		state.offset += int(skipped)
		return LongValue(skipped), true, nil
	})
	vm.RegisterNative("java/io/Reader", "close", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.readerState(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state.closed = true
		if stream, streamErr := vm.inputStream(state.stream); streamErr == nil {
			stream.closed = true
		}
		return Value{}, false, nil
	})
}

func (vm *VM) readerState(reference uint32) (*inputStreamReaderState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid InputStreamReader")
	}
	state, ok := object.Native.(*inputStreamReaderState)
	if !ok {
		return nil, fmt.Errorf("invalid InputStreamReader state")
	}
	return state, nil
}

func (vm *VM) prepareReader(reference uint32) (*inputStreamReaderState, error) {
	state, err := vm.readerState(reference)
	if err != nil {
		return nil, err
	}
	if state.closed {
		return nil, vm.newThrowable("java/io/IOException", "reader closed")
	}
	if state.initialized {
		return state, nil
	}
	stream, err := vm.inputStream(state.stream)
	if err != nil {
		return nil, err
	}
	if stream.closed {
		return nil, vm.newThrowable("java/io/IOException", "stream closed")
	}
	text, err := vm.services.Text.Decode(stream.data[stream.offset:], state.encoding)
	if err != nil {
		return nil, vm.newThrowable("java/io/IOException", err.Error())
	}
	stream.offset = len(stream.data)
	state.chars = utf16.Encode([]rune(text))
	state.initialized = true
	return state, nil
}

func (vm *VM) readerRead(receiver, destinationReference uint32, offset, length int32) (Value, bool, error) {
	destination, ok := vm.Object(destinationReference)
	if !ok || destination.Array == nil || destination.Array.Descriptor != "[C" {
		return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "invalid char array")
	}
	if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(destination.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	if length == 0 {
		return IntValue(0), true, nil
	}
	state, err := vm.prepareReader(receiver)
	if err != nil {
		return Value{}, false, err
	}
	if state.offset >= len(state.chars) {
		return IntValue(-1), true, nil
	}
	count := min(int(length), len(state.chars)-state.offset)
	for index := range count {
		destination.Array.Elements[int(offset)+index] = IntValue(int32(state.chars[state.offset+index]))
	}
	state.offset += count
	return IntValue(int32(count)), true, nil
}

func (vm *VM) installCLDCWriterNatives() {
	for _, descriptor := range []string{"()V", "(Ljava/lang/Object;)V"} {
		vm.RegisterNative("java/io/Writer", "<init>", descriptor, nativeVoid)
	}
	constructor := func(named bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			stream, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if stream == 0 {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "null output stream")
			}
			if _, err = vm.outputStream(stream); err != nil {
				return Value{}, false, err
			}
			encoding := shared.EncodingEUCKR
			if named {
				name, nameErr := vm.stringArgument(args, 1)
				if nameErr != nil {
					return Value{}, false, nameErr
				}
				encoding, err = vm.textEncoding(name)
				if err != nil {
					return Value{}, false, err
				}
			}
			object, _ := vm.Object(receiver)
			object.Fields[writerStreamField] = ReferenceValue(stream)
			object.Fields[writerEncodingField] = ReferenceValue(vm.NewString(string(encoding)))
			object.Fields[writerClosedField] = IntValue(0)
			return Value{}, false, nil
		}
	}
	vm.RegisterNative("java/io/OutputStreamWriter", "<init>", "(Ljava/io/OutputStream;)V", constructor(false))
	vm.RegisterNative("java/io/OutputStreamWriter", "<init>", "(Ljava/io/OutputStream;Ljava/lang/String;)V", constructor(true))
	writeChars := func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		offset, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		length, err := intArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
		text, err := vm.charArrayArgument(args, 0, offset, length)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.writerWrite(receiver, text)
	}
	writeString := func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		text, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		offset, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		length, err := intArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
		units := utf16.Encode([]rune(text))
		if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(units)) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		return Value{}, false, vm.writerWrite(receiver, string(utf16.Decode(units[offset:offset+length])))
	}
	writeOne := func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.writerWrite(receiver, string(rune(uint16(value))))
	}
	for _, class := range []string{"java/io/Writer", "java/io/OutputStreamWriter"} {
		vm.RegisterNative(class, "write", "([CII)V", writeChars)
		vm.RegisterNative(class, "write", "(Ljava/lang/String;II)V", writeString)
		vm.RegisterNative(class, "write", "(I)V", writeOne)
	}
	vm.RegisterNative("java/io/OutputStreamWriter", "write", "(C)V", writeOne)
	vm.RegisterNative("java/io/Writer", "write", "([C)V", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		reference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		object, ok := vm.Object(reference)
		if !ok || object.Array == nil || object.Array.Descriptor != "[C" {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "invalid char array")
		}
		return writeChars(ctx, vm, receiver, []Value{args[0], IntValue(0), IntValue(int32(len(object.Array.Elements)))})
	})
	vm.RegisterNative("java/io/Writer", "write", "(Ljava/lang/String;)V", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		text, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return writeString(ctx, vm, receiver, []Value{args[0], IntValue(0), IntValue(int32(len(utf16.Encode([]rune(text)))))})
	})
	flush := func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		_, _, _, err := vm.writerConfig(receiver)
		return Value{}, false, err
	}
	closeWriter := func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, _, _, err := vm.writerConfig(receiver)
		if err != nil {
			return Value{}, false, err
		}
		object.Fields[writerClosedField] = IntValue(1)
		return Value{}, false, nil
	}
	for _, class := range []string{"java/io/Writer", "java/io/OutputStreamWriter"} {
		vm.RegisterNative(class, "flush", "()V", flush)
		vm.RegisterNative(class, "close", "()V", closeWriter)
	}
}

func (vm *VM) writerConfig(reference uint32) (*Object, *outputStreamState, shared.TextEncoding, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, nil, "", fmt.Errorf("invalid Writer")
	}
	closed, _ := object.Fields[writerClosedField].Int()
	if closed != 0 {
		return nil, nil, "", vm.newThrowable("java/io/IOException", "writer closed")
	}
	streamReference, err := object.Fields[writerStreamField].Reference()
	if err != nil {
		return nil, nil, "", fmt.Errorf("invalid Writer stream")
	}
	stream, err := vm.outputStream(streamReference)
	if err != nil {
		return nil, nil, "", err
	}
	encodingReference, err := object.Fields[writerEncodingField].Reference()
	if err != nil {
		return nil, nil, "", fmt.Errorf("invalid Writer encoding")
	}
	name, err := vm.String(encodingReference)
	if err != nil {
		return nil, nil, "", err
	}
	return object, stream, shared.TextEncoding(name), nil
}

func (vm *VM) writerWrite(reference uint32, text string) error {
	_, stream, encoding, err := vm.writerConfig(reference)
	if err != nil {
		return err
	}
	data, err := vm.services.Text.Encode(text, encoding)
	if err != nil {
		return vm.newThrowable("java/io/IOException", err.Error())
	}
	return vm.writeOutputStream(stream, data)
}

func (vm *VM) installCLDCPrintStreamNatives() {
	vm.RegisterNative("java/io/PrintStream", "<init>", "(Ljava/io/OutputStream;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		stream, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if stream == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "null output stream")
		}
		if _, err = vm.outputStream(stream); err != nil {
			return Value{}, false, err
		}
		object, _ := vm.Object(receiver)
		object.Fields[printStreamField] = ReferenceValue(stream)
		object.Fields[printErrorField] = IntValue(0)
		return Value{}, false, nil
	})
	print := func(descriptor string, newline bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			text, err := vm.printArgument(args, descriptor)
			if err != nil {
				return Value{}, false, err
			}
			if newline {
				text += "\n"
			}
			data, err := vm.services.Text.Encode(text, shared.EncodingEUCKR)
			if err != nil {
				if object, ok := vm.Object(receiver); ok {
					object.Fields[printErrorField] = IntValue(1)
				}
				return Value{}, false, nil
			}
			vm.printWrite(receiver, data)
			return Value{}, false, nil
		}
	}
	for _, descriptor := range []string{"(Z)V", "(C)V", "([C)V", "(D)V", "(F)V", "(I)V", "(J)V", "(Ljava/lang/Object;)V", "(Ljava/lang/String;)V"} {
		vm.RegisterNative("java/io/PrintStream", "print", descriptor, print(descriptor, false))
		vm.RegisterNative("java/io/PrintStream", "println", descriptor, print(descriptor, true))
	}
	vm.RegisterNative("java/io/PrintStream", "println", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		vm.printWrite(receiver, []byte("\n"))
		return Value{}, false, nil
	})
	vm.RegisterNative("java/io/PrintStream", "write", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		vm.printWrite(receiver, []byte{byte(value)})
		return Value{}, false, nil
	})
	vm.RegisterNative("java/io/PrintStream", "write", "([BII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		data, err := vm.byteSliceArgument(args)
		if err != nil {
			return Value{}, false, err
		}
		vm.printWrite(receiver, data)
		return Value{}, false, nil
	})
	vm.RegisterNative("java/io/PrintStream", "checkError", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid PrintStream")
		}
		value, _ := object.Fields[printErrorField].Int()
		return boolValue(value != 0), true, nil
	})
	vm.RegisterNative("java/io/PrintStream", "setError", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if object, ok := vm.Object(receiver); ok {
			object.Fields[printErrorField] = IntValue(1)
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("java/io/PrintStream", "flush", "()V", nativeVoid)
	vm.RegisterNative("java/io/PrintStream", "close", "()V", nativeVoid)
}

func (vm *VM) printArgument(args []Value, descriptor string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	switch args[0].Kind {
	case ValueInt:
		value, _ := args[0].Int()
		if descriptor == "(Z)V" {
			return strconv.FormatBool(value != 0), nil
		}
		if descriptor == "(C)V" {
			return string(rune(uint16(value))), nil
		}
		return strconv.FormatInt(int64(value), 10), nil
	case ValueLong:
		value, _ := args[0].Long()
		return strconv.FormatInt(value, 10), nil
	case ValueFloat:
		value, _ := args[0].Float()
		return javaFloatText(float64(value), 32), nil
	case ValueDouble:
		value, _ := args[0].Double()
		return javaFloatText(value, 64), nil
	case ValueReference:
		reference, _ := args[0].Reference()
		if reference == 0 {
			return "null", nil
		}
		if text, err := vm.String(reference); err == nil {
			return text, nil
		}
		if object, ok := vm.Object(reference); ok && object.Array != nil && object.Array.Descriptor == "[C" {
			units := make([]uint16, len(object.Array.Elements))
			for index, element := range object.Array.Elements {
				value, err := element.Int()
				if err != nil {
					return "", err
				}
				units[index] = uint16(value)
			}
			return string(utf16.Decode(units)), nil
		}
		return vm.objectString(reference), nil
	default:
		return "", fmt.Errorf("unsupported PrintStream value %s", args[0].Kind)
	}
}

func (vm *VM) printWrite(receiver uint32, data []byte) {
	object, ok := vm.Object(receiver)
	if !ok {
		return
	}
	streamReference, err := object.Fields[printStreamField].Reference()
	var stream *outputStreamState
	if err == nil && streamReference != 0 {
		stream, err = vm.outputStream(streamReference)
	} else {
		stream, err = vm.outputStream(receiver)
	}
	if err != nil || vm.writeOutputStream(stream, data) != nil {
		object.Fields[printErrorField] = IntValue(1)
	}
}
