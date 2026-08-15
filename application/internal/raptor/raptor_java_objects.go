package raptor

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func (r *Runtime) NewRaptorJavaObject(holder uint32) (uint32, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	class := r.raptorJavaClassForObject(java, holder)
	if class != nil {
		holder = class.Holder
	}
	class, err = r.inspectRaptorJavaClass(java, holder)
	if err != nil {
		if host := java.classes[holder]; host != nil {
			class = host
		} else {
			return 0, err
		}
	}
	if class.vtable == 0 && len(java.flatVirtual) != 0 {
		if err := r.buildRaptorJavaVTable(java, class, uint32(len(java.flatVirtual))); err != nil {
			return 0, err
		}
	}
	instance, err := r.Public.Heap.Allocate(12, true)
	if err != nil || instance == 0 {
		return 0, errors.New("allocate Raptor Java object")
	}
	fields, err := r.Public.Heap.Allocate(max(uint32(4), class.fieldSize*4), true)
	if err != nil || fields == 0 {
		return 0, errors.New("allocate Raptor Java object fields")
	}
	if err := r.Public.WriteU32(instance, class.vtable); err != nil {
		return 0, err
	}
	if err := r.Public.WriteU32(instance+4, holder); err != nil {
		return 0, err
	}
	if err := r.Public.WriteU32(instance+8, fields); err != nil {
		return 0, err
	}
	hostName := class.Name
	if class.hostClass == 0 {
		hostName = class.parentName
	}
	for hostName != "" && java.ClassByName[hostName] != nil &&
		java.ClassByName[hostName].hostClass == 0 {
		hostName = java.ClassByName[hostName].parentName
	}
	if hostName == "" {
		hostName = "java/lang/Object"
	}
	hostClass, err := java.Host.EnsureJavaClass(hostName)
	if err != nil {
		return 0, err
	}
	hostInfo, err := java.Host.InspectJavaClass(hostClass)
	if err != nil {
		return 0, err
	}
	mirror, err := java.Host.NewJavaInstanceForClass(hostInfo)
	if err != nil {
		return 0, err
	}
	java.lgtToKTF[instance] = mirror
	java.ktfToLGT[mirror] = instance
	if class.vtable != 0 {
		if err := r.Public.WriteU32(instance, class.vtable); err != nil {
			return 0, err
		}
	}
	return instance, nil
}

func (r *Runtime) ensureRaptorJavaClassObject(
	java *JavaRuntime,
	class *raptorJavaClass,
) (uint32, error) {
	if class.classObject != 0 {
		return class.classObject, nil
	}
	object, err := r.Public.Heap.Allocate(12, true)
	if err != nil || object == 0 {
		return 0, errors.New("allocate Raptor Java class object")
	}
	// Class data includes eight VM bookkeeping words followed by storage whose
	// high-water mark is described independently from instance field size.
	// AOT code addresses static fields through staticBase; ignoring it lets a
	// large class overwrite the allocation that follows its class object.
	dataWords := max(class.fieldSize, class.staticBase) + 8
	data, err := r.Public.Heap.Allocate(dataWords*4, true)
	if err != nil || data == 0 {
		return 0, errors.New("allocate Raptor Java class data")
	}
	if err := r.Public.WriteU32(object+4, class.Holder); err != nil {
		return 0, err
	}
	if err := r.Public.WriteU32(object+8, data); err != nil {
		return 0, err
	}
	class.classObject = object
	if err := r.writeRaptorJavaClassState(class, 3); err != nil {
		return 0, err
	}
	return object, nil
}

func (r *Runtime) writeRaptorJavaClassState(
	class *raptorJavaClass,
	state uint16,
) error {
	if class.classObject == 0 {
		return errors.New("Raptor Java class object is absent")
	}
	data, err := r.Public.ReadU32(class.classObject + 8)
	if err != nil {
		return err
	}
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], state)
	return r.CPU.WriteMemory(data+0x10, encoded[:])
}

func (r *Runtime) raptorJavaClassForObject(
	java *JavaRuntime,
	object uint32,
) *raptorJavaClass {
	if object == 0 {
		return nil
	}
	if class := java.classes[object]; class != nil {
		return class
	}
	for _, class := range java.classes {
		if class.classObject != 0 && class.classObject == object {
			return class
		}
	}
	holder, err := r.Public.ReadU32(object + 4)
	if err != nil {
		return nil
	}
	return java.classes[holder]
}

func (r *Runtime) newRaptorJavaArray(element, count uint32) (uint32, error) {
	if count > 1<<24 {
		return 0, fmt.Errorf("Raptor Java array length %d exceeds limit", count)
	}
	elementSize := uint32(4)
	if element != 0 && element <= 0x100 {
		switch byte(element) {
		case 'Z', 'B':
			elementSize = 1
		case 'C', 'S':
			elementSize = 2
		case 'J', 'D':
			elementSize = 8
		}
	}
	instance, err := r.Public.Heap.Allocate(12, true)
	if err != nil || instance == 0 {
		return 0, errors.New("allocate Raptor Java array")
	}
	body, err := r.Public.Heap.Allocate(4+count*elementSize, true)
	if err != nil || body == 0 {
		return 0, errors.New("allocate Raptor Java array body")
	}
	if err := r.Public.WriteU32(instance+8, body); err != nil {
		return 0, err
	}
	if err := r.Public.WriteU32(body, count); err != nil {
		return 0, err
	}
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	className := "[Ljava/lang/Object;"
	if element != 0 && element <= 0x100 {
		className = "[" + string(byte(element))
	}
	mirror, err := java.Host.NewJavaArray(className, count, elementSize)
	if err != nil {
		return 0, err
	}
	java.lgtToKTF[instance] = mirror
	java.ktfToLGT[mirror] = instance
	return instance, nil
}

func (r *Runtime) storeRaptorJavaArray(
	array, index, value uint32,
) error {
	if array == 0 {
		return errors.New("Raptor Java array is null")
	}
	body, err := r.Public.ReadU32(array + 8)
	if err != nil || body == 0 {
		return errors.New("Raptor Java array body is null")
	}
	length, err := r.Public.ReadU32(body)
	if err != nil {
		return err
	}
	if index >= length {
		return fmt.Errorf("Raptor Java array index %d exceeds length %d", index, length)
	}
	if err := r.Public.WriteU32(body+4+index*4, value); err != nil {
		return err
	}
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return err
	}
	if mirror := java.lgtToKTF[array]; mirror != 0 {
		mirrorWords, readErr := java.Host.ReadWords(mirror, 2)
		if readErr != nil {
			return readErr
		}
		if mapped := java.lgtToKTF[value]; mapped != 0 {
			value = mapped
		}
		return java.Host.WriteU32(mirrorWords[0]+8+index*4, value)
	}
	return nil
}

func (r *Runtime) checkRaptorJavaType() (guest.WIPIReturn, string, bool, error) {
	instance, err := r.CPU.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return guest.WIPIReturn{}, "RAPTOR.java.checkType", true, err
	}
	if instance == 0 {
		return guest.WIPIReturn{}, "RAPTOR.java.checkType", true, nil
	}
	return guest.WIPIReturn{Low: 1}, "RAPTOR.java.checkType", true, nil
}

func (r *Runtime) callJavaHostMethod(
	ctx context.Context,
	method raptorJavaMethod,
) (guest.WIPIReturn, error) {
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	if method.Name == "<class>" {
		class := java.ClassByName[method.className]
		if class == nil {
			return guest.WIPIReturn{}, fmt.Errorf(
				"Raptor Java host class %q is absent",
				method.className,
			)
		}
		return guest.WIPIReturn{Low: class.Holder}, nil
	}
	if method.Name == "<noop>" {
		// Placeholder installed in a vtable own-method slot that no source
		// resolved. A real device ships a complete runtime vtable; we lack this
		// specific entry. Return 0 rather than branch through a null slot.
		return guest.WIPIReturn{}, nil
	}
	argumentCount := raptorJavaDescriptorArgumentCount(method.descriptor)
	if !method.isStatic {
		argumentCount++
	}
	arguments, err := r.readRaptorJavaArguments(argumentCount)
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	if !method.isStatic && len(arguments) != 0 {
		receiver := arguments[0]
		switch method.className + "." + method.Name + method.descriptor {
		case "org/kwis/msp/lcdui/Card.repaint()V",
			"org/kwis/msp/lcdui/Card.repaint(IIII)V":
			java.dirtyCards[receiver] = true
			return guest.WIPIReturn{}, nil
		case "org/kwis/msp/lcdui/Card.serviceRepaints()V":
			if !java.dirtyCards[receiver] {
				return guest.WIPIReturn{}, nil
			}
			delete(java.dirtyCards, receiver)
			return guest.WIPIReturn{}, r.paintRaptorJavaCard(ctx, java, receiver)
		case "org/kwis/msp/lcdui/Display.getDockedCard()Lorg/kwis/msp/lcdui/Card;":
			return guest.WIPIReturn{Low: java.currentCard}, nil
		case "org/kwis/msp/lcdui/Display.pushCard(Lorg/kwis/msp/lcdui/Card;)V":
			if len(arguments) < 2 {
				return guest.WIPIReturn{}, errors.New("Raptor Java pushCard has no card")
			}
			java.currentCard = arguments[1]
			if java.currentCard == 0 {
				return guest.WIPIReturn{}, nil
			}
			return guest.WIPIReturn{}, r.paintRaptorJavaCard(ctx, java, java.currentCard)
		case "java/lang/Thread.start()V":
			target := uint32(0)
			if mirror := java.lgtToKTF[receiver]; mirror != 0 {
				target = java.ktfToLGT[java.Host.ThreadTargets[mirror]]
			}
			if target == 0 {
				target = receiver
			}
			java.threadTargets = append(java.threadTargets, target)
			class := r.raptorJavaClassForObject(java, target)
			for depth := 0; class != nil && depth < 256; depth++ {
				if run, found := DeclaredMethod(class, "run", "()V"); found && run.Body != 0 {
					java.Tasks = append(java.Tasks, &JavaTask{
						Target: target, Procedure: run.Body,
					})
					break
				}
				class = java.ClassByName[class.parentName]
			}
			return guest.WIPIReturn{}, nil
		case "org/kwis/msp/lcdui/Display.callSerially(Ljava/lang/Runnable;)V",
			"org/kwis/msp/lcdui/Display.callSerially(Ljava/lang/Runnable;I)V":
			// The Runnable is a Raptor Java object; resolve and run its run()
			// through the Raptor runtime, whose vtable matches the guest. Routing
			// it through the KTF mirror (the default path below) resolves run() to
			// a bogus body because the mirror lacks the linked method table.
			if len(arguments) < 2 || arguments[1] == 0 {
				return guest.WIPIReturn{}, nil
			}
			runnable := arguments[1]
			class := r.raptorJavaClassForObject(java, runnable)
			for depth := 0; class != nil && depth < 256; depth++ {
				if run, found := DeclaredMethod(class, "run", "()V"); found && run.Body != 0 {
					if r.Public.InvokeSync == nil {
						break
					}
					_, callErr := r.Public.InvokeSync(ctx, wipirt.GuestCallback{
						Procedure: run.Body,
						Args:      [4]uint32{runnable},
					})
					return guest.WIPIReturn{}, callErr
				}
				class = java.ClassByName[class.parentName]
			}
			return guest.WIPIReturn{}, nil
		}
	}
	for index, value := range arguments {
		if mirror := java.lgtToKTF[value]; mirror != 0 {
			arguments[index] = mirror
		}
	}
	data := make([]byte, len(arguments)*4)
	for index, value := range arguments {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	if err := r.CPU.WriteMemory(java.scratch, data); err != nil {
		return guest.WIPIReturn{}, err
	}
	parameterBase := java.Host.NativeParameterBase
	java.Host.NativeParameterBase = java.scratch
	value, callErr := ktfrt.HostJavaMethod(
		method.className,
		method.Name,
		method.descriptor,
	)(ctx, java.Host)
	java.Host.NativeParameterBase = parameterBase
	if callErr != nil {
		return guest.WIPIReturn{}, callErr
	}
	if raptorJavaDescriptorReturnsReference(method.descriptor) && value != 0 {
		value, err = r.wrapRaptorJavaObject(java, value)
		if err != nil {
			return guest.WIPIReturn{}, err
		}
	}
	return guest.WIPIReturn{Low: value, High: java.Host.JavaReturnHigh}, nil
}

func (r *Runtime) paintRaptorJavaCard(
	ctx context.Context,
	java *JavaRuntime,
	card uint32,
) error {
	class := r.raptorJavaClassForObject(java, card)
	var paint raptorJavaDeclaredMethod
	found := false
	for depth := 0; class != nil && depth < 256; depth++ {
		if paint, found = DeclaredMethod(
			class,
			"paint",
			"(Lorg/kwis/msp/lcdui/Graphics;)V",
		); found {
			break
		}
		class = java.ClassByName[class.parentName]
	}
	if !found || paint.Body == 0 {
		return nil
	}
	graphicsMirror, err := java.Host.EnsureScreenGraphics()
	if err != nil {
		return err
	}
	java.Host.ResetScreenGraphics(graphicsMirror)
	graphics, err := r.wrapRaptorJavaObject(java, graphicsMirror)
	if err != nil {
		return err
	}
	if r.Public.InvokeSync == nil {
		return errors.New("Raptor Java callback bridge is unavailable")
	}
	if _, err := r.Public.InvokeSync(ctx, wipirt.GuestCallback{
		Procedure: paint.Body,
		Args:      [4]uint32{card, graphics},
	}); err != nil {
		return err
	}
	return java.Host.RecordPresentation()
}
