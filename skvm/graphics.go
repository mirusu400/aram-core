package skvm

import (
	"context"
	"fmt"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (vm *VM) ScreenGraphics() uint32 {
	if vm.screenGraphics == 0 {
		vm.screenGraphics = vm.NewObject(
			"javax/microedition/lcdui/Graphics",
			&graphicsState{
				width:   vm.ScreenWidth,
				height:  vm.ScreenHeight,
				surface: vm.screenSurface,
				font:    vm.defaultFont,
				color:   0xff000000,
			},
		)
	}
	return vm.screenGraphics
}

func nativeFillRect(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	x, y, width, height, err := rectangleArguments(args)
	if err != nil {
		return Value{}, false, err
	}
	if err := fillRectangle(vm, state, x, y, width, height, state.color); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func nativeDrawRect(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	x, y, width, height, err := rectangleArguments(args)
	if err != nil {
		return Value{}, false, err
	}
	for _, line := range [][4]int{
		{x, y, x + width, y},
		{x, y + height, x + width, y + height},
		{x, y, x, y + height},
		{x + width, y, x + width, y + height},
	} {
		if err := drawLine(vm, state, line[0], line[1], line[2], line[3], state.color); err != nil {
			return Value{}, false, err
		}
	}
	return Value{}, false, nil
}

func nativeDrawLine(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	x1, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	y1, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	x2, err := intArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	y2, err := intArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	if err := drawLine(vm, state, int(x1), int(y1), int(x2), int(y2), state.color); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func nativeDrawImage(
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
	source, err := vm.image(imageReference)
	if err != nil {
		return Value{}, false, err
	}
	x, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	y, err := intArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	anchor, err := intArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	destinationX, destinationY := anchored(
		int(x),
		int(y),
		source.width,
		source.height,
		int(anchor),
	)
	if err := blit(
		vm,
		state,
		source,
		destinationX,
		destinationY,
		0,
		0,
		source.width,
		source.height,
	); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func nativeDrawString(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	value, err := vm.stringArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	x, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	y, err := intArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	anchor, err := intArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	font := state.font
	if font == 0 {
		font = vm.defaultFont
	}
	width, err := vm.services.Text.Measure(vm.serviceOwner, font, value)
	if err != nil {
		return Value{}, false, err
	}
	metrics, err := vm.services.Text.Metrics(vm.serviceOwner, font)
	if err != nil {
		return Value{}, false, err
	}
	startX, startY := anchored(
		int(x),
		int(y),
		int(width),
		int(metrics.Height),
		int(anchor),
	)
	if err := vm.services.Text.Draw(
		vm.serviceOwner,
		font,
		state.surface,
		value,
		int32(startX),
		int32(startY),
		shared.AnchorLeft|shared.AnchorTop,
		skvmColor(state.color),
	); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func nativeGraphics2DDrawImage(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.graphics(receiver)
	if err != nil {
		return Value{}, false, err
	}
	destinationX, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	destinationY, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	imageReference, err := referenceArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	sourceX, err := intArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	sourceY, err := intArgument(args, 4)
	if err != nil {
		return Value{}, false, err
	}
	width, err := intArgument(args, 5)
	if err != nil {
		return Value{}, false, err
	}
	height, err := intArgument(args, 6)
	if err != nil {
		return Value{}, false, err
	}
	source, err := vm.image(imageReference)
	if err != nil {
		return Value{}, false, err
	}
	if err := blit(
		vm,
		state,
		source,
		int(destinationX),
		int(destinationY),
		int(sourceX),
		int(sourceY),
		int(width),
		int(height),
	); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func rectangleArguments(args []Value) (int, int, int, int, error) {
	if len(args) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("rectangle argument mismatch")
	}
	values := [4]int{}
	for index := range values {
		value, err := intArgument(args, index)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		values[index] = int(value)
	}
	return values[0], values[1], values[2], values[3], nil
}

func fillRectangle(
	vm *VM,
	state *graphicsState,
	x, y, width, height int,
	color uint32,
) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	return vm.services.Graphics.Rectangle(
		vm.serviceOwner,
		state.surface,
		shared.Rectangle{
			X:      int32(x),
			Y:      int32(y),
			Width:  int32(width),
			Height: int32(height),
		},
		skvmColor(color),
		true,
	)
}

func drawLine(
	vm *VM,
	state *graphicsState,
	x0, y0, x1, y1 int,
	color uint32,
) error {
	return vm.services.Graphics.Line(
		vm.serviceOwner,
		state.surface,
		int32(x0),
		int32(y0),
		int32(x1),
		int32(y1),
		skvmColor(color),
	)
}

func blit(
	vm *VM,
	destination *graphicsState,
	source *imageState,
	destinationX, destinationY, sourceX, sourceY, width, height int,
) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	sourceLeft := max(0, sourceX)
	sourceTop := max(0, sourceY)
	sourceRight := min(source.width, sourceX+width)
	sourceBottom := min(source.height, sourceY+height)
	if sourceRight <= sourceLeft || sourceBottom <= sourceTop {
		return nil
	}
	destinationX += sourceLeft - sourceX
	destinationY += sourceTop - sourceY
	return vm.services.Graphics.Blit(
		vm.serviceOwner,
		destination.surface,
		source.surface,
		int32(destinationX),
		int32(destinationY),
		shared.Rectangle{
			X:      int32(sourceLeft),
			Y:      int32(sourceTop),
			Width:  int32(sourceRight - sourceLeft),
			Height: int32(sourceBottom - sourceTop),
		},
	)
}

func anchored(x, y, width, height, anchor int) (int, int) {
	if anchor&1 != 0 {
		x -= width / 2
	} else if anchor&8 != 0 {
		x -= width
	}
	if anchor&2 != 0 {
		y -= height / 2
	} else if anchor&32 != 0 || anchor&64 != 0 {
		y -= height
	}
	return x, y
}

func setPixel(vm *VM, state *graphicsState, x, y int, color uint32) error {
	if x < 0 || y < 0 || x >= state.width || y >= state.height {
		return nil
	}
	return vm.services.Graphics.SetPixel(
		vm.serviceOwner,
		state.surface,
		int32(x),
		int32(y),
		skvmColor(color),
	)
}

func skvmColor(value uint32) shared.Color {
	return shared.Color{
		A: uint8(value >> 24),
		R: uint8(value >> 16),
		G: uint8(value >> 8),
		B: uint8(value),
	}
}
