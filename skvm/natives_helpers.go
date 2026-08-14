package skvm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (vm *VM) newImage(data []byte) (uint32, error) {
	if len(data) != 0 {
		asset, decodeErr := vm.services.Assets.Decode(
			vm.serviceOwner,
			data,
			shared.DecodeOptions{},
		)
		if decodeErr == nil {
			info, infoErr := vm.services.Assets.Info(vm.serviceOwner, asset)
			if infoErr != nil {
				_ = vm.services.Assets.Release(vm.serviceOwner, asset)
				return 0, infoErr
			}
			state := &imageState{
				width:   int(info.Width),
				height:  int(info.Height),
				asset:   asset,
				surface: info.Frames[0].Surface,
			}
			return vm.NewObject("javax/microedition/lcdui/Image", state), nil
		}
	}
	state, err := vm.newImageState(1, 1)
	if err != nil {
		return 0, err
	}
	return vm.NewObject("javax/microedition/lcdui/Image", state), nil
}

func (vm *VM) newImageState(width, height int) (*imageState, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid image geometry %dx%d", width, height)
	}
	surface, err := vm.services.Graphics.CreateSurface(
		vm.serviceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(width),
			Height: int32(height),
			Format: shared.PixelRGBA8888,
		},
	)
	if err != nil {
		return nil, err
	}
	return &imageState{
		width:   width,
		height:  height,
		surface: surface,
	}, nil
}

func (vm *VM) image(reference uint32) (*imageState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Image reference")
	}
	state, ok := object.Native.(*imageState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an Image", reference)
	}
	return state, nil
}

func (vm *VM) font(reference uint32) (*fontState, error) {
	object, ok := vm.Object(reference)
	if !ok || (object.Class != "javax/microedition/lcdui/Font" &&
		object.Class != "org/kwis/msp/lcdui/Font") {
		return nil, fmt.Errorf("invalid Font reference")
	}
	if state, ok := object.Native.(*fontState); ok {
		return state, nil
	}
	if object.Native == nil {
		state := &fontState{font: vm.defaultFont}
		object.Native = state
		return state, nil
	}
	return nil, fmt.Errorf("object %d is not a Font", reference)
}

func (vm *VM) newFontObject(class string, args []Value) (Value, bool, error) {
	style, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	sizeValue, err := intArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	size := int32(12)
	switch {
	case sizeValue&16 != 0:
		size = 16
	case sizeValue&8 != 0:
		size = 8
	}
	var fontStyle shared.FontStyle
	if style&1 != 0 {
		fontStyle |= shared.FontBold
	}
	if style&2 != 0 {
		fontStyle |= shared.FontItalic
	}
	if style&4 != 0 {
		fontStyle |= shared.FontUnderlined
	}
	id, err := vm.services.Text.CreateFont(vm.serviceOwner, shared.FontDescriptor{
		Family: "aram-fallback",
		Size:   size,
		Style:  fontStyle,
	})
	if err != nil {
		return Value{}, false, err
	}
	return ReferenceValue(vm.NewObject(class, &fontState{font: id})), true, nil
}

func (vm *VM) graphics(reference uint32) (*graphicsState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Graphics reference")
	}
	state, ok := object.Native.(*graphicsState)
	if !ok {
		return nil, fmt.Errorf("object %d is not Graphics", reference)
	}
	return state, nil
}

func nativeVoid(
	context.Context,
	*VM,
	uint32,
	[]Value,
) (Value, bool, error) {
	return Value{}, false, nil
}

func nativeArrayCopy(
	_ context.Context,
	vm *VM,
	_ uint32,
	args []Value,
) (Value, bool, error) {
	if len(args) != 5 {
		return Value{}, false, fmt.Errorf("System.arraycopy argument mismatch")
	}
	sourceRef, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	sourceOffset, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	destinationRef, err := referenceArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	destinationOffset, err := intArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	length, err := intArgument(args, 4)
	if err != nil {
		return Value{}, false, err
	}
	source, sourceOK := vm.Object(sourceRef)
	destination, destinationOK := vm.Object(destinationRef)
	if !sourceOK || !destinationOK || source.Array == nil || destination.Array == nil {
		return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	if sourceOffset < 0 || destinationOffset < 0 || length < 0 ||
		int64(sourceOffset)+int64(length) > int64(len(source.Array.Elements)) ||
		int64(destinationOffset)+int64(length) > int64(len(destination.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
	}
	copy(
		destination.Array.Elements[destinationOffset:destinationOffset+length],
		source.Array.Elements[sourceOffset:sourceOffset+length],
	)
	return Value{}, false, nil
}

func nativeStreamRead(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.inputStream(receiver)
	if err != nil {
		return Value{}, false, err
	}
	if state.closed {
		return Value{}, false, vm.newThrowable("java/io/IOException", "stream closed")
	}
	arrayReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	object, ok := vm.Object(arrayReference)
	if !ok || object.Array == nil || object.Array.Descriptor != "[B" {
		return Value{}, false, fmt.Errorf("InputStream.read destination is not byte[]")
	}
	offset := int32(0)
	length := int32(len(object.Array.Elements))
	if len(args) == 3 {
		offset, err = intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		length, err = intArgument(args, 2)
		if err != nil {
			return Value{}, false, err
		}
	}
	if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(object.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	if state.offset >= len(state.data) {
		return IntValue(-1), true, nil
	}
	count := min(int(length), len(state.data)-state.offset)
	for index := range count {
		object.Array.Elements[int(offset)+index] = IntValue(
			int32(int8(state.data[state.offset+index])),
		)
	}
	state.offset += count
	return IntValue(int32(count)), true, nil
}

func nativeByteToCharConvert(
	_ context.Context,
	vm *VM,
	_ uint32,
	args []Value,
) (Value, bool, error) {
	sourceReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	sourceOffset, err := intArgument(args, 1)
	if err != nil {
		return Value{}, false, err
	}
	sourceEnd, err := intArgument(args, 2)
	if err != nil {
		return Value{}, false, err
	}
	destinationReference, err := referenceArgument(args, 3)
	if err != nil {
		return Value{}, false, err
	}
	destinationOffset, err := intArgument(args, 4)
	if err != nil {
		return Value{}, false, err
	}
	destinationEnd, err := intArgument(args, 5)
	if err != nil {
		return Value{}, false, err
	}
	source, sourceOK := vm.Object(sourceReference)
	destination, destinationOK := vm.Object(destinationReference)
	if !sourceOK || !destinationOK || source.Array == nil || destination.Array == nil ||
		source.Array.Descriptor != "[B" || destination.Array.Descriptor != "[C" {
		return Value{}, false, fmt.Errorf("ByteToCharConverter array type mismatch")
	}
	if sourceOffset < 0 || sourceEnd < sourceOffset ||
		int(sourceEnd) > len(source.Array.Elements) ||
		destinationOffset < 0 || destinationEnd < destinationOffset ||
		int(destinationEnd) > len(destination.Array.Elements) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	count := min(
		int(sourceEnd-sourceOffset),
		int(destinationEnd-destinationOffset),
	)
	for index := range count {
		value, intErr := source.Array.Elements[int(sourceOffset)+index].Int()
		if intErr != nil {
			return Value{}, false, intErr
		}
		destination.Array.Elements[int(destinationOffset)+index] =
			IntValue(value & 0xff)
	}
	return IntValue(int32(count)), true, nil
}

func nativeStreamWrite(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	state, err := vm.outputStream(receiver)
	if err != nil {
		return Value{}, false, err
	}
	var data []byte
	if len(args) == 1 && args[0].Kind == ValueInt {
		value, intErr := args[0].Int()
		if intErr != nil {
			return Value{}, false, intErr
		}
		data = []byte{byte(value)}
	} else {
		reference, refErr := referenceArgument(args, 0)
		if refErr != nil {
			return Value{}, false, refErr
		}
		data, err = vm.ByteArray(reference)
		if err != nil {
			return Value{}, false, err
		}
		if len(args) == 3 {
			offset, offsetErr := intArgument(args, 1)
			if offsetErr != nil {
				return Value{}, false, offsetErr
			}
			length, lengthErr := intArgument(args, 2)
			if lengthErr != nil {
				return Value{}, false, lengthErr
			}
			if offset < 0 || length < 0 ||
				int64(offset)+int64(length) > int64(len(data)) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			data = data[offset : offset+length]
		}
	}
	if err := vm.writeOutputStream(state, data); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func nativeReaderRead(
	_ context.Context,
	vm *VM,
	receiver uint32,
	args []Value,
) (Value, bool, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return Value{}, false, fmt.Errorf("invalid InputStreamReader")
	}
	reader, ok := object.Native.(*inputStreamReaderState)
	if !ok {
		return Value{}, false, fmt.Errorf("invalid InputStreamReader state")
	}
	stream, err := vm.inputStream(reader.stream)
	if err != nil {
		return Value{}, false, err
	}
	destinationReference, err := referenceArgument(args, 0)
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
	destination, ok := vm.Object(destinationReference)
	if !ok || destination.Array == nil || destination.Array.Descriptor != "[C" {
		return Value{}, false, fmt.Errorf("InputStreamReader destination is not char[]")
	}
	if offset < 0 || length < 0 ||
		int64(offset)+int64(length) > int64(len(destination.Array.Elements)) {
		return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	if stream.offset >= len(stream.data) {
		return IntValue(-1), true, nil
	}
	count := min(int(length), len(stream.data)-stream.offset)
	for index := range count {
		destination.Array.Elements[int(offset)+index] =
			IntValue(int32(stream.data[stream.offset+index]))
	}
	stream.offset += count
	return IntValue(int32(count)), true, nil
}

func (vm *VM) setNative(reference uint32, native any) error {
	object, ok := vm.Object(reference)
	if !ok {
		return fmt.Errorf("invalid object reference %d", reference)
	}
	object.Native = native
	return nil
}

func (vm *VM) stringBuffer(reference uint32) (*stringBufferState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid StringBuffer reference")
	}
	state, ok := object.Native.(*stringBufferState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a StringBuffer", reference)
	}
	return state, nil
}

func (vm *VM) inputStream(reference uint32) (*inputStreamState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid InputStream reference")
	}
	state, ok := object.Native.(*inputStreamState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an InputStream", reference)
	}
	if err := vm.refreshSocketInput(state); err != nil {
		return nil, err
	}
	return state, nil
}

func (vm *VM) recordStore(reference uint32) (*recordStoreState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid RecordStore reference")
	}
	state, ok := object.Native.(*recordStoreState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a RecordStore", reference)
	}
	return state, nil
}

func (vm *VM) rmsThrowable(err error) error {
	message := "record store operation failed"
	if err != nil {
		message = err.Error()
	}
	return vm.newThrowable(
		"javax/microedition/rms/RecordStoreException",
		message,
	)
}

func (vm *VM) xFile(reference uint32) (*xFileState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid XFile reference")
	}
	state, ok := object.Native.(*xFileState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an XFile", reference)
	}
	return state, nil
}

func (vm *VM) fileNameArgument(args []Value, index int) (string, error) {
	name, err := vm.stringArgument(args, index)
	if err != nil {
		return "", err
	}
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimPrefix(name, "file://")
	normalized, err := vm.services.Storage.NormalizePath(name)
	if err != nil {
		return "", vm.newThrowable("java/io/IOException", "invalid file path")
	}
	return normalized, nil
}

func (vm *VM) newXFile(args []Value) (*xFileState, error) {
	name, err := vm.fileNameArgument(args, 0)
	if err != nil {
		return nil, err
	}
	if _, err := intArgument(args, 1); err != nil {
		return nil, err
	}
	data, err := vm.services.Storage.ReadFile(shared.NamespacePrivate, name)
	if errors.Is(err, shared.ErrNotFound) {
		if err := vm.services.Storage.WriteFile(
			shared.NamespacePrivate,
			name,
			nil,
		); err != nil {
			return nil, err
		}
		data = nil
	} else if err != nil {
		return nil, err
	}
	return &xFileState{name: name, data: data}, nil
}

func (vm *VM) persistXFile(state *xFileState) error {
	if state == nil || state.name == "" {
		return nil
	}
	return vm.services.Storage.WriteFile(
		shared.NamespacePrivate,
		state.name,
		state.data,
	)
}

func (vm *VM) audioClip(reference uint32) (*audioClipState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid AudioClip reference")
	}
	state, ok := object.Native.(*audioClipState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an AudioClip", reference)
	}
	return state, nil
}

func (vm *VM) xTextField(reference uint32) (*xTextFieldState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid XTextField reference")
	}
	state, ok := object.Native.(*xTextFieldState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an XTextField", reference)
	}
	return state, nil
}

func (vm *VM) outputStream(reference uint32) (*outputStreamState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid OutputStream reference")
	}
	state, ok := object.Native.(*outputStreamState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an OutputStream", reference)
	}
	return state, nil
}

func (vm *VM) stringArgument(args []Value, index int) (string, error) {
	reference, err := referenceArgument(args, index)
	if err != nil {
		return "", err
	}
	if reference == 0 {
		return "", vm.newThrowable("java/lang/NullPointerException", "")
	}
	return vm.String(reference)
}

func referenceArgument(args []Value, index int) (uint32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("native argument %d is missing", index)
	}
	return args[index].Reference()
}

func intArgument(args []Value, index int) (int32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("native argument %d is missing", index)
	}
	return args[index].Int()
}

func (vm *VM) byteSliceArgument(args []Value) ([]byte, error) {
	reference, err := referenceArgument(args, 0)
	if err != nil {
		return nil, err
	}
	offset, err := intArgument(args, 1)
	if err != nil {
		return nil, err
	}
	length, err := intArgument(args, 2)
	if err != nil {
		return nil, err
	}
	data, err := vm.ByteArray(reference)
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(data)) {
		return nil, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	return append([]byte(nil), data[offset:offset+length]...), nil
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
