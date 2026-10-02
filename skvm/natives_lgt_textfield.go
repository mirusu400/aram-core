package skvm

import (
	"context"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/internal/ime"
)

const (
	lgtTextFieldClass = "mmpp/microedition/lcdui/TextFieldX"
	lgtTextPrefix     = "\x00lgt.textfield."
	lgtTextWidth      = lgtTextPrefix + "width"
	lgtTextRows       = lgtTextPrefix + "rows"
	lgtTextOwner      = lgtTextPrefix + "owner"
	lgtTextFont       = lgtTextPrefix + "font"
)

const (
	lgtModeNone    int32 = 0
	lgtModeCaps    int32 = 1
	lgtModeSmall   int32 = 2
	lgtModeNumeric int32 = 4
	lgtModeSymbol  int32 = 8
	lgtModeKorean  int32 = 32
)

var lgtInputModes = [...]int32{lgtModeKorean, lgtModeCaps, lgtModeSmall, lgtModeNumeric, lgtModeSymbol}

var lgtIMEFields = [...]string{
	"ime.mode", "ime.key", "ime.index", "ime.cho", "ime.jung", "ime.jong",
	"ime.composing", "ime.lastKey", "ime.lastIndex",
}

func lgtAutomataMode(mode int32) ime.Mode {
	switch mode {
	case lgtModeCaps:
		return ime.ModeENUpper
	case lgtModeSmall:
		return ime.ModeENLower
	case lgtModeNumeric:
		return ime.ModeNumeric
	default:
		return ime.ModeKorean
	}
}

func lgtModeForAutomata(mode ime.Mode) int32 {
	switch mode {
	case ime.ModeENUpper:
		return lgtModeCaps
	case ime.ModeENLower:
		return lgtModeSmall
	case ime.ModeNumeric:
		return lgtModeNumeric
	default:
		return lgtModeKorean
	}
}

func lgtFieldInt(vm *VM, receiver uint32, name string, fallback int32) int32 {
	object, ok := vm.Object(receiver)
	if !ok {
		return fallback
	}
	value, ok := object.Fields[lgtTextPrefix+name]
	if !ok {
		return fallback
	}
	result, err := value.Int()
	if err != nil {
		return fallback
	}
	return result
}

func lgtSetFieldInt(vm *VM, receiver uint32, name string, value int32) error {
	return setObjectField(vm, receiver, lgtTextPrefix+name, IntValue(value))
}

func lgtTextAutomata(vm *VM, receiver uint32, mode int32) ime.Automata {
	automata := ime.New(lgtAutomataMode(mode))
	object, ok := vm.Object(receiver)
	if !ok {
		return automata
	}
	if _, exists := object.Fields[lgtTextPrefix+lgtIMEFields[0]]; !exists {
		return automata
	}
	state := ime.State{
		Mode:           lgtFieldInt(vm, receiver, lgtIMEFields[0], int32(lgtAutomataMode(mode))),
		ComposingKey:   lgtFieldInt(vm, receiver, lgtIMEFields[1], 0),
		ComposingIndex: lgtFieldInt(vm, receiver, lgtIMEFields[2], 0),
		Choseong:       lgtFieldInt(vm, receiver, lgtIMEFields[3], -1),
		Jungseong:      lgtFieldInt(vm, receiver, lgtIMEFields[4], -1),
		Jongseong:      lgtFieldInt(vm, receiver, lgtIMEFields[5], 0),
		Composing:      lgtFieldInt(vm, receiver, lgtIMEFields[6], 0) != 0,
		LastKey:        lgtFieldInt(vm, receiver, lgtIMEFields[7], 0),
		LastIndex:      lgtFieldInt(vm, receiver, lgtIMEFields[8], 0),
	}
	automata.Restore(state)
	return automata
}

func lgtSaveAutomata(vm *VM, receiver uint32, automata ime.Automata) {
	state := automata.Snapshot()
	values := [...]int32{
		state.Mode, state.ComposingKey, state.ComposingIndex, state.Choseong,
		state.Jungseong, state.Jongseong, int32(boolInt(state.Composing)),
		state.LastKey, state.LastIndex,
	}
	for index, value := range values {
		_ = lgtSetFieldInt(vm, receiver, lgtIMEFields[index], value)
	}
}

func (vm *VM) installLGTTextFieldNatives() {
	vm.RegisterHostClass(lgtTextFieldClass, "java/lang/Object")
	vm.RegisterNative(lgtTextFieldClass, "<init>", "(Ljava/lang/String;Ljava/lang/String;II)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			maximum, err := intArgument(args, 2)
			if err != nil {
				return Value{}, false, err
			}
			if maximum <= 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "TextFieldX maximum must be positive")
			}
			if err := setObjectField(vm, receiver, midpMaxSizeField, IntValue(maximum)); err != nil {
				return Value{}, false, err
			}
			if err := setObjectField(vm, receiver, midpConstraintsField, args[3]); err != nil {
				return Value{}, false, err
			}
			if err := setObjectField(vm, receiver, lgtTextPrefix+"label", args[0]); err != nil {
				return Value{}, false, err
			}
			if err := vm.setText(receiver, args[1]); err != nil {
				return Value{}, false, err
			}
			_ = lgtSetFieldInt(vm, receiver, "mode", lgtModeKorean)
			_ = lgtSetFieldInt(vm, receiver, "rows", 1)
			lgtSaveAutomata(vm, receiver, ime.New(ime.ModeKorean))
			return Value{}, false, nil
		})
	vm.RegisterNative(lgtTextFieldClass, "setOwner", "(Ljavax/microedition/lcdui/Canvas;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			owner, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if owner != 0 && !vm.IsInstance(owner, "javax/microedition/lcdui/Canvas") {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "TextFieldX owner is not a Canvas")
			}
			return Value{}, false, setObjectField(vm, receiver, lgtTextOwner, args[0])
		})
	for _, property := range []struct {
		name, field string
	}{
		{"setWidth", lgtTextWidth}, {"setMaxRow", lgtTextRows},
	} {
		property := property
		vm.RegisterNative(lgtTextFieldClass, property.name, "(I)V",
			func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
				value, err := intArgument(args, 0)
				if err != nil {
					return Value{}, false, err
				}
				if value <= 0 {
					return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", property.name+" must be positive")
				}
				return Value{}, false, setObjectField(vm, receiver, property.field, IntValue(value))
			})
	}
	vm.RegisterNative(lgtTextFieldClass, "setFocus", "(Z)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			focus, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if focus == 0 {
				automata := lgtTextAutomata(vm, receiver, lgtFieldInt(vm, receiver, "mode", lgtModeKorean))
				automata.Commit()
				lgtSaveAutomata(vm, receiver, automata)
			}
			return Value{}, false, lgtSetFieldInt(vm, receiver, "focus", int32(boolInt(focus != 0)))
		})
	vm.RegisterNative(lgtTextFieldClass, "getInputMode", "()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if lgtFieldInt(vm, receiver, "focus", 0) == 0 {
				return IntValue(lgtModeNone), true, nil
			}
			return IntValue(lgtFieldInt(vm, receiver, "mode", lgtModeKorean)), true, nil
		})
	vm.RegisterNative(lgtTextFieldClass, "nextInputMode", "()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if lgtFieldInt(vm, receiver, "focus", 0) == 0 {
				return IntValue(lgtModeNone), true, nil
			}
			current := lgtFieldInt(vm, receiver, "mode", lgtModeKorean)
			next := lgtInputModes[0]
			for index, mode := range lgtInputModes {
				if current == mode {
					next = lgtInputModes[(index+1)%len(lgtInputModes)]
					break
				}
			}
			_ = lgtSetFieldInt(vm, receiver, "mode", next)
			lgtSaveAutomata(vm, receiver, ime.New(lgtAutomataMode(next)))
			return IntValue(next), true, nil
		})
	vm.RegisterNative(lgtTextFieldClass, "getString", "()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			value, err := objectField(vm, receiver, midpTextField)
			return value, true, err
		})
	vm.RegisterNative(lgtTextFieldClass, "getCaretPosition", "()I",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			text, err := vm.textValue(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(int32(len(utf16.Encode([]rune(text))))), true, nil
		})
	vm.RegisterNative(lgtTextFieldClass, "getFont", "()Ljavax/microedition/lcdui/Font;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			if stored, exists := object.Fields[lgtTextFont]; exists {
				reference, err := stored.Reference()
				if err != nil {
					return Value{}, false, err
				}
				if reference != 0 {
					return stored, true, nil
				}
			}
			reference := vm.NewObject("javax/microedition/lcdui/Font", &fontState{font: vm.defaultFont})
			value := ReferenceValue(reference)
			object.Fields[lgtTextFont] = value
			return value, true, nil
		})
	vm.RegisterNative(lgtTextFieldClass, "setFont", "(Ljavax/microedition/lcdui/Font;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			reference, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if reference != 0 {
				if !vm.IsInstance(reference, "javax/microedition/lcdui/Font") {
					return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "TextFieldX font is not a MIDP Font")
				}
				if _, err := vm.font(reference); err != nil {
					return Value{}, false, err
				}
			}
			return Value{}, false, setObjectField(vm, receiver, lgtTextFont, args[0])
		})
	vm.RegisterNative(lgtTextFieldClass, "keyPressed", "(I)V", nativeLGTTextKeyPressed)
	vm.RegisterNative(lgtTextFieldClass, "keyRepeated", "(I)V", nativeLGTTextKeyPressed)
	vm.RegisterNative(lgtTextFieldClass, "keyReleased", "(I)V", nativeVoid)
	vm.RegisterNative(lgtTextFieldClass, "paint", "(Ljavax/microedition/lcdui/Graphics;)V", nativeLGTTextPaint)
}

func nativeLGTTextKeyPressed(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
	if lgtFieldInt(vm, receiver, "focus", 0) == 0 {
		return Value{}, false, nil
	}
	key, err := intArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	text, err := vm.textValue(receiver)
	if err != nil {
		return Value{}, false, err
	}
	runes := []rune(text)
	mode := lgtFieldInt(vm, receiver, "mode", lgtModeKorean)
	automata := lgtTextAutomata(vm, receiver, mode)
	if key == 8 || key == 127 || key == -8 {
		if len(runes) != 0 {
			runes = runes[:len(runes)-1]
		}
		automata.Commit()
	} else if mode == lgtModeSymbol {
		symbols := map[int32]rune{'0': ' ', '1': '.', '2': ',', '3': '?', '4': '!', '5': '@', '6': '#', '7': '$', '8': '%', '9': '&'}
		if symbol, ok := symbols[key]; ok {
			runes = append(runes, symbol)
		}
	} else {
		ops, _ := automata.Press(key)
		for _, op := range ops {
			switch op.Kind {
			case ime.OpInsert:
				runes = append(runes, op.Char)
			case ime.OpReplace:
				if len(runes) == 0 {
					runes = append(runes, op.Char)
				} else {
					runes[len(runes)-1] = op.Char
				}
			case ime.OpDelete:
				if len(runes) != 0 {
					runes = runes[:len(runes)-1]
				}
			}
		}
		mode = lgtModeForAutomata(automata.CurrentMode())
	}
	maximum := int32(0)
	if value, fieldErr := objectField(vm, receiver, midpMaxSizeField); fieldErr == nil {
		maximum, _ = value.Int()
	}
	if maximum > 0 && len(utf16.Encode(runes)) > int(maximum) {
		return Value{}, false, nil
	}
	if string(runes) != text {
		if err := vm.setText(receiver, ReferenceValue(vm.NewString(string(runes)))); err != nil {
			return Value{}, false, err
		}
	}
	_ = lgtSetFieldInt(vm, receiver, "mode", mode)
	lgtSaveAutomata(vm, receiver, automata)
	return Value{}, false, nil
}

func nativeLGTTextPaint(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
	graphicsRef, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	graphics, err := vm.graphics(graphicsRef)
	if err != nil {
		return Value{}, false, err
	}
	text, err := vm.textValue(receiver)
	if err != nil {
		return Value{}, false, err
	}
	if lgtFieldInt(vm, receiver, "focus", 0) != 0 {
		text += "|"
	}
	width := lgtFieldInt(vm, receiver, "width", 0)
	font := graphics.font
	if object, ok := vm.Object(receiver); ok {
		if stored, exists := object.Fields[lgtTextFont]; exists {
			if reference, referenceErr := stored.Reference(); referenceErr == nil && reference != 0 {
				selected, fontErr := vm.font(reference)
				if fontErr != nil {
					return Value{}, false, fontErr
				}
				font = selected.font
			}
		}
	}
	if font == 0 {
		font = vm.defaultFont
	}
	for width > 0 && len(text) != 0 {
		measured, measureErr := vm.services.Text.Measure(vm.serviceOwner, font, text)
		if measureErr != nil {
			return Value{}, false, measureErr
		}
		if measured <= width {
			break
		}
		text = string([]rune(text)[1:])
	}
	if text == "" {
		return Value{}, false, nil
	}
	previousFont := graphics.font
	graphics.font = font
	defer func() { graphics.font = previousFont }()
	return nativeDrawString(ctx, vm, graphicsRef, []Value{
		ReferenceValue(vm.NewString(text)), IntValue(0), IntValue(0), IntValue(20),
	})
}
