package skvm

import (
	"context"
	"fmt"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	xTextFieldMaxLength = "\x00xce.max-length"
	xTextFieldX         = "\x00xce.x"
	xTextFieldY         = "\x00xce.y"
	xTextFieldWidth     = "\x00xce.width"
	xTextFieldHeight    = "\x00xce.height"
	xTextFieldBlocked   = "\x00xce.ime-blocked"
)

func (vm *VM) xceTextHandler() (uint32, *textComponentHandlerState, error) {
	key := fieldStorageKey(
		"com/xce/lcdui/TextComponentHandler", "__aramSingleton",
		"Lcom/xce/lcdui/TextComponentHandler;",
	)
	reference, err := vm.hostStatic[key].Reference()
	if err != nil || reference == 0 {
		return 0, nil, fmt.Errorf("XCE text handler is unavailable")
	}
	state, err := vm.textComponentHandler(reference)
	return reference, state, err
}

func (vm *VM) limitXTextField(receiver uint32, value string) string {
	object, ok := vm.Object(receiver)
	if !ok {
		return value
	}
	limit, _ := object.Fields[xTextFieldMaxLength].Int()
	if limit <= 0 {
		return value
	}
	units := int32(0)
	for index, char := range value {
		size := int32(1)
		if char > 0xffff {
			size = 2
		}
		if units+size > limit {
			return value[:index]
		}
		units += size
	}
	return value
}

func nativeXTextFieldSetFocus(
	_ context.Context, vm *VM, receiver uint32, args []Value,
) (Value, bool, error) {
	field, err := vm.xTextField(receiver)
	if err != nil {
		return Value{}, false, err
	}
	value, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	_, handler, err := vm.xceTextHandler()
	if err != nil {
		return Value{}, false, err
	}
	if value != 0 {
		if handler.component != receiver {
			if old, err := vm.xTextField(handler.component); err == nil {
				old.focus = false
			}
			handler.component = receiver
			handler.automata.Reset()
		}
		field.focus = true
	} else {
		field.focus = false
		if handler.component == receiver {
			handler.component = 0
			handler.automata.Reset()
		}
	}
	object, _ := vm.Object(receiver)
	object.Fields[xTextFieldBlocked] = IntValue(0)
	return Value{}, false, nil
}

func nativeXTextFieldSetBounds(
	_ context.Context, vm *VM, receiver uint32, args []Value,
) (Value, bool, error) {
	if _, err := vm.xTextField(receiver); err != nil {
		return Value{}, false, err
	}
	object, _ := vm.Object(receiver)
	for index, key := range [...]string{
		xTextFieldX, xTextFieldY, xTextFieldWidth, xTextFieldHeight,
	} {
		value, err := intArgument(args, index)
		if err != nil {
			return Value{}, false, err
		}
		object.Fields[key] = IntValue(value)
	}
	return Value{}, false, nil
}

func nativeXTextFieldPaint(
	ctx context.Context, vm *VM, receiver uint32, args []Value,
) (Value, bool, error) {
	field, err := vm.xTextField(receiver)
	if err != nil {
		return Value{}, false, err
	}
	graphicsReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	graphics, err := vm.graphics(graphicsReference)
	if err != nil {
		return Value{}, false, err
	}
	object, _ := vm.Object(receiver)
	x, _ := object.Fields[xTextFieldX].Int()
	y, _ := object.Fields[xTextFieldY].Int()
	width, _ := object.Fields[xTextFieldWidth].Int()
	height, _ := object.Fields[xTextFieldHeight].Int()
	if width <= 0 || height <= 0 {
		return Value{}, false, nil
	}
	oldColor := graphics.color
	defer func() { graphics.color = oldColor }()
	if err := fillRectangle(vm, graphics, int(x), int(y), int(width), int(height), 0xffffffff); err != nil {
		return Value{}, false, err
	}
	graphics.color = 0xff222222
	if field.focus {
		graphics.color = 0xff1855a5
	}
	if _, _, err := nativeDrawRect(ctx, vm, graphicsReference,
		[]Value{IntValue(x), IntValue(y), IntValue(width - 1), IntValue(height - 1)}); err != nil {
		return Value{}, false, err
	}
	font := graphics.font
	if font == 0 {
		font = vm.defaultFont
	}
	if err := vm.services.Text.Draw(
		vm.serviceOwner, font, graphics.surface, field.text,
		x+2, y+1, shared.AnchorLeft|shared.AnchorTop, shared.RGB(0, 0, 0),
	); err != nil {
		return Value{}, false, err
	}
	return Value{}, false, nil
}

func nativeXTextFieldKeyPressed(
	ctx context.Context, vm *VM, receiver uint32, args []Value,
) (Value, bool, error) {
	field, err := vm.xTextField(receiver)
	if err != nil {
		return Value{}, false, err
	}
	if !field.focus {
		return Value{}, false, nil
	}
	handlerReference, handler, err := vm.xceTextHandler()
	if err != nil {
		return Value{}, false, err
	}
	if handler.component != receiver {
		return Value{}, false, nil
	}
	key, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	if key == 8 {
		handler.automata.Commit()
		_, _, err := nativeXTextFieldDelete(ctx, vm, receiver, nil)
		return Value{}, false, err
	}
	_, _, err = nativeTextComponentKeyPressed(ctx, vm, handlerReference, args)
	return Value{}, false, err
}

func nativeXTextFieldInsert(
	_ context.Context, vm *VM, receiver uint32, args []Value,
) (Value, bool, error) {
	field, err := vm.xTextField(receiver)
	if err != nil {
		return Value{}, false, err
	}
	char, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	object, _ := vm.Object(receiver)
	updated := vm.limitXTextField(receiver, field.text+string(rune(char)))
	object.Fields[xTextFieldBlocked] = boolValue(updated == field.text)
	field.text = updated
	return Value{}, false, nil
}

func nativeXTextFieldReplace(
	_ context.Context, vm *VM, receiver uint32, args []Value,
) (Value, bool, error) {
	field, err := vm.xTextField(receiver)
	if err != nil {
		return Value{}, false, err
	}
	char, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	object, _ := vm.Object(receiver)
	blocked, _ := object.Fields[xTextFieldBlocked].Int()
	if blocked != 0 {
		return Value{}, false, nil
	}
	runes := []rune(field.text)
	if len(runes) != 0 {
		runes = runes[:len(runes)-1]
	}
	field.text = vm.limitXTextField(receiver, string(runes)+string(rune(char)))
	return Value{}, false, nil
}

func nativeXTextFieldDelete(
	_ context.Context, vm *VM, receiver uint32, _ []Value,
) (Value, bool, error) {
	field, err := vm.xTextField(receiver)
	if err != nil {
		return Value{}, false, err
	}
	object, _ := vm.Object(receiver)
	object.Fields[xTextFieldBlocked] = IntValue(0)
	runes := []rune(field.text)
	if len(runes) != 0 {
		field.text = string(runes[:len(runes)-1])
	}
	return Value{}, false, nil
}
