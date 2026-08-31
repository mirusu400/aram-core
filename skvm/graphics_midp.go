package skvm

import (
	"context"

	shared "github.com/mirusu400/aram-core/runtime"
)

// MIDP transform codes for Graphics.drawRegion. The transform is applied to
// the source region before it reaches the destination, and the four codes that
// involve a quarter turn swap the drawn width and height.
const (
	transNone         int32 = 0
	transMirrorRot180 int32 = 1
	transMirror       int32 = 2
	transRot180       int32 = 3
	transMirrorRot270 int32 = 4
	transRot90        int32 = 5
	transRot270       int32 = 6
	transMirrorRot90  int32 = 7
)

// MIDP stroke styles. The rasterizer draws solid lines only, so the style is
// remembered for getStrokeStyle rather than honoured.
const (
	strokeSolid  int32 = 0
	strokeDotted int32 = 1
)

// midpRegionSource maps a pixel of a transformed region back to the source
// pixel it was taken from. Composite codes mirror horizontally first and then
// rotate clockwise, matching the Sprite transform vocabulary MIDP shares with
// Graphics.drawRegion.
func midpRegionSource(transform, u, v, width, height int32) (int32, int32) {
	switch transform {
	case transMirror:
		return width - 1 - u, v
	case transRot180:
		return width - 1 - u, height - 1 - v
	case transMirrorRot180:
		return u, height - 1 - v
	case transRot90:
		return v, height - 1 - u
	case transRot270:
		return width - 1 - v, u
	case transMirrorRot90:
		return width - 1 - v, height - 1 - u
	case transMirrorRot270:
		return v, u
	default:
		return u, v
	}
}

// midpRegionQuarterTurn reports whether a transform swaps the drawn width and
// height.
func midpRegionQuarterTurn(transform int32) bool {
	switch transform {
	case transMirrorRot270, transRot90, transRot270, transMirrorRot90:
		return true
	}
	return false
}

func nativeDrawRegion(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	imageReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	if imageReference == 0 {
		return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	source, err := vm.image(imageReference)
	if err != nil {
		return Value{}, false, err
	}
	values := [8]int32{}
	for index := range values {
		values[index], err = intArgument(args, index+1)
		if err != nil {
			return Value{}, false, err
		}
	}
	sourceX, sourceY := values[0], values[1]
	width, height := values[2], values[3]
	transform := values[4]
	destinationX, destinationY, anchor := values[5], values[6], values[7]
	if width < 0 || height < 0 ||
		transform < transNone || transform > transMirrorRot90 ||
		sourceX < 0 || sourceY < 0 ||
		int64(sourceX)+int64(width) > int64(source.width) ||
		int64(sourceY)+int64(height) > int64(source.height) ||
		source.surface == state.surface {
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"invalid drawRegion request",
		)
	}
	if width == 0 || height == 0 {
		return Value{}, false, nil
	}
	drawnWidth, drawnHeight := width, height
	if midpRegionQuarterTurn(transform) {
		drawnWidth, drawnHeight = height, width
	}
	left, top := anchored(
		int(destinationX),
		int(destinationY),
		int(drawnWidth),
		int(drawnHeight),
		int(anchor),
	)
	if transform == transNone {
		err = blit(
			vm,
			state,
			source,
			left,
			top,
			int(sourceX),
			int(sourceY),
			int(width),
			int(height),
		)
		return Value{}, false, err
	}
	descriptor, err := vm.services.Graphics.Descriptor(
		vm.serviceOwner,
		source.surface,
	)
	if err != nil {
		return Value{}, false, err
	}
	for v := int32(0); v < drawnHeight; v++ {
		for u := int32(0); u < drawnWidth; u++ {
			offsetX, offsetY := midpRegionSource(transform, u, v, width, height)
			color, pixelErr := vm.services.Graphics.Pixel(
				vm.serviceOwner,
				source.surface,
				sourceX+offsetX,
				sourceY+offsetY,
			)
			if pixelErr != nil {
				return Value{}, false, pixelErr
			}
			if descriptor.Transparent != nil && color == *descriptor.Transparent {
				continue
			}
			if setErr := vm.services.Graphics.SetPixel(
				vm.serviceOwner,
				state.surface,
				int32(left)+u,
				int32(top)+v,
				color,
			); setErr != nil {
				return Value{}, false, setErr
			}
		}
	}
	return Value{}, false, nil
}

// nativeCopyArea copies a region of the destination surface onto itself. The
// source is read before anything is written so an overlapping copy still moves
// the pixels the guest asked for.
func nativeCopyArea(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	values := [7]int32{}
	for index := range values {
		values[index], err = intArgument(args, index)
		if err != nil {
			return Value{}, false, err
		}
	}
	sourceX, sourceY := values[0], values[1]
	width, height := values[2], values[3]
	destinationX, destinationY, anchor := values[4], values[5], values[6]
	drawState, err := vm.services.Graphics.DrawState(
		vm.serviceOwner,
		state.surface,
	)
	if err != nil {
		return Value{}, false, err
	}
	// The source rectangle is in the translated coordinate system, but reading
	// a pixel is not translate-aware the way plotting one is.
	readX := int64(sourceX) + int64(drawState.TranslateX)
	readY := int64(sourceY) + int64(drawState.TranslateY)
	if width < 0 || height < 0 ||
		readX < 0 || readY < 0 ||
		readX+int64(width) > int64(state.width) ||
		readY+int64(height) > int64(state.height) {
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"invalid copyArea region",
		)
	}
	if width == 0 || height == 0 {
		return Value{}, false, nil
	}
	colors := make([]shared.Color, int(width)*int(height))
	for row := int32(0); row < height; row++ {
		for column := int32(0); column < width; column++ {
			color, pixelErr := vm.services.Graphics.Pixel(
				vm.serviceOwner,
				state.surface,
				int32(readX)+column,
				int32(readY)+row,
			)
			if pixelErr != nil {
				return Value{}, false, pixelErr
			}
			colors[int(row)*int(width)+int(column)] = color
		}
	}
	left, top := anchored(
		int(destinationX),
		int(destinationY),
		int(width),
		int(height),
		int(anchor),
	)
	for row := int32(0); row < height; row++ {
		for column := int32(0); column < width; column++ {
			if err := vm.services.Graphics.SetPixel(
				vm.serviceOwner,
				state.surface,
				int32(left)+column,
				int32(top)+row,
				colors[int(row)*int(width)+int(column)],
			); err != nil {
				return Value{}, false, err
			}
		}
	}
	return Value{}, false, nil
}

// nativeDrawRGB plots a packed ARGB block. A negative scan length walks the
// array backwards, which MIDP allows so a guest can draw a vertically mirrored
// block without copying it first.
func nativeDrawRGB(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	reference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	if reference == 0 {
		return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	array, ok := vm.Object(reference)
	if !ok || array.Array == nil || array.Array.Descriptor != "[I" {
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"drawRGB source is not int[]",
		)
	}
	values := [6]int32{}
	for index := range values {
		values[index], err = intArgument(args, index+1)
		if err != nil {
			return Value{}, false, err
		}
	}
	offset, scanLength := values[0], values[1]
	x, y := values[2], values[3]
	width, height := values[4], values[5]
	processAlpha, err := intArgument(args, 7)
	if err != nil {
		return Value{}, false, err
	}
	if width < 0 || height < 0 {
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"negative drawRGB size",
		)
	}
	if width == 0 || height == 0 {
		return Value{}, false, nil
	}
	length := int64(len(array.Array.Elements))
	for row := int32(0); row < height; row++ {
		start := int64(offset) + int64(row)*int64(scanLength)
		if start < 0 || start+int64(width) > length {
			return Value{}, false, vm.newThrowable(
				"java/lang/ArrayIndexOutOfBoundsException",
				"",
			)
		}
	}
	for row := int32(0); row < height; row++ {
		start := int64(offset) + int64(row)*int64(scanLength)
		for column := int32(0); column < width; column++ {
			packed, intErr := array.Array.Elements[start+int64(column)].Int()
			if intErr != nil {
				return Value{}, false, intErr
			}
			color := uint32(packed)
			if processAlpha == 0 {
				color |= 0xff000000
			}
			if err := vm.services.Graphics.SetPixel(
				vm.serviceOwner,
				state.surface,
				x+column,
				y+row,
				skvmColor(color),
			); err != nil {
				return Value{}, false, err
			}
		}
	}
	return Value{}, false, nil
}

func nativeFillTriangle(
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
	err = vm.services.Graphics.Polygon(
		vm.serviceOwner,
		state.surface,
		[]shared.Point{
			{X: values[0], Y: values[1]},
			{X: values[2], Y: values[3]},
			{X: values[4], Y: values[5]},
		},
		skvmColor(state.color),
		true,
	)
	return Value{}, false, err
}

func nativeDrawChar(
	ctx context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	character, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	return nativeDrawString(ctx, vm, receiver, []Value{
		ReferenceValue(vm.NewString(string(rune(uint16(character))))),
		args[1],
		args[2],
		args[3],
	})
}

// nativeColorComponent reports one channel of the current color.
func nativeColorComponent(shift uint) NativeFunc {
	return func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		state, err := vm.graphics(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(int32(state.color >> shift & 0xff)), true, nil
	}
}

func nativeGetGrayScale(
	_ context.Context,
	vm *VM,
	receiver uint32,
	_ []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	// MIDP reports the gray value a color would be displayed at on a
	// monochrome device, which is its luminance.
	gray := (int32(state.color>>16&0xff)*30 +
		int32(state.color>>8&0xff)*59 +
		int32(state.color&0xff)*11) / 100
	return IntValue(gray), true, nil
}

func nativeSetGrayScale(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	value, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	if value < 0 || value > 0xff {
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"gray value out of range",
		)
	}
	component := uint32(value)
	state.color = 0xff000000 | component<<16 | component<<8 | component
	return Value{}, false, nil
}

func nativeGetStrokeStyle(
	_ context.Context,
	vm *VM,
	receiver uint32,
	_ []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	return IntValue(state.stroke), true, nil
}

func nativeSetStrokeStyle(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	style, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	if style != strokeSolid && style != strokeDotted {
		return Value{}, false, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"invalid stroke style",
		)
	}
	state.stroke = style
	return Value{}, false, nil
}

// nativeGetDisplayColor answers with the color unchanged: the framebuffer keeps
// eight bits per channel, so a requested color is displayed exactly.
func nativeGetDisplayColor(
	_ context.Context,
	_ *VM,
	_ uint32,
	args []Value,
) (Value, bool, error) {
	color, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	return IntValue(color & 0x00ffffff), true, nil
}

// installMIDPGraphicsExtras registers the rest of the MIDP 2.0 Graphics class.
// A method the class defines but the VM does not register is a hard fault, not
// a no-op, so every drawing entry point has to exist even when a title only
// reaches a few of them (aram-core issue #117, aram-frontend issue #20).
func (vm *VM) installMIDPGraphicsExtras() {
	for _, method := range []struct {
		name       string
		descriptor string
		native     NativeFunc
	}{
		{
			"drawRegion",
			"(Ljavax/microedition/lcdui/Image;IIIIIIII)V",
			nativeDrawRegion,
		},
		{"copyArea", "(IIIIIII)V", nativeCopyArea},
		{"drawRGB", "([IIIIIIIZ)V", nativeDrawRGB},
		{"fillTriangle", "(IIIIII)V", nativeFillTriangle},
		{"drawChar", "(CIII)V", nativeDrawChar},
		{"getRedComponent", "()I", nativeColorComponent(16)},
		{"getGreenComponent", "()I", nativeColorComponent(8)},
		{"getBlueComponent", "()I", nativeColorComponent(0)},
		{"getGrayScale", "()I", nativeGetGrayScale},
		{"setGrayScale", "(I)V", nativeSetGrayScale},
		{"getStrokeStyle", "()I", nativeGetStrokeStyle},
		{"setStrokeStyle", "(I)V", nativeSetStrokeStyle},
		{"getDisplayColor", "(I)I", nativeGetDisplayColor},
	} {
		vm.RegisterNative(
			"javax/microedition/lcdui/Graphics",
			method.name,
			method.descriptor,
			method.native,
		)
	}
}
