package skvm

import (
	"context"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

const lgtGraphicsClass = "mmpp/microedition/lcdui/GraphicsX"

// The public LGT GraphicsX Javadoc specifies that all Graphics objects are
// GraphicsX instances. Only this exact class and its documented alpha extension
// are installed; other extension methods remain explicitly unsupported.
func (vm *VM) installLGTGraphicsTypes() {
	vm.RegisterHostClass(lgtGraphicsClass, "javax/microedition/lcdui/Graphics")
	vm.RegisterStaticField(lgtGraphicsClass, "DEFAULT_ALPHA", "I", IntValue(256))
	vm.RegisterNative(lgtGraphicsClass, "setXORMode", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.graphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		color, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		state.color = 0xff000000 | uint32(color)&0xffffff
		state.transparency256 = 0
		state.xorMode = true
		return Value{}, false, nil
	})
	vm.RegisterNative(lgtGraphicsClass, "setPaintMode", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.graphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state.xorMode = false
		return Value{}, false, nil
	})
	vm.RegisterNative(lgtGraphicsClass, "isXORMode", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.graphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return boolValue(state.xorMode), true, nil
	})
	vm.RegisterNative(lgtGraphicsClass, "getPixel", "(II)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, draw, err := vm.lgtReadableGraphics(receiver)
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
		px, py := int64(x)+int64(draw.TranslateX), int64(y)+int64(draw.TranslateY)
		if !lgtReadablePixel(state, draw, px, py) {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "GraphicsX pixel outside clip")
		}
		color, err := vm.services.Graphics.Pixel(vm.serviceOwner, state.surface, int32(px), int32(py))
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(int32(uint32(color.R)<<16 | uint32(color.G)<<8 | uint32(color.B))), true, nil
	})
	vm.RegisterNative(lgtGraphicsClass, "capture", "(IIII)Ljavax/microedition/lcdui/Image;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, draw, err := vm.lgtReadableGraphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		x, y, width, height, err := rectangleArguments(args)
		if err != nil {
			return Value{}, false, err
		}
		left, top := int64(x)+int64(draw.TranslateX), int64(y)+int64(draw.TranslateY)
		if width <= 0 || height <= 0 || !lgtReadablePixel(state, draw, left, top) ||
			!lgtReadablePixel(state, draw, left+int64(width)-1, top+int64(height)-1) {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "GraphicsX capture outside clip")
		}
		image, err := vm.newImageState(width, height)
		if err != nil {
			return Value{}, false, err
		}
		pixels := make([]byte, width*height*4)
		for row := 0; row < height; row++ {
			for column := 0; column < width; column++ {
				color, err := vm.services.Graphics.Pixel(vm.serviceOwner, state.surface, int32(left)+int32(column), int32(top)+int32(row))
				if err != nil {
					return Value{}, false, err
				}
				index := (row*width + column) * 4
				pixels[index], pixels[index+1], pixels[index+2], pixels[index+3] = color.R, color.G, color.B, color.A
			}
		}
		if err := vm.services.Graphics.ReplacePixels(vm.serviceOwner, image.surface, pixels); err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.newImmutableImageObject(image)), true, nil
	})
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

func (vm *VM) lgtReadableGraphics(receiver uint32) (*graphicsState, shared.SurfaceDrawState, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return nil, shared.SurfaceDrawState{}, err
	}
	draw, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
	return state, draw, err
}

func lgtReadablePixel(state *graphicsState, draw shared.SurfaceDrawState, x, y int64) bool {
	return x >= 0 && y >= 0 && x < int64(state.width) && y < int64(state.height) &&
		x >= int64(draw.Clip.X) && y >= int64(draw.Clip.Y) &&
		x < int64(draw.Clip.X)+int64(draw.Clip.Width) &&
		y < int64(draw.Clip.Y)+int64(draw.Clip.Height)
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
		if state.xorMode {
			draw.Raster = shared.RasterXOR
		}
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
