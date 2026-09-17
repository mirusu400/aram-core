package skvm

import (
	"context"
	"strings"
)

const lgtGraphicsClass = "mmpp/microedition/lcdui/GraphicsX"

// The public LGT GraphicsX Javadoc specifies that all Graphics objects are
// GraphicsX instances. Only this exact class and its documented alpha extension
// are installed; other extension methods remain explicitly unsupported.
func (vm *VM) installLGTGraphicsTypes() {
	vm.RegisterHostClass(lgtGraphicsClass, "javax/microedition/lcdui/Graphics")
	vm.RegisterStaticField(lgtGraphicsClass, "DEFAULT_ALPHA", "I", IntValue(256))
	vm.RegisterNative(lgtGraphicsClass, "setAlpha", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.graphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		alpha, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if alpha < 0 || alpha > 256 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "GraphicsX alpha out of range")
		}
		state.transparency256 = uint16(256 - alpha)
		return Value{}, false, nil
	})
	// Wrap base-class entries, not just GraphicsX method references: Java code
	// commonly invokes inherited methods using a Graphics-typed reference.
	for key, native := range vm.natives {
		if key.class == "javax/microedition/lcdui/Graphics" &&
			(strings.HasPrefix(key.name, "draw") || strings.HasPrefix(key.name, "fill") || key.name == "copyArea") {
			vm.natives[key] = withLGTGraphicsAlpha(native)
		}
	}
}

// Alpha belongs to a Java graphics context, not the target surface. Install it
// only during drawing and restore even on failure, so another getGraphics()
// context or a GameCanvas presentation never inherits it accidentally.
func withLGTGraphicsAlpha(native NativeFunc) NativeFunc {
	return func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		if vm.nativePolicy != NativePolicyLGT {
			return native(ctx, vm, receiver, args)
		}
		state, err := vm.graphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		original, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
		if err != nil {
			return Value{}, false, err
		}
		draw := original
		draw.GlobalTransparency256 = state.transparency256
		if err := vm.services.Graphics.SetDrawState(vm.serviceOwner, state.surface, draw); err != nil {
			return Value{}, false, err
		}
		value, returns, drawErr := native(ctx, vm, receiver, args)
		restoreErr := vm.services.Graphics.SetDrawState(vm.serviceOwner, state.surface, original)
		if drawErr != nil {
			return value, returns, drawErr
		}
		return value, returns, restoreErr
	}
}

func (vm *VM) newGraphicsObject(state *graphicsState) uint32 {
	class := "javax/microedition/lcdui/Graphics"
	if vm.nativePolicy == NativePolicyLGT {
		class = lgtGraphicsClass
	}
	return vm.NewObject(class, state)
}
