package ktf

import (
	"context"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strings"
)

func (r *Runtime) handleClassMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "getName()Ljava/lang/String;":
		classObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		classAddress, err := r.javaClassObjectTarget(classObject)
		if err != nil {
			return 0, err
		}
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(strings.ReplaceAll(class.Name, "/", "."))
	case "isAssignableFrom(Ljava/lang/Class;)Z":
		expectedObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		expected, err := r.javaClassObjectTarget(expectedObject)
		if err != nil {
			return 0, err
		}
		actualObject, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		actual, err := r.javaClassObjectTarget(actualObject)
		if err != nil {
			return 0, err
		}
		for depth := 0; actual != 0; depth++ {
			if depth >= 256 {
				return 0, errors.New("KTF Java class hierarchy exceeds limit")
			}
			if actual == expected {
				return 1, nil
			}
			class, inspectErr := r.InspectJavaClass(actual)
			if inspectErr != nil {
				return 0, inspectErr
			}
			actual = class.Parent
		}
		return 0, nil
	case "getResourceAsStream(Ljava/lang/String;)Ljava/io/InputStream;":
		classObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		nameAddress, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		className := ""
		if classAddress, classErr := r.javaClassObjectTarget(classObject); classErr == nil {
			if class, inspectErr := r.InspectJavaClass(classAddress); inspectErr == nil {
				className = class.Name
			}
		}
		resourceName := strings.ReplaceAll(r.javaText(nameAddress), `\`, "/")
		resourceName = strings.TrimPrefix(resourceName, "/")
		resourceName = path.Clean(resourceName)
		if resourceName == "." || resourceName == ".." ||
			strings.HasPrefix(resourceName, "../") {
			return 0, nil
		}
		data, ok := r.Pkg.Resources[resourceName]
		if !ok {
			for candidate, payload := range r.Pkg.Resources {
				if strings.EqualFold(candidate, resourceName) {
					resourceName = candidate
					data = payload
					ok = true
					break
				}
			}
		}
		r.tracef(
			"java_resource:%s:class=%s:found=%t:size=%d",
			resourceName,
			className,
			ok,
			len(data),
		)
		if !ok {
			return 0, nil
		}
		classAddress, err := r.EnsureJavaClass("java/io/InputStream")
		if err != nil {
			return 0, err
		}
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		instance, err := r.NewJavaInstanceForClass(class)
		if err != nil {
			return 0, err
		}
		r.inputStreams[instance] = &ktfInputStream{data: data}
		return instance, nil
	case "forName(Ljava/lang/String;)Ljava/lang/Class;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		className := strings.ReplaceAll(r.javaText(nameAddress), ".", "/")
		if className == "" {
			return 0, nil
		}
		classAddress := r.JavaClasses[className]
		if classAddress == 0 {
			if _, hostClass := HostJavaClassSpecs[className]; hostClass {
				classAddress, err = r.EnsureJavaClass(className)
				if err != nil {
					return 0, err
				}
			} else {
				class, loadErr := r.LoadClass(ctx, className)
				if loadErr != nil {
					r.tracef(
						"java_class_for_name:%s:found=false",
						className,
					)
					return 0, nil
				}
				classAddress = class.Address
			}
		}
		r.tracef("java_class_for_name:%s:found=true", className)
		return r.javaClassObject(classAddress)
	case "isArray()Z", "isInterface()Z":
		// Host class objects only model loadable classes; arrays and
		// interfaces are never materialized through Class objects here.
		return 0, nil
	case "isInstance(Ljava/lang/Object;)Z":
		classObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		expected, err := r.javaClassObjectTarget(classObject)
		if err != nil {
			return 0, err
		}
		object, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if object == 0 {
			return 0, nil
		}
		actual, err := r.ReadU32(object + 4)
		if err != nil {
			return 0, err
		}
		for depth := 0; actual != 0; depth++ {
			if depth >= 256 {
				return 0, errors.New("KTF Java class hierarchy exceeds limit")
			}
			if actual == expected {
				return 1, nil
			}
			class, inspectErr := r.InspectJavaClass(actual)
			if inspectErr != nil {
				return 0, nil
			}
			actual = class.Parent
		}
		return 0, nil
	case "newInstance()Ljava/lang/Object;":
		classObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		classAddress, err := r.javaClassObjectTarget(classObject)
		if err != nil {
			return 0, err
		}
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		instance, err := r.NewJavaInstanceForClass(class)
		if err != nil {
			return 0, err
		}
		if _, invokeErr := r.invokeJavaVirtual(
			ctx,
			instance,
			"<init>",
			"()V",
		); invokeErr != nil {
			r.tracef("java_class_new_instance_init:%s", invokeErr)
		}
		return instance, nil
	case "toString()Ljava/lang/String;":
		classObject, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		classAddress, err := r.javaClassObjectTarget(classObject)
		if err != nil {
			return 0, err
		}
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(
			"class " + strings.ReplaceAll(class.Name, "/", "."),
		)
	default:
		return 0, nil
	}
}

func (r *Runtime) javaClassObject(classAddress uint32) (uint32, error) {
	if classAddress == 0 {
		return 0, nil
	}
	if object := r.javaClassObjs[classAddress]; object != 0 {
		return object, nil
	}
	classClassAddress, err := r.EnsureJavaClass("java/lang/Class")
	if err != nil {
		return 0, err
	}
	classClass, err := r.InspectJavaClass(classClassAddress)
	if err != nil {
		return 0, err
	}
	object, err := r.NewJavaInstanceForClass(classClass)
	if err != nil {
		return 0, err
	}
	r.javaClassObjs[classAddress] = object
	r.classObjTarget[object] = classAddress
	return object, nil
}

func (r *Runtime) javaClassObjectTarget(object uint32) (uint32, error) {
	if object == 0 {
		return 0, errors.New("KTF java.lang.Class instance is null")
	}
	if target := r.classObjTarget[object]; target != 0 {
		return target, nil
	}
	return 0, fmt.Errorf("unknown KTF java.lang.Class instance 0x%08x", object)
}

func (r *Runtime) handleThreadMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V", "<init>(Z)V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if r.currentThread == 0 {
			r.currentThread = thread
		}
		return 0, nil
	case "<init>(Ljava/lang/Runnable;)V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		target, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		r.ThreadTargets[thread] = target
		return 0, nil
	case "start()V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if r.DeferThreads {
			target := r.ThreadTargets[thread]
			if target == 0 {
				target = thread
			}
			task, err := r.queueJavaVirtualTask(target, "run", "()V")
			if err != nil {
				return 0, err
			}
			r.deferStartedThread(task)
			return 0, nil
		}
		previous := r.currentThread
		r.currentThread = thread
		_, invokeErr := r.invokeJavaVirtual(ctx, thread, "run", "()V")
		r.currentThread = previous
		return 0, invokeErr
	case "run()V":
		thread, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		target := r.ThreadTargets[thread]
		if target == 0 {
			return 0, nil
		}
		return r.invokeJavaVirtual(ctx, target, "run", "()V")
	case "join()V", "setPriority(I)V":
		return 0, nil
	case "sleep(J)V":
		low, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		high, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		millis := int64(uint64(high)<<32 | uint64(low))
		if millis < 0 {
			return r.raiseJavaException("java/lang/IllegalArgumentException", 0)
		}
		if r.DeferThreads {
			if r.activeTask != nil && millis != 0 {
				delay := uint64(millis)
				if delay > ^uint64(0)-r.TickMS {
					r.activeTask.WakeAtMS = ^uint64(0)
				} else {
					r.activeTask.WakeAtMS = r.TickMS + delay
				}
				r.tracef(
					"java_thread_sleep:duration_ms=%d:wake_at_ms=%d",
					millis,
					r.activeTask.WakeAtMS,
				)
			}
			r.yieldRequested = true
		}
		return 0, nil
	case "yield()V":
		if r.DeferThreads {
			r.yieldRequested = true
		}
		return 0, nil
	case "isAlive()Z":
		return 0, nil
	case "currentThread()Ljava/lang/Thread;":
		if r.currentThread != 0 {
			return r.currentThread, nil
		}
		classAddress, err := r.EnsureJavaClass("java/lang/Thread")
		if err != nil {
			return 0, err
		}
		class, err := r.InspectJavaClass(classAddress)
		if err != nil {
			return 0, err
		}
		r.currentThread, err = r.NewJavaInstanceForClass(class)
		return r.currentThread, err
	case "activeCount()I":
		return 1, nil
	case "getPriority()I":
		// NORM_PRIORITY; the host scheduler runs one Java thread at a time.
		return 5, nil
	case "toString()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(fmt.Sprintf("Thread-%08x", instance))
	default:
		return 0, nil
	}
}

func (r *Runtime) handleDisplayMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(Lorg/kwis/msp/lcdui/Jlet;Lorg/kwis/msp/lcdui/DisplayProxy;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if instance == 0 {
			return 0, errors.New("initialize KTF Display: instance is null")
		}
		if r.DefaultDisplay == 0 {
			r.DefaultDisplay = instance
		}
		return 0, nil
	case "getDisplay(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Display;",
		"getDefaultDisplay()Lorg/kwis/msp/lcdui/Display;":
		return r.ensureDefaultDisplay()
	case "isDoubleBuffered()Z":
		return 1, nil
	case "getDockedCard()Lorg/kwis/msp/lcdui/Card;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.DisplayCards[instance], nil
	case "pushCard(Lorg/kwis/msp/lcdui/Card;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		card, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		wasDirty := r.dirtyCards[card]
		r.tracef(
			"java_display_push_card:card=0x%08x:dirty=%t:tasks=%d",
			card,
			wasDirty,
			len(r.Tasks),
		)
		r.DisplayCards[instance] = card
		if card == 0 {
			return 0, nil
		}
		r.dirtyCards[card] = true
		if r.DeferThreads && r.activeTask != nil {
			r.deferCardPaint(r.activeTask, card, true)
			return 0, nil
		}
		if err := r.notifyCardShown(ctx, card, true); err != nil {
			return 0, err
		}
		if err := r.paintCard(ctx, card); err != nil {
			var unhandled *ktfUnhandledJavaException
			if !errors.As(err, &unhandled) {
				return 0, err
			}
			r.paintInitializedCards[card] = true
			r.tracef(
				"java_initial_paint_discard:%s:card=0x%08x",
				unhandled.name,
				card,
			)
		}
		return 0, nil
	case "removeAllCards()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		delete(r.DisplayCards, instance)
		return 0, nil
	case "getWidth()I":
		return r.DisplayWidth(), nil
	case "getHeight()I":
		return r.displayHeight(), nil
	case "callSerially(Ljava/lang/Runnable;)V",
		"callSerially(Ljava/lang/Runnable;I)V":
		runnable, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if runnable == 0 {
			return 0, nil
		}
		if r.DeferThreads {
			return 0, r.QueueJavaVirtual(runnable, "run", "()V")
		}
		return r.invokeJavaVirtual(ctx, runnable, "run", "()V")
	case "getGameAction(I)I":
		key, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return ktfGameAction(int32(key)), nil
	case "getKeyCode(I)I":
		action, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(ktfGameKeyCode(int32(action))), nil
	case "getKeyName(I)Ljava/lang/String;":
		key, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(ktfKeyName(int32(key)))
	case "countCard()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if r.DisplayCards[instance] != 0 {
			return 1, nil
		}
		return 0, nil
	case "popCard()Lorg/kwis/msp/lcdui/Card;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		card := r.DisplayCards[instance]
		delete(r.DisplayCards, instance)
		r.tracef("java_display_pop_card:card=0x%08x", card)
		return card, nil
	case "removeCard(Lorg/kwis/msp/lcdui/Card;)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		card, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if card != 0 && r.DisplayCards[instance] == card {
			delete(r.DisplayCards, instance)
			r.tracef("java_display_remove_card:card=0x%08x", card)
			return 1, nil
		}
		return 0, nil
	case "setDockedCard(Lorg/kwis/msp/lcdui/Card;I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		card, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		r.DisplayCards[instance] = card
		if card != 0 {
			r.dirtyCards[card] = true
		}
		return 0, nil
	case "flush()V", "where()V", "grabKey(ILorg/kwis/msp/lcdui/JletEventListener;)V",
		"ungrabKey(I)V",
		"setJletEventListener(Lorg/kwis/msp/lcdui/JletEventListener;)V",
		"removeJletEventListener(Lorg/kwis/msp/lcdui/JletEventListener;)V",
		"addJletEventListener(Lorg/kwis/msp/lcdui/JletEventListener;)V":
		// Jlet event listeners and key grabs are satisfied through the
		// Card key-notify path; the display presents every frame already.
		return 0, nil
	case "isColor()Z":
		return 1, nil
	case "numColors()I":
		return 65536, nil
	case "getBitsPerPixel()I":
		return 16, nil
	case "hasPointerEvents()Z", "hasPointerMotionEvents()Z":
		return 0, nil
	case "hasRepeatEvents()Z":
		return 1, nil
	default:
		return 0, nil
	}
}

func ktfGameAction(key int32) uint32 {
	switch key {
	case -1:
		return 1 // EventQueue.UP
	case -2:
		return 6 // EventQueue.DOWN
	case -3:
		return 2 // EventQueue.LEFT
	case -4:
		return 5 // EventQueue.RIGHT
	case -5:
		return 8 // EventQueue.FIRE
	case -6:
		return 90 // EventQueue.SOFT1
	case -7:
		return 91 // EventQueue.SOFT2
	case -8:
		return 92 // EventQueue.SOFT3
	case -13:
		return 96 // EventQueue.SIDE_UP
	case -14:
		return 97 // EventQueue.SIDE_DOWN
	case -15:
		return 98 // EventQueue.SIDE_SEL
	case -16:
		return 99 // EventQueue.CLEAR
	default:
		return uint32(key)
	}
}

func ktfGameKeyCode(action int32) int32 {
	switch action {
	case 1: // EventQueue.UP
		return -1
	case 6: // EventQueue.DOWN
		return -2
	case 2: // EventQueue.LEFT
		return -3
	case 5: // EventQueue.RIGHT
		return -4
	case 8: // EventQueue.FIRE
		return -5
	case 90: // EventQueue.SOFT1
		return -6
	case 91: // EventQueue.SOFT2
		return -7
	case 92: // EventQueue.SOFT3
		return -8
	case 96: // EventQueue.SIDE_UP
		return -13
	case 97: // EventQueue.SIDE_DOWN
		return -14
	case 98: // EventQueue.SIDE_SEL
		return -15
	case 99: // EventQueue.CLEAR
		return -16
	default:
		return action
	}
}

func ktfKeyName(key int32) string {
	switch key {
	case -1:
		return "UP"
	case -2:
		return "DOWN"
	case -3:
		return "LEFT"
	case -4:
		return "RIGHT"
	case -5:
		return "FIRE"
	case -6:
		return "SOFT1"
	case -7:
		return "SOFT2"
	case -8:
		return "SOFT3"
	case -10:
		return "SEND"
	case -11:
		return "END"
	case -12:
		return "POWER"
	case -13:
		return "SIDE_UP"
	case -14:
		return "SIDE_DOWN"
	case -15:
		return "SIDE_SEL"
	case -16:
		return "CLEAR"
	case '*', '#', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return string(rune(key))
	default:
		return ""
	}
}

func (r *Runtime) ensureDefaultDisplay() (uint32, error) {
	if r.DefaultDisplay != 0 {
		return r.DefaultDisplay, nil
	}
	classAddress, err := r.EnsureJavaClass("org/kwis/msp/lcdui/Display")
	if err != nil {
		return 0, err
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	r.DefaultDisplay, err = r.NewJavaInstanceForClass(class)
	if err != nil {
		return 0, err
	}
	return r.DefaultDisplay, nil
}

func (r *Runtime) ensureJavaRuntime() (uint32, error) {
	if r.defaultRuntime != 0 {
		return r.defaultRuntime, nil
	}
	classAddress, err := r.EnsureJavaClass("java/lang/Runtime")
	if err != nil {
		return 0, err
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	r.defaultRuntime, err = r.NewJavaInstanceForClass(class)
	if err != nil {
		return 0, err
	}
	return r.defaultRuntime, nil
}

func (r *Runtime) invokeJavaVirtual(
	ctx context.Context,
	instance uint32,
	name, descriptor string,
	args ...uint32,
) (uint32, error) {
	if instance == 0 {
		return 0, fmt.Errorf("invoke Java method %s%s: instance is null", name, descriptor)
	}
	instanceWords, err := r.ReadWords(instance, 2)
	if err != nil {
		return 0, err
	}
	methodAddress, err := r.resolveJavaMethod(instanceWords[1], name, descriptor)
	if err != nil {
		return 0, err
	}
	method, err := r.InspectJavaMethod(methodAddress)
	if err != nil {
		return 0, err
	}
	if method.Body == 0 {
		return 0, fmt.Errorf(
			"Java class 0x%08x method %s%s has no executable body",
			instanceWords[1],
			name,
			descriptor,
		)
	}
	callArgs := make([]uint32, 0, len(args)+2)
	callArgs = append(callArgs, 0, instance)
	callArgs = append(callArgs, args...)
	result, value, err := r.call(
		ctx,
		method.Body,
		callArgs,
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"invoke Java method %s%s at PC 0x%08x after %d instructions: %w",
			name,
			descriptor,
			result.PC,
			result.Instructions,
			err,
		)
	}
	return value, nil
}
