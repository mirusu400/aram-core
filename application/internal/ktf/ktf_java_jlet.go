package ktf

import "context"

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

// InputMethodHandler models a handset IME the host never opens: text input
// arrives fully composed through the text component natives.
func (r *Runtime) handleInputMethodHandlerMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "getCurrentModeCode()Ljava/lang/String;":
		return r.NewJavaString("")
	case "setCurrentMode(I)Z":
		return 1, nil
	case "notifyKeyInput(II)Z":
		return 0, nil
	case "getCurrentInputMode()I", "getCurrentMode()I":
		return 0, nil
	case "changeCurrentModeToNext()V", "hideSymbolCard()V",
		"setInputMethodListener(Lorg/kwis/msp/lcdui/InputMethodListener;)V",
		"setSymbolPosition(IIII)V":
		return 0, nil
	default:
		return 0, nil
	}
}
