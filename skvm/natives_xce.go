package skvm

import (
	"context"
	"fmt"
)

// installCompatibilityNatives contains the less common APIs exposed by SKT,
// XCE, and KWIS handsets. Keeping them here makes the main J2ME surface easy to
// review while still giving every method referenced by the reference corpus a
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
