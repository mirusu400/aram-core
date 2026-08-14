package ktf

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

func (r *Runtime) handleEventQueueMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "getNextEvent([I)V":
		// The host delivers input through Card.keyNotify, so the queue is
		// always empty. Zero the caller's buffer and yield so polling
		// loops cannot starve the scheduler.
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if array != 0 {
			length, lengthErr := r.javaArrayLength(array)
			if lengthErr != nil {
				return 0, lengthErr
			}
			fields, fieldsErr := r.ReadU32(array)
			if fieldsErr != nil {
				return 0, fieldsErr
			}
			if length != 0 {
				if writeErr := r.CPU.WriteMemory(
					fields+8,
					make([]byte, length*4),
				); writeErr != nil {
					return 0, writeErr
				}
			}
		}
		if r.DeferThreads {
			r.yieldRequested = true
		}
		return 0, nil
	case "dispatchEvent([I)V":
		return 0, nil
	case "postEvent([I)Z":
		r.trace("java_event_queue_post_dropped")
		return 1, nil
	case "hookEvent(ILorg/kwis/msp/lcdui/JletEventListener;)V":
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
