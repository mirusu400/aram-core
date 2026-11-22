package skvm

import (
	"context"
	"errors"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

// SKT handsets shipped WIPI Java titles as well as SK-VM ones: a Jlet built
// against org.kwis.msp that also reaches for com.skt.m and com.xce. The
// natives here complete the org.kwis.msp surface those hybrids use for
// storage, text, and sound.

const (
	kwisFileClass       = "org/kwis/msp/io/File"
	kwisFileSystemClass = "org/kwis/msp/io/FileSystem"
	kwisFontClass       = "org/kwis/msp/lcdui/Font"
	kwisGraphicsClass   = "org/kwis/msp/lcdui/Graphics"
	kwisClipClass       = "org/kwis/msp/media/Clip"
	kwisPlayerClass     = "org/kwis/msp/media/Player"

	kwisClipListenerField = "$kwis.listener"
	kwisClipTypeField     = "$kwis.type"

	// org.kwis.msp.io.File open modes.
	kwisFileReadOnly   = int32(1)
	kwisFileWrite      = int32(2)
	kwisFileWriteTrunc = int32(3)
	kwisFileReadWrite  = int32(4)

	// PlayListener.END_OF_DATA.
	kwisPlayEndOfData = int32(1)

	kwisPlayUpdateDescriptor = "(Lorg/kwis/msp/media/Clip;II)V"
)

func (vm *VM) kwisIOFailure(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*thrown); ok {
		return err
	}
	return vm.newThrowable("java/io/IOException", err.Error())
}

// newKWISFile opens a File the way WIPI defines its modes: READ_ONLY needs an
// existing file, WRITE appends, WRITE_TRUNC starts empty, and READ_WRITE
// starts at the first byte. The access flag selects a storage area on a
// handset; every area maps to the title's private storage here.
func (vm *VM) newKWISFile(args []Value) (*xFileState, error) {
	name, err := vm.fileNameArgument(args, 0)
	if err != nil {
		return nil, err
	}
	mode, err := intArgument(args, 1)
	if err != nil {
		return nil, err
	}
	data, err := vm.readXFile(name)
	missing := errors.Is(err, shared.ErrNotFound)
	if err != nil && !missing {
		return nil, vm.kwisIOFailure(err)
	}
	state := &xFileState{name: name, data: data}
	switch mode {
	case kwisFileReadOnly:
		if missing {
			return nil, vm.newThrowable("java/io/IOException", "file not found")
		}
		return state, nil
	case kwisFileWrite:
		state.offset = len(state.data)
	case kwisFileWriteTrunc:
		state.data = nil
	case kwisFileReadWrite:
	default:
		return nil, vm.newThrowable("java/io/IOException", "invalid file mode")
	}
	if missing || mode == kwisFileWriteTrunc {
		if err := vm.persistXFile(state); err != nil {
			return nil, vm.kwisIOFailure(err)
		}
	}
	return state, nil
}

func (vm *VM) writeKWISFile(state *xFileState, data []byte) error {
	end := state.offset + len(data)
	if end > len(state.data) {
		state.data = append(state.data, make([]byte, end-len(state.data))...)
	}
	copy(state.data[state.offset:end], data)
	state.offset = end
	return vm.kwisIOFailure(vm.persistXFile(state))
}

func (vm *VM) installKWISFileNatives() {
	open := func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.newKWISFile(args)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.setNative(receiver, state)
	}
	vm.RegisterNative(kwisFileClass, "<init>", "(Ljava/lang/String;I)V", open)
	vm.RegisterNative(kwisFileClass, "<init>", "(Ljava/lang/String;II)V", open)
	vm.RegisterNative(kwisFileClass, "write", "([B)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.ByteArray(reference)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.writeKWISFile(state, data); err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(data))), true, nil
		},
	)
	vm.RegisterNative(kwisFileClass, "write", "([BII)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.writeKWISFile(state, data); err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(data))), true, nil
		},
	)
	vm.RegisterNative(kwisFileClass, "write", "(I)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			value, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.writeKWISFile(state, []byte{byte(value)}); err != nil {
				return Value{}, false, err
			}
			return IntValue(1), true, nil
		},
	)
	vm.RegisterNative(kwisFileClass, "seek", "(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			position, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if position < 0 {
				return Value{}, false, vm.newThrowable("java/io/IOException", "invalid seek position")
			}
			state.offset = int(position)
			return Value{}, false, nil
		},
	)
	input := func(data bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			offset := min(state.offset, len(state.data))
			stream := vm.NewObject(
				"java/io/InputStream",
				&inputStreamState{data: append([]byte(nil), state.data[offset:]...)},
			)
			if !data {
				return ReferenceValue(stream), true, nil
			}
			return ReferenceValue(vm.NewObject(
				"java/io/DataInputStream",
				&dataInputState{stream: stream},
			)), true, nil
		}
	}
	output := func(data bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			stream := vm.NewObject(
				"java/io/OutputStream",
				&outputStreamState{file: state},
			)
			if !data {
				return ReferenceValue(stream), true, nil
			}
			return ReferenceValue(vm.NewObject(
				"java/io/DataOutputStream",
				&dataOutputState{stream: stream},
			)), true, nil
		}
	}
	vm.RegisterNative(kwisFileClass, "openInputStream", "()Ljava/io/InputStream;", input(false))
	vm.RegisterNative(kwisFileClass, "openDataInputStream", "()Ljava/io/DataInputStream;", input(true))
	vm.RegisterNative(kwisFileClass, "openOutputStream", "()Ljava/io/OutputStream;", output(false))
	vm.RegisterNative(kwisFileClass, "openDataOutputStream", "()Ljava/io/DataOutputStream;", output(true))
}

func (vm *VM) installKWISFileSystemNatives() {
	// Every path-taking FileSystem method has a twin that names the storage
	// area with a trailing access flag.
	both := func(name, parameters, result string, native NativeFunc) {
		vm.RegisterNative(kwisFileSystemClass, name, "("+parameters+")"+result, native)
		vm.RegisterNative(kwisFileSystemClass, name, "("+parameters+"I)"+result, native)
	}
	const path = "Ljava/lang/String;"
	// A predicate answers false for a path the storage cannot name.
	predicate := func(test func(vm *VM, name string) bool) NativeFunc {
		return func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				if failure, ok := err.(*thrown); ok && failure.class == "java/io/IOException" {
					return boolValue(false), true, nil
				}
				return Value{}, false, err
			}
			return boolValue(test(vm, name)), true, nil
		}
	}
	isFile := func(vm *VM, name string) bool {
		_, err := vm.statXFile(name)
		return err == nil
	}
	isDirectory := func(vm *VM, name string) bool {
		return vm.services.Storage.DirectoryExists(shared.NamespacePrivate, name)
	}
	both("isFile", path, "Z", predicate(isFile))
	both("isDirectory", path, "Z", predicate(isDirectory))
	both("exists", path, "Z", predicate(func(vm *VM, name string) bool {
		return isFile(vm, name) || isDirectory(vm, name)
	}))
	mutate := func(apply func(*shared.Storage, shared.Namespace, string) error) NativeFunc {
		return func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.kwisIOFailure(
				apply(vm.services.Storage, shared.NamespacePrivate, name),
			)
		}
	}
	both("remove", path, "V", mutate((*shared.Storage).Delete))
	both("mkdir", path, "V", mutate((*shared.Storage).MakeDirectory))
	both("rmdir", path, "V", mutate((*shared.Storage).RemoveDirectory))
	both("rename", path+path, "V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			oldName, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			newName, err := vm.fileNameArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			storage := vm.services.Storage
			if storage.DirectoryExists(shared.NamespacePrivate, oldName) {
				return Value{}, false, vm.kwisIOFailure(
					storage.RenameDirectory(shared.NamespacePrivate, oldName, newName),
				)
			}
			return Value{}, false, vm.kwisIOFailure(
				storage.Rename(shared.NamespacePrivate, oldName, newName),
			)
		},
	)
	both("list", path, "Ljava/util/Vector;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			directory, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			directory = strings.ReplaceAll(directory, `\`, "/")
			if directory == "" || directory == "." {
				directory = "/"
			}
			names, err := vm.services.Storage.List(shared.NamespacePrivate, directory)
			if err != nil {
				return Value{}, false, vm.kwisIOFailure(err)
			}
			values := make([]uint32, len(names))
			for index, name := range names {
				values[index] = vm.NewString(name)
			}
			return ReferenceValue(vm.NewObject(
				"java/util/Vector",
				&vectorState{values: values},
			)), true, nil
		},
	)
	space := func(vm *VM) (free, total uint64) {
		total = vm.services.Config.Limits.Storage.MaxStorageBytes
		used := vm.services.Storage.Used(shared.NamespacePrivate)
		if used < total {
			free = total - used
		}
		return free, total
	}
	clamp := func(value uint64) int32 { return int32(min(value, uint64(1<<31-1))) }
	vm.RegisterNative(kwisFileSystemClass, "available", "()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			free, _ := space(vm)
			return IntValue(clamp(free)), true, nil
		},
	)
	vm.RegisterNative(kwisFileSystemClass, "getFreeSpace", "()J",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			free, _ := space(vm)
			return LongValue(int64(free)), true, nil
		},
	)
	vm.RegisterNative(kwisFileSystemClass, "totalSpace", "()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			_, total := space(vm)
			return IntValue(clamp(total)), true, nil
		},
	)
	vm.RegisterNative(kwisFileSystemClass, "getMaxFilenameLength", "()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(clamp(uint64(
				vm.services.Config.Limits.Storage.MaxPathBytes,
			))), true, nil
		},
	)
	vm.RegisterNative(kwisFileSystemClass, "toCString", "(Ljava/lang/String;)[B",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.services.Text.Encode(value, shared.EncodingEUCKR)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewByteArray(append(data, 0))), true, nil
		},
	)
}

// aliasKWISNatives gives an org.kwis.msp class every method its MIDP twin
// already implements under the same name and descriptor. Methods whose
// descriptor names a MIDP type are a different signature in WIPI and are
// registered by hand.
func (vm *VM) aliasKWISNatives(midpClass, kwisClass string) {
	pending := make(map[nativeKey]NativeFunc)
	for key, native := range vm.natives {
		if key.class != midpClass || key.name == "<init>" ||
			strings.Contains(key.descriptor, "javax/") {
			continue
		}
		target := nativeKey{class: kwisClass, name: key.name, descriptor: key.descriptor}
		if _, registered := vm.natives[target]; !registered {
			pending[target] = native
		}
	}
	for key, native := range pending {
		vm.natives[key] = native
	}
}

func (vm *VM) installKWISLCDUIExtras() {
	vm.aliasKWISNatives("javax/microedition/lcdui/Font", kwisFontClass)
	vm.aliasKWISNatives("javax/microedition/lcdui/Graphics", kwisGraphicsClass)
	// WIPI reset restores the whole context. Titles bracket every clipped
	// sprite with setClip ... reset, so a reset that kept the clip left each
	// later draw confined to the last sprite's rectangle.
	vm.RegisterNative(kwisGraphicsClass, "reset", "()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.resetGraphics(state)
		},
	)
	vm.RegisterNative(kwisGraphicsClass, "getFont", "()Lorg/kwis/msp/lcdui/Font;",
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
				kwisFontClass,
				&fontState{font: font},
			)), true, nil
		},
	)
	if native, ok := vm.natives[nativeKey{
		class:      "javax/microedition/midlet/MIDlet",
		name:       "getAppProperty",
		descriptor: "(Ljava/lang/String;)Ljava/lang/String;",
	}]; ok {
		vm.RegisterNative(
			"org/kwis/msp/lcdui/Jlet",
			"getAppProperty",
			"(Ljava/lang/String;)Ljava/lang/String;",
			native,
		)
	}
}

func (vm *VM) kwisClip(reference uint32) (*Object, *audioClipState, error) {
	if reference == 0 {
		return nil, nil, vm.newThrowable("java/lang/NullPointerException", "")
	}
	object, ok := vm.Object(reference)
	if !ok {
		return nil, nil, errors.New("invalid Clip reference")
	}
	state, ok := object.Native.(*audioClipState)
	if !ok {
		return nil, nil, errors.New("object is not a Clip")
	}
	return object, state, nil
}

// initKWISClip backs a Clip with a media clip. A handset that has run out of
// sound channels still constructs the Clip; it just stays silent, so a full
// clip table or a payload the mixer refuses leaves the object without a
// service instead of faulting the title.
func (vm *VM) initKWISClip(receiver uint32, mediaType string, data []byte) error {
	object, ok := vm.Object(receiver)
	if !ok {
		return errors.New("invalid Clip reference")
	}
	state := &audioClipState{}
	object.Native = state
	object.Fields[kwisClipTypeField] = ReferenceValue(vm.NewString(mediaType))
	var clip shared.ServiceID
	err := vm.withCollectRetryPreserving([]uint32{receiver}, func() error {
		var createErr error
		// The Clip type names a synthesizer generation, not a container;
		// the media service identifies the payload from its bytes.
		clip, createErr = vm.services.Media.CreateClip(vm.serviceOwner, "", 0)
		return createErr
	})
	if err != nil {
		if errors.Is(err, shared.ErrLimitExceeded) {
			return nil
		}
		return err
	}
	if len(data) != 0 {
		if _, err := vm.services.Media.Append(vm.serviceOwner, clip, data); err != nil {
			_ = vm.services.Media.DestroyClip(vm.serviceOwner, clip, vm.services.Events)
			return nil
		}
	}
	state.clip = clip
	return nil
}

func (vm *VM) installKWISMediaNatives() {
	vm.RegisterHostClass(kwisClipClass, "java/lang/Object")
	vm.RegisterHostClass(kwisPlayerClass, "java/lang/Object")
	vm.RegisterHostClass("org/kwis/msp/media/PlayListener", "java/lang/Object")
	vm.RegisterHostClass("org/kwis/msp/media/Vibrator", "java/lang/Object")

	construct := func(payload func(vm *VM, args []Value) ([]byte, error)) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			mediaType, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			data, err := payload(vm, args)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.initKWISClip(receiver, mediaType, data)
		}
	}
	empty := func(*VM, []Value) ([]byte, error) { return nil, nil }
	vm.RegisterNative(kwisClipClass, "<init>", "(Ljava/lang/String;)V", construct(empty))
	vm.RegisterNative(kwisClipClass, "<init>", "(Ljava/lang/String;I)V", construct(empty))
	vm.RegisterNative(kwisClipClass, "<init>", "(Ljava/lang/String;[B)V",
		construct(func(vm *VM, args []Value) ([]byte, error) {
			reference, err := referenceArgument(args, 1)
			if err != nil || reference == 0 {
				return nil, err
			}
			return vm.ByteArray(reference)
		}),
	)
	vm.RegisterNative(kwisClipClass, "<init>", "(Ljava/lang/String;Ljava/lang/String;)V",
		construct(func(vm *VM, args []Value) ([]byte, error) {
			name, err := vm.stringArgument(args, 1)
			if err != nil {
				return nil, err
			}
			name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
			data, _ := vm.resource(name)
			return data, nil
		}),
	)
	vm.RegisterNative(kwisClipClass, "setListener", "(Lorg/kwis/msp/media/PlayListener;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			object, _, err := vm.kwisClip(receiver)
			if err != nil {
				return Value{}, false, err
			}
			listener, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			object.Fields[kwisClipListenerField] = ReferenceValue(listener)
			return Value{}, false, nil
		},
	)
	// Titles were compiled against both a void and a boolean setVolume.
	for _, result := range []string{"V", "Z"} {
		returns := result == "Z"
		vm.RegisterNative(kwisClipClass, "setVolume", "(I)"+result,
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				_, state, err := vm.kwisClip(receiver)
				if err != nil {
					return Value{}, false, err
				}
				volume, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				applied := false
				if state.clip != 0 {
					info, infoErr := vm.services.Media.Info(vm.serviceOwner, state.clip)
					if infoErr != nil {
						return Value{}, false, infoErr
					}
					if err := vm.services.Media.SetClipGain(
						vm.serviceOwner,
						state.clip,
						uint8(max(0, min(100, volume))),
						info.Muted,
						info.Pan,
					); err != nil {
						return Value{}, false, err
					}
					applied = true
				}
				if returns {
					return boolValue(applied), true, nil
				}
				return Value{}, false, nil
			},
		)
	}
	vm.RegisterNative(kwisClipClass, "getVolume", "()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			_, state, err := vm.kwisClip(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state.clip == 0 {
				return IntValue(0), true, nil
			}
			info, err := vm.services.Media.Info(vm.serviceOwner, state.clip)
			return IntValue(int32(info.Volume)), true, err
		},
	)
	vm.RegisterNative(kwisClipClass, "getType", "()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, _, err := vm.kwisClip(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return object.Fields[kwisClipTypeField], true, nil
		},
	)
	vm.RegisterNative(kwisClipClass, "putData", "([BII)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			_, state, err := vm.kwisClip(receiver)
			if err != nil {
				return Value{}, false, err
			}
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			if state.clip == 0 {
				return IntValue(0), true, nil
			}
			if _, err := vm.services.Media.Append(vm.serviceOwner, state.clip, data); err != nil {
				return IntValue(0), true, nil
			}
			return IntValue(int32(len(data))), true, nil
		},
	)
	vm.RegisterNative(kwisClipClass, "clearData", "()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			_, state, err := vm.kwisClip(receiver)
			if err != nil || state.clip == 0 {
				return Value{}, false, err
			}
			if info, infoErr := vm.services.Media.Info(vm.serviceOwner, state.clip); infoErr == nil &&
				info.State != shared.ClipStopped {
				if err := vm.services.Media.Stop(vm.serviceOwner, state.clip); err != nil {
					return Value{}, false, err
				}
			}
			return Value{}, false, vm.services.Media.ReplaceSource(vm.serviceOwner, state.clip, nil)
		},
	)

	vm.RegisterNative(kwisPlayerClass, "play", "(Lorg/kwis/msp/media/Clip;Z)Z",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			_, state, err := vm.kwisClip(reference)
			if err != nil {
				return Value{}, false, err
			}
			repeat, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			if state.clip == 0 {
				return boolValue(false), true, nil
			}
			info, err := vm.services.Media.Info(vm.serviceOwner, state.clip)
			if err != nil {
				return Value{}, false, err
			}
			// Playing a clip that is already sounding restarts it.
			if info.State != shared.ClipStopped {
				if err := vm.services.Media.Stop(vm.serviceOwner, state.clip); err != nil {
					return Value{}, false, err
				}
			}
			plays := int32(1)
			if repeat != 0 {
				plays = -1
			}
			if err := vm.services.Media.Play(vm.serviceOwner, state.clip, plays); err != nil {
				if errors.Is(err, shared.ErrMediaUnsupported) ||
					errors.Is(err, shared.ErrInvalidState) {
					return boolValue(false), true, nil
				}
				return Value{}, false, err
			}
			return boolValue(true), true, nil
		},
	)
	for _, method := range []struct {
		name  string
		from  func(shared.ClipPlaybackState) bool
		apply func(*shared.Media, shared.OwnerID, shared.ServiceID) error
	}{
		{
			name:  "stop",
			from:  func(state shared.ClipPlaybackState) bool { return state != shared.ClipStopped },
			apply: (*shared.Media).Stop,
		},
		{
			name:  "pause",
			from:  func(state shared.ClipPlaybackState) bool { return state == shared.ClipPlaying },
			apply: (*shared.Media).Pause,
		},
		{
			name:  "resume",
			from:  func(state shared.ClipPlaybackState) bool { return state == shared.ClipPaused },
			apply: (*shared.Media).Resume,
		},
	} {
		spec := method
		vm.RegisterNative(kwisPlayerClass, spec.name, "(Lorg/kwis/msp/media/Clip;)Z",
			func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
				reference, err := referenceArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				_, state, err := vm.kwisClip(reference)
				if err != nil {
					return Value{}, false, err
				}
				if state.clip == 0 {
					return boolValue(false), true, nil
				}
				info, err := vm.services.Media.Info(vm.serviceOwner, state.clip)
				if err != nil {
					return Value{}, false, err
				}
				if !spec.from(info.State) {
					return boolValue(false), true, nil
				}
				if err := spec.apply(vm.services.Media, vm.serviceOwner, state.clip); err != nil {
					return Value{}, false, err
				}
				return boolValue(true), true, nil
			},
		)
	}
}

// notifyKWISClipEnd tells a Clip's PlayListener that the clip played to its
// end. It reports whether the object carries a WIPI listener slot at all.
func (vm *VM) notifyKWISClipEnd(ctx context.Context, reference uint32, object *Object) (bool, error) {
	value, ok := object.Fields[kwisClipListenerField]
	if !ok {
		return false, nil
	}
	listener, _ := value.Reference()
	if listener == 0 {
		return true, nil
	}
	_, _, err := vm.InvokeVirtual(
		ctx,
		listener,
		"playUpdate",
		kwisPlayUpdateDescriptor,
		ReferenceValue(reference),
		IntValue(kwisPlayEndOfData),
		IntValue(0),
	)
	if err != nil && !errors.Is(err, ErrMethodNotFound) {
		return true, err
	}
	return true, nil
}
