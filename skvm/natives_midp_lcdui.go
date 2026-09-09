package skvm

import (
	"context"
	"fmt"
	"unicode/utf16"
)

const (
	midpTitleField       = "$midp.title"
	midpTickerField      = "$midp.ticker"
	midpListenerField    = "$midp.listener"
	midpCommandsField    = "$midp.commands"
	midpLabelField       = "$midp.label"
	midpLayoutField      = "$midp.layout"
	midpPreferredWField  = "$midp.preferredWidth"
	midpPreferredHField  = "$midp.preferredHeight"
	midpDefaultCmdField  = "$midp.defaultCommand"
	midpTextField        = "$midp.text"
	midpMaxSizeField     = "$midp.maxSize"
	midpConstraintsField = "$midp.constraints"
	midpImageField       = "$midp.image"
	midpAltTextField     = "$midp.altText"
	midpAppearanceField  = "$midp.appearance"
	midpValueField       = "$midp.value"
	midpMaximumField     = "$midp.maximum"
	midpInteractiveField = "$midp.interactive"
	midpDateField        = "$midp.date"
	midpInputModeField   = "$midp.inputMode"
	midpItemsField       = "$midp.items"
	midpItemListener     = "$midp.itemListener"
	midpChoiceTypeField  = "$midp.choiceType"
	midpStringsField     = "$midp.strings"
	midpImagesField      = "$midp.images"
	midpFontsField       = "$midp.fonts"
	midpSelectedField    = "$midp.selected"
	midpFitPolicyField   = "$midp.fitPolicy"
	midpSelectCmdField   = "$midp.selectCommand"
	midpAlertTypeField   = "$midp.alertType"
	midpIndicatorField   = "$midp.indicator"
	midpTimeoutField     = "$midp.timeout"
)

func (vm *VM) installMIDPLCDUINatives() {
	vm.installDisplayableNatives()
	vm.installMIDPCanvasImageNatives()
	vm.installItemNatives()
	vm.installBasicItemNatives()
	vm.installTextComponentNatives()
	vm.installChoiceNatives()
	vm.installFormAlertNatives()
	vm.installMIDPLCDUIStatics()
}

func (vm *VM) installMIDPCanvasImageNatives() {
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "getKeyCode", "(I)I", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		action, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		keys := map[int32]int32{1: -1, 2: -3, 5: -4, 6: -2, 8: -5, 9: '1', 10: '3', 11: '7', 12: '9'}
		key, ok := keys[action]
		if !ok {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		return IntValue(key), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "getKeyName", "(I)Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		key, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		names := map[int32]string{-1: "UP", -2: "DOWN", -3: "LEFT", -4: "RIGHT", -5: "SELECT"}
		name, ok := names[key]
		if !ok && key >= '0' && key <= '9' {
			name, ok = string(rune(key)), true
		}
		if !ok {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		return ReferenceValue(vm.NewString(name)), true, nil
	})
	for _, name := range []string{"hasPointerEvents", "hasPointerMotionEvents", "hasRepeatEvents"} {
		vm.RegisterNative("javax/microedition/lcdui/Canvas", name, "()Z", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(0), true, nil })
	}
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "isDoubleBuffered", "()Z", nativeReturnOne)
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "setFullScreenMode", "(Z)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, setObjectField(vm, receiver, "$midp.fullScreen", args[0])
	})
	for _, name := range []string{"keyPressed", "keyReleased", "keyRepeated"} {
		vm.RegisterNative("javax/microedition/lcdui/Canvas", name, "(I)V", nativeVoid)
	}
	for _, name := range []string{"pointerDragged", "pointerPressed", "pointerReleased"} {
		vm.RegisterNative("javax/microedition/lcdui/Canvas", name, "(II)V", nativeVoid)
	}
	for _, name := range []string{"showNotify", "hideNotify"} {
		vm.RegisterNative("javax/microedition/lcdui/Canvas", name, "()V", nativeVoid)
	}
	vm.RegisterNative("javax/microedition/lcdui/Canvas", "sizeChanged", "(II)V", nativeVoid)

	vm.RegisterNative("javax/microedition/lcdui/Font", "getFont", "(I)Ljavax/microedition/lcdui/Font;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(vm.NewObject("javax/microedition/lcdui/Font", &fontState{font: vm.defaultFont})), true, nil
	})
	for _, method := range []struct{ name, field string }{
		{"getStyle", "\x00aram-font-style"},
		{"getSize", "\x00aram-font-size"},
		{"getFace", "\x00aram-font-face"},
	} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/Font", method.name, "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := vm.midpFontAttribute(receiver, method.field)
			return IntValue(value), err == nil, err
		})
	}
	for _, method := range []struct {
		name string
		mask int32
	}{
		{"isPlain", 0}, {"isBold", 1}, {"isItalic", 2}, {"isUnderlined", 4},
	} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/Font", method.name, "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			style, err := vm.midpFontAttribute(receiver, "\x00aram-font-style")
			if err != nil {
				return Value{}, false, err
			}
			matched := style&method.mask != 0
			if method.mask == 0 {
				matched = style == 0
			}
			if matched {
				return IntValue(1), true, nil
			}
			return IntValue(0), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/Font", "getBaselinePosition", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		font, err := vm.font(receiver)
		if err != nil {
			return Value{}, false, err
		}
		metrics, err := vm.services.Text.Metrics(vm.serviceOwner, font.font)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(metrics.Ascent), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Image", "isMutable", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if _, err := vm.image(receiver); err != nil {
			return Value{}, false, err
		}
		object, _ := vm.Object(receiver)
		mutable, _ := object.Fields["\x00aram-image-mutable"].Int()
		if mutable != 0 {
			return IntValue(1), true, nil
		}
		return IntValue(0), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Image", "createImage", "(Ljava/io/InputStream;)Ljavax/microedition/lcdui/Image;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		streamReference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if streamReference == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		stream, err := vm.inputStream(streamReference)
		if err != nil {
			return Value{}, false, err
		}
		data := append([]byte(nil), stream.data[stream.offset:]...)
		stream.offset = len(stream.data)
		reference, err := vm.newImage(data)
		return ReferenceValue(reference), true, err
	})
	vm.RegisterNative("javax/microedition/lcdui/Image", "createImage", "(Ljavax/microedition/lcdui/Image;)Ljavax/microedition/lcdui/Image;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		source, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		state, err := vm.image(source)
		if err != nil {
			return Value{}, false, err
		}
		return vm.copyImageRegion(state, 0, 0, int32(state.width), int32(state.height), transNone)
	})
	vm.RegisterNative("javax/microedition/lcdui/Image", "createImage", "(Ljavax/microedition/lcdui/Image;IIIII)Ljavax/microedition/lcdui/Image;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		sourceReference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		source, err := vm.image(sourceReference)
		if err != nil {
			return Value{}, false, err
		}
		values := [5]int32{}
		for index := range values {
			values[index], err = intArgument(args, index+1)
			if err != nil {
				return Value{}, false, err
			}
		}
		return vm.copyImageRegion(source, values[0], values[1], values[2], values[3], values[4])
	})
	vm.RegisterNative("javax/microedition/lcdui/Image", "createRGBImage", "([IIIZ)Ljavax/microedition/lcdui/Image;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		arrayReference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		arrayObject, ok := vm.Object(arrayReference)
		if !ok || arrayObject.Array == nil {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		width, _ := intArgument(args, 1)
		height, _ := intArgument(args, 2)
		alpha, _ := intArgument(args, 3)
		if width <= 0 || height <= 0 || int64(width)*int64(height) > int64(len(arrayObject.Array.Elements)) {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		state, err := vm.newImageState(int(width), int(height))
		if err != nil {
			return Value{}, false, err
		}
		for y := int32(0); y < height; y++ {
			for x := int32(0); x < width; x++ {
				packed, valueErr := arrayObject.Array.Elements[y*width+x].Int()
				if valueErr != nil {
					return Value{}, false, valueErr
				}
				color := uint32(packed)
				if alpha == 0 {
					color |= 0xff000000
				}
				if err := vm.services.Graphics.SetPixel(vm.serviceOwner, state.surface, x, y, skvmColor(color)); err != nil {
					return Value{}, false, err
				}
			}
		}
		return ReferenceValue(vm.NewObject("javax/microedition/lcdui/Image", state)), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Image", "getRGB", "([IIIIIII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.image(receiver)
		if err != nil {
			return Value{}, false, err
		}
		arrayReference, _ := referenceArgument(args, 0)
		arrayObject, ok := vm.Object(arrayReference)
		if !ok || arrayObject.Array == nil {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		values := [6]int32{}
		for index := range values {
			values[index], err = intArgument(args, index+1)
			if err != nil {
				return Value{}, false, err
			}
		}
		offset, scan, x, y, width, height := values[0], values[1], values[2], values[3], values[4], values[5]
		if width < 0 || height < 0 || x < 0 || y < 0 || x+width > int32(state.width) || y+height > int32(state.height) {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		for row := int32(0); row < height; row++ {
			for column := int32(0); column < width; column++ {
				index := offset + row*scan + column
				if index < 0 || int(index) >= len(arrayObject.Array.Elements) {
					return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
				}
				color, pixelErr := vm.services.Graphics.Pixel(vm.serviceOwner, state.surface, x+column, y+row)
				if pixelErr != nil {
					return Value{}, false, pixelErr
				}
				arrayObject.Array.Elements[index] = IntValue(int32(uint32(color.A)<<24 | uint32(color.R)<<16 | uint32(color.G)<<8 | uint32(color.B)))
			}
		}
		return Value{}, false, nil
	})
}

func (vm *VM) copyImageRegion(source *imageState, x, y, width, height, transform int32) (Value, bool, error) {
	if width <= 0 || height <= 0 || x < 0 || y < 0 || x+width > int32(source.width) || y+height > int32(source.height) || transform < transNone || transform > transMirrorRot90 {
		return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	destinationWidth, destinationHeight := width, height
	if midpRegionQuarterTurn(transform) {
		destinationWidth, destinationHeight = height, width
	}
	destination, err := vm.newImageState(int(destinationWidth), int(destinationHeight))
	if err != nil {
		return Value{}, false, err
	}
	for v := int32(0); v < destinationHeight; v++ {
		for u := int32(0); u < destinationWidth; u++ {
			sx, sy := midpRegionSource(transform, u, v, width, height)
			color, pixelErr := vm.services.Graphics.Pixel(vm.serviceOwner, source.surface, x+sx, y+sy)
			if pixelErr != nil {
				return Value{}, false, pixelErr
			}
			if pixelErr = vm.services.Graphics.SetPixel(vm.serviceOwner, destination.surface, u, v, color); pixelErr != nil {
				return Value{}, false, pixelErr
			}
		}
	}
	return ReferenceValue(vm.NewObject("javax/microedition/lcdui/Image", destination)), true, nil
}

func objectField(vm *VM, receiver uint32, field string) (Value, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return Value{}, fmt.Errorf("invalid object reference %d", receiver)
	}
	if value, ok := object.Fields[field]; ok {
		return value, nil
	}
	return ReferenceValue(0), nil
}

func setObjectField(vm *VM, receiver uint32, field string, value Value) error {
	object, ok := vm.Object(receiver)
	if !ok {
		return fmt.Errorf("invalid object reference %d", receiver)
	}
	object.Fields[field] = value
	return nil
}

func (vm *VM) objectArray(receiver uint32, field, descriptor string) (*Array, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return nil, fmt.Errorf("invalid object reference %d", receiver)
	}
	value, ok := object.Fields[field]
	if !ok {
		reference := vm.newArray(descriptor, nil)
		object.Fields[field] = ReferenceValue(reference)
		return vm.heap[reference].Array, nil
	}
	reference, err := value.Reference()
	if err != nil {
		return nil, err
	}
	arrayObject, ok := vm.Object(reference)
	if !ok || arrayObject.Array == nil {
		return nil, fmt.Errorf("invalid %s state array", field)
	}
	return arrayObject.Array, nil
}

func (vm *VM) installDisplayableNatives() {
	for _, method := range []struct{ name, descriptor, field string }{
		{"getTitle", "()Ljava/lang/String;", midpTitleField},
		{"getTicker", "()Ljavax/microedition/lcdui/Ticker;", midpTickerField},
	} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/Displayable", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := objectField(vm, receiver, method.field)
			return value, true, err
		})
	}
	for _, method := range []struct{ name, descriptor, field string }{
		{"setTitle", "(Ljava/lang/String;)V", midpTitleField},
		{"setTicker", "(Ljavax/microedition/lcdui/Ticker;)V", midpTickerField},
		{"setCommandListener", "(Ljavax/microedition/lcdui/CommandListener;)V", midpListenerField},
	} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/Displayable", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			if len(args) != 1 || args[0].Kind != ValueReference {
				return Value{}, false, fmt.Errorf("%s argument mismatch", method.name)
			}
			return Value{}, false, setObjectField(vm, receiver, method.field, args[0])
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/Displayable", "getWidth", "()I", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return IntValue(int32(vm.ScreenWidth)), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Displayable", "getHeight", "()I", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return IntValue(int32(vm.canvasHeight())), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Displayable", "isShown", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return IntValue(boolInt(vm.currentDisplay == receiver)), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Displayable", "sizeChanged", "(II)V", nativeVoid)
	for _, name := range []string{"addCommand", "removeCommand"} {
		name := name
		vm.RegisterNative("javax/microedition/lcdui/Displayable", name, "(Ljavax/microedition/lcdui/Command;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			command, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if command == 0 && name == "addCommand" {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			array, err := vm.objectArray(receiver, midpCommandsField, "[Ljavax/microedition/lcdui/Command;")
			if err != nil {
				return Value{}, false, err
			}
			for index, value := range array.Elements {
				reference, _ := value.Reference()
				if reference == command {
					if name == "removeCommand" {
						array.Elements = append(array.Elements[:index], array.Elements[index+1:]...)
					}
					return Value{}, false, nil
				}
			}
			if name == "addCommand" {
				array.Elements = append(array.Elements, ReferenceValue(command))
			}
			return Value{}, false, nil
		})
	}

	vm.RegisterNative("javax/microedition/lcdui/Display", "getCurrent", "()Ljavax/microedition/lcdui/Displayable;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(vm.currentDisplay), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Display", "setCurrent", "(Ljavax/microedition/lcdui/Alert;Ljavax/microedition/lcdui/Displayable;)V", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		alert, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		next, err := referenceArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if alert == 0 || next == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		if vm.IsInstance(next, "javax/microedition/lcdui/Alert") {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		_ = setObjectField(vm, alert, "$midp.nextDisplayable", ReferenceValue(next))
		vm.currentDisplay = alert
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Display", "setCurrentItem", "(Ljavax/microedition/lcdui/Item;)V", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		item, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if item == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		return Value{}, false, nil
	})
	for _, name := range []string{"flashBacklight", "vibrate"} {
		name := name
		vm.RegisterNative("javax/microedition/lcdui/Display", name, "(I)Z", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			duration, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if duration < 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			return IntValue(1), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/Display", "isColor", "()Z", nativeReturnOne)
	vm.RegisterNative("javax/microedition/lcdui/Display", "numColors", "()I", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(1 << 24), true, nil })
	vm.RegisterNative("javax/microedition/lcdui/Display", "numAlphaLevels", "()I", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(256), true, nil })
	vm.RegisterNative("javax/microedition/lcdui/Display", "getBorderStyle", "(Z)I", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(0), true, nil })
	vm.RegisterNative("javax/microedition/lcdui/Display", "getColor", "(I)I", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		which, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		colors := []int32{0xffffff, 0x000000, 0x000080, 0xffffff, 0x000000, 0xffffff}
		if which < 0 || int(which) >= len(colors) {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		return IntValue(colors[which]), true, nil
	})
	for _, name := range []string{"getBestImageWidth", "getBestImageHeight"} {
		name := name
		vm.RegisterNative("javax/microedition/lcdui/Display", name, "(I)I", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			kind, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if kind < 1 || kind > 3 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			if name == "getBestImageWidth" {
				return IntValue(int32(vm.ScreenWidth)), true, nil
			}
			return IntValue(int32(vm.canvasHeight())), true, nil
		})
	}
}

func boolInt(value bool) int32 {
	if value {
		return 1
	}
	return 0
}

func nativeReturnOne(context.Context, *VM, uint32, []Value) (Value, bool, error) {
	return IntValue(1), true, nil
}

func (vm *VM) installItemNatives() {
	for _, method := range []struct {
		name, descriptor, field string
		fallback                int32
	}{
		{"getLayout", "()I", midpLayoutField, 0}, {"getPreferredWidth", "()I", midpPreferredWField, -1}, {"getPreferredHeight", "()I", midpPreferredHField, -1},
	} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/Item", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Item")
			}
			if value, ok := object.Fields[method.field]; ok {
				return value, true, nil
			}
			return IntValue(method.fallback), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/Item", "getMinimumWidth", "()I", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(0), true, nil })
	vm.RegisterNative("javax/microedition/lcdui/Item", "getMinimumHeight", "()I", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(0), true, nil })
	vm.RegisterNative("javax/microedition/lcdui/Item", "getLabel", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := objectField(vm, receiver, midpLabelField)
		return value, true, err
	})
	for _, method := range []struct{ name, descriptor, field string }{
		{"setLabel", "(Ljava/lang/String;)V", midpLabelField}, {"setDefaultCommand", "(Ljavax/microedition/lcdui/Command;)V", midpDefaultCmdField}, {"setItemCommandListener", "(Ljavax/microedition/lcdui/ItemCommandListener;)V", midpListenerField},
	} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/Item", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			return Value{}, false, setObjectField(vm, receiver, method.field, args[0])
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/Item", "setLayout", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, setObjectField(vm, receiver, midpLayoutField, args[0])
	})
	vm.RegisterNative("javax/microedition/lcdui/Item", "setPreferredSize", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		width, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		height, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if width < -1 || height < -1 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		_ = setObjectField(vm, receiver, midpPreferredWField, IntValue(width))
		_ = setObjectField(vm, receiver, midpPreferredHField, IntValue(height))
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Item", "notifyStateChanged", "()V", nativeVoid)
	for _, name := range []string{"addCommand", "removeCommand"} {
		name := name
		vm.RegisterNative("javax/microedition/lcdui/Item", name, "(Ljavax/microedition/lcdui/Command;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			command, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if command == 0 && name == "addCommand" {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			array, err := vm.objectArray(receiver, midpCommandsField, "[Ljavax/microedition/lcdui/Command;")
			if err != nil {
				return Value{}, false, err
			}
			for index, value := range array.Elements {
				ref, _ := value.Reference()
				if ref == command {
					if name == "removeCommand" {
						array.Elements = append(array.Elements[:index], array.Elements[index+1:]...)
					}
					return Value{}, false, nil
				}
			}
			if name == "addCommand" {
				array.Elements = append(array.Elements, ReferenceValue(command))
			}
			return Value{}, false, nil
		})
	}
}

func (vm *VM) initializeItem(receiver uint32, label Value) {
	_ = setObjectField(vm, receiver, midpLabelField, label)
	_ = setObjectField(vm, receiver, midpPreferredWField, IntValue(-1))
	_ = setObjectField(vm, receiver, midpPreferredHField, IntValue(-1))
}

func (vm *VM) installBasicItemNatives() {
	vm.RegisterNative("javax/microedition/lcdui/Command", "<init>", "(Ljava/lang/String;II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, vm.initCommand(receiver, args[0], args[0], args[1], args[2])
	})
	vm.RegisterNative("javax/microedition/lcdui/Command", "<init>", "(Ljava/lang/String;Ljava/lang/String;II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, vm.initCommand(receiver, args[0], args[1], args[2], args[3])
	})
	for _, method := range []struct{ name, descriptor, field string }{{"getLabel", "()Ljava/lang/String;", "$midp.shortLabel"}, {"getLongLabel", "()Ljava/lang/String;", "$midp.longLabel"}, {"getCommandType", "()I", "$midp.commandType"}, {"getPriority", "()I", "$midp.priority"}} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/Command", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := objectField(vm, receiver, method.field)
			return value, true, err
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/Ticker", "<init>", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		ref, _ := args[0].Reference()
		if ref == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		return Value{}, false, setObjectField(vm, receiver, midpTextField, args[0])
	})
	vm.RegisterNative("javax/microedition/lcdui/Ticker", "getString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := objectField(vm, receiver, midpTextField)
		return value, true, err
	})
	vm.RegisterNative("javax/microedition/lcdui/Ticker", "setString", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		ref, _ := args[0].Reference()
		if ref == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		return Value{}, false, setObjectField(vm, receiver, midpTextField, args[0])
	})

	vm.RegisterNative("javax/microedition/lcdui/StringItem", "<init>", "(Ljava/lang/String;Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		vm.initializeItem(receiver, args[0])
		_ = setObjectField(vm, receiver, midpTextField, args[1])
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/StringItem", "<init>", "(Ljava/lang/String;Ljava/lang/String;I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		vm.initializeItem(receiver, args[0])
		_ = setObjectField(vm, receiver, midpTextField, args[1])
		_ = setObjectField(vm, receiver, midpAppearanceField, args[2])
		return Value{}, false, nil
	})
	vm.registerReferenceProperty("javax/microedition/lcdui/StringItem", "Text", "Ljava/lang/String;", midpTextField)
	vm.registerReferenceProperty("javax/microedition/lcdui/StringItem", "Font", "Ljavax/microedition/lcdui/Font;", midpFontsField)
	vm.registerIntGetter("javax/microedition/lcdui/StringItem", "getAppearanceMode", midpAppearanceField, 0)

	for _, descriptor := range []string{"(Ljava/lang/String;Ljavax/microedition/lcdui/Image;ILjava/lang/String;)V", "(Ljava/lang/String;Ljavax/microedition/lcdui/Image;ILjava/lang/String;I)V"} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/lcdui/ImageItem", "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			vm.initializeItem(receiver, args[0])
			_ = setObjectField(vm, receiver, midpImageField, args[1])
			_ = setObjectField(vm, receiver, midpLayoutField, args[2])
			_ = setObjectField(vm, receiver, midpAltTextField, args[3])
			if len(args) > 4 {
				_ = setObjectField(vm, receiver, midpAppearanceField, args[4])
			}
			return Value{}, false, nil
		})
	}
	vm.registerReferenceProperty("javax/microedition/lcdui/ImageItem", "Image", "Ljavax/microedition/lcdui/Image;", midpImageField)
	vm.registerReferenceProperty("javax/microedition/lcdui/ImageItem", "AltText", "Ljava/lang/String;", midpAltTextField)
	vm.registerIntGetter("javax/microedition/lcdui/ImageItem", "getAppearanceMode", midpAppearanceField, 0)

	vm.RegisterNative("javax/microedition/lcdui/Spacer", "<init>", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, vm.setSpacerSize(receiver, args)
	})
	vm.RegisterNative("javax/microedition/lcdui/Spacer", "setMinimumSize", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, vm.setSpacerSize(receiver, args)
	})

	vm.RegisterNative("javax/microedition/lcdui/Gauge", "<init>", "(Ljava/lang/String;ZII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		vm.initializeItem(receiver, args[0])
		_ = setObjectField(vm, receiver, midpInteractiveField, args[1])
		return Value{}, false, vm.setGauge(receiver, args[2], args[3])
	})
	vm.registerIntGetter("javax/microedition/lcdui/Gauge", "getMaxValue", midpMaximumField, 0)
	vm.registerIntGetter("javax/microedition/lcdui/Gauge", "getValue", midpValueField, 0)
	vm.RegisterNative("javax/microedition/lcdui/Gauge", "isInteractive", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := objectField(vm, receiver, midpInteractiveField)
		return value, true, err
	})
	vm.RegisterNative("javax/microedition/lcdui/Gauge", "setValue", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		maximum, _ := objectField(vm, receiver, midpMaximumField)
		return Value{}, false, vm.setGauge(receiver, maximum, args[0])
	})
	vm.RegisterNative("javax/microedition/lcdui/Gauge", "setMaxValue", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		value, _ := objectField(vm, receiver, midpValueField)
		return Value{}, false, vm.setGauge(receiver, args[0], value)
	})

	for _, descriptor := range []string{"(Ljava/lang/String;I)V", "(Ljava/lang/String;ILjava/util/TimeZone;)V"} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/lcdui/DateField", "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			vm.initializeItem(receiver, args[0])
			return Value{}, false, setObjectField(vm, receiver, midpInputModeField, args[1])
		})
	}
	vm.registerReferenceProperty("javax/microedition/lcdui/DateField", "Date", "Ljava/util/Date;", midpDateField)
	vm.registerIntGetter("javax/microedition/lcdui/DateField", "getInputMode", midpInputModeField, 0)
	vm.RegisterNative("javax/microedition/lcdui/DateField", "setInputMode", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		mode, _ := intArgument(args, 0)
		if mode < 1 || mode > 3 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		return Value{}, false, setObjectField(vm, receiver, midpInputModeField, args[0])
	})
	vm.RegisterNative("javax/microedition/lcdui/CustomItem", "<init>", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		vm.initializeItem(receiver, args[0])
		return Value{}, false, nil
	})
	for _, descriptor := range []string{"()V", "(IIII)V"} {
		vm.RegisterNative("javax/microedition/lcdui/CustomItem", "repaint", descriptor, nativeVoid)
	}
	vm.RegisterNative("javax/microedition/lcdui/CustomItem", "invalidate", "()V", nativeVoid)
	vm.RegisterNative("javax/microedition/lcdui/CustomItem", "getInteractionModes", "()I", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(0x1f), true, nil })
	vm.RegisterNative("javax/microedition/lcdui/CustomItem", "getGameAction", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		native, ok := vm.natives[nativeKey{"javax/microedition/lcdui/Canvas", "getGameAction", "(I)I"}]
		if !ok {
			return args[0], true, nil
		}
		return native(context.Background(), vm, receiver, args)
	})
	for _, method := range []struct{ name, descriptor string }{
		{"sizeChanged", "(II)V"}, {"traverseOut", "()V"},
		{"keyPressed", "(I)V"}, {"keyReleased", "(I)V"}, {"keyRepeated", "(I)V"},
		{"pointerPressed", "(II)V"}, {"pointerReleased", "(II)V"}, {"pointerDragged", "(II)V"},
		{"showNotify", "()V"}, {"hideNotify", "()V"},
	} {
		vm.RegisterNative("javax/microedition/lcdui/CustomItem", method.name, method.descriptor, nativeVoid)
	}
	vm.RegisterNative("javax/microedition/lcdui/CustomItem", "traverse", "(III[I)Z", func(context.Context, *VM, uint32, []Value) (Value, bool, error) {
		return IntValue(0), true, nil
	})
}

func (vm *VM) midpFontAttribute(reference uint32, field string) (int32, error) {
	font, err := vm.font(reference)
	if err != nil {
		return 0, err
	}
	object, _ := vm.Object(reference)
	if value, ok := object.Fields[field]; ok {
		return value.Int()
	}
	for _, saved := range vm.services.Text.Snapshot().Fonts {
		if saved.ID != font.font || saved.Owner != vm.serviceOwner {
			continue
		}
		switch field {
		case "\x00aram-font-style":
			return int32(saved.Descriptor.Style), nil
		case "\x00aram-font-size":
			if saved.Descriptor.Size <= 8 {
				return 8, nil
			}
			if saved.Descriptor.Size >= 16 {
				return 16, nil
			}
		}
		return 0, nil
	}
	return 0, fmt.Errorf("font service is unavailable")
}

func (vm *VM) initCommand(receiver uint32, short, long, kind, priority Value) error {
	ref, _ := short.Reference()
	if ref == 0 {
		return vm.newThrowable("java/lang/NullPointerException", "")
	}
	_ = setObjectField(vm, receiver, "$midp.shortLabel", short)
	_ = setObjectField(vm, receiver, "$midp.longLabel", long)
	_ = setObjectField(vm, receiver, "$midp.commandType", kind)
	return setObjectField(vm, receiver, "$midp.priority", priority)
}

func (vm *VM) registerReferenceProperty(class, suffix, descriptor, field string) {
	vm.RegisterNative(class, "get"+suffix, "()"+descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := objectField(vm, receiver, field)
		return value, true, err
	})
	vm.RegisterNative(class, "set"+suffix, "("+descriptor+")V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, setObjectField(vm, receiver, field, args[0])
	})
}

func (vm *VM) registerIntGetter(class, name, field string, fallback int32) {
	vm.RegisterNative(class, name, "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid %s", class)
		}
		if value, ok := object.Fields[field]; ok {
			return value, true, nil
		}
		return IntValue(fallback), true, nil
	})
}

func (vm *VM) setSpacerSize(receiver uint32, args []Value) error {
	width, err := intArgument(args, 0)
	if err != nil {
		return err
	}
	height, err := intArgument(args, 1)
	if err != nil {
		return err
	}
	if width < 0 || height < 0 {
		return vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	_ = setObjectField(vm, receiver, "$midp.minimumWidth", IntValue(width))
	return setObjectField(vm, receiver, "$midp.minimumHeight", IntValue(height))
}

func (vm *VM) setGauge(receiver uint32, maximumValue, valueValue Value) error {
	maximum, err := maximumValue.Int()
	if err != nil {
		return err
	}
	value, err := valueValue.Int()
	if err != nil {
		return err
	}
	interactiveValue, _ := objectField(vm, receiver, midpInteractiveField)
	interactive, _ := interactiveValue.Int()
	if maximum <= 0 && !(interactive == 0 && maximum == -1) {
		return vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	if maximum > 0 {
		if value < 0 {
			value = 0
		}
		if value > maximum {
			value = maximum
		}
	} else if value < 0 || value > 3 {
		return vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	_ = setObjectField(vm, receiver, midpMaximumField, IntValue(maximum))
	return setObjectField(vm, receiver, midpValueField, IntValue(value))
}

func (vm *VM) installTextComponentNatives() {
	for _, class := range []string{"javax/microedition/lcdui/TextField", "javax/microedition/lcdui/TextBox"} {
		class := class
		descriptor := "(Ljava/lang/String;Ljava/lang/String;II)V"
		vm.RegisterNative(class, "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			if class == "javax/microedition/lcdui/TextField" {
				vm.initializeItem(receiver, args[0])
			} else {
				_ = setObjectField(vm, receiver, midpTitleField, args[0])
			}
			maximum, _ := intArgument(args, 2)
			if maximum <= 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			_ = setObjectField(vm, receiver, midpMaxSizeField, args[2])
			_ = setObjectField(vm, receiver, midpConstraintsField, args[3])
			return Value{}, false, vm.setText(receiver, args[1])
		})
		vm.RegisterNative(class, "getString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := objectField(vm, receiver, midpTextField)
			return value, true, err
		})
		vm.RegisterNative(class, "setString", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			return Value{}, false, vm.setText(receiver, args[0])
		})
		vm.RegisterNative(class, "size", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			text, err := vm.textValue(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(utf16.Encode([]rune(text))))), true, nil
		})
		vm.RegisterNative(class, "getCaretPosition", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			text, err := vm.textValue(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(utf16.Encode([]rune(text))))), true, nil
		})
		vm.registerIntGetter(class, "getMaxSize", midpMaxSizeField, 1)
		vm.registerIntGetter(class, "getConstraints", midpConstraintsField, 0)
		vm.RegisterNative(class, "setConstraints", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			return Value{}, false, setObjectField(vm, receiver, midpConstraintsField, args[0])
		})
		vm.RegisterNative(class, "setInitialInputMode", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			return Value{}, false, setObjectField(vm, receiver, "$midp.initialInputMode", args[0])
		})
		vm.RegisterNative(class, "setMaxSize", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			maximum, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if maximum <= 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			text, _ := vm.textValue(receiver)
			units := utf16.Encode([]rune(text))
			if len(units) > int(maximum) {
				_ = vm.setTextUnits(receiver, units[:maximum])
			}
			_ = setObjectField(vm, receiver, midpMaxSizeField, IntValue(maximum))
			return IntValue(maximum), true, nil
		})
		vm.RegisterNative(class, "getChars", "([C)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			destination, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			object, ok := vm.Object(destination)
			if !ok || object.Array == nil {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			text, _ := vm.textValue(receiver)
			units := utf16.Encode([]rune(text))
			if len(object.Array.Elements) < len(units) {
				return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
			}
			for i, unit := range units {
				object.Array.Elements[i] = IntValue(int32(unit))
			}
			return IntValue(int32(len(units))), true, nil
		})
		vm.RegisterNative(class, "setChars", "([CII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			units, err := vm.charSubrange(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setTextUnits(receiver, units)
		})
		vm.RegisterNative(class, "insert", "(Ljava/lang/String;I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			source, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			position, err := intArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.insertText(receiver, utf16.Encode([]rune(source)), position)
		})
		vm.RegisterNative(class, "insert", "([CIII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			units, err := vm.charSubrange(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			position, err := intArgument(args, 3)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.insertText(receiver, units, position)
		})
		vm.RegisterNative(class, "delete", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			offset, _ := intArgument(args, 0)
			length, _ := intArgument(args, 1)
			text, _ := vm.textValue(receiver)
			units := utf16.Encode([]rune(text))
			if offset < 0 || length < 0 || int(offset+length) > len(units) {
				return Value{}, false, vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
			}
			units = append(units[:offset], units[offset+length:]...)
			return Value{}, false, vm.setTextUnits(receiver, units)
		})
	}
}

func (vm *VM) textValue(receiver uint32) (string, error) {
	value, err := objectField(vm, receiver, midpTextField)
	if err != nil {
		return "", err
	}
	ref, _ := value.Reference()
	if ref == 0 {
		return "", nil
	}
	return vm.String(ref)
}
func (vm *VM) setText(receiver uint32, value Value) error {
	ref, err := value.Reference()
	if err != nil {
		return err
	}
	text := ""
	if ref != 0 {
		text, err = vm.String(ref)
		if err != nil {
			return err
		}
	}
	maximumValue, _ := objectField(vm, receiver, midpMaxSizeField)
	maximum, _ := maximumValue.Int()
	if maximum > 0 && len(utf16.Encode([]rune(text))) > int(maximum) {
		return vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	return setObjectField(vm, receiver, midpTextField, ReferenceValue(vm.NewString(text)))
}
func (vm *VM) setTextUnits(receiver uint32, units []uint16) error {
	return vm.setText(receiver, ReferenceValue(vm.NewString(string(utf16.Decode(units)))))
}
func (vm *VM) insertText(receiver uint32, source []uint16, position int32) error {
	text, _ := vm.textValue(receiver)
	units := utf16.Encode([]rune(text))
	if position < 0 || int(position) > len(units) {
		return vm.newThrowable("java/lang/StringIndexOutOfBoundsException", "")
	}
	units = append(units, make([]uint16, len(source))...)
	copy(units[int(position)+len(source):], units[int(position):len(units)-len(source)])
	copy(units[position:], source)
	return vm.setTextUnits(receiver, units)
}
func (vm *VM) charSubrange(args []Value, start int) ([]uint16, error) {
	reference, err := referenceArgument(args, start)
	if err != nil {
		return nil, err
	}
	if reference == 0 {
		return nil, vm.newThrowable("java/lang/NullPointerException", "")
	}
	object, ok := vm.Object(reference)
	if !ok || object.Array == nil {
		return nil, fmt.Errorf("not a char array")
	}
	offset, _ := intArgument(args, start+1)
	length, _ := intArgument(args, start+2)
	if offset < 0 || length < 0 || int(offset+length) > len(object.Array.Elements) {
		return nil, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
	}
	result := make([]uint16, length)
	for i := range result {
		value, _ := object.Array.Elements[int(offset)+i].Int()
		result[i] = uint16(value)
	}
	return result, nil
}

func (vm *VM) installChoiceNatives() {
	for _, class := range []string{"javax/microedition/lcdui/ChoiceGroup", "javax/microedition/lcdui/List"} {
		class := class
		for _, descriptor := range []string{"(Ljava/lang/String;I)V", "(Ljava/lang/String;I[Ljava/lang/String;[Ljavax/microedition/lcdui/Image;)V"} {
			descriptor := descriptor
			vm.RegisterNative(class, "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				if class == "javax/microedition/lcdui/ChoiceGroup" {
					vm.initializeItem(receiver, args[0])
				} else {
					_ = setObjectField(vm, receiver, midpTitleField, args[0])
				}
				_ = setObjectField(vm, receiver, midpChoiceTypeField, args[1])
				if len(args) == 4 {
					return Value{}, false, vm.initChoices(receiver, args[2], args[3])
				}
				return Value{}, false, vm.initChoices(receiver, ReferenceValue(vm.newArray("[Ljava/lang/String;", nil)), ReferenceValue(0))
			})
		}
		vm.RegisterNative(class, "size", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			array, err := vm.objectArray(receiver, midpStringsField, "[Ljava/lang/String;")
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(array.Elements))), true, nil
		})
		for _, method := range []struct{ name, descriptor, field string }{{"getString", "(I)Ljava/lang/String;", midpStringsField}, {"getImage", "(I)Ljavax/microedition/lcdui/Image;", midpImagesField}, {"getFont", "(I)Ljavax/microedition/lcdui/Font;", midpFontsField}} {
			method := method
			vm.RegisterNative(class, method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				index, _ := intArgument(args, 0)
				array, err := vm.objectArray(receiver, method.field, "[Ljava/lang/Object;")
				if err != nil {
					return Value{}, false, err
				}
				if index < 0 || int(index) >= len(array.Elements) {
					return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
				}
				return array.Elements[index], true, nil
			})
		}
		vm.RegisterNative(class, "append", "(Ljava/lang/String;Ljavax/microedition/lcdui/Image;)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, err := vm.insertChoice(receiver, -1, args[0], args[1])
			return IntValue(index), true, err
		})
		vm.RegisterNative(class, "insert", "(ILjava/lang/String;Ljavax/microedition/lcdui/Image;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, _ := intArgument(args, 0)
			_, err := vm.insertChoice(receiver, index, args[1], args[2])
			return Value{}, false, err
		})
		vm.RegisterNative(class, "delete", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, _ := intArgument(args, 0)
			return Value{}, false, vm.deleteChoice(receiver, index)
		})
		vm.RegisterNative(class, "deleteAll", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			for _, field := range []string{midpStringsField, midpImagesField, midpFontsField, midpSelectedField} {
				array, _ := vm.objectArray(receiver, field, "[Ljava/lang/Object;")
				array.Elements = nil
			}
			return Value{}, false, nil
		})
		vm.RegisterNative(class, "set", "(ILjava/lang/String;Ljavax/microedition/lcdui/Image;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, _ := intArgument(args, 0)
			stringsArray, _ := vm.objectArray(receiver, midpStringsField, "[Ljava/lang/String;")
			images, _ := vm.objectArray(receiver, midpImagesField, "[Ljavax/microedition/lcdui/Image;")
			ref, _ := args[1].Reference()
			if ref == 0 {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			if index < 0 || int(index) >= len(stringsArray.Elements) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			stringsArray.Elements[index], images.Elements[index] = args[1], args[2]
			return Value{}, false, nil
		})
		vm.RegisterNative(class, "isSelected", "(I)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, _ := intArgument(args, 0)
			selected, _ := vm.objectArray(receiver, midpSelectedField, "[Z")
			if index < 0 || int(index) >= len(selected.Elements) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			return selected.Elements[index], true, nil
		})
		vm.RegisterNative(class, "getSelectedIndex", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			selected, _ := vm.objectArray(receiver, midpSelectedField, "[Z")
			for i, value := range selected.Elements {
				on, _ := value.Int()
				if on != 0 {
					return IntValue(int32(i)), true, nil
				}
			}
			return IntValue(-1), true, nil
		})
		vm.RegisterNative(class, "setSelectedIndex", "(IZ)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, _ := intArgument(args, 0)
			on, _ := intArgument(args, 1)
			return Value{}, false, vm.setChoiceSelected(receiver, index, on != 0)
		})
		vm.RegisterNative(class, "getSelectedFlags", "([Z)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			destination, _ := referenceArgument(args, 0)
			object, ok := vm.Object(destination)
			if !ok || object.Array == nil {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			selected, _ := vm.objectArray(receiver, midpSelectedField, "[Z")
			count := 0
			for i := range object.Array.Elements {
				value := IntValue(0)
				if i < len(selected.Elements) {
					value = selected.Elements[i]
					on, _ := value.Int()
					if on != 0 {
						count++
					}
				}
				object.Array.Elements[i] = value
			}
			return IntValue(int32(count)), true, nil
		})
		vm.RegisterNative(class, "setSelectedFlags", "([Z)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			source, _ := referenceArgument(args, 0)
			object, ok := vm.Object(source)
			if !ok || object.Array == nil {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			selected, _ := vm.objectArray(receiver, midpSelectedField, "[Z")
			for i := range selected.Elements {
				selected.Elements[i] = IntValue(0)
				if i < len(object.Array.Elements) {
					value, _ := object.Array.Elements[i].Int()
					if value != 0 {
						_ = vm.setChoiceSelected(receiver, int32(i), true)
					}
				}
			}
			return Value{}, false, nil
		})
		vm.RegisterNative(class, "setFont", "(ILjavax/microedition/lcdui/Font;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, _ := intArgument(args, 0)
			fonts, _ := vm.objectArray(receiver, midpFontsField, "[Ljavax/microedition/lcdui/Font;")
			if index < 0 || int(index) >= len(fonts.Elements) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			fonts.Elements[index] = args[1]
			return Value{}, false, nil
		})
		vm.registerIntGetter(class, "getFitPolicy", midpFitPolicyField, 0)
		vm.RegisterNative(class, "setFitPolicy", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			policy, _ := intArgument(args, 0)
			if policy < 0 || policy > 2 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			return Value{}, false, setObjectField(vm, receiver, midpFitPolicyField, args[0])
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/List", "setSelectCommand", "(Ljavax/microedition/lcdui/Command;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, setObjectField(vm, receiver, midpSelectCmdField, args[0])
	})
}

func (vm *VM) initChoices(receiver uint32, stringsValue, imagesValue Value) error {
	stringsRef, _ := stringsValue.Reference()
	stringsObject, ok := vm.Object(stringsRef)
	if !ok || stringsObject.Array == nil {
		return vm.newThrowable("java/lang/NullPointerException", "")
	}
	count := len(stringsObject.Array.Elements)
	for _, value := range stringsObject.Array.Elements {
		ref, _ := value.Reference()
		if ref == 0 {
			return vm.newThrowable("java/lang/NullPointerException", "")
		}
	}
	images := make([]Value, count)
	fonts := make([]Value, count)
	selected := make([]Value, count)
	for index := range count {
		images[index] = ReferenceValue(0)
		fonts[index] = ReferenceValue(0)
		selected[index] = IntValue(0)
	}
	imagesRef, _ := imagesValue.Reference()
	if imagesRef != 0 {
		imagesObject, ok := vm.Object(imagesRef)
		if !ok || imagesObject.Array == nil || len(imagesObject.Array.Elements) != count {
			return vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		copy(images, imagesObject.Array.Elements)
	}
	stringsCopy := append([]Value(nil), stringsObject.Array.Elements...)
	object, _ := vm.Object(receiver)
	object.Fields[midpStringsField] = ReferenceValue(vm.newArray("[Ljava/lang/String;", stringsCopy))
	object.Fields[midpImagesField] = ReferenceValue(vm.newArray("[Ljavax/microedition/lcdui/Image;", images))
	object.Fields[midpFontsField] = ReferenceValue(vm.newArray("[Ljavax/microedition/lcdui/Font;", fonts))
	if count > 0 {
		selected[0] = IntValue(1)
	}
	object.Fields[midpSelectedField] = ReferenceValue(vm.newArray("[Z", selected))
	return nil
}
func (vm *VM) insertChoice(receiver uint32, index int32, text, image Value) (int32, error) {
	ref, _ := text.Reference()
	if ref == 0 {
		return 0, vm.newThrowable("java/lang/NullPointerException", "")
	}
	stringsArray, _ := vm.objectArray(receiver, midpStringsField, "[Ljava/lang/String;")
	if index == -1 {
		index = int32(len(stringsArray.Elements))
	}
	if index < 0 || int(index) > len(stringsArray.Elements) {
		return 0, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	for _, pair := range []struct {
		field, descriptor string
		value             Value
	}{{midpStringsField, "[Ljava/lang/String;", text}, {midpImagesField, "[Ljavax/microedition/lcdui/Image;", image}, {midpFontsField, "[Ljavax/microedition/lcdui/Font;", ReferenceValue(0)}, {midpSelectedField, "[Z", IntValue(0)}} {
		array, _ := vm.objectArray(receiver, pair.field, pair.descriptor)
		array.Elements = append(array.Elements, Value{})
		copy(array.Elements[index+1:], array.Elements[index:len(array.Elements)-1])
		array.Elements[index] = pair.value
	}
	stringsArray, _ = vm.objectArray(receiver, midpStringsField, "[Ljava/lang/String;")
	if len(stringsArray.Elements) == 1 {
		_ = vm.setChoiceSelected(receiver, 0, true)
	}
	return index, nil
}
func (vm *VM) deleteChoice(receiver uint32, index int32) error {
	stringsArray, _ := vm.objectArray(receiver, midpStringsField, "[Ljava/lang/String;")
	if index < 0 || int(index) >= len(stringsArray.Elements) {
		return vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	for _, field := range []string{midpStringsField, midpImagesField, midpFontsField, midpSelectedField} {
		array, _ := vm.objectArray(receiver, field, "[Ljava/lang/Object;")
		array.Elements = append(array.Elements[:index], array.Elements[index+1:]...)
	}
	if len(stringsArray.Elements) > 0 {
		selected, _ := vm.objectArray(receiver, midpSelectedField, "[Z")
		any := false
		for _, value := range selected.Elements {
			on, _ := value.Int()
			any = any || on != 0
		}
		if !any {
			selected.Elements[0] = IntValue(1)
		}
	}
	return nil
}
func (vm *VM) setChoiceSelected(receiver uint32, index int32, on bool) error {
	selected, _ := vm.objectArray(receiver, midpSelectedField, "[Z")
	if index < 0 || int(index) >= len(selected.Elements) {
		return vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	kindValue, _ := objectField(vm, receiver, midpChoiceTypeField)
	kind, _ := kindValue.Int()
	if kind != 2 && on {
		for i := range selected.Elements {
			selected.Elements[i] = IntValue(0)
		}
	}
	if kind == 2 || on {
		selected.Elements[index] = IntValue(boolInt(on))
	}
	return nil
}

func (vm *VM) installFormAlertNatives() {
	for _, descriptor := range []string{"(Ljava/lang/String;)V", "(Ljava/lang/String;[Ljavax/microedition/lcdui/Item;)V"} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/lcdui/Form", "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			_ = setObjectField(vm, receiver, midpTitleField, args[0])
			items := []Value{}
			if len(args) == 2 {
				ref, _ := args[1].Reference()
				object, ok := vm.Object(ref)
				if !ok || object.Array == nil {
					return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
				}
				items = append(items, object.Array.Elements...)
				for _, item := range items {
					itemRef, _ := item.Reference()
					if itemRef == 0 {
						return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
					}
				}
			}
			return Value{}, false, setObjectField(vm, receiver, midpItemsField, ReferenceValue(vm.newArray("[Ljavax/microedition/lcdui/Item;", items)))
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/Form", "size", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		items, _ := vm.objectArray(receiver, midpItemsField, "[Ljavax/microedition/lcdui/Item;")
		return IntValue(int32(len(items.Elements))), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "get", "(I)Ljavax/microedition/lcdui/Item;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, _ := intArgument(args, 0)
		items, _ := vm.objectArray(receiver, midpItemsField, "[Ljavax/microedition/lcdui/Item;")
		if index < 0 || int(index) >= len(items.Elements) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		return items.Elements[index], true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "append", "(Ljavax/microedition/lcdui/Item;)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return vm.appendFormItem(receiver, args[0])
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "append", "(Ljava/lang/String;)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		ref, _ := args[0].Reference()
		if ref == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		item := vm.NewObject("javax/microedition/lcdui/StringItem", nil)
		vm.initializeItem(item, ReferenceValue(0))
		_ = setObjectField(vm, item, midpTextField, args[0])
		return vm.appendFormItem(receiver, ReferenceValue(item))
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "append", "(Ljavax/microedition/lcdui/Image;)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		ref, _ := args[0].Reference()
		if ref == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		item := vm.NewObject("javax/microedition/lcdui/ImageItem", nil)
		vm.initializeItem(item, ReferenceValue(0))
		_ = setObjectField(vm, item, midpImageField, args[0])
		return vm.appendFormItem(receiver, ReferenceValue(item))
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "insert", "(ILjavax/microedition/lcdui/Item;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, _ := intArgument(args, 0)
		ref, _ := args[1].Reference()
		if ref == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		items, _ := vm.objectArray(receiver, midpItemsField, "[Ljavax/microedition/lcdui/Item;")
		if index < 0 || int(index) > len(items.Elements) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		items.Elements = append(items.Elements, Value{})
		copy(items.Elements[index+1:], items.Elements[index:len(items.Elements)-1])
		items.Elements[index] = args[1]
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "set", "(ILjavax/microedition/lcdui/Item;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, _ := intArgument(args, 0)
		ref, _ := args[1].Reference()
		items, _ := vm.objectArray(receiver, midpItemsField, "[Ljavax/microedition/lcdui/Item;")
		if ref == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		if index < 0 || int(index) >= len(items.Elements) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		items.Elements[index] = args[1]
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "delete", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, _ := intArgument(args, 0)
		items, _ := vm.objectArray(receiver, midpItemsField, "[Ljavax/microedition/lcdui/Item;")
		if index < 0 || int(index) >= len(items.Elements) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		items.Elements = append(items.Elements[:index], items.Elements[index+1:]...)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "deleteAll", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		items, _ := vm.objectArray(receiver, midpItemsField, "[Ljavax/microedition/lcdui/Item;")
		items.Elements = nil
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/Form", "setItemStateListener", "(Ljavax/microedition/lcdui/ItemStateListener;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, setObjectField(vm, receiver, midpItemListener, args[0])
	})

	for _, descriptor := range []string{"(Ljava/lang/String;)V", "(Ljava/lang/String;Ljava/lang/String;Ljavax/microedition/lcdui/Image;Ljavax/microedition/lcdui/AlertType;)V"} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/lcdui/Alert", "<init>", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			_ = setObjectField(vm, receiver, midpTitleField, args[0])
			_ = setObjectField(vm, receiver, midpTimeoutField, IntValue(2000))
			if len(args) == 4 {
				_ = setObjectField(vm, receiver, midpTextField, args[1])
				_ = setObjectField(vm, receiver, midpImageField, args[2])
				_ = setObjectField(vm, receiver, midpAlertTypeField, args[3])
			}
			return Value{}, false, nil
		})
	}
	vm.registerReferenceProperty("javax/microedition/lcdui/Alert", "String", "Ljava/lang/String;", midpTextField)
	vm.registerReferenceProperty("javax/microedition/lcdui/Alert", "Image", "Ljavax/microedition/lcdui/Image;", midpImageField)
	vm.registerReferenceProperty("javax/microedition/lcdui/Alert", "Type", "Ljavax/microedition/lcdui/AlertType;", midpAlertTypeField)
	vm.registerReferenceProperty("javax/microedition/lcdui/Alert", "Indicator", "Ljavax/microedition/lcdui/Gauge;", midpIndicatorField)
	vm.registerIntGetter("javax/microedition/lcdui/Alert", "getTimeout", midpTimeoutField, 2000)
	vm.RegisterNative("javax/microedition/lcdui/Alert", "getDefaultTimeout", "()I", func(context.Context, *VM, uint32, []Value) (Value, bool, error) { return IntValue(2000), true, nil })
	vm.RegisterNative("javax/microedition/lcdui/Alert", "setTimeout", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		timeout, _ := intArgument(args, 0)
		if timeout <= 0 && timeout != -2 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		return Value{}, false, setObjectField(vm, receiver, midpTimeoutField, args[0])
	})
	vm.RegisterNative("javax/microedition/lcdui/AlertType", "<init>", "()V", nativeVoid)
	vm.RegisterNative("javax/microedition/lcdui/AlertType", "playSound", "(Ljavax/microedition/lcdui/Display;)Z", nativeReturnOne)
}

func (vm *VM) appendFormItem(receiver uint32, item Value) (Value, bool, error) {
	ref, _ := item.Reference()
	if ref == 0 {
		return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	items, err := vm.objectArray(receiver, midpItemsField, "[Ljavax/microedition/lcdui/Item;")
	if err != nil {
		return Value{}, false, err
	}
	index := len(items.Elements)
	items.Elements = append(items.Elements, item)
	return IntValue(int32(index)), true, nil
}

func (vm *VM) installMIDPLCDUIStatics() {
	staticInts := map[string]map[string]int32{
		"javax/microedition/lcdui/Canvas": {
			"UP": 1, "LEFT": 2, "RIGHT": 5, "DOWN": 6, "FIRE": 8,
			"GAME_A": 9, "GAME_B": 10, "GAME_C": 11, "GAME_D": 12,
			"KEY_NUM0": '0', "KEY_NUM1": '1', "KEY_NUM2": '2', "KEY_NUM3": '3', "KEY_NUM4": '4',
			"KEY_NUM5": '5', "KEY_NUM6": '6', "KEY_NUM7": '7', "KEY_NUM8": '8', "KEY_NUM9": '9',
			"KEY_STAR": '*', "KEY_POUND": '#',
		},
		"javax/microedition/lcdui/Choice": {
			"EXCLUSIVE": 1, "MULTIPLE": 2, "IMPLICIT": 3, "POPUP": 4,
			"TEXT_WRAP_DEFAULT": 0, "TEXT_WRAP_ON": 1, "TEXT_WRAP_OFF": 2,
		},
		"javax/microedition/lcdui/DateField": {"DATE": 1, "TIME": 2, "DATE_TIME": 3},
		"javax/microedition/lcdui/Display": {
			"LIST_ELEMENT": 1, "CHOICE_GROUP_ELEMENT": 2, "ALERT": 3,
			"COLOR_BACKGROUND": 0, "COLOR_FOREGROUND": 1, "COLOR_HIGHLIGHTED_BACKGROUND": 2,
			"COLOR_HIGHLIGHTED_FOREGROUND": 3, "COLOR_BORDER": 4, "COLOR_HIGHLIGHTED_BORDER": 5,
		},
		"javax/microedition/lcdui/Font": {
			"FACE_SYSTEM": 0, "FACE_MONOSPACE": 32, "FACE_PROPORTIONAL": 64,
			"STYLE_PLAIN": 0, "STYLE_BOLD": 1, "STYLE_ITALIC": 2, "STYLE_UNDERLINED": 4,
			"SIZE_MEDIUM": 0, "SIZE_SMALL": 8, "SIZE_LARGE": 16,
			"FONT_STATIC_TEXT": 0, "FONT_INPUT_TEXT": 1,
		},
		"javax/microedition/lcdui/Gauge": {
			"INDEFINITE": -1, "CONTINUOUS_IDLE": 0, "INCREMENTAL_IDLE": 1,
			"CONTINUOUS_RUNNING": 2, "INCREMENTAL_UPDATING": 3,
		},
		"javax/microedition/lcdui/Graphics": {
			"HCENTER": 1, "VCENTER": 2, "LEFT": 4, "RIGHT": 8,
			"TOP": 16, "BOTTOM": 32, "BASELINE": 64, "SOLID": 0, "DOTTED": 1,
		},
		"javax/microedition/lcdui/Item": {
			"LAYOUT_DEFAULT": 0, "LAYOUT_LEFT": 1, "LAYOUT_RIGHT": 2, "LAYOUT_CENTER": 3,
			"LAYOUT_TOP": 0x10, "LAYOUT_BOTTOM": 0x20, "LAYOUT_VCENTER": 0x30,
			"LAYOUT_NEWLINE_BEFORE": 0x100, "LAYOUT_NEWLINE_AFTER": 0x200,
			"LAYOUT_SHRINK": 0x400, "LAYOUT_EXPAND": 0x800, "LAYOUT_VSHRINK": 0x1000,
			"LAYOUT_VEXPAND": 0x2000, "LAYOUT_2": 0x4000,
			"PLAIN": 0, "HYPERLINK": 1, "BUTTON": 2,
		},
		"javax/microedition/lcdui/TextField": {
			"ANY": 0, "EMAILADDR": 1, "NUMERIC": 2, "PHONENUMBER": 3, "URL": 4, "DECIMAL": 5,
			"CONSTRAINT_MASK": 0xffff, "PASSWORD": 0x10000, "UNEDITABLE": 0x20000,
			"SENSITIVE": 0x40000, "NON_PREDICTIVE": 0x80000,
			"INITIAL_CAPS_WORD": 0x100000, "INITIAL_CAPS_SENTENCE": 0x200000,
		},
	}
	for class, fields := range staticInts {
		for name, value := range fields {
			vm.RegisterStaticField(class, name, "I", IntValue(value))
		}
	}
	for name, kind := range map[string]int32{"SCREEN": 1, "BACK": 2, "CANCEL": 3, "OK": 4, "HELP": 5, "STOP": 6, "EXIT": 7, "ITEM": 8} {
		vm.RegisterStaticField("javax/microedition/lcdui/Command", name, "I", IntValue(kind))
	}
	for index, name := range []string{"ALARM", "CONFIRMATION", "ERROR", "INFO", "WARNING"} {
		vm.RegisterStaticField("javax/microedition/lcdui/AlertType", name, "Ljavax/microedition/lcdui/AlertType;", ReferenceValue(vm.NewObject("javax/microedition/lcdui/AlertType", nil)))
		_ = index
	}
	selectCommand := vm.NewObject("javax/microedition/lcdui/Command", nil)
	_ = vm.initCommand(selectCommand, ReferenceValue(vm.NewString("Select")), ReferenceValue(vm.NewString("Select")), IntValue(8), IntValue(0))
	vm.RegisterStaticField("javax/microedition/lcdui/List", "SELECT_COMMAND", "Ljavax/microedition/lcdui/Command;", ReferenceValue(selectCommand))
	dismiss := vm.NewObject("javax/microedition/lcdui/Command", nil)
	_ = vm.initCommand(dismiss, ReferenceValue(vm.NewString("Dismiss")), ReferenceValue(vm.NewString("Dismiss")), IntValue(4), IntValue(0))
	vm.RegisterStaticField("javax/microedition/lcdui/Alert", "DISMISS_COMMAND", "Ljavax/microedition/lcdui/Command;", ReferenceValue(dismiss))
	vm.RegisterStaticField("javax/microedition/lcdui/Alert", "FOREVER", "I", IntValue(-2))
}
