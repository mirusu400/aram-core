package skvm

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

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
			return IntValue(int32(vm.canvasHeight())), true, nil
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
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			target, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if target == 0 {
				return Value{}, false, vm.newThrowable(
					"java/lang/NullPointerException",
					"",
				)
			}
			if _, ok := vm.Object(target); !ok {
				return Value{}, false, fmt.Errorf(
					"invalid callSerially target %d",
					target,
				)
			}
			now := vm.services.Clock.Monotonic()
			if now == time.Duration(1<<63-1) {
				return Value{}, false, fmt.Errorf("callSerially deadline overflow")
			}
			// A callback must run after this native call returns. Advancing the
			// deadline by one virtual nanosecond also prevents a Runnable that
			// reschedules itself from spinning inside one event-pump pass.
			_, err = vm.services.Events.Enqueue(shared.Event{
				At:    now + time.Nanosecond,
				Kind:  shared.EventApplication,
				Owner: vm.serviceOwner,
				Name:  callSeriallyEventName,
				Value: int64(target),
			})
			return Value{}, false, err
		},
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

func (vm *VM) canvasHeight() int {
	height := vm.ScreenHeight
	if vm.services != nil &&
		vm.services.Device.Quirk(CanvasHeightInset16Quirk) &&
		height > 16 {
		return height - 16
	}
	return height
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
		native     NativeFunc
	}{
		{"setClip", "(IIII)V", nativeSetClip},
		{"clipRect", "(IIII)V", nativeClipRect},
		{"translate", "(II)V", nativeTranslate},
	} {
		vm.RegisterNative(
			"javax/microedition/lcdui/Graphics",
			method.name,
			method.descriptor,
			method.native,
		)
	}
	for _, method := range []struct {
		name   string
		native NativeFunc
	}{
		{"getClipX", nativeGraphicsState(func(state shared.SurfaceDrawState) int32 {
			return state.Clip.X - state.TranslateX
		})},
		{"getClipY", nativeGraphicsState(func(state shared.SurfaceDrawState) int32 {
			return state.Clip.Y - state.TranslateY
		})},
		{"getClipWidth", nativeGraphicsState(func(state shared.SurfaceDrawState) int32 {
			return state.Clip.Width
		})},
		{"getClipHeight", nativeGraphicsState(func(state shared.SurfaceDrawState) int32 {
			return state.Clip.Height
		})},
		{"getTranslateX", nativeGraphicsState(func(state shared.SurfaceDrawState) int32 {
			return state.TranslateX
		})},
		{"getTranslateY", nativeGraphicsState(func(state shared.SurfaceDrawState) int32 {
			return state.TranslateY
		})},
	} {
		vm.RegisterNative(
			"javax/microedition/lcdui/Graphics",
			method.name,
			"()I",
			method.native,
		)
	}
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"getColor",
		"()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(state.color & 0xffffff)), true, nil
		},
	)
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

func (vm *VM) installDisplayCompatibilityNatives() {
	gameAction := func(
		_ context.Context,
		_ *VM,
		_ uint32,
		args []Value,
	) (Value, bool, error) {
		key, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(gameActionForKey(key)), true, nil
	}
	vm.RegisterNative(
		"javax/microedition/lcdui/Canvas",
		"getGameAction",
		"(I)I",
		gameAction,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Display",
		"getGameAction",
		"(I)I",
		gameAction,
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Displayable",
		"isShown",
		"()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			return boolValue(receiver != 0 && receiver == vm.currentDisplay), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Displayable",
		"repaintIM",
		"()V",
		nativeVoid,
	)
}

func gameActionForKey(key int32) int32 {
	switch key {
	case -1, '2', 141:
		return 1 // UP
	case -3, '4', 142:
		return 2 // LEFT
	case -4, '6', 145:
		return 5 // RIGHT
	case -2, '8', 146:
		return 6 // DOWN
	case -5, '5', 148:
		return 8 // FIRE
	default:
		return 0
	}
}

func (vm *VM) installGraphicsCompatibilityNatives() {
	vm.RegisterNative(
		"com/skt/m/Graphics2D",
		"setPixel",
		"(III)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			x, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			y, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			color, err := intArgument(args, 2)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, setPixel(
				vm,
				state,
				int(x),
				int(y),
				0xff000000|uint32(color)&0x00ffffff,
			)
		},
	)

	measure := func(characters bool) NativeFunc {
		return func(
			_ context.Context,
			vm *VM,
			receiver uint32,
			args []Value,
		) (Value, bool, error) {
			var value string
			var err error
			if characters {
				offset, offsetErr := intArgument(args, 1)
				if offsetErr != nil {
					return Value{}, false, offsetErr
				}
				length, lengthErr := intArgument(args, 2)
				if lengthErr != nil {
					return Value{}, false, lengthErr
				}
				value, err = vm.charArrayArgument(args, 0, offset, length)
			} else {
				value, err = vm.stringArgument(args, 0)
				if err == nil {
					offset, offsetErr := intArgument(args, 1)
					if offsetErr != nil {
						return Value{}, false, offsetErr
					}
					length, lengthErr := intArgument(args, 2)
					if lengthErr != nil {
						return Value{}, false, lengthErr
					}
					units := utf16.Encode([]rune(value))
					if offset < 0 || length < 0 ||
						int64(offset)+int64(length) > int64(len(units)) {
						return Value{}, false, vm.newThrowable(
							"java/lang/StringIndexOutOfBoundsException",
							"",
						)
					}
					value = string(utf16.Decode(units[offset : offset+length]))
				}
			}
			if err != nil {
				return Value{}, false, err
			}
			font, err := vm.font(receiver)
			if err != nil {
				return Value{}, false, err
			}
			width, err := vm.services.Text.Measure(vm.serviceOwner, font.font, value)
			return IntValue(width), true, err
		}
	}
	vm.RegisterNative(
		"javax/microedition/lcdui/Font",
		"charsWidth",
		"([CII)I",
		measure(true),
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Font",
		"substringWidth",
		"(Ljava/lang/String;II)I",
		measure(false),
	)

	drawTextSlice := func(characters bool) NativeFunc {
		return func(
			ctx context.Context,
			vm *VM,
			receiver uint32,
			args []Value,
		) (Value, bool, error) {
			var value string
			var err error
			if characters {
				offset, offsetErr := intArgument(args, 1)
				if offsetErr != nil {
					return Value{}, false, offsetErr
				}
				length, lengthErr := intArgument(args, 2)
				if lengthErr != nil {
					return Value{}, false, lengthErr
				}
				value, err = vm.charArrayArgument(args, 0, offset, length)
			} else {
				value, err = vm.stringArgument(args, 0)
				if err == nil {
					offset, offsetErr := intArgument(args, 1)
					if offsetErr != nil {
						return Value{}, false, offsetErr
					}
					length, lengthErr := intArgument(args, 2)
					if lengthErr != nil {
						return Value{}, false, lengthErr
					}
					units := utf16.Encode([]rune(value))
					if offset < 0 || length < 0 ||
						int64(offset)+int64(length) > int64(len(units)) {
						return Value{}, false, vm.newThrowable(
							"java/lang/StringIndexOutOfBoundsException",
							"",
						)
					}
					value = string(utf16.Decode(units[offset : offset+length]))
				}
			}
			if err != nil {
				return Value{}, false, err
			}
			return nativeDrawString(ctx, vm, receiver, []Value{
				ReferenceValue(vm.NewString(value)),
				args[3],
				args[4],
				args[5],
			})
		}
	}
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"drawChars",
		"([CIIIII)V",
		drawTextSlice(true),
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"drawSubstring",
		"(Ljava/lang/String;IIIII)V",
		drawTextSlice(false),
	)

	arc := func(fill bool) NativeFunc {
		return func(
			_ context.Context,
			vm *VM,
			receiver uint32,
			args []Value,
		) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			values := [6]int32{}
			for index := range values {
				values[index], err = intArgument(args, index)
				if err != nil {
					return Value{}, false, err
				}
			}
			if values[2] <= 0 || values[3] <= 0 {
				return Value{}, false, nil
			}
			err = vm.services.Graphics.Arc(
				vm.serviceOwner,
				state.surface,
				shared.Rectangle{
					X: values[0], Y: values[1],
					Width: values[2], Height: values[3],
				},
				values[4],
				values[5],
				skvmColor(state.color),
				fill,
			)
			return Value{}, false, err
		}
	}
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"fillArc",
		"(IIIIII)V",
		arc(true),
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"drawArc",
		"(IIIIII)V",
		arc(false),
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"fillArc",
		"(IIIIII)V",
		arc(true),
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"drawRoundRect",
		"(IIIIII)V",
		nativeRoundedRectangle(false),
	)
	vm.RegisterNative(
		"javax/microedition/lcdui/Graphics",
		"fillRoundRect",
		"(IIIIII)V",
		nativeRoundedRectangle(true),
	)

	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"drawImage",
		"(Lorg/kwis/msp/lcdui/Image;III)V",
		nativeDrawImage,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"drawString",
		"(Ljava/lang/String;III)V",
		nativeDrawString,
	)
	for _, method := range []struct {
		name   string
		native NativeFunc
	}{
		{"setClip", nativeSetClip},
		{"clipRect", nativeClipRect},
	} {
		vm.RegisterNative(
			"org/kwis/msp/lcdui/Graphics",
			method.name,
			"(IIII)V",
			method.native,
		)
	}
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"setColor",
		"(III)V",
		vm.nativeSetGraphicsRGB,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"setAlpha",
		"(I)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
			if err != nil {
				return Value{}, false, err
			}
			alpha, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state.color = uint32(alpha&0xff)<<24 | state.color&0x00ffffff
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"setFont",
		"(Lorg/kwis/msp/lcdui/Font;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.graphics(receiver)
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
			state.font = font.font
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"getRGBPixels",
		"(IIII[III)V",
		vm.nativeGetRGBPixels,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Graphics",
		"setRGBPixels",
		"(IIII[III)V",
		vm.nativeSetRGBPixels,
	)
	vm.RegisterNative(
		"org/kwis/msp/lcdui/Image",
		"createImage",
		"([BII)Lorg/kwis/msp/lcdui/Image;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			data, err := vm.byteSliceArgument(args)
			if err != nil {
				return Value{}, false, err
			}
			reference, err := vm.newImage(data)
			if err == nil {
				if object, ok := vm.Object(reference); ok {
					object.Class = "org/kwis/msp/lcdui/Image"
				}
			}
			return ReferenceValue(reference), true, err
		},
	)
}
