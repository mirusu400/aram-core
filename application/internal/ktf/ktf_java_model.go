package ktf

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
)

func findKTFJavaMethod(class JavaClass, name, descriptor string) (JavaMethod, bool) {
	// Host augmentation appends a concrete implementation after an abstract
	// or unusable framework declaration with the same signature. The last
	// declaration is therefore the effective one, matching vtable rebuilding.
	for index := len(class.Methods) - 1; index >= 0; index-- {
		method := class.Methods[index]
		if method.Name == name && method.Descriptor == descriptor {
			return method, true
		}
	}
	return JavaMethod{}, false
}

func findKTFDeclaredJavaMethod(
	class JavaClass,
	name, descriptor string,
) (JavaMethod, bool) {
	for index := len(class.Methods) - 1; index >= 0; index-- {
		method := class.Methods[index]
		if method.DeclaringClass == class.Address &&
			method.Name == name &&
			method.Descriptor == descriptor {
			return method, true
		}
	}
	return JavaMethod{}, false
}

func (r *Runtime) ensureJavaClassInitialized(
	ctx context.Context,
	class JavaClass,
) (returnedErr error) {
	switch r.javaClassInit[class.Address] {
	case ktfJavaClassInitializing, ktfJavaClassInitialized:
		return nil
	}
	r.javaClassInit[class.Address] = ktfJavaClassInitializing
	defer func() {
		if returnedErr != nil {
			delete(r.javaClassInit, class.Address)
			return
		}
		r.javaClassInit[class.Address] = ktfJavaClassInitialized
	}()

	if class.Parent != 0 {
		parent, err := r.InspectJavaClass(class.Parent)
		if err != nil {
			return fmt.Errorf(
				"inspect parent of Java class %q: %w",
				class.Name,
				err,
			)
		}
		if err := r.ensureJavaClassInitialized(ctx, parent); err != nil {
			return fmt.Errorf(
				"initialize parent of Java class %q: %w",
				class.Name,
				err,
			)
		}
	}
	initializer, ok := findKTFDeclaredJavaMethod(
		class,
		"<clinit>",
		"()V",
	)
	if !ok || initializer.Body == 0 {
		r.tracef("java_class_initialized:%s:no_clinit", class.Name)
		return nil
	}
	result, _, err := r.call(
		ctx,
		initializer.Body,
		nil,
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf(
			"run Java class initializer %s.<clinit>()V at PC 0x%08x "+
				"after %d instructions: %w",
			class.Name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	r.tracef(
		"java_class_initialized:%s:<clinit>@0x%08x",
		class.Name,
		initializer.Body,
	)
	return nil
}

func (r *Runtime) NewJavaInstanceForClass(class JavaClass) (uint32, error) {
	vtableIndex, err := r.ensureJavaVTableIndex(class.Address, class.VTable)
	if err != nil {
		return 0, err
	}
	instance, err := r.AllocateWords(2)
	if err != nil {
		return 0, err
	}
	fields, err := r.allocateJavaHeapBytes(uint32(class.FieldSize)+4, true)
	if err != nil {
		return 0, err
	}
	if fields == 0 {
		return 0, errors.New("KTF guest heap exhausted allocating Java object fields")
	}
	if err := r.WriteU32(fields, (vtableIndex*4)<<5); err != nil {
		return 0, err
	}
	if err := r.writeWords(instance, []uint32{fields, class.Address}); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *Runtime) NewJavaString(value string) (uint32, error) {
	instance, err := r.newJavaInstance("java/lang/String", 0)
	if err != nil {
		return 0, err
	}
	if err := r.materializeJavaString(instance, value); err != nil {
		return 0, err
	}
	return instance, nil
}

// materializeJavaString records value for instance host-side and writes the
// value/offset/count fields plus a fresh char array into guest memory.
// Platform AOT code (String.getChars, compiled concatenation) reads those
// fields directly, so a map-only string turns into NUL characters the moment
// the guest copies it (issue #44).
func (r *Runtime) materializeJavaString(instance uint32, value string) error {
	codeUnits := utf16.Encode([]rune(value))
	characters, err := r.NewJavaArray(
		"[C",
		uint32(len(codeUnits)),
		2,
	)
	if err != nil {
		return err
	}
	fields, err := r.ReadU32(characters)
	if err != nil {
		return err
	}
	if len(codeUnits) != 0 {
		encoded := make([]byte, len(codeUnits)*2)
		for index, codeUnit := range codeUnits {
			binary.LittleEndian.PutUint16(encoded[index*2:], codeUnit)
		}
		if err := r.CPU.WriteMemory(fields+8, encoded); err != nil {
			return err
		}
	}
	if err := r.WriteJavaFieldWord(instance, 0, characters); err != nil {
		return err
	}
	if err := r.WriteJavaFieldWord(instance, 4, 0); err != nil {
		return err
	}
	if err := r.WriteJavaFieldWord(
		instance,
		8,
		uint32(len(codeUnits)),
	); err != nil {
		return err
	}
	r.JavaStrings[instance] = value
	return nil
}

func (r *Runtime) newJavaReferenceArray(
	className string,
	elements []uint32,
) (uint32, error) {
	instance, err := r.NewJavaArray(className, uint32(len(elements)), 4)
	if err != nil {
		return 0, err
	}
	instanceWords, err := r.ReadWords(instance, 2)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(instanceWords[0]+8, elements); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *Runtime) NewJavaArray(
	className string,
	count uint32,
	elementSize uint32,
) (uint32, error) {
	if elementSize == 0 || elementSize > 8 {
		return 0, fmt.Errorf("invalid KTF Java array element size %d", elementSize)
	}
	if uint64(count)*uint64(elementSize)+8 > uint64(^uint32(0)) {
		return 0, fmt.Errorf(
			"KTF Java array allocation overflows: count=%d element_size=%d",
			count,
			elementSize,
		)
	}
	classAddress, err := r.EnsureJavaClass(className)
	if err != nil {
		return 0, err
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	vtableIndex, err := r.ensureJavaVTableIndex(class.Address, class.VTable)
	if err != nil {
		return 0, err
	}
	instance, err := r.AllocateWords(2)
	if err != nil {
		return 0, err
	}
	fields, err := r.allocateJavaHeapBytes(count*elementSize+8, true)
	if err != nil {
		return 0, err
	}
	if fields == 0 {
		return 0, errors.New("KTF guest heap exhausted allocating Java array")
	}
	if err := r.WriteU32(fields, (vtableIndex*4)<<5); err != nil {
		return 0, err
	}
	if err := r.writeWords(instance, []uint32{fields, class.Address}); err != nil {
		return 0, err
	}
	if err := r.WriteU32(fields+4, count); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *Runtime) RegisterHostCall(name string, handler ktfHostHandler) uint32 {
	address := r.nextHostCall
	if address+4 > HostBase+HostSize {
		panic("KTF host-call page exhausted")
	}
	r.nextHostCall += 4
	r.hostCalls[address] = ktfHostCall{name: name, handler: handler}
	return address | 1
}

func (r *Runtime) AllocateWords(count uint32) (uint32, error) {
	if count > ^uint32(0)/4 {
		return 0, errors.New("KTF allocation word count overflows")
	}
	address, err := r.allocateJavaHeapBytes(count*4, true)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return 0, fmt.Errorf("KTF guest heap exhausted allocating %d words", count)
	}
	return address, nil
}

func (r *Runtime) allocateBytes(data []byte, terminate bool) (uint32, error) {
	size := len(data)
	if terminate {
		size++
	}
	if uint64(size) > uint64(^uint32(0)) {
		return 0, errors.New("KTF byte allocation exceeds uint32")
	}
	address, err := r.allocateJavaHeapBytes(uint32(size), true)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return 0, fmt.Errorf("KTF guest heap exhausted allocating %d bytes", size)
	}
	if err := r.CPU.WriteMemory(address, data); err != nil {
		return 0, err
	}
	return address, nil
}

func (r *Runtime) writeWords(address uint32, words []uint32) error {
	data := make([]byte, len(words)*4)
	for index, value := range words {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	if err := r.CPU.WriteMemory(address, data); err != nil {
		return fmt.Errorf("write KTF structure at 0x%08x: %w", address, err)
	}
	return nil
}

func ktfAlloc(_ context.Context, runtime *Runtime) (uint32, error) {
	size, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := runtime.Heap.Allocate(size, false)
	if err != nil {
		return 0, err
	}
	return address, nil
}

func ktfUnsupportedJavaCallback(name string) ktfHostHandler {
	return func(context.Context, *Runtime) (uint32, error) {
		return 0, fmt.Errorf("%s is not implemented", name)
	}
}

func ktfJavaThrow(_ context.Context, runtime *Runtime) (uint32, error) {
	runtime.snapshotJavaThrow()
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if nameAddress == 0 {
		return 0, errors.New("KTF Java exception has a null class name")
	}
	name, err := runtime.readCString(nameAddress, 1024)
	if err != nil {
		return 0, fmt.Errorf(
			"read KTF Java exception class at 0x%08x: %w",
			nameAddress,
			err,
		)
	}
	runtime.rememberJavaThrowName(name)
	detail, _ := runtime.parameter(1)
	return runtime.raiseJavaException(name, detail)
}

func ktfJavaThrowObject(_ context.Context, runtime *Runtime) (uint32, error) {
	runtime.snapshotJavaThrow()
	detail, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if detail == 0 {
		return 0, errors.New("KTF Java exception object is null")
	}
	classAddress, err := runtime.ReadU32(detail + 4)
	if err != nil {
		return 0, fmt.Errorf(
			"read KTF Java exception object class at 0x%08x: %w",
			detail,
			err,
		)
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		return 0, fmt.Errorf(
			"inspect KTF Java exception object class at 0x%08x: %w",
			classAddress,
			err,
		)
	}
	runtime.rememberJavaThrowName(class.Name)
	return runtime.raiseJavaException(class.Name, detail)
}

func (r *Runtime) snapshotJavaThrow() {
	r.LastJavaThrowRegisters = make([]uint32, cpu.RegisterR12+1)
	for register := range r.LastJavaThrowRegisters {
		r.LastJavaThrowRegisters[register], _ =
			r.CPU.ReadRegister(uint32(register))
	}
	r.LastJavaThrowSP, _ = r.CPU.ReadRegister(cpu.RegisterSP)
	r.LastJavaThrowStack, _ = r.ReadWords(r.LastJavaThrowSP, 64)
	r.tracef("java_throw_snapshot:sp=0x%08x", r.LastJavaThrowSP)
}

func (r *Runtime) rememberJavaThrowName(name string) {
	r.LastJavaThrowName = name
	if r.FirstJavaThrowName != "" {
		return
	}
	r.FirstJavaThrowName = name
	r.FirstJavaThrowRegisters = append(
		[]uint32(nil),
		r.LastJavaThrowRegisters...,
	)
	r.FirstJavaThrowSP = r.LastJavaThrowSP
	r.FirstJavaThrowStack = append(
		[]uint32(nil),
		r.LastJavaThrowStack...,
	)
}

func (r *Runtime) raiseJavaException(
	name string,
	detail uint32,
) (uint32, error) {
	if detail == 0 {
		var err error
		detail, err = r.NewHostJavaObject(name)
		if err != nil {
			return 0, fmt.Errorf(
				"construct KTF Java exception %s: %w",
				name,
				err,
			)
		}
	}
	r.JavaExceptionFrames = r.JavaExceptionFrames[:0]
	target, caught, dispatchErr := r.dispatchJavaException(name, detail)
	if dispatchErr != nil {
		return 0, fmt.Errorf("dispatch KTF Java exception %s: %w", name, dispatchErr)
	}
	if caught {
		r.tracef(
			"java_exception_caught:%s@0x%08x:restore=0x%08x",
			name,
			target.handler,
			target.restore,
		)
		return 0, &ktfJavaExceptionUnwind{Target: target}
	}
	exceptionState, _ := r.ReadWords(
		r.exceptionContext,
		ktfJavaEnvironmentWords,
	)
	if r.LastJavaMethod != "" {
		return 0, &ktfUnhandledJavaException{
			name:   name,
			detail: detail,
			Context: fmt.Sprintf(
				"after=%s, return=0x%08x, jump=0x%08x, "+
					"call_lr=0x%08x, frames=%v, state=%08x",
				r.LastJavaMethod,
				r.LastJavaReturn,
				r.lastJavaJump,
				r.LastJavaCallLR,
				r.JavaExceptionFrames,
				exceptionState,
			),
		}
	}
	return 0, &ktfUnhandledJavaException{
		name:    name,
		detail:  detail,
		Context: fmt.Sprintf("state=%08x", exceptionState),
	}
}

func (r *Runtime) dispatchJavaException(
	name string,
	detail uint32,
) (ktfJavaExceptionTarget, bool, error) {
	if r.exceptionContext == 0 {
		return ktfJavaExceptionTarget{}, false, nil
	}
	frame, err := r.ReadU32(r.exceptionContext + 8*4)
	if err != nil {
		return ktfJavaExceptionTarget{}, false, err
	}
	for depth := 0; frame != 0; depth++ {
		if depth >= 4096 {
			return ktfJavaExceptionTarget{}, false, errors.New(
				"KTF Java exception frame chain exceeds limit",
			)
		}
		frameWords, err := r.ReadWords(frame, 6)
		if err != nil {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"inspect Java exception frame 0x%08x: %w",
				frame,
				err,
			)
		}
		methodAddress := frameWords[0]
		previousFrame := frameWords[2]
		bytecodePC := frameWords[3]
		methodWords, err := r.ReadWords(methodAddress, 7)
		if err != nil {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"inspect Java exception method 0x%08x: %w",
				methodAddress,
				err,
			)
		}
		methodName := fmt.Sprintf("0x%08x", methodAddress)
		if method, inspectErr := r.InspectJavaMethod(methodAddress); inspectErr == nil {
			methodName = method.Name + method.Descriptor
			if declaring, classErr := r.InspectJavaClass(
				methodWords[1],
			); classErr == nil {
				methodName = declaring.Name + "." + methodName
			}
		}
		frameTrace := fmt.Sprintf(
			"java_exception_frame:%s:bcp=%d:frame=0x%08x",
			methodName,
			bytecodePC,
			frame,
		)
		r.trace(frameTrace)
		r.JavaExceptionFrames = append(r.JavaExceptionFrames, frameTrace)
		exceptionCount := int(methodWords[4] & 0xffff)
		if exceptionCount > 4096 {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"Java method 0x%08x has excessive exception count %d",
				methodAddress,
				exceptionCount,
			)
		}
		table, err := r.ReadWords(methodWords[2], exceptionCount)
		if err != nil {
			return ktfJavaExceptionTarget{}, false, fmt.Errorf(
				"inspect Java exception table for method 0x%08x: %w",
				methodAddress,
				err,
			)
		}
		for _, entryAddress := range table {
			entry, err := r.ReadWords(entryAddress, 4)
			if err != nil {
				return ktfJavaExceptionTarget{}, false, fmt.Errorf(
					"inspect Java exception entry 0x%08x: %w",
					entryAddress,
					err,
				)
			}
			if bytecodePC < entry[0] || bytecodePC > entry[1] {
				continue
			}
			matches, err := r.javaExceptionMatches(name, entry[3])
			if err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			if !matches {
				continue
			}
			if err := r.WriteU32(frame+4*4, detail); err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			handler := entry[2]
			if handler == 0 {
				handler = 1
			}
			// Move the frame's bytecode cursor to the handler before
			// resuming. Compiled KTF methods only publish a bytecode PC at
			// the points that can throw inside the protected region, so a
			// frame left pointing at the original throw makes any exception
			// raised by the handler body look like it came from inside the
			// same try. This method wraps and rethrows in its catch block,
			// which then caught itself forever until the guest heap ran out.
			if err := r.WriteU32(frame+3*4, handler); err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			if err := r.WriteU32(r.exceptionContext+8*4, frame); err != nil {
				return ktfJavaExceptionTarget{}, false, err
			}
			restore, err := r.ReadU32(frameWords[5] + 4)
			if err != nil {
				return ktfJavaExceptionTarget{}, false, fmt.Errorf(
					"resolve Java exception restore function for frame 0x%08x: %w",
					frame,
					err,
				)
			}
			if restore == 0 {
				return ktfJavaExceptionTarget{}, false, fmt.Errorf(
					"Java exception frame 0x%08x has no restore function",
					frame,
				)
			}
			return ktfJavaExceptionTarget{
				contextBase: frame + 6*4,
				handler:     handler,
				restore:     restore,
			}, true, nil
		}
		frame = previousFrame
	}
	return ktfJavaExceptionTarget{}, false, nil
}

func (r *Runtime) javaExceptionMatches(
	thrownName string,
	catchClass uint32,
) (bool, error) {
	if catchClass == 0 {
		return true, nil
	}
	catch, err := r.InspectJavaClass(catchClass)
	if err != nil {
		return false, fmt.Errorf(
			"inspect Java exception catch class 0x%08x: %w",
			catchClass,
			err,
		)
	}
	if thrownClass := r.JavaClasses[thrownName]; thrownClass != 0 {
		for depth, address := 0, thrownClass; address != 0; depth++ {
			if depth >= 256 {
				return false, fmt.Errorf(
					"Java exception hierarchy for %q exceeds limit",
					thrownName,
				)
			}
			if address == catchClass {
				return true, nil
			}
			class, err := r.InspectJavaClass(address)
			if err != nil {
				return false, err
			}
			address = class.Parent
		}
	}
	for depth, current := 0, thrownName; current != ""; depth++ {
		if depth >= 256 {
			return false, fmt.Errorf(
				"Java exception name hierarchy for %q exceeds limit",
				thrownName,
			)
		}
		if current == catch.Name {
			return true, nil
		}
		parent, known := ktfJavaExceptionParents[current]
		if !known {
			if strings.HasSuffix(current, "Exception") {
				parent = "java/lang/Exception"
			} else {
				break
			}
		}
		current = parent
	}
	return false, nil
}

func ktfJavaCheckType(_ context.Context, runtime *Runtime) (uint32, error) {
	targetClass, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	instance, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	unknown, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if instance == 0 {
		return 0, nil
	}
	instanceWords, err := runtime.ReadWords(instance, 2)
	if err != nil {
		return 0, fmt.Errorf(
			"inspect KTF Java instance at 0x%08x: %w",
			instance,
			err,
		)
	}
	actualClass := instanceWords[1]
	if actualClass == 0 {
		return 0, fmt.Errorf(
			"KTF Java instance at 0x%08x has a null class",
			instance,
		)
	}
	actual, err := runtime.InspectJavaClass(actualClass)
	if err != nil {
		return 0, fmt.Errorf(
			"inspect KTF Java instance class at 0x%08x: %w",
			actualClass,
			err,
		)
	}
	// KTF's reference runtime treats every array check and the unknown
	// nonzero form as successful before consulting the Java type system.
	if strings.HasPrefix(actual.Name, "[") || unknown != 0 {
		return 1, nil
	}
	if targetClass == 0 {
		return 0, nil
	}
	for depth := 0; ; depth++ {
		if depth >= 256 {
			return 0, fmt.Errorf(
				"KTF Java class hierarchy for %q exceeds limit",
				actual.Name,
			)
		}
		if actual.Address == targetClass {
			return 1, nil
		}
		if actual.Parent == 0 {
			return 0, nil
		}
		actual, err = runtime.InspectJavaClass(actual.Parent)
		if err != nil {
			return 0, fmt.Errorf(
				"inspect KTF Java parent class at 0x%08x: %w",
				actual.Parent,
				err,
			)
		}
	}
}

func ktfJavaClassLoad(_ context.Context, runtime *Runtime) (uint32, error) {
	target, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	nameAddress, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(nameAddress, 1024)
	if err != nil {
		return 0, err
	}
	runtime.tracef("java_class_load:%s@0x%08x", name, target)
	class, err := runtime.EnsureJavaClass(name)
	if err != nil {
		return 0, err
	}
	if err := runtime.writeWords(target, []uint32{class}); err != nil {
		return 0, err
	}
	return 0, nil
}

func ktfJavaNew(ctx context.Context, runtime *Runtime) (uint32, error) {
	classAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if classAddress == 0 {
		return 0, errors.New("Java class pointer is null")
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		return 0, fmt.Errorf("inspect Java class at 0x%08x: %w", classAddress, err)
	}
	if err := runtime.ensureJavaClassInitialized(ctx, class); err != nil {
		return 0, err
	}
	instance, err := runtime.NewJavaInstanceForClass(class)
	if err != nil {
		return 0, fmt.Errorf("allocate Java instance of %q: %w", class.Name, err)
	}
	instanceWords, _ := runtime.ReadWords(instance, 2)
	header, _ := runtime.ReadU32(instanceWords[0])
	runtime.tracef(
		"java_new:%s@0x%08x:fields=0x%08x:header=0x%08x",
		class.Name,
		instance,
		instanceWords[0],
		header,
	)
	return instance, nil
}

func ktfJavaArrayNew(_ context.Context, runtime *Runtime) (uint32, error) {
	elementType, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	className, elementSize, err := runtime.javaArrayClass(elementType)
	if err != nil {
		return 0, err
	}
	instance, err := runtime.NewJavaArray(className, count, elementSize)
	if err != nil {
		return 0, fmt.Errorf(
			"allocate Java array %s[%d]: %w",
			className,
			count,
			err,
		)
	}
	runtime.tracef(
		"java_array_new:%s[%d]@0x%08x",
		className,
		count,
		instance,
	)
	return instance, nil
}

// ktfJavaStringCopy implements the AOT VM callback used by native Clet
// wrappers to materialize a Java String as a zero-terminated byte string.
// The carrier runtime passes the destination capacity including the trailing
// zero. WIPI application and class names are ASCII; UTF-8 also preserves
// deterministic behavior for other host-created strings.
func ktfJavaStringCopy(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	source, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	destination, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	capacity, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if capacity == 0 {
		return 0, nil
	}
	if destination == 0 {
		return 0, errors.New("copy KTF Java string: destination is null")
	}
	encoded := []byte(runtime.javaStringValue(source))
	copyCount := uint32(len(encoded))
	if copyCount >= capacity {
		copyCount = capacity - 1
	}
	output := make([]byte, int(copyCount)+1)
	copy(output, encoded[:copyCount])
	if err := runtime.CPU.WriteMemory(destination, output); err != nil {
		return 0, fmt.Errorf("copy KTF Java string: %w", err)
	}
	runtime.tracef(
		"java_string_copy:source=0x%08x:destination=0x%08x:"+
			"capacity=%d:bytes=%d",
		source,
		destination,
		capacity,
		copyCount,
	)
	return copyCount, nil
}

func (r *Runtime) javaArrayClass(elementType uint32) (string, uint32, error) {
	if elementType <= 0x100 {
		descriptor := byte(elementType)
		elementSizes := map[byte]uint32{
			'Z': 1,
			'B': 1,
			'C': 2,
			'S': 2,
			'I': 4,
			'F': 4,
			'J': 8,
			'D': 8,
		}
		elementSize, ok := elementSizes[descriptor]
		if !ok {
			return "", 0, fmt.Errorf(
				"unsupported KTF Java primitive array type 0x%02x",
				elementType,
			)
		}
		return "[" + string(descriptor), elementSize, nil
	}
	class, err := r.InspectJavaClass(elementType)
	if err != nil {
		return "", 0, fmt.Errorf(
			"inspect KTF Java array element class at 0x%08x: %w",
			elementType,
			err,
		)
	}
	switch {
	case strings.HasPrefix(class.Name, "["):
		// The KTF multi-array helper passes the full array class for each
		// recursive level. Unlike anewarray, it does not pass the component
		// class, so prepending another '[' creates an extra dimension.
		return class.Name, 4, nil
	case strings.HasPrefix(class.Name, "L") &&
		strings.HasSuffix(class.Name, ";"):
		return "[" + class.Name, 4, nil
	default:
		return "[L" + class.Name + ";", 4, nil
	}
}

func (r *Runtime) EnsureJavaClass(name string) (uint32, error) {
	if class := r.JavaClasses[name]; class != 0 {
		// The collector can reclaim the guest heap slot a cached class lived
		// in while this name cache still points at it (issue #145): the
		// descriptor word reads back zero, and callers three layers away
		// (array/vtable construction, a Raptor-forwarded selectRecord) see
		// an unrelated "string pointer is null" crash with no hint the real
		// problem is here. A live class always has a descriptor; one that
		// does not is stale, not a legitimate zero-descriptor class, so
		// rebuild it instead of handing back a name that no longer resolves.
		if descriptor, err := r.ReadU32(class + 8); err == nil && descriptor != 0 {
			if _, ok := HostJavaClassSpecs[name]; ok {
				if err := r.augmentHostJavaClass(class, name); err != nil {
					return 0, err
				}
			}
			return class, nil
		}
	}
	if name == "" || len(name) > 1024 {
		return 0, fmt.Errorf("invalid Java class name %q", name)
	}
	spec, hasSpec := HostJavaClassSpecs[name]
	parentName := spec.Parent
	if !hasSpec && name != "java/lang/Object" {
		parentName = "java/lang/Object"
	}
	var parent uint32
	if parentName != "" {
		var err error
		parent, err = r.EnsureJavaClass(parentName)
		if err != nil {
			return 0, err
		}
	}
	class, err := r.AllocateWords(5)
	if err != nil {
		return 0, err
	}
	nameAddress, err := r.allocateBytes([]byte(name), true)
	if err != nil {
		return 0, err
	}
	methods, err := r.AllocateWords(1)
	if err != nil {
		return 0, err
	}
	fields, err := r.AllocateWords(1)
	if err != nil {
		return 0, err
	}
	vtable, err := r.AllocateWords(1)
	if err != nil {
		return 0, err
	}
	descriptor, err := r.AllocateWords(9)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(descriptor, []uint32{
		nameAddress,
		0,
		parent,
		methods,
		0,
		fields,
		uint32(spec.fieldSize) << 16,
		0x21,
		0,
	}); err != nil {
		return 0, err
	}
	if err := r.writeWords(class, []uint32{
		class + 4,
		0,
		descriptor,
		vtable,
		8 << 16,
	}); err != nil {
		return 0, err
	}
	r.JavaClasses[name] = class
	r.javaClassGeneration++
	r.hostJavaClass[class] = true
	if err := r.rebuildHostJavaVTable(class); err != nil {
		return 0, err
	}
	for _, method := range spec.methods {
		if _, err := r.addHostJavaMethod(
			JavaClass{Address: class, Name: name},
			method.name,
			method.descriptor,
		); err != nil {
			return 0, err
		}
	}
	return class, nil
}

func (r *Runtime) augmentHostJavaClass(classAddress uint32, name string) error {
	spec, ok := HostJavaClassSpecs[name]
	if !ok {
		return nil
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return err
	}
	r.hostJavaClass[classAddress] = true
	for _, wanted := range spec.methods {
		method, found := findKTFJavaMethod(
			class,
			wanted.name,
			wanted.descriptor,
		)
		if found && (method.Body != 0 || method.NativeBody != 0) {
			_, bodyIsHost := r.hostCalls[method.Body&^1]
			if name != "java/util/Enumeration" || bodyIsHost {
				continue
			}
		}
		if _, err := r.addHostJavaMethod(
			class,
			wanted.name,
			wanted.descriptor,
		); err != nil {
			return err
		}
		class, err = r.InspectJavaClass(classAddress)
		if err != nil {
			return err
		}
	}
	return nil
}
