package skvm

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

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
			if _, err = vm.services.Device.Request(
				vm.serviceOwner,
				shared.RequestBrowser,
				target,
				nil,
				vm.services.Clock.Monotonic(),
			); err != nil {
				// The method returns void, so the handset has no way to tell
				// the title its browser did not open, and a URL the title
				// built badly is not a reason to stop running it.
				if errors.Is(err, shared.ErrInvalidArgument) ||
					errors.Is(err, shared.ErrLimitExceeded) {
					return Value{}, false, nil
				}
				return Value{}, false, err
			}
			return Value{}, false, nil
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
				// A MIDP FileInputStream constructor reports an open
				// failure (a missing options/save file on first run
				// included) as a catchable java.io.IOException, not a
				// fatal VM error, so the title can fall back to defaults.
				return Value{}, false, vm.newThrowable(
					"java/io/IOException",
					err.Error(),
				)
			}
			return Value{}, false, vm.setNative(
				receiver,
				&inputStreamState{data: data},
			)
		},
	)
	// ProgressBar is the SKT loading-progress widget. A title fills it during a
	// load loop and then reads the level back to decide when the bar is full, so
	// the current value has to survive setValue and reappear from getValue; an
	// integer-state receiver holds it. setMaxValue stays a no-op because the
	// ceiling the title picks is never read back.
	vm.RegisterNative(
		"com/skt/m/ProgressBar",
		"<init>",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.setNative(receiver, &integerState{})
		},
	)
	vm.RegisterNative("com/skt/m/ProgressBar", "setMaxValue", "(I)V", nativeVoid)
	vm.RegisterNative("com/skt/m/ProgressBar", "setValue", "(I)V", nativeSetIntegerState)
	vm.RegisterNative("com/skt/m/ProgressBar", "getValue", "()I", nativeIntegerState)
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
			// A title that has not managed to load its clip bytes still opens
			// the AudioClip with a null buffer; treat that as an empty (silent)
			// clip instead of faulting so playback simply produces no sound.
			var data []byte
			if reference, refErr := referenceArgument(args, 0); refErr != nil {
				return Value{}, false, refErr
			} else if reference != 0 {
				data, err = vm.byteSliceArgument(args)
				if err != nil {
					return Value{}, false, err
				}
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
