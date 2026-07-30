package skvm

import (
	"context"
	"fmt"
	"time"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

// installCompatibilityNatives contains the less common APIs exposed by SKT,
// XCE, and KWIS handsets. Keeping them here makes the main J2ME surface easy to
// review while still giving every method referenced by the reference corpus a
// concrete host implementation.
func (vm *VM) installCompatibilityNatives() {
	vm.installDisplayCompatibilityNatives()
	vm.installGraphicsCompatibilityNatives()
	vm.installXCECompatibilityNatives()
	vm.installKWISCompatibilityNatives()
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
	case -1, '2':
		return 1 // UP
	case -3, '4':
		return 2 // LEFT
	case -4, '6':
		return 5 // RIGHT
	case -2, '8':
		return 6 // DOWN
	case -5, '5':
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
	for _, name := range []string{"setClip", "clipRect"} {
		vm.RegisterNative(
			"org/kwis/msp/lcdui/Graphics",
			name,
			"(IIII)V",
			nativeVoid,
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

func nativeRoundedRectangle(fill bool) NativeFunc {
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
		if width <= 0 || height <= 0 {
			return Value{}, false, nil
		}
		err = vm.services.Graphics.Rectangle(
			vm.serviceOwner,
			state.surface,
			shared.Rectangle{X: x, Y: y, Width: width, Height: height},
			skvmColor(state.color),
			fill,
		)
		return Value{}, false, err
	}
}

func (vm *VM) nativeSetGraphicsRGB(
	_ context.Context,
	_ *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
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
}

func (vm *VM) nativeGetRGBPixels(
	_ context.Context,
	_ *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, array, x, y, width, height, offset, stride, err :=
		vm.rgbPixelArguments(receiver, args)
	if err != nil {
		return Value{}, false, err
	}
	for row := int32(0); row < height; row++ {
		for column := int32(0); column < width; column++ {
			color, pixelErr := vm.services.Graphics.Pixel(
				vm.serviceOwner,
				state.surface,
				x+column,
				y+row,
			)
			if pixelErr != nil {
				return Value{}, false, pixelErr
			}
			index := int64(offset) + int64(row)*int64(stride) + int64(column)
			array.Array.Elements[index] = IntValue(int32(
				uint32(color.A)<<24 |
					uint32(color.R)<<16 |
					uint32(color.G)<<8 |
					uint32(color.B),
			))
		}
	}
	return Value{}, false, nil
}

func (vm *VM) nativeSetRGBPixels(
	_ context.Context,
	_ *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, array, x, y, width, height, offset, stride, err :=
		vm.rgbPixelArguments(receiver, args)
	if err != nil {
		return Value{}, false, err
	}
	for row := int32(0); row < height; row++ {
		for column := int32(0); column < width; column++ {
			index := int64(offset) + int64(row)*int64(stride) + int64(column)
			color, intErr := array.Array.Elements[index].Int()
			if intErr != nil {
				return Value{}, false, intErr
			}
			if setErr := setPixel(
				vm,
				state,
				int(x+column),
				int(y+row),
				uint32(color),
			); setErr != nil {
				return Value{}, false, setErr
			}
		}
	}
	return Value{}, false, nil
}

func (vm *VM) rgbPixelArguments(
	receiver uint32,
	args []Value,
) (*graphicsState, *Object, int32, int32, int32, int32, int32, int32, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, 0, 0, err
	}
	values := [4]int32{}
	for index := range values {
		values[index], err = intArgument(args, index)
		if err != nil {
			return nil, nil, 0, 0, 0, 0, 0, 0, err
		}
	}
	reference, err := referenceArgument(args, 4)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, 0, 0, err
	}
	array, ok := vm.Object(reference)
	if !ok || array.Array == nil || array.Array.Descriptor != "[I" {
		return nil, nil, 0, 0, 0, 0, 0, 0, fmt.Errorf("RGB pixel target is not int[]")
	}
	offset, err := intArgument(args, 5)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, 0, 0, err
	}
	stride, err := intArgument(args, 6)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, 0, 0, err
	}
	x, y, width, height := values[0], values[1], values[2], values[3]
	last := int64(offset)
	if height > 0 {
		last += int64(height-1)*int64(stride) + int64(width)
	}
	if x < 0 || y < 0 || width < 0 || height < 0 ||
		int64(x)+int64(width) > int64(state.width) ||
		int64(y)+int64(height) > int64(state.height) ||
		offset < 0 || stride < width || last > int64(len(array.Array.Elements)) {
		return nil, nil, 0, 0, 0, 0, 0, 0, vm.newThrowable(
			"java/lang/ArrayIndexOutOfBoundsException",
			"",
		)
	}
	return state, array, x, y, width, height, offset, stride, nil
}

func (vm *VM) installXCECompatibilityNatives() {
	inputMethod := vm.NewObject(
		"com/sun/midp/lcdui/InputMethodHandler",
		&integerState{},
	)
	vm.RegisterStaticField(
		"com/sun/midp/lcdui/InputMethodHandler",
		"__aramSingleton",
		"Lcom/sun/midp/lcdui/InputMethodHandler;",
		ReferenceValue(inputMethod),
	)
	textHandler := vm.NewObject(
		"com/xce/lcdui/TextComponentHandler",
		&integerState{},
	)
	vm.RegisterStaticField(
		"com/xce/lcdui/TextComponentHandler",
		"__aramSingleton",
		"Lcom/xce/lcdui/TextComponentHandler;",
		ReferenceValue(textHandler),
	)
	vm.RegisterNative(
		"com/sun/midp/lcdui/InputMethodHandler",
		"getInputMethodHandler",
		"()Lcom/sun/midp/lcdui/InputMethodHandler;",
		func(context.Context, *VM, uint32, []Value) (Value, bool, error) {
			return ReferenceValue(inputMethod), true, nil
		},
	)
	vm.RegisterNative(
		"com/sun/midp/lcdui/InputMethodHandler",
		"getInputMode",
		"()I",
		nativeIntegerState,
	)
	vm.RegisterNative(
		"com/sun/midp/lcdui/InputMethodHandler",
		"switchInputMode",
		"(I)V",
		nativeSetIntegerState,
	)
	vm.RegisterNative(
		"com/xce/lcdui/TextComponentHandler",
		"getTextComponentHandler",
		"()Lcom/xce/lcdui/TextComponentHandler;",
		func(context.Context, *VM, uint32, []Value) (Value, bool, error) {
			return ReferenceValue(textHandler), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/TextComponentHandler",
		"getInputMode",
		"()I",
		nativeIntegerState,
	)
	for _, method := range []string{"keyPressed", "keyReleased", "keyRepeated"} {
		vm.RegisterNative(
			"com/xce/lcdui/TextComponentHandler",
			method,
			"(I)Z",
			func(context.Context, *VM, uint32, []Value) (Value, bool, error) {
				return IntValue(0), true, nil
			},
		)
	}
	for _, method := range []struct {
		class, name, descriptor string
	}{
		{"com/xce/lcdui/TextComponentHandler", "clear", "()V"},
		{
			"com/xce/lcdui/TextComponentHandler",
			"setTextComponent",
			"(Lcom/xce/lcdui/TextComponent;)V",
		},
		{"com/xce/lcdui/XEventHandler", "restoreDisplay", "()V"},
		{"com/xce/jam/XBrowser", "setNetworkMode", "(I)V"},
		{"com/xce/net/Socket", "setPPPPreserveTime", "(I)V"},
	} {
		vm.RegisterNative(method.class, method.name, method.descriptor, nativeVoid)
	}
	vm.RegisterNative(
		"com/xce/lcdui/XDisplay",
		"drawImageEx",
		"(Ljavax/microedition/lcdui/Graphics;Ljavax/microedition/lcdui/Image;IILjavax/microedition/lcdui/Image;IIIII)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			graphicsReference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			imageReference, err := referenceArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			destinationX, err := intArgument(args, 2)
			if err != nil {
				return Value{}, false, err
			}
			destinationY, err := intArgument(args, 3)
			if err != nil {
				return Value{}, false, err
			}
			// The mask parameter is handset-specific. The shared blitter still
			// preserves the source image's own alpha channel.
			if _, err := referenceArgument(args, 4); err != nil {
				return Value{}, false, err
			}
			sourceX, err := intArgument(args, 5)
			if err != nil {
				return Value{}, false, err
			}
			sourceY, err := intArgument(args, 6)
			if err != nil {
				return Value{}, false, err
			}
			width, err := intArgument(args, 7)
			if err != nil {
				return Value{}, false, err
			}
			height, err := intArgument(args, 8)
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
			return Value{}, false, blit(
				vm,
				graphics,
				image,
				int(destinationX),
				int(destinationY),
				int(sourceX),
				int(sourceY),
				int(width),
				int(height),
			)
		},
	)
}

func nativeIntegerState(
	_ context.Context,
	vm *VM,
	receiver uint32,
	_ []Value,
) (Value, bool, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return Value{}, false, fmt.Errorf("invalid integer-state receiver")
	}
	state, ok := object.Native.(*integerState)
	if !ok {
		return Value{}, false, fmt.Errorf("object %d has no integer state", receiver)
	}
	return IntValue(state.value), true, nil
}

func nativeSetIntegerState(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return Value{}, false, fmt.Errorf("invalid integer-state receiver")
	}
	state, ok := object.Native.(*integerState)
	if !ok {
		return Value{}, false, fmt.Errorf("object %d has no integer state", receiver)
	}
	value, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	state.value = value
	return Value{}, false, nil
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
