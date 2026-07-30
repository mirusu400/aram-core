package skvm

import (
	"context"
	"fmt"
)

func (vm *VM) ScreenGraphics() uint32 {
	if vm.screenGraphics == 0 {
		vm.screenGraphics = vm.NewObject(
			"javax/microedition/lcdui/Graphics",
			&graphicsState{
				width:  vm.ScreenWidth,
				height: vm.ScreenHeight,
				pixels: vm.screen,
				color:  0xff000000,
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
	fillRectangle(state, x, y, width, height, state.color)
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
	drawLine(state, x, y, x+width, y, state.color)
	drawLine(state, x, y+height, x+width, y+height, state.color)
	drawLine(state, x, y, x, y+height, state.color)
	drawLine(state, x+width, y, x+width, y+height, state.color)
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
	drawLine(state, int(x1), int(y1), int(x2), int(y2), state.color)
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
	blit(state, source, destinationX, destinationY, 0, 0, source.width, source.height)
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
	width := len([]rune(value)) * 6
	startX, startY := anchored(int(x), int(y), width, 8, int(anchor))
	for index, character := range []rune(value) {
		drawPlaceholderGlyph(state, startX+index*6, startY, character, state.color)
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
	blit(
		state,
		source,
		int(destinationX),
		int(destinationY),
		int(sourceX),
		int(sourceY),
		int(width),
		int(height),
	)
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

func fillRectangle(state *graphicsState, x, y, width, height int, color uint32) {
	if width <= 0 || height <= 0 {
		return
	}
	startX := max(0, x)
	startY := max(0, y)
	endX := min(state.width, x+width)
	endY := min(state.height, y+height)
	for row := startY; row < endY; row++ {
		for column := startX; column < endX; column++ {
			state.pixels[row*state.width+column] = color
		}
	}
}

func drawLine(state *graphicsState, x0, y0, x1, y1 int, color uint32) {
	dx := absInt(x1 - x0)
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	dy := -absInt(y1 - y0)
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		setPixel(state, x0, y0, color)
		if x0 == x1 && y0 == y1 {
			break
		}
		twice := 2 * err
		if twice >= dy {
			err += dy
			x0 += sx
		}
		if twice <= dx {
			err += dx
			y0 += sy
		}
	}
}

func blit(
	destination *graphicsState,
	source *imageState,
	destinationX, destinationY, sourceX, sourceY, width, height int,
) {
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			sx, sy := sourceX+column, sourceY+row
			dx, dy := destinationX+column, destinationY+row
			if sx < 0 || sy < 0 || sx >= source.width || sy >= source.height ||
				dx < 0 || dy < 0 || dx >= destination.width || dy >= destination.height {
				continue
			}
			color := source.pixels[sy*source.width+sx]
			if color>>24 == 0 {
				continue
			}
			destination.pixels[dy*destination.width+dx] = color
		}
	}
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

func drawPlaceholderGlyph(
	state *graphicsState,
	x, y int,
	character rune,
	color uint32,
) {
	if character == ' ' {
		return
	}
	for row := 0; row < 7; row++ {
		for column := 0; column < 5; column++ {
			if row == 0 || row == 6 || column == 0 || column == 4 {
				setPixel(state, x+column, y+row, color)
			}
		}
	}
}

func setPixel(state *graphicsState, x, y int, color uint32) {
	if x < 0 || y < 0 || x >= state.width || y >= state.height {
		return
	}
	state.pixels[y*state.width+x] = color
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
