package ktf

import (
	"context"

	"github.com/mirusu400/aram-core/internal/ime"
)

func (r *Runtime) handleJletMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "notifyDestroyed()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		r.requestJavaTermination(instance)
		return 0, nil
	case "getActiveJlet()Lorg/kwis/msp/lcdui/Jlet;",
		"getCurrentJlet()Lorg/kwis/msp/lcdui/Jlet;":
		return r.MainJlet, nil
	case "getJletFromPID(I)Lorg/kwis/msp/lcdui/Jlet;":
		return r.MainJlet, nil
	case "setActiveJlet(Lorg/kwis/msp/lcdui/Jlet;)V":
		jlet, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if jlet != 0 {
			r.MainJlet = jlet
		}
		return 0, nil
	case "getCurrentProgramID()I":
		return 1, nil
	case "getEventQueue()Lorg/kwis/msp/lcdui/EventQueue;":
		if r.eventQueue != 0 {
			return r.eventQueue, nil
		}
		queue, err := r.NewHostJavaObject("org/kwis/msp/lcdui/EventQueue")
		if err != nil {
			return 0, err
		}
		r.eventQueue = queue
		return queue, nil
	case "getAppProperty(Ljava/lang/String;)Ljava/lang/String;":
		key, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		value := ""
		switch r.javaStringValue(key) {
		case "AID":
			value = r.Pkg.Descriptor.AID
		case "PID":
			value = r.Pkg.Descriptor.PID
		case "MClass", "MainClass":
			value = r.Pkg.Descriptor.MainClass
		default:
			// Unknown properties resolve to null.
			return 0, nil
		}
		return r.NewJavaString(value)
	case "removeAllResource(I)V", "pauseApp()V", "resumeApp()V",
		"destroyApp(Z)V", "startApp([Ljava/lang/String;)V":
		return 0, nil
	default:
		return 0, nil
	}
}

const ktfJavaEventQueueCapacity = 64

func (r *Runtime) readJavaEvent(array uint32) (ktfJavaEvent, error) {
	if array == 0 {
		return ktfJavaEvent{}, r.raiseHostJavaException(
			"java/lang/NullPointerException",
		)
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return ktfJavaEvent{}, err
	}
	if length < 4 {
		return ktfJavaEvent{}, r.raiseHostJavaException(
			"java/lang/ArrayIndexOutOfBoundsException",
		)
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return ktfJavaEvent{}, err
	}
	words, err := r.ReadWords(fields+8, 4)
	if err != nil {
		return ktfJavaEvent{}, err
	}
	return ktfJavaEvent(words), nil
}

func (r *Runtime) writeJavaEvent(array uint32, event ktfJavaEvent) error {
	if array == 0 {
		return r.raiseHostJavaException("java/lang/NullPointerException")
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return err
	}
	if length < 4 {
		return r.raiseHostJavaException(
			"java/lang/ArrayIndexOutOfBoundsException",
		)
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return err
	}
	return r.writeWords(fields+8, event[:])
}

func (r *Runtime) enqueueJavaEvent(event ktfJavaEvent) bool {
	if len(r.eventQueueEvents) >= ktfJavaEventQueueCapacity {
		return false
	}
	r.eventQueueEvents = append(r.eventQueueEvents, event)
	return true
}

func (r *Runtime) queueJletEventListener(
	listener uint32,
	event ktfJavaEvent,
) error {
	if listener == 0 {
		return nil
	}
	return r.QueueJavaVirtual(
		listener,
		"notifyEvent",
		"(III)V",
		event[0],
		event[1],
		event[2],
	)
}

func (r *Runtime) dispatchJavaEvent(event ktfJavaEvent) error {
	// EventQueue.KEY_EVENT = 1. A grabbed key is delivered to its listener;
	// every other key follows the normal top-card path.
	if event[0] == 1 {
		if listener := r.grabbedKeys[int32(event[2])]; listener != 0 {
			return r.queueJletEventListener(listener, event)
		}
		queued, err := r.QueueKeyEvent(event[1] != KeyReleased, int32(event[2]))
		if err != nil {
			return err
		}
		if !queued {
			// Preserve ordering when the card or task queue is temporarily busy.
			r.eventQueueEvents = append([]ktfJavaEvent{event}, r.eventQueueEvents...)
			if r.DeferThreads {
				r.yieldRequested = true
			}
		}
		return nil
	}

	delivered := make(map[uint32]bool, len(r.jletEventListeners)+1)
	if listener := r.eventHooks[event[0]]; listener != 0 {
		if err := r.queueJletEventListener(listener, event); err != nil {
			return err
		}
		delivered[listener] = true
	}
	for _, listener := range r.jletEventListeners {
		if listener == 0 || delivered[listener] {
			continue
		}
		if err := r.queueJletEventListener(listener, event); err != nil {
			return err
		}
		delivered[listener] = true
	}
	return nil
}

func (r *Runtime) handleEventQueueMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	_ = ctx
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "getNextEvent([I)V":
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		event := ktfJavaEvent{}
		if len(r.eventQueueEvents) != 0 {
			event = r.eventQueueEvents[0]
			copy(r.eventQueueEvents, r.eventQueueEvents[1:])
			r.eventQueueEvents = r.eventQueueEvents[:len(r.eventQueueEvents)-1]
		}
		if err := r.writeJavaEvent(array, event); err != nil {
			return 0, err
		}
		if r.DeferThreads {
			r.yieldRequested = true
		}
		return 0, nil
	case "dispatchEvent([I)V":
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		event, err := r.readJavaEvent(array)
		if err != nil {
			return 0, err
		}
		return 0, r.dispatchJavaEvent(event)
	case "postEvent([I)Z":
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		event, err := r.readJavaEvent(array)
		if err != nil {
			return 0, err
		}
		return boolWord(r.enqueueJavaEvent(event)), nil
	case "postEvent(I[I)V":
		id, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		event, err := r.readJavaEvent(array)
		if err != nil {
			return 0, err
		}
		if int32(id) == -1 || id == 1 {
			r.enqueueJavaEvent(event)
		}
		return 0, nil
	case "hookEvent(ILorg/kwis/msp/lcdui/JletEventListener;)V":
		id, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		listener, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if listener == 0 {
			delete(r.eventHooks, id)
		} else {
			r.eventHooks[id] = listener
		}
		return 0, nil
	default:
		return 0, nil
	}
}

const (
	ktfInputConstraintAny int32 = iota
	ktfInputConstraintNumber
	ktfInputConstraintPassword
	ktfInputConstraintEmail
	ktfInputConstraintURL
	ktfInputConstraintPhone
)

func validKTFInputConstraint(constraint int32) bool {
	return constraint >= ktfInputConstraintAny &&
		constraint <= ktfInputConstraintPhone
}

func ktfInputModeAllowed(constraint int32, mode ime.Mode) bool {
	switch constraint {
	case ktfInputConstraintNumber, ktfInputConstraintPassword,
		ktfInputConstraintPhone:
		return mode == ime.ModeNumeric
	case ktfInputConstraintEmail, ktfInputConstraintURL:
		return mode == ime.ModeENLower || mode == ime.ModeENUpper ||
			mode == ime.ModeNumeric
	default:
		return mode >= 0 && mode < ime.ModeCount
	}
}

func ktfInitialInputMode(constraint int32) ime.Mode {
	if constraint == ktfInputConstraintNumber ||
		constraint == ktfInputConstraintPassword ||
		constraint == ktfInputConstraintPhone {
		return ime.ModeNumeric
	}
	if constraint == ktfInputConstraintEmail || constraint == ktfInputConstraintURL {
		return ime.ModeENLower
	}
	return ime.ModeKorean
}

func (r *Runtime) inputMethodAutomata(instance uint32) *ime.Automata {
	if automata := r.lwcTextInput[instance]; automata != nil {
		return automata
	}
	constraint := r.inputConstraints[instance]
	mode := ktfInitialInputMode(constraint)
	if savedMode, ok := r.inputModes[instance]; ok &&
		ktfInputModeAllowed(constraint, ime.Mode(savedMode)) {
		mode = ime.Mode(savedMode)
	}
	r.inputConstraints[instance] = constraint
	r.inputModes[instance] = int32(mode)
	automata := ime.New(mode)
	r.lwcTextInput[instance] = &automata
	return &automata
}

func (r *Runtime) nextInputMethodMode(instance uint32) {
	automata := r.inputMethodAutomata(instance)
	constraint := r.inputConstraints[instance]
	for offset := ime.Mode(1); offset <= ime.ModeCount; offset++ {
		mode := (automata.CurrentMode() + offset) % ime.ModeCount
		if ktfInputModeAllowed(constraint, mode) {
			automata.SetMode(mode)
			r.inputModes[instance] = int32(mode)
			return
		}
	}
}

func (r *Runtime) queueInputMethodEdit(listener uint32, op ime.Op) error {
	characters, err := r.newJavaCharArray(string(op.Char))
	if err != nil {
		return err
	}
	length, err := r.javaArrayLength(characters)
	if err != nil {
		return err
	}
	mode := int32(-1)
	switch op.Kind {
	case ime.OpReplace:
		mode = 0
	case ime.OpDelete:
		mode = 1
	}
	return r.QueueJavaVirtual(
		listener,
		"notifyTextChanged",
		"([CII)V",
		characters,
		length,
		uint32(mode),
	)
}

// InputMethodHandler owns a per-instance keypad automata and delivers its
// insert/replace/delete edits through InputMethodListener.notifyTextChanged.
func (r *Runtime) handleInputMethodHandlerMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		constraintWord, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		constraint := int32(constraintWord)
		if !validKTFInputConstraint(constraint) {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		r.inputConstraints[instance] = constraint
		mode := ktfInitialInputMode(constraint)
		r.inputModes[instance] = int32(mode)
		automata := ime.New(mode)
		r.lwcTextInput[instance] = &automata
		return 0, nil
	case "getCurrentModeCode()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		mode := r.inputMethodAutomata(instance).CurrentMode()
		if mode < 0 || int(mode) >= len(ktfWIPICInputModes) {
			return 0, r.raiseHostJavaException("java/lang/IllegalStateException")
		}
		return r.NewJavaString(ktfWIPICInputModes[mode])
	case "setCurrentMode(I)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		modeWord, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		mode := ime.Mode(int32(modeWord))
		if !ktfInputModeAllowed(r.inputConstraints[instance], mode) {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		r.inputMethodAutomata(instance).SetMode(mode)
		r.inputModes[instance] = int32(mode)
		return 1, nil
	case "notifyKeyInput(II)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		keyWord, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		typeWord, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		listener := r.inputListeners[instance]
		if listener == 0 || typeWord != KeyPressed || int32(keyWord) == ime.ModeKey {
			return 0, nil
		}
		constraint := r.inputConstraints[instance]
		key := int32(keyWord)
		var ops []ime.Op
		var handled bool
		if (constraint == ktfInputConstraintPassword ||
			constraint == ktfInputConstraintPhone) && (key < '0' || key > '9') {
			return 0, nil
		}
		if (constraint == ktfInputConstraintEmail || constraint == ktfInputConstraintURL) &&
			key == ime.SpaceKey {
			return 0, nil
		}
		if constraint == ktfInputConstraintNumber && key == ime.SpaceKey {
			key = ' '
		}
		if constraint == ktfInputConstraintNumber && (key == '-' || key == ' ') {
			ops, handled = []ime.Op{{Kind: ime.OpInsert, Char: rune(key)}}, true
		} else {
			ops, handled = r.inputMethodAutomata(instance).Press(key)
		}
		if !handled {
			return 0, nil
		}
		for _, op := range ops {
			if err := r.queueInputMethodEdit(listener, op); err != nil {
				return 0, err
			}
		}
		return 1, nil
	case "getCurrentInputMode()I", "getCurrentMode()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(r.inputMethodAutomata(instance).CurrentMode()), nil
	case "changeCurrentModeToNext()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		r.nextInputMethodMode(instance)
		return 0, nil
	case "setInputMethodListener(Lorg/kwis/msp/lcdui/InputMethodListener;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		listener, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if listener == 0 {
			delete(r.inputListeners, instance)
		} else {
			r.inputListeners[instance] = listener
		}
		return 0, nil
	case "setSymbolPosition(IIII)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		var bounds [4]int32
		for index := range bounds {
			value, parameterErr := r.parameter(uint32(index + 2))
			if parameterErr != nil {
				return 0, parameterErr
			}
			bounds[index] = int32(value)
		}
		if bounds[2] <= 0 || bounds[3] <= 0 {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		r.inputSymbolBounds[instance] = bounds
		return 0, nil
	case "hideSymbolCard()V":
		// Symbol selection is hostless; hiding it has no visible side effect.
		return 0, nil
	default:
		return 0, nil
	}
}
