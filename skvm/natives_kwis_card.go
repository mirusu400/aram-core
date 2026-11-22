package skvm

import (
	"context"
	"errors"
)

// A WIPI Jlet shows itself by pushing a Card onto the Display. The top card is
// the VM's current display: it is painted with an org.kwis.msp Graphics and
// receives keys through keyNotify, where a MIDP Canvas gets paint and
// keyPressed.

const (
	kwisCardClass    = "org/kwis/msp/lcdui/Card"
	kwisDisplayClass = "org/kwis/msp/lcdui/Display"

	kwisCardStackField      = "\x00aram-kwis-card-stack"
	kwisScreenGraphicsField = "\x00aram-kwis-screen-graphics"

	midpPaintDescriptor = "(Ljavax/microedition/lcdui/Graphics;)V"
	kwisPaintDescriptor = "(Lorg/kwis/msp/lcdui/Graphics;)V"

	// EventQueue key event types handed to Card.keyNotify.
	kwisKeyPressed  = int32(1)
	kwisKeyReleased = int32(2)
)

func (vm *VM) currentDisplayIsCard() bool {
	return vm.IsInstance(vm.currentDisplay, kwisCardClass)
}

// paintGraphics is the Graphics handed to the current display's paint. A Card
// gets an org.kwis.msp Graphics over the screen surface: it shares the screen
// Graphics state, but dispatches as the WIPI class, whose methods differ from
// their MIDP namesakes (reset restores the clip as well as the color).
func (vm *VM) paintGraphics() uint32 {
	screen := vm.ScreenGraphics()
	if !vm.currentDisplayIsCard() {
		return screen
	}
	state, err := vm.graphics(screen)
	if err != nil {
		return screen
	}
	reference, _ := vm.hostStatic[kwisScreenGraphicsField].Reference()
	object, ok := vm.Object(reference)
	if reference == 0 || !ok {
		reference = vm.NewObject(kwisGraphicsClass, state)
		vm.hostStatic[kwisScreenGraphicsField] = ReferenceValue(reference)
		return reference
	}
	// A restored snapshot gives each object its own copy of the state.
	object.Native = state
	return reference
}

func (vm *VM) paintDescriptor() string {
	if vm.currentDisplayIsCard() {
		return kwisPaintDescriptor
	}
	return midpPaintDescriptor
}

// kwisCardStack is the Display's card stack, bottom first. It lives on the
// guest heap so it is collected, saved, and restored with everything else.
func (vm *VM) kwisCardStack() *Array {
	if reference, err := vm.hostStatic[kwisCardStackField].Reference(); err == nil {
		if object, ok := vm.Object(reference); ok && object.Array != nil {
			return object.Array
		}
	}
	reference := vm.newArray("[Lorg/kwis/msp/lcdui/Card;", nil)
	vm.hostStatic[kwisCardStackField] = ReferenceValue(reference)
	return vm.heap[reference].Array
}

func (vm *VM) removeKWISCard(card uint32) bool {
	stack := vm.kwisCardStack()
	for index, value := range stack.Elements {
		if current, _ := value.Reference(); current == card {
			stack.Elements = append(stack.Elements[:index], stack.Elements[index+1:]...)
			return true
		}
	}
	return false
}

func (vm *VM) showTopKWISCard() {
	stack := vm.kwisCardStack()
	top := uint32(0)
	if len(stack.Elements) != 0 {
		top, _ = stack.Elements[len(stack.Elements)-1].Reference()
	}
	vm.setCurrentDisplay(top)
}

// cardKeyCode turns the SKT keypad code the host delivers into the code an
// SKT handset hands Card.keyNotify. The handset reports its function keys as
// the positive WIPI action codes rather than the negative key codes of other
// carriers: one corpus title looks keyNotify's argument up, untranslated, in a
// table of 1, 6, 2, 5, 8, 90, and 99, and another switches on the same values
// plus 91 and 110 after passing only negative codes through getGameAction.
// Digits, '*' and '#' keep their spelling.
func cardKeyCode(key int32) int32 {
	switch key {
	case 141:
		return 1 // UP
	case 146:
		return 6 // DOWN
	case 142:
		return 2 // LEFT
	case 145:
		return 5 // RIGHT
	case 148:
		return 8 // FIRE
	case 131:
		return 90 // SOFT1
	case 129:
		return 91 // SOFT2
	case 8:
		return 99 // CLEAR
	case 190:
		return 110 // SEND
	default:
		return key
	}
}

// kwisGameAction is Display.getGameAction. It maps the negative WIPI key
// codes onto their action codes and leaves everything else alone, so a title
// may pass it the action code keyNotify already delivered.
func kwisGameAction(key int32) int32 {
	switch key {
	case -1:
		return 1 // UP
	case -2:
		return 6 // DOWN
	case -3:
		return 2 // LEFT
	case -4:
		return 5 // RIGHT
	case -5:
		return 8 // FIRE
	case -6:
		return 90 // SOFT1
	case -7:
		return 91 // SOFT2
	case -8:
		return 92 // SOFT3
	case -13:
		return 96 // SIDE_UP
	case -14:
		return 97 // SIDE_DOWN
	case -15:
		return 98 // SIDE_SEL
	case -16:
		return 99 // CLEAR
	case -10:
		return 110 // SEND
	default:
		if key < 0 {
			return 0
		}
		return key
	}
}

func (vm *VM) dispatchKWISCardKey(ctx context.Context, key int32, pressed bool) error {
	kind := kwisKeyReleased
	if pressed {
		kind = kwisKeyPressed
	}
	_, _, err := vm.InvokeVirtual(
		ctx,
		vm.currentDisplay,
		"keyNotify",
		"(II)Z",
		IntValue(kind),
		IntValue(cardKeyCode(key)),
	)
	if errors.Is(err, ErrMethodNotFound) {
		return nil
	}
	return vm.absorbUncaught("keyNotify", err)
}

func (vm *VM) installKWISCardNatives() {
	for _, descriptor := range []string{
		"()V",
		"(I)V",
		"(Z)V",
		"(IIII)V",
		"(Lorg/kwis/msp/lcdui/Display;)V",
		"(Lorg/kwis/msp/lcdui/Display;IIII)V",
		"(Lorg/kwis/msp/lcdui/Display;IIIIZ)V",
	} {
		vm.RegisterNative(kwisCardClass, "<init>", descriptor, nativeVoid)
	}
	requestRepaint := func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		if receiver != 0 && receiver == vm.currentDisplay {
			vm.setRepaintPending(true)
		}
		return Value{}, false, nil
	}
	vm.RegisterNative(kwisCardClass, "repaint", "()V", requestRepaint)
	vm.RegisterNative(kwisCardClass, "repaint", "(IIII)V", requestRepaint)
	vm.RegisterNative(kwisCardClass, "serviceRepaints", "()V",
		func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if receiver == 0 || receiver != vm.currentDisplay || !vm.RepaintPending() {
				return Value{}, false, nil
			}
			return Value{}, false, vm.PaintCurrent(ctx)
		},
	)
	vm.RegisterNative(kwisCardClass, "isShown", "()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			return boolValue(receiver != 0 && receiver == vm.currentDisplay), true, nil
		},
	)
	// A card covers the whole screen; it has no position of its own.
	for _, name := range []string{"getX", "getY"} {
		vm.RegisterNative(kwisCardClass, name, "()I",
			func(context.Context, *VM, uint32, []Value) (Value, bool, error) {
				return IntValue(0), true, nil
			},
		)
	}
	vm.RegisterNative(kwisCardClass, "getDisplay", "()Lorg/kwis/msp/lcdui/Display;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return vm.hostStatic[fieldStorageKey(
				kwisDisplayClass,
				"__aramSingleton",
				"Lorg/kwis/msp/lcdui/Display;",
			)], true, nil
		},
	)

	vm.RegisterNative(kwisDisplayClass, "pushCard", "(Lorg/kwis/msp/lcdui/Card;)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			card, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if card == 0 {
				return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
			}
			vm.removeKWISCard(card)
			stack := vm.kwisCardStack()
			stack.Elements = append(stack.Elements, ReferenceValue(card))
			vm.showTopKWISCard()
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(kwisDisplayClass, "removeCard", "(Lorg/kwis/msp/lcdui/Card;)Z",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			card, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			removed := vm.removeKWISCard(card)
			if removed {
				vm.showTopKWISCard()
			}
			return boolValue(removed), true, nil
		},
	)
	vm.RegisterNative(kwisDisplayClass, "popCard", "()Lorg/kwis/msp/lcdui/Card;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			stack := vm.kwisCardStack()
			if len(stack.Elements) == 0 {
				return ReferenceValue(0), true, nil
			}
			top := stack.Elements[len(stack.Elements)-1]
			stack.Elements = stack.Elements[:len(stack.Elements)-1]
			vm.showTopKWISCard()
			return top, true, nil
		},
	)
	vm.RegisterNative(kwisDisplayClass, "removeAllCards", "()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			vm.kwisCardStack().Elements = nil
			vm.showTopKWISCard()
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(kwisDisplayClass, "getCurrentCard", "()Lorg/kwis/msp/lcdui/Card;",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			stack := vm.kwisCardStack()
			if len(stack.Elements) == 0 {
				return ReferenceValue(0), true, nil
			}
			return stack.Elements[len(stack.Elements)-1], true, nil
		},
	)
	vm.RegisterNative(kwisDisplayClass, "getWidth", "()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenWidth)), true, nil
		},
	)
	vm.RegisterNative(kwisDisplayClass, "getHeight", "()I",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			return IntValue(int32(vm.ScreenHeight)), true, nil
		},
	)
	vm.RegisterNative(kwisDisplayClass, "isDoubleBuffered", "()Z", nativeReturnOne)
	vm.RegisterNative(kwisDisplayClass, "getGameAction", "(I)I",
		func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			key, err := intArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return IntValue(kwisGameAction(key)), true, nil
		},
	)
	if native, ok := vm.natives[nativeKey{
		class:      "javax/microedition/lcdui/Display",
		name:       "callSerially",
		descriptor: "(Ljava/lang/Runnable;)V",
	}]; ok {
		vm.RegisterNative(kwisDisplayClass, "callSerially", "(Ljava/lang/Runnable;)V", native)
	}
}
