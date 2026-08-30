package skvm

import (
	"context"
	"fmt"
	"github.com/mirusu400/aram-core/internal/ime"
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
		newTextComponentHandlerState(),
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
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.textComponentHandler(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(state.automata.CurrentMode())), true, nil
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/TextComponentHandler",
		"keyPressed",
		"(I)Z",
		nativeTextComponentKeyPressed,
	)
	// A key release or auto-repeat carries no multi-tap meaning here: the
	// automata advances on press, so releases are reported unhandled and repeats
	// are swallowed so a held key does not spray glyphs.
	for _, method := range []string{"keyReleased", "keyRepeated"} {
		vm.RegisterNative(
			"com/xce/lcdui/TextComponentHandler",
			method,
			"(I)Z",
			func(context.Context, *VM, uint32, []Value) (Value, bool, error) {
				return IntValue(0), true, nil
			},
		)
	}
	vm.RegisterNative(
		"com/xce/lcdui/TextComponentHandler",
		"clear",
		"()V",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.textComponentHandler(receiver)
			if err != nil {
				return Value{}, false, err
			}
			state.automata.Reset()
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"com/xce/lcdui/TextComponentHandler",
		"setTextComponent",
		"(Lcom/xce/lcdui/TextComponent;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.textComponentHandler(receiver)
			if err != nil {
				return Value{}, false, err
			}
			component, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state.component = component
			state.automata.Reset()
			return Value{}, false, nil
		},
	)
	for _, method := range []struct {
		class, name, descriptor string
	}{
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

func (vm *VM) textComponentHandler(
	receiver uint32,
) (*textComponentHandlerState, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return nil, fmt.Errorf("invalid TextComponentHandler receiver %d", receiver)
	}
	state, ok := object.Native.(*textComponentHandlerState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a TextComponentHandler", receiver)
	}
	return state, nil
}

// nativeTextComponentKeyPressed drives the multi-tap automata for one key and
// pushes the resulting glyph edits into the registered guest TextComponent. It
// reports whether the input method consumed the key, which is what the guest
// XTextField forwards from keyPressed.
func nativeTextComponentKeyPressed(
	ctx context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.textComponentHandler(receiver)
	if err != nil {
		return Value{}, false, err
	}
	key, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	if state.component == 0 {
		// No field is registered, so the key cannot be composed into anything;
		// leave it for the guest to handle.
		return boolValue(false), true, nil
	}
	ops, handled := state.automata.Press(key)
	for _, op := range ops {
		if err := vm.applyIMEOp(ctx, state.component, op); err != nil {
			return Value{}, false, err
		}
	}
	return boolValue(handled), true, nil
}

// applyIMEOp turns one automata callback into an InvokeVirtual on the guest
// TextComponent. Chars ride in an int slot, matching the (C)V descriptors.
func (vm *VM) applyIMEOp(ctx context.Context, component uint32, op ime.Op) error {
	switch op.Kind {
	case ime.OpInsert:
		_, _, err := vm.InvokeVirtual(
			ctx, component, "insert", "(C)V", IntValue(int32(op.Char)),
		)
		return err
	case ime.OpReplace:
		_, _, err := vm.InvokeVirtual(
			ctx, component, "replace", "(C)V", IntValue(int32(op.Char)),
		)
		return err
	case ime.OpDelete:
		_, _, err := vm.InvokeVirtual(ctx, component, "delete", "()V")
		return err
	}
	return nil
}
