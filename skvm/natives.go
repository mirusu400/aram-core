package skvm

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

type stringBufferState struct {
	value string
}

type inputStreamState struct {
	data       []byte
	offset     int
	closed     bool
	connection uint32
}

type randomState struct {
	stream string
}

type threadState struct {
	target       uint32
	active       bool
	wakeAt       time.Duration
	continuation []*frame
}

type threadYield struct {
	delay time.Duration
}

func (e *threadYield) Error() string {
	return fmt.Sprintf("SKVM thread yielded for %s", e.delay)
}

type recordStoreState struct {
	name string
	id   shared.ServiceID
}

type xFileState struct {
	name   string
	data   []byte
	offset int
}

type xTextFieldState struct {
	text  string
	focus bool
}

type outputStreamState struct {
	data       []byte
	file       *xFileState
	name       string
	connection uint32
}

type audioClipState struct {
	clip shared.ServiceID
}

type inputStreamReaderState struct {
	stream uint32
}

type imageState struct {
	width   int
	height  int
	asset   shared.ServiceID
	surface shared.ServiceID
}

type fontState struct {
	font shared.ServiceID
}

type graphicsState struct {
	width   int
	height  int
	surface shared.ServiceID
	font    shared.ServiceID
	color   uint32
}

func (vm *VM) installCoreNatives() {
	vm.RegisterNative("java/lang/Object", "<init>", "()V", nativeVoid)
	vm.RegisterNative(
		"java/lang/Object",
		"getClass",
		"()Ljava/lang/Class;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid receiver %d", receiver)
			}
			return ReferenceValue(vm.NewObject("java/lang/Class", object.Class)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Class",
		"getResourceAsStream",
		"(Ljava/lang/String;)Ljava/io/InputStream;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			classObject, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Class receiver")
			}
			className, _ := classObject.Native.(string)
			resourceName := strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
			if !strings.HasPrefix(name, "/") && strings.Contains(className, "/") {
				resourceName = path.Join(path.Dir(className), resourceName)
			}
			data, ok := vm.resource(resourceName)
			if !ok {
				return ReferenceValue(0), true, nil
			}
			stream := &inputStreamState{data: append([]byte(nil), data...)}
			return ReferenceValue(vm.NewObject("java/io/InputStream", stream)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Class",
		"forName",
		"(Ljava/lang/String;)Ljava/lang/Class;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			name = strings.ReplaceAll(name, ".", "/")
			return ReferenceValue(vm.NewObject("java/lang/Class", name)), true, nil
		},
	)

	vm.RegisterNative("java/lang/String", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, "")
	})
	vm.RegisterNative("java/lang/String", "<init>", "([B)V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		args []Value,
	) (Value, bool, error) {
		reference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		data, err := vm.ByteArray(reference)
		if err != nil {
			return Value{}, false, err
		}
		value, err := vm.services.Text.Decode(data, shared.EncodingEUCKR)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, value)
	})
	vm.RegisterNative("java/lang/String", "<init>", "([BII)V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		args []Value,
	) (Value, bool, error) {
		reference, err := referenceArgument(args, 0)
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
		data, err := vm.ByteArray(reference)
		if err != nil {
			return Value{}, false, err
		}
		if offset < 0 || length < 0 ||
			int64(offset)+int64(length) > int64(len(data)) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		value, err := vm.services.Text.Decode(
			data[offset:offset+length],
			shared.EncodingEUCKR,
		)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, value)
	})
	vm.RegisterNative(
		"java/lang/String",
		"length",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(utf16.Encode([]rune(value))))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"charAt",
		"(I)C",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			index, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			units := utf16.Encode([]rune(value))
			if index < 0 || int(index) >= len(units) {
				return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
			}
			return IntValue(int32(units[index])), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"substring",
		"(II)Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.String(receiver)
			if err != nil {
				return Value{}, false, err
			}
			start, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			end, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			units := utf16.Encode([]rune(value))
			if start < 0 || end < start || int(end) > len(units) {
				return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
			}
			return ReferenceValue(vm.NewString(string(utf16.Decode(units[start:end])))), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/String",
		"toString",
		"()Ljava/lang/String;",
		func(_ context.Context, _ *VM, receiver uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(receiver), true, nil
		},
	)

	vm.RegisterNative("java/lang/StringBuffer", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &stringBufferState{})
	})
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"append",
		"(I)Ljava/lang/StringBuffer;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state.value += fmt.Sprintf("%d", value)
			return ReferenceValue(receiver), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"append",
		"(Ljava/lang/String;)Ljava/lang/StringBuffer;",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value := "null"
			if reference != 0 {
				value, err = vm.String(reference)
				if err != nil {
					return Value{}, false, err
				}
			}
			state.value += value
			return ReferenceValue(receiver), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/StringBuffer",
		"toString",
		"()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.stringBuffer(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewString(state.value)), true, nil
		},
	)

	vm.RegisterNative(
		"java/lang/Math",
		"abs",
		"(I)I",
		func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if value < 0 {
				value = -value
			}
			return IntValue(value), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Math",
		"abs",
		"(J)J",
		func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
			if value < 0 {
				value = -value
			}
			return LongValue(value), true, nil
		},
	)
	for _, method := range []struct {
		name       string
		descriptor string
		native     NativeFunc
	}{
		{
			"min",
			"(II)I",
			func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
				left, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				right, err := intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
				return IntValue(min(left, right)), true, nil
			},
		},
		{
			"max",
			"(II)I",
			func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
				left, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				right, err := intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
				return IntValue(max(left, right)), true, nil
			},
		},
		{
			"max",
			"(JJ)J",
			func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
				left, err := args[0].Long()
				if err != nil {
					return Value{}, false, err
				}
				right, err := args[1].Long()
				if err != nil {
					return Value{}, false, err
				}
				return LongValue(max(left, right)), true, nil
			},
		},
	} {
		vm.RegisterNative("java/lang/Math", method.name, method.descriptor, method.native)
	}
	vm.RegisterNative(
		"java/lang/System",
		"currentTimeMillis",
		"()J",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return LongValue(vm.services.Clock.WallMillis()), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/System",
		"gc",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.collectGarbage()
		},
	)
	vm.RegisterNative("java/lang/System", "exit", "(I)V", nativeVoid)
	vm.RegisterNative(
		"java/lang/System",
		"getProperty",
		"(Ljava/lang/String;)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value, ok := vm.properties[name]
			if !ok {
				value, ok = vm.services.Device.Property(name)
			}
			if !ok {
				config := vm.services.Device.Config()
				switch name {
				case "MIN":
					value = config.PhoneNumber
					if value == "" {
						value = "MIN0000000000"
					}
					ok = true
				case "com.xce.wipi.version":
					value, ok = config.WIPIVersion, true
				case "microedition.platform":
					value = config.Model
					if value == "" {
						value = "SKVM"
					}
					ok = true
				case "microedition.configuration":
					value, ok = "M_Configuration-1.0", true
				case "microedition.profiles":
					value, ok = "M_Profile-1.0 SKTP-1.0", true
				case "microedition.locale":
					value, ok = config.Locale, true
				case "microedition.encoding":
					value, ok = "EUC-KR", true
				default:
					value, ok = "0", true
				}
			}
			if !ok {
				return ReferenceValue(0), true, nil
			}
			return ReferenceValue(vm.NewString(value)), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/System",
		"arraycopy",
		"(Ljava/lang/Object;ILjava/lang/Object;II)V",
		nativeArrayCopy,
	)

	vm.installInputStreamNatives()
	vm.installOutputStreamNatives()
	vm.installDataIONatives()
	vm.installConnectionNatives()
	vm.installThreadNatives()
	vm.installRandomNatives()
	vm.installMIDletNatives()
	vm.installDisplayNatives()
	vm.installGraphicsNatives()
	vm.installRecordStoreNatives()
	vm.installSKTNatives()
	vm.installHostStaticFields()
	vm.installExtendedCoreNatives()
	vm.installCompatibilityNatives()
}

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

func (vm *VM) installThreadNatives() {
	vm.RegisterNative("java/lang/Thread", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &threadState{target: receiver})
	})
	vm.RegisterNative(
		"java/lang/Thread",
		"<init>",
		"(Ljava/lang/Runnable;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			target, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &threadState{target: target})
		},
	)
	vm.RegisterNative(
		"java/lang/Thread",
		"start",
		"()V",
		func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.thread(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state.active {
				// Several SK-VM MIDlets call startApp again after a lifecycle
				// resume and unconditionally start their long-lived worker.
				// The legacy runtime treated an already-active start as a
				// resume no-op.
				return Value{}, false, nil
			}
			state.active = true
			state.wakeAt = vm.services.Clock.Monotonic()
			return Value{}, false, vm.runThread(ctx, receiver, state)
		},
	)
	vm.RegisterNative(
		"java/lang/Thread",
		"yield",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			if vm.runningThread != 0 {
				return Value{}, false, &threadYield{delay: time.Nanosecond}
			}
			return Value{}, false, nil
		},
	)
	vm.RegisterNative("java/lang/Thread", "setPriority", "(I)V", nativeVoid)
	vm.RegisterNative(
		"java/lang/Thread",
		"isAlive",
		"()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.thread(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return boolValue(state.active), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Thread",
		"sleep",
		"(J)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			if len(args) != 1 {
				return Value{}, false, fmt.Errorf("Thread.sleep argument mismatch")
			}
			duration, err := args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
			if duration < 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			if duration > int64((^uint64(0)>>1)/uint64(time.Millisecond)) {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			delay := time.Duration(duration) * time.Millisecond
			if vm.runningThread != 0 {
				return Value{}, false, &threadYield{delay: delay}
			}
			if err := vm.services.Advance(
				vm.serviceOwner,
				delay,
			); err != nil {
				return Value{}, false, err
			}
			return Value{}, false, nil
		},
	)
}

func (vm *VM) thread(reference uint32) (*threadState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Thread reference")
	}
	state, ok := object.Native.(*threadState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a Thread", reference)
	}
	return state, nil
}

func (vm *VM) runThread(
	ctx context.Context,
	reference uint32,
	state *threadState,
) error {
	if !state.active {
		return nil
	}
	previous := vm.runningThread
	previousBase := vm.threadFrameBase
	vm.runningThread = reference
	vm.threadFrameBase = len(vm.frames)
	var err error
	if len(state.continuation) != 0 {
		continuation := state.continuation
		state.continuation = nil
		budget := vm.remainingBudget()
		_, _, err = vm.resumeFrames(ctx, continuation, 0, &budget)
	} else {
		_, _, err = vm.InvokeVirtual(ctx, state.target, "run", "()V")
	}
	vm.runningThread = previous
	vm.threadFrameBase = previousBase
	var yielded *threadYield
	if errors.As(err, &yielded) {
		now := vm.services.Clock.Monotonic()
		if yielded.delay < 0 || yielded.delay > time.Duration(^uint64(0)>>1)-now {
			state.active = false
			return fmt.Errorf("invalid thread yield duration %s", yielded.delay)
		}
		state.wakeAt = now + yielded.delay
		return nil
	}
	state.active = false
	state.continuation = nil
	if errors.Is(err, ErrMethodNotFound) {
		return nil
	}
	return err
}

func (vm *VM) runReadyThreads(ctx context.Context) error {
	now := vm.services.Clock.Monotonic()
	references := make([]uint32, 0)
	for reference, object := range vm.heap {
		state, ok := object.Native.(*threadState)
		if ok && state.active && state.wakeAt <= now &&
			reference != vm.runningThread {
			references = append(references, reference)
		}
	}
	sort.Slice(references, func(left, right int) bool {
		return references[left] < references[right]
	})
	for _, reference := range references {
		state, err := vm.thread(reference)
		if err != nil {
			return err
		}
		if state.active && state.wakeAt <= vm.services.Clock.Monotonic() {
			if err := vm.runThread(ctx, reference, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func (vm *VM) installRandomNatives() {
	seed := func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value := int64(0)
		if len(args) != 0 {
			var err error
			value, err = args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
		}
		stream := fmt.Sprintf("skvm.java.random.%08x", receiver)
		if err := vm.services.Random.SetJavaSeed(stream, value); err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, &randomState{stream: stream})
	}
	vm.RegisterNative("java/util/Random", "<init>", "()V", seed)
	vm.RegisterNative("java/util/Random", "<init>", "(J)V", seed)
	vm.RegisterNative("java/util/Random", "setSeed", "(J)V", seed)
	vm.RegisterNative(
		"java/util/Random",
		"nextInt",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Random receiver")
			}
			state, ok := object.Native.(*randomState)
			if !ok {
				state = &randomState{
					stream: fmt.Sprintf("skvm.java.random.%08x", receiver),
				}
				if err := vm.services.Random.SetJavaSeed(state.stream, 0); err != nil {
					return Value{}, false, err
				}
				object.Native = state
			}
			value, err := vm.services.Random.JavaInt(state.stream)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(value), true, nil
		},
	)
}

func (vm *VM) installMIDletNatives() {
	vm.RegisterNative("javax/microedition/midlet/MIDlet", "<init>", "()V", nativeVoid)
	vm.RegisterNative(
		"javax/microedition/midlet/MIDlet",
		"notifyDestroyed",
		"()V",
		nativeVoid,
	)
	vm.RegisterNative(
		"javax/microedition/midlet/MIDlet",
		"getAppProperty",
		"(Ljava/lang/String;)Ljava/lang/String;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value, ok := vm.properties[name]
			if !ok {
				return ReferenceValue(0), true, nil
			}
			return ReferenceValue(vm.NewString(value)), true, nil
		},
	)
}

func (vm *VM) installDisplayNatives() {
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "<init>", "()V", nativeVoid)
	vm.RegisterNative(
		"javax/microedition/lcdui/Canvas",
		"getWidth",
		"()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenWidth)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"getNumRecords",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.recordStore(receiver)
			if err != nil {
				return Value{}, false, err
			}
			count, err := vm.services.Storage.RecordCount(vm.serviceOwner, state.id)
			if err != nil {
				return Value{}, false, vm.rmsThrowable(err)
			}
			return IntValue(int32(count)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/rms/RecordStore",
		"getRecord",
		"(I)[B",
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
			return ReferenceValue(vm.NewByteArray(record)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Canvas",
		"getHeight",
		"()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenHeight)), true, nil
		},
	)
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "repaint", "(IIII)V", nativeVoid)
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "repaint", "()V", nativeVoid)
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "serviceRepaints", "()V", nativeVoid)
	vm.RegisterNative(
		"javax/microedition/lcdui/Display",
		"getDisplay",
		"(Ljavax/microedition/midlet/MIDlet;)Ljavax/microedition/lcdui/Display;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			if vm.displayReference == 0 {
				vm.displayReference = vm.NewObject("javax/microedition/lcdui/Display", nil)
			}
			return ReferenceValue(vm.displayReference), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Display",
		"callSerially",
		"(Ljava/lang/Runnable;)V",
		nativeVoid,
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Display",
		"setCurrent",
		"(Ljavax/microedition/lcdui/Displayable;)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			vm.currentDisplay = reference
			return Value{}, false, nil
		},
	)
}

func (vm *VM) installGraphicsNatives() {
	vm.RegisterNative(
		"javax/microedition/lcdui/Image",
		"createImage",
		"(II)Ljavax/microedition/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			width, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			height, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			if width <= 0 || height <= 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			state, err := vm.newImageState(int(width), int(height))
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(
				vm.NewObject("javax/microedition/lcdui/Image", state),
			), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Image",
		"createImage",
		"([BII)Ljavax/microedition/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			reference, err := vm.newImage(data)
			return ReferenceValue(reference), true, err
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Image",
		"createImage",
		"(Ljava/lang/String;)Ljavax/microedition/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
			data, _ := vm.resource(name)
			reference, err := vm.newImage(data)
			return ReferenceValue(reference), true, err
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Image",
		"getWidth",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.image(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(state.width)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Image",
		"getHeight",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.image(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(state.height)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Image",
		"getGraphics",
		"()Ljavax/microedition/lcdui/Graphics;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.image(receiver)
			if err != nil {
				return Value{}, false, err
			}
			graphics := &graphicsState{
				width:   state.width,
				height:  state.height,
				surface: state.surface,
				font:    vm.defaultFont,
				color:   0xff000000,
			}
			return ReferenceValue(
				vm.NewObject("javax/microedition/lcdui/Graphics", graphics),
			), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Font",
		"getDefaultFont",
		"()Ljavax/microedition/lcdui/Font;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(vm.NewObject(
				"javax/microedition/lcdui/Font",
				&fontState{font: vm.defaultFont},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Font",
		"charWidth",
		"(C)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			font, err := vm.font(receiver)
			if err != nil {
				return Value{}, false, err
			}
			character, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			glyph, err := vm.services.Text.Glyph(
				vm.serviceOwner,
				font.font,
				rune(character),
			)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(glyph.Advance), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Font",
		"getHeight",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			font, err := vm.font(receiver)
			if err != nil {
				return Value{}, false, err
			}
			metrics, err := vm.services.Text.Metrics(vm.serviceOwner, font.font)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(metrics.Height), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Font",
		"getFont",
		"(III)Ljavax/microedition/lcdui/Font;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			return vm.newFontObject("javax/microedition/lcdui/Font", args)
		},
	)
	vm.RegisterNative("javax/microedition/lcdui/Graphics", "reset", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		state, err := vm.graphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state.color = 0xff000000
		return Value{}, false, nil
	})
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"setFont",
		"(Ljavax/microedition/lcdui/Font;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			graphics, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			font, err := vm.font(reference)
			if err != nil {
				return Value{}, false, err
			}
			graphics.font = font.font
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"getFont",
		"()Ljavax/microedition/lcdui/Font;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			graphics, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			font := graphics.font
			if font == 0 {
				font = vm.defaultFont
			}
			return ReferenceValue(vm.NewObject(
				"javax/microedition/lcdui/Font",
				&fontState{font: font},
			)), true, nil
		},
	)
	for _, method := range []struct {
		name       string
		descriptor string
	}{
		{"setClip", "(IIII)V"},
		{"clipRect", "(IIII)V"},
		{"translate", "(II)V"},
	} {
		vm.RegisterNative(
			"javax/microedition/lcdui/Graphics",
			method.name,
			method.descriptor,
			nativeVoid,
		)
	}
	for _, method := range []struct {
		name  string
		value func(*graphicsState) int32
	}{
		{"getClipX", func(*graphicsState) int32 { return 0 }},
		{"getClipY", func(*graphicsState) int32 { return 0 }},
		{"getClipWidth", func(state *graphicsState) int32 { return int32(state.width) }},
		{"getClipHeight", func(state *graphicsState) int32 { return int32(state.height) }},
		{"getTranslateX", func(*graphicsState) int32 { return 0 }},
		{"getTranslateY", func(*graphicsState) int32 { return 0 }},
		{"getColor", func(state *graphicsState) int32 { return int32(state.color & 0xffffff) }},
	} {
		spec := method
		vm.RegisterNative(
			"javax/microedition/lcdui/Graphics",
			spec.name,
			"()I",
			func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
				state, err := vm.graphics(receiver)
				if err != nil {
					return Value{}, false, err
				}
				return IntValue(spec.value(state)), true, nil
			},
		)
	}
	vm.RegisterNative(
		"javax/microedition/lcdui/Font",
		"stringWidth",
		"(Ljava/lang/String;)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			font, err := vm.font(receiver)
			if err != nil {
				return Value{}, false, err
			}
			width, err := vm.services.Text.Measure(vm.serviceOwner, font.font, value)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(width), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"setColor",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			color, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state.color = 0xff000000 | uint32(color)&0x00ffffff
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"setColor",
		"(III)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			red, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			green, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			blue, err := intArgument(args, 2)
			if err != nil {
				return Value{}, false, err
			}
			state.color = 0xff000000 |
				uint32(red&0xff)<<16 |
				uint32(green&0xff)<<8 |
				uint32(blue&0xff)
			return Value{}, false, nil
		},
	)
	for _, method := range []struct {
		name       string
		descriptor string
		native     NativeFunc
	}{
		{"fillRect", "(IIII)V", nativeFillRect},
		{"drawRect", "(IIII)V", nativeDrawRect},
		{"drawLine", "(IIII)V", nativeDrawLine},
		{"drawImage", "(Ljavax/microedition/lcdui/Image;III)V", nativeDrawImage},
		{"drawString", "(Ljava/lang/String;III)V", nativeDrawString},
	} {
		vm.RegisterNative(
			"javax/microedition/lcdui/Graphics",
			method.name,
			method.descriptor,
			method.native,
		)
	}
	vm.RegisterNative(
		"com/skt/m/Graphics2D",
		"getGraphics2D",
		"(Ljavax/microedition/lcdui/Graphics;)Lcom/skt/m/Graphics2D;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			graphics, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state, err := vm.graphics(graphics)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewObject("com/skt/m/Graphics2D", state)), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/Graphics2D",
		"createMaskableImage",
		"(II)Ljavax/microedition/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			width, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			height, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			if width <= 0 || height <= 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			state, err := vm.newImageState(int(width), int(height))
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(
				vm.NewObject("javax/microedition/lcdui/Image", state),
			), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/Graphics2D",
		"captureLCD",
		"(IIII)Ljavax/microedition/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			x, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			y, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			width, err := intArgument(args, 2)
			if err != nil {
				return Value{}, false, err
			}
			height, err := intArgument(args, 3)
			if err != nil {
				return Value{}, false, err
			}
			if width < 0 || height < 0 {
				reference, imageErr := vm.newImage(nil)
				return ReferenceValue(reference), true, imageErr
			}
			state, err := vm.newImageState(int(width), int(height))
			if err != nil {
				return Value{}, false, err
			}
			sourceLeft := max(0, int(x))
			sourceTop := max(0, int(y))
			sourceRight := min(vm.ScreenWidth, int(x)+int(width))
			sourceBottom := min(vm.ScreenHeight, int(y)+int(height))
			if sourceRight > sourceLeft && sourceBottom > sourceTop {
				if err := vm.services.Graphics.Blit(
					vm.serviceOwner,
					state.surface,
					vm.screenSurface,
					int32(sourceLeft-int(x)),
					int32(sourceTop-int(y)),
					shared.Rectangle{
						X:      int32(sourceLeft),
						Y:      int32(sourceTop),
						Width:  int32(sourceRight - sourceLeft),
						Height: int32(sourceBottom - sourceTop),
					},
				); err != nil {
					return Value{}, false, err
				}
			}
			return ReferenceValue(
				vm.NewObject("javax/microedition/lcdui/Image", state),
			), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/Graphics2D",
		"drawImage",
		"(IILjavax/microedition/lcdui/Image;IIIII)V",
		nativeGraphics2DDrawImage,
	)
}

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

func (vm *VM) installSKTNatives() {
	for _, method := range []struct {
		class, name, descriptor string
	}{
		{"com/skt/m/Device", "setColorMode", "(I)V"},
		{"com/skt/m/Device", "setKeyToneEnabled", "(Z)V"},
		{"com/skt/m/Device", "enableRestoreLCD", "(Z)V"},
	} {
		vm.RegisterNative(method.class, method.name, method.descriptor, nativeVoid)
	}
	vm.RegisterNative(
		"com/skt/m/Device",
		"setBacklightEnabled",
		"(Z)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			enabled, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			err = vm.services.Device.SetBacklight(
				enabled != 0,
				0,
				vm.services.Clock.Monotonic(),
			)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"com/skt/m/Device",
		"invokeWapBrowser",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			target, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			_, err = vm.services.Device.Request(
				vm.serviceOwner,
				shared.RequestBrowser,
				target,
				nil,
				vm.services.Clock.Monotonic(),
			)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"com/skt/m/BackLight",
		"on",
		"(I)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			millis, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if millis < 0 {
				millis = 0
			}
			err = vm.services.Device.SetBacklight(
				true,
				time.Duration(millis)*time.Millisecond,
				vm.services.Clock.Monotonic(),
			)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"com/skt/m/Vibration",
		"start",
		"(II)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			level, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			millis, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			level = max(0, min(100, level))
			millis = max(0, millis)
			err = vm.services.Device.Vibrate(
				uint8(level),
				time.Duration(millis)*time.Millisecond,
				vm.services.Clock.Monotonic(),
			)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"com/skt/m/Vibration",
		"getLevelNum",
		"()I",
		func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(1), true, nil
		},
	)
	for _, method := range []string{
		"isBacklightEnabled",
		"isKeyToneEnabled",
	} {
		methodName := method
		vm.RegisterNative(
			"com/skt/m/Device",
			methodName,
			"()Z",
			func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
				if methodName == "isBacklightEnabled" {
					enabled, _ := vm.services.Device.Backlight()
					return boolValue(enabled), true, nil
				}
				return IntValue(1), true, nil
			},
		)
	}
	vm.RegisterNative(
		"com/skt/m/Vibration",
		"isSupported",
		"()Z",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return boolValue(vm.services.Device.Capability("vibration")), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/Vibration",
		"stop",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.services.Device.Vibrate(
				0,
				0,
				vm.services.Clock.Monotonic(),
			)
		},
	)
	vm.RegisterNative("com/xce/lcdui/XDisplay", "refresh", "(IIII)V", nativeVoid)
	vm.RegisterNative(
		"com/xce/lcdui/XDisplay",
		"copyLCD",
		"(Ljavax/microedition/lcdui/Graphics;Ljavax/microedition/lcdui/Image;IIII)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			graphicsReference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			imageReference, err := referenceArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			graphics, err := vm.graphics(graphicsReference)
			if err != nil {
				return Value{}, false, err
			}
			image, err := vm.image(imageReference)
			if err != nil {
				return Value{}, false, err
			}
			values := [4]int{}
			for index := range values {
				value, intErr := intArgument(args, index+2)
				if intErr != nil {
					return Value{}, false, intErr
				}
				values[index] = int(value)
			}
			x, y, width, height := values[0], values[1], values[2], values[3]
			if width <= 0 || height <= 0 {
				return Value{}, false, nil
			}
			sourceLeft := max(0, x)
			sourceTop := max(0, y)
			sourceRight := min(graphics.width, x+width, x+image.width)
			sourceBottom := min(graphics.height, y+height, y+image.height)
			if sourceRight <= sourceLeft || sourceBottom <= sourceTop {
				return Value{}, false, nil
			}
			err = vm.services.Graphics.Blit(
				vm.serviceOwner,
				image.surface,
				graphics.surface,
				int32(sourceLeft-x),
				int32(sourceTop-y),
				shared.Rectangle{
					X:      int32(sourceLeft),
					Y:      int32(sourceTop),
					Width:  int32(sourceRight - sourceLeft),
					Height: int32(sourceBottom - sourceTop),
				},
			)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"exists",
		"(Ljava/lang/String;)Z",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			_, err = vm.services.Storage.Stat(shared.NamespacePrivate, name)
			return boolValue(err == nil), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"fsavail",
		"()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			limit := vm.services.Config.Limits.Storage.MaxStorageBytes
			used := vm.services.Storage.Used(shared.NamespacePrivate)
			available := uint64(0)
			if used < limit {
				available = limit - used
			}
			return IntValue(int32(min(available, uint64(1<<31-1)))), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"filesize",
		"(Ljava/lang/String;)I",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			info, err := vm.services.Storage.Stat(shared.NamespacePrivate, name)
			if err != nil {
				return IntValue(-1), true, nil
			}
			return IntValue(int32(min(info.Size, uint64(1<<31-1)))), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"unlink",
		"(Ljava/lang/String;)I",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.services.Storage.Delete(
				shared.NamespacePrivate,
				name,
			); err != nil {
				return IntValue(-1), true, nil
			}
			return IntValue(0), true, nil
		},
	)
	vm.RegisterNative("com/xce/io/XFile", "flush", "()V", nativeVoid)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"seek",
		"(II)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			offset, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			origin, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			position := int(offset)
			if origin == 1 {
				position += state.offset
			} else if origin == 2 {
				position += len(state.data)
			}
			if position < 0 {
				position = 0
			}
			state.offset = position
			return IntValue(int32(position)), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/io/FileInputStream",
		"<init>",
		"(Lcom/xce/io/XFile;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			fileReference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			file, err := vm.xFile(fileReference)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(
				receiver,
				&inputStreamState{data: append([]byte(nil), file.data...)},
			)
		},
	)
	vm.RegisterNative(
		"com/xce/io/FileOutputStream",
		"<init>",
		"(Lcom/xce/io/XFile;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			fileReference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			file, err := vm.xFile(fileReference)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &outputStreamState{file: file})
		},
	)
	vm.RegisterNative(
		"com/xce/io/FileOutputStream",
		"<init>",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.services.Storage.WriteFile(
				shared.NamespacePrivate,
				name,
				nil,
			); err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(
				receiver,
				&outputStreamState{name: name},
			)
		},
	)
	vm.RegisterNative(
		"com/xce/io/FileInputStream",
		"<init>",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.services.Storage.ReadFile(
				shared.NamespacePrivate,
				name,
			)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(
				receiver,
				&inputStreamState{data: data},
			)
		},
	)
	vm.RegisterNative(
		"com/skt/m/ProgressBar",
		"<init>",
		"(Ljava/lang/String;)V",
		nativeVoid,
	)
	vm.RegisterNative("com/skt/m/ProgressBar", "setMaxValue", "(I)V", nativeVoid)
	vm.RegisterNative("com/skt/m/ProgressBar", "setValue", "(I)V", nativeVoid)
	vm.RegisterNative("com/xce/io/ByteToCharEUC_KR", "<init>", "()V", nativeVoid)
	vm.RegisterNative(
		"com/xce/io/ByteToCharConverter",
		"convert",
		"([BII[CII)I",
		nativeByteToCharConvert,
	)
	vm.RegisterNative(
		"com/xce/io/ByteToCharConverter",
		"flush",
		"([CII)I",
		func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(0), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/XTextField",
		"<init>",
		"(Ljava/lang/String;IILjavax/microedition/lcdui/Canvas;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &xTextFieldState{text: value})
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/XTextField",
		"getText",
		"()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.xTextField(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewString(state.text)), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/XTextField",
		"setText",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xTextField(receiver)
			if err != nil {
				return Value{}, false, err
			}
			state.text, err = vm.stringArgument(args, 0)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/XTextField",
		"hasFocus",
		"()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.xTextField(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return boolValue(state.focus), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/XTextField",
		"setFocus",
		"(Z)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xTextField(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := intArgument(args, 0)
			state.focus = value != 0
			return Value{}, false, err
		},
	)
	for _, method := range []struct {
		name       string
		descriptor string
	}{
		{"setBounds", "(IIII)V"},
		{"paint", "(Ljavax/microedition/lcdui/Graphics;)V"},
		{"keyPressed", "(I)V"},
		{"keyReleased", "(I)V"},
		{"keyRepeated", "(I)V"},
	} {
		vm.RegisterNative(
			"com/xce/lcdui/XTextField",
			method.name,
			method.descriptor,
			nativeVoid,
		)
	}
	vm.RegisterNative(
		"com/xce/io/XFile",
		"<init>",
		"(Ljava/lang/String;I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.newXFile(args)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, state)
		},
	)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"write",
		"([BII)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			end := state.offset + len(data)
			if end > len(state.data) {
				state.data = append(state.data, make([]byte, end-len(state.data))...)
			}
			copy(state.data[state.offset:end], data)
			state.offset = end
			if err := vm.persistXFile(state); err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(data))), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"read",
		"([BII)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			arrayReference, err := referenceArgument(args, 0)
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
			object, ok := vm.Object(arrayReference)
			if !ok || object.Array == nil || object.Array.Descriptor != "[B" {
				return Value{}, false, fmt.Errorf("XFile.read destination is not byte[]")
			}
			if offset < 0 || length < 0 ||
				int64(offset)+int64(length) > int64(len(object.Array.Elements)) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			if state.offset >= len(state.data) {
				return IntValue(-1), true, nil
			}
			count := min(int(length), len(state.data)-state.offset)
			for index := range count {
				object.Array.Elements[int(offset)+index] =
					IntValue(int32(int8(state.data[state.offset+index])))
			}
			state.offset += count
			return IntValue(int32(count)), true, nil
		},
	)
	vm.RegisterNative("com/xce/io/XFile", "close", "()V", nativeVoid)
	vm.RegisterNative(
		"com/xce/io/XFile",
		"available",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(max(0, len(state.data)-state.offset))), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/AudioSystem",
		"getAudioClip",
		"(Ljava/lang/String;)Lcom/skt/m/AudioClip;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, _ := vm.resource(name)
			mediaType := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
			clip, err := vm.services.Media.CreateClip(
				vm.serviceOwner,
				mediaType,
				0,
			)
			if err != nil {
				return Value{}, false, err
			}
			if len(data) != 0 {
				if _, err := vm.services.Media.Append(
					vm.serviceOwner,
					clip,
					data,
				); err != nil {
					_ = vm.services.Media.DestroyClip(
						vm.serviceOwner,
						clip,
						vm.services.Events,
					)
					return Value{}, false, err
				}
			}
			return ReferenceValue(vm.NewObject(
				"com/skt/m/AudioClip",
				&audioClipState{clip: clip},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/AudioSystem",
		"getMaxVolume",
		"(Ljava/lang/String;)I",
		func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(10), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/AudioSystem",
		"getVolume",
		"(Ljava/lang/String;)I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.services.Media.Snapshot().GlobalVolume / 10)), true, nil
		},
	)
	vm.RegisterNative(
		"com/skt/m/AudioSystem",
		"setVolume",
		"(Ljava/lang/String;I)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			volume, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			volume = max(0, min(10, volume))
			err = vm.services.Media.SetGlobalGain(uint8(volume*10), false)
			return Value{}, false, err
		},
	)
	vm.RegisterNative(
		"com/skt/m/AudioClip",
		"open",
		"([BII)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.audioClip(receiver)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			created := false
			if state.clip == 0 {
				state.clip, err = vm.services.Media.CreateClip(
					vm.serviceOwner,
					"",
					0,
				)
				if err != nil {
					return Value{}, false, err
				}
				created = true
			} else if err := vm.services.Media.Clear(
				vm.serviceOwner,
				state.clip,
			); err != nil {
				return Value{}, false, err
			}
			_, err = vm.services.Media.Append(vm.serviceOwner, state.clip, data)
			if err != nil && created {
				_ = vm.services.Media.DestroyClip(
					vm.serviceOwner,
					state.clip,
					vm.services.Events,
				)
				state.clip = 0
			}
			return Value{}, false, err
		},
	)
	for _, method := range []struct {
		name  string
		plays int32
	}{
		{name: "play", plays: 1},
		{name: "loop", plays: -1},
	} {
		spec := method
		vm.RegisterNative(
			"com/skt/m/AudioClip",
			spec.name,
			"()V",
			func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
				state, err := vm.audioClip(receiver)
				if err != nil {
					return Value{}, false, err
				}
				return Value{}, false, vm.services.Media.Play(
					vm.serviceOwner,
					state.clip,
					spec.plays,
				)
			},
		)
	}
	vm.RegisterNative(
		"com/skt/m/AudioClip",
		"stop",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.audioClip(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state.clip == 0 {
				return Value{}, false, nil
			}
			return Value{}, false, vm.services.Media.Stop(vm.serviceOwner, state.clip)
		},
	)
	vm.RegisterNative(
		"com/skt/m/AudioClip",
		"close",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.audioClip(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state.clip == 0 {
				return Value{}, false, nil
			}
			err = vm.services.Media.DestroyClip(
				vm.serviceOwner,
				state.clip,
				vm.services.Events,
			)
			if err == nil {
				state.clip = 0
			}
			return Value{}, false, err
		},
	)
}

func (vm *VM) installHostStaticFields() {
	vm.RegisterStaticField(
		applicationRootClass,
		applicationRootField,
		applicationRootDescriptor,
		ReferenceValue(0),
	)
	font := ReferenceValue(vm.NewObject("javax/microedition/lcdui/Font", nil))
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"DEFAULT_FONT",
		"Ljavax/microedition/lcdui/Font;",
		font,
	)
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"FONT_HEIGHT",
		"I",
		IntValue(8),
	)
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"MAX_CHARWIDTH",
		"I",
		IntValue(6),
	)
	vm.RegisterStaticField(
		"com/xce/lcdui/Toolkit",
		"graphics",
		"Ljavax/microedition/lcdui/Graphics;",
		ReferenceValue(vm.ScreenGraphics()),
	)
	vm.RegisterStaticField("com/xce/lcdui/XDisplay", "width", "I", IntValue(int32(vm.ScreenWidth)))
	vm.RegisterStaticField("com/xce/lcdui/XDisplay", "height", "I", IntValue(int32(vm.ScreenHeight)))
	vm.RegisterStaticField("com/xce/lcdui/XDisplay", "height2", "I", IntValue(int32(vm.ScreenHeight)))
	vm.RegisterStaticField(
		"com/xce/lcdui/XEventHandler",
		"eventHandler",
		"Lcom/xce/lcdui/XEventHandler;",
		ReferenceValue(vm.NewObject("com/xce/lcdui/XEventHandler", nil)),
	)
	vm.RegisterStaticField(
		"java/lang/System",
		"out",
		"Ljava/io/PrintStream;",
		ReferenceValue(vm.NewObject("java/io/PrintStream", nil)),
	)
	vm.RegisterStaticField(
		"javax/microedition/lcdui/TextField",
		"CONSTRAINT_MASK",
		"I",
		IntValue(0xffff),
	)
	vm.RegisterStaticField(
		"javax/microedition/lcdui/TextField",
		"PASSWORD",
		"I",
		IntValue(0x10000),
	)
	for name, value := range map[string]int32{
		"FACE_MONOSPACE": 32,
		"SIZE_SMALL":     8,
		"STYLE_PLAIN":    0,
	} {
		vm.RegisterStaticField("org/kwis/msp/lcdui/Font", name, "I", IntValue(value))
	}
}

func (vm *VM) newImage(data []byte) (uint32, error) {
	if len(data) != 0 {
		asset, decodeErr := vm.services.Assets.Decode(
			vm.serviceOwner,
			data,
			shared.DecodeOptions{},
		)
		if decodeErr == nil {
			info, infoErr := vm.services.Assets.Info(vm.serviceOwner, asset)
			if infoErr != nil {
				_ = vm.services.Assets.Release(vm.serviceOwner, asset)
				return 0, infoErr
			}
			state := &imageState{
				width:   int(info.Width),
				height:  int(info.Height),
				asset:   asset,
				surface: info.Frames[0].Surface,
			}
			return vm.NewObject("javax/microedition/lcdui/Image", state), nil
		}
	}
	state, err := vm.newImageState(1, 1)
	if err != nil {
		return 0, err
	}
	return vm.NewObject("javax/microedition/lcdui/Image", state), nil
}

func (vm *VM) newImageState(width, height int) (*imageState, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid image geometry %dx%d", width, height)
	}
	surface, err := vm.services.Graphics.CreateSurface(
		vm.serviceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(width),
			Height: int32(height),
			Format: shared.PixelRGBA8888,
		},
	)
	if err != nil {
		return nil, err
	}
	return &imageState{
		width:   width,
		height:  height,
		surface: surface,
	}, nil
}

func (vm *VM) image(reference uint32) (*imageState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Image reference")
	}
	state, ok := object.Native.(*imageState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an Image", reference)
	}
	return state, nil
}

func (vm *VM) font(reference uint32) (*fontState, error) {
	object, ok := vm.Object(reference)
	if !ok || (object.Class != "javax/microedition/lcdui/Font" &&
		object.Class != "org/kwis/msp/lcdui/Font") {
		return nil, fmt.Errorf("invalid Font reference")
	}
	if state, ok := object.Native.(*fontState); ok {
		return state, nil
	}
	if object.Native == nil {
		state := &fontState{font: vm.defaultFont}
		object.Native = state
		return state, nil
	}
	return nil, fmt.Errorf("object %d is not a Font", reference)
}

func (vm *VM) newFontObject(class string, args []Value) (Value, bool, error) {
	style, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	sizeValue, err := intArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	size := int32(12)
	switch {
	case sizeValue&16 != 0:
		size = 16
	case sizeValue&8 != 0:
		size = 8
	}
	var fontStyle shared.FontStyle
	if style&1 != 0 {
		fontStyle |= shared.FontBold
	}
	if style&2 != 0 {
		fontStyle |= shared.FontItalic
	}
	if style&4 != 0 {
		fontStyle |= shared.FontUnderlined
	}
	id, err := vm.services.Text.CreateFont(vm.serviceOwner, shared.FontDescriptor{
		Family: "aram-fallback",
		Size:   size,
		Style:  fontStyle,
	})
	if err != nil {
		return Value{}, false, err
	}
	return ReferenceValue(vm.NewObject(class, &fontState{font: id})), true, nil
}

func (vm *VM) graphics(reference uint32) (*graphicsState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Graphics reference")
	}
	state, ok := object.Native.(*graphicsState)
	if !ok {
		return nil, fmt.Errorf("object %d is not Graphics", reference)
	}
	return state, nil
}

func nativeVoid(
	context.Context,
	*VM,
	uint32,
	[]Value,
) (Value, bool, error) {
	return Value{}, false, nil
}

func nativeArrayCopy(
	_ context.Context,
	vm *VM,
	_ uint32,
	args []Value,
) (Value, bool, error) {
	if len(args) != 5 {
		return Value{}, false, fmt.Errorf("System.arraycopy argument mismatch")
	}
	sourceRef, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	sourceOffset, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	destinationRef, err := referenceArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	destinationOffset, err := intArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	length, err := intArgument(args, 4)
	if err != nil {
		return Value{}, false, err
	}
	source, sourceOK := vm.Object(sourceRef)
	destination, destinationOK := vm.Object(destinationRef)
	if !sourceOK || !destinationOK || source.Array == nil || destination.Array == nil {
		return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	if sourceOffset < 0 || destinationOffset < 0 || length < 0 ||
		int64(sourceOffset)+int64(length) > int64(len(source.Array.Elements)) ||
		int64(destinationOffset)+int64(length) > int64(len(destination.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
	}
	copy(
		destination.Array.Elements[destinationOffset:destinationOffset+length],
		source.Array.Elements[sourceOffset:sourceOffset+length],
	)
	return Value{}, false, nil
}

func nativeStreamRead(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.inputStream(receiver)
	if err != nil {
		return Value{}, false, err
	}
	if state.closed {
		return Value{}, false, vm.newThrowable("java/io/IOException", "stream closed")
	}
	arrayReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	object, ok := vm.Object(arrayReference)
	if !ok || object.Array == nil || object.Array.Descriptor != "[B" {
		return Value{}, false, fmt.Errorf("InputStream.read destination is not byte[]")
	}
	offset := int32(0)
	length := int32(len(object.Array.Elements))
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
	if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(object.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	if state.offset >= len(state.data) {
		return IntValue(-1), true, nil
	}
	count := min(int(length), len(state.data)-state.offset)
	for index := range count {
		object.Array.Elements[int(offset)+index] = IntValue(
			int32(int8(state.data[state.offset+index])),
		)
	}
	state.offset += count
	return IntValue(int32(count)), true, nil
}

func nativeByteToCharConvert(
	_ context.Context,
	vm *VM,
	_ uint32,
	args []Value,
) (Value, bool, error) {
	sourceReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	sourceOffset, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	sourceEnd, err := intArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	destinationReference, err := referenceArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	destinationOffset, err := intArgument(args, 4)
	if err != nil {
		return Value{}, false, err
	}
	destinationEnd, err := intArgument(args, 5)
	if err != nil {
		return Value{}, false, err
	}
	source, sourceOK := vm.Object(sourceReference)
	destination, destinationOK := vm.Object(destinationReference)
	if !sourceOK || !destinationOK || source.Array == nil || destination.Array == nil ||
		source.Array.Descriptor != "[B" || destination.Array.Descriptor != "[C" {
		return Value{}, false, fmt.Errorf("ByteToCharConverter array type mismatch")
	}
	if sourceOffset < 0 || sourceEnd < sourceOffset ||
		int(sourceEnd) > len(source.Array.Elements) ||
		destinationOffset < 0 || destinationEnd < destinationOffset ||
		int(destinationEnd) > len(destination.Array.Elements) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	count := min(
		int(sourceEnd-sourceOffset),
		int(destinationEnd-destinationOffset),
	)
	for index := range count {
		value, intErr := source.Array.Elements[int(sourceOffset)+index].Int()
		if intErr != nil {
			return Value{}, false, intErr
		}
		destination.Array.Elements[int(destinationOffset)+index] =
			IntValue(value & 0xff)
	}
	return IntValue(int32(count)), true, nil
}

func nativeStreamWrite(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.outputStream(receiver)
	if err != nil {
		return Value{}, false, err
	}
	var data []byte
	if len(args) == 1 && args[0].Kind == ValueInt {
		value, intErr := args[0].Int()
		if intErr != nil {
			return Value{}, false, intErr
		}
		data = []byte{byte(value)}
	} else {
		reference, refErr := referenceArgument(args, 0)
		if refErr != nil {
			return Value{}, false, refErr
		}
		data, err = vm.ByteArray(reference)
		if err != nil {
			return Value{}, false, err
		}
		if len(args) == 3 {
			offset, offsetErr := intArgument(args, 1)
			if offsetErr != nil {
				return Value{}, false, offsetErr
			}
			length, lengthErr := intArgument(args, 2)
			if lengthErr != nil {
				return Value{}, false, lengthErr
			}
			if offset < 0 || length < 0 ||
				int64(offset)+int64(length) > int64(len(data)) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			data = data[offset : offset+length]
		}
	}
	if err := vm.writeOutputStream(state, data); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func nativeReaderRead(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return Value{}, false, fmt.Errorf("invalid InputStreamReader")
	}
	reader, ok := object.Native.(*inputStreamReaderState)
	if !ok {
		return Value{}, false, fmt.Errorf("invalid InputStreamReader state")
	}
	stream, err := vm.inputStream(reader.stream)
	if err != nil {
		return Value{}, false, err
	}
	destinationReference, err := referenceArgument(args, 0)
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
	destination, ok := vm.Object(destinationReference)
	if !ok || destination.Array == nil || destination.Array.Descriptor != "[C" {
		return Value{}, false, fmt.Errorf("InputStreamReader destination is not char[]")
	}
	if offset < 0 || length < 0 ||
		int64(offset)+int64(length) > int64(len(destination.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	if stream.offset >= len(stream.data) {
		return IntValue(-1), true, nil
	}
	count := min(int(length), len(stream.data)-stream.offset)
	for index := range count {
		destination.Array.Elements[int(offset)+index] =
			IntValue(int32(stream.data[stream.offset+index]))
	}
	stream.offset += count
	return IntValue(int32(count)), true, nil
}

func (vm *VM) setNative(reference uint32, native any) error {
	object, ok := vm.Object(reference)
	if !ok {
		return fmt.Errorf("invalid object reference %d", reference)
	}
	object.Native = native
	return nil
}

func (vm *VM) stringBuffer(reference uint32) (*stringBufferState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid StringBuffer reference")
	}
	state, ok := object.Native.(*stringBufferState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a StringBuffer", reference)
	}
	return state, nil
}

func (vm *VM) inputStream(reference uint32) (*inputStreamState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid InputStream reference")
	}
	state, ok := object.Native.(*inputStreamState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an InputStream", reference)
	}
	if err := vm.refreshSocketInput(state); err != nil {
		return nil, err
	}
	return state, nil
}

func (vm *VM) recordStore(reference uint32) (*recordStoreState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid RecordStore reference")
	}
	state, ok := object.Native.(*recordStoreState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a RecordStore", reference)
	}
	return state, nil
}

func (vm *VM) rmsThrowable(err error) error {
	message := "record store operation failed"
	if err != nil {
		message = err.Error()
	}
	return vm.newThrowable(
		"javax/microedition/rms/RecordStoreException",
		message,
	)
}

func (vm *VM) xFile(reference uint32) (*xFileState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid XFile reference")
	}
	state, ok := object.Native.(*xFileState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an XFile", reference)
	}
	return state, nil
}

func (vm *VM) fileNameArgument(args []Value, index int) (string, error) {
	name, err := vm.stringArgument(args, index)
	if err != nil {
		return "", err
	}
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimPrefix(name, "file://")
	normalized, err := vm.services.Storage.NormalizePath(name)
	if err != nil {
		return "", vm.newThrowable("java/io/IOException", "invalid file path")
	}
	return normalized, nil
}

func (vm *VM) newXFile(args []Value) (*xFileState, error) {
	name, err := vm.fileNameArgument(args, 0)
	if err != nil {
		return nil, err
	}
	if _, err := intArgument(args, 1); err != nil {
		return nil, err
	}
	data, err := vm.services.Storage.ReadFile(shared.NamespacePrivate, name)
	if errors.Is(err, shared.ErrNotFound) {
		if err := vm.services.Storage.WriteFile(
			shared.NamespacePrivate,
			name,
			nil,
		); err != nil {
			return nil, err
		}
		data = nil
	} else if err != nil {
		return nil, err
	}
	return &xFileState{name: name, data: data}, nil
}

func (vm *VM) persistXFile(state *xFileState) error {
	if state == nil || state.name == "" {
		return nil
	}
	return vm.services.Storage.WriteFile(
		shared.NamespacePrivate,
		state.name,
		state.data,
	)
}

func (vm *VM) audioClip(reference uint32) (*audioClipState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid AudioClip reference")
	}
	state, ok := object.Native.(*audioClipState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an AudioClip", reference)
	}
	return state, nil
}

func (vm *VM) xTextField(reference uint32) (*xTextFieldState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid XTextField reference")
	}
	state, ok := object.Native.(*xTextFieldState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an XTextField", reference)
	}
	return state, nil
}

func (vm *VM) outputStream(reference uint32) (*outputStreamState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid OutputStream reference")
	}
	state, ok := object.Native.(*outputStreamState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an OutputStream", reference)
	}
	return state, nil
}

func (vm *VM) stringArgument(args []Value, index int) (string, error) {
	reference, err := referenceArgument(args, index)
	if err != nil {
		return "", err
	}
	if reference == 0 {
		return "", vm.newThrowable("java/lang/NullPointerException", "")
	}
	return vm.String(reference)
}

func referenceArgument(args []Value, index int) (uint32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("native argument %d is missing", index)
	}
	return args[index].Reference()
}

func intArgument(args []Value, index int) (int32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("native argument %d is missing", index)
	}
	return args[index].Int()
}

func (vm *VM) byteSliceArgument(args []Value) ([]byte, error) {
	reference, err := referenceArgument(args, 0)
	if err != nil {
		return nil, err
	}
	offset, err := intArgument(args, 1)
	if err != nil {
		return nil, err
	}
	length, err := intArgument(args, 2)
	if err != nil {
		return nil, err
	}
	data, err := vm.ByteArray(reference)
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(data)) {
		return nil, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	return append([]byte(nil), data[offset:offset+length]...), nil
}
