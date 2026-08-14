package skvm

import (
	"context"
	"fmt"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (vm *VM) installKWISNatives() {
	vm.RegisterNative(
		"org/kwis/msp/handset/BackLight",
		"alwaysOn",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return Value{}, false, vm.services.Device.SetBacklight(
				true,
				0,
				vm.services.Clock.Monotonic(),
			)
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/File",
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
		"org/kwis/msp/io/File",
		"write",
		"([B)I",
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
			state.data = append(state.data, data...)
			state.offset = len(state.data)
			if err := vm.persistXFile(state); err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(data))), true, nil
		},
	)
	vm.RegisterNative("org/kwis/msp/io/File", "close", "()V", nativeVoid)
	vm.RegisterNative(
		"org/kwis/msp/io/File",
		"sizeOf",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(state.data))), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/File",
		"read",
		"([B)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
			destination, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			object, ok := vm.Object(destination)
			if !ok || object.Array == nil {
				return Value{}, false, fmt.Errorf("File.read destination is not array")
			}
			if state.offset >= len(state.data) {
				return IntValue(-1), true, nil
			}
			count := min(len(object.Array.Elements), len(state.data)-state.offset)
			for index := range count {
				object.Array.Elements[index] =
					IntValue(int32(int8(state.data[state.offset+index])))
			}
			state.offset += count
			return IntValue(int32(count)), true, nil
		},
	)
	vm.RegisterNative("org/kwis/msp/lcdui/Jlet", "<init>", "()V", nativeVoid)
	vm.RegisterNative("org/kwis/msp/lcdui/Jlet", "notifyDestroyed", "()V", nativeVoid)
	vm.RegisterNative("org/kwis/msp/lcdui/Card", "<init>", "()V", nativeVoid)
	display := vm.NewObject("org/kwis/msp/lcdui/Display", nil)
	vm.RegisterStaticField(
		"org/kwis/msp/lcdui/Display",
		"__aramSingleton",
		"Lorg/kwis/msp/lcdui/Display;",
		ReferenceValue(display),
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Display",
		"getDefaultDisplay",
		"()Lorg/kwis/msp/lcdui/Display;",
		func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(display), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Display",
		"pushCard",
		"(Lorg/kwis/msp/lcdui/Card;)V",
		nativeVoid,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Card",
		"repaint",
		"(IIII)V",
		nativeVoid,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Card",
		"getWidth",
		"()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenWidth)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Card",
		"getHeight",
		"()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenHeight)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Font",
		"getDefaultFont",
		"()Lorg/kwis/msp/lcdui/Font;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return ReferenceValue(vm.NewObject(
				"org/kwis/msp/lcdui/Font",
				&fontState{font: vm.defaultFont},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/io/FileSystem",
		"isFile",
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
		"org/kwis/msp/io/FileSystem",
		"remove",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.fileNameArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.services.Storage.Delete(
				shared.NamespacePrivate,
				name,
			)
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Font",
		"getFont",
		"(III)Lorg/kwis/msp/lcdui/Font;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			return vm.newFontObject("org/kwis/msp/lcdui/Font", args)
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Font",
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
		"org/kwis/msp/lcdui/Image",
		"createImage",
		"(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
			data, _ := vm.resource(name)
			reference, err := vm.newImage(data)
			if err != nil {
				return Value{}, false, err
			}
			object, _ := vm.Object(reference)
			object.Class = "org/kwis/msp/lcdui/Image"
			return ReferenceValue(reference), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Image",
		"createImage",
		"(II)Lorg/kwis/msp/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			width, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			height, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			state, err := vm.newImageState(int(max(1, width)), int(max(1, height)))
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(
				vm.NewObject("org/kwis/msp/lcdui/Image", state),
			), true, nil
		},
	)
	for _, method := range []struct {
		name  string
		value func(*imageState) int32
	}{
		{"getWidth", func(state *imageState) int32 { return int32(state.width) }},
		{"getHeight", func(state *imageState) int32 { return int32(state.height) }},
	} {
		spec := method
		vm.RegisterNative(
			"org/kwis/msp/lcdui/Image",
			spec.name,
			"()I",
			func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
				state, err := vm.image(receiver)
				if err != nil {
					return Value{}, false, err
				}
				return IntValue(spec.value(state)), true, nil
			},
		)
	}
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Image",
		"getGraphics",
		"()Lorg/kwis/msp/lcdui/Graphics;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.image(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return ReferenceValue(vm.NewObject(
				"org/kwis/msp/lcdui/Graphics",
				&graphicsState{
					width: state.width, height: state.height,
					surface: state.surface, font: vm.defaultFont,
					color: 0xff000000,
				},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"setColor",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			color, err := intArgument(args, 0)
			state.color = 0xff000000 | uint32(color)&0xffffff
			return Value{}, false, err
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
	} {
		vm.RegisterNative(
			"org/kwis/msp/lcdui/Graphics",
			method.name,
			method.descriptor,
			method.native,
		)
	}
}

func (vm *VM) installKWISCompatibilityNatives() {
	vm.RegisterNative(
		"org/kwis/msp/io/File",
		"read",
		"([BII)I",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.xFile(receiver)
			if err != nil {
				return Value{}, false, err
			}
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
			object, ok := vm.Object(reference)
			if !ok || object.Array == nil || object.Array.Descriptor != "[B" ||
				offset < 0 || length < 0 ||
				int64(offset)+int64(length) > int64(len(object.Array.Elements)) {
				return Value{}, false, vm.newThrowable(
					"java/lang/ArrayIndexOutOfBoundsException",
					"",
				)
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
	vm.RegisterNative(
		"org/kwis/msp/media/Vibrator",
		"on",
		"(II)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			level, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			milliseconds, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			level = max(0, min(100, level))
			milliseconds = max(0, milliseconds)
			return Value{}, false, vm.services.Device.Vibrate(
				uint8(level),
				time.Duration(milliseconds)*time.Millisecond,
				vm.services.Clock.Monotonic(),
			)
		},
	)
}
