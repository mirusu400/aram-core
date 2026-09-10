package ktf

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
)

// A relocatable KTF client is an MN module: the same AOT Java client the
// ordinary loader runs, published as position-independent code with a load
// descriptor in front of it and a different way of introducing itself to the
// handset.
//
// An ordinary client is entered at its first byte and answers with a WipiExe
// structure the runtime then drives. An MN module is entered at the address
// its descriptor names, is handed a table whose first word is a callback, and
// asks that callback for the interface it wants by name - "MNInterface". What
// comes back is a related callback table: word 0 points back at the table,
// because module glue reaches a callback both flat and as *(interface)[slot];
// slot 8 throws by class name; later slots allocate and resolve classes,
// members, and arrays; and a separate six-word helper table invokes resolved
// instance and static methods.
//
// It also carries its classes with it. The module table the descriptor names
// holds a sparse table of pointers to class objects in the layout
// InspectJavaClass already reads, so the runtime registers them by name instead
// of calling into the module for each one. An MN module has no WipiExe and so
// no class lookup procedure for LoadClass to call.
//
// A callback whose meaning is not yet observed records itself as mn.slot or
// mn.helper rather than being silently guessed, so a new module identifies the
// missing ABI operation in its trace.
const (
	// The module table begins with a slot-array pointer, live class count,
	// and slot capacity. Empty slots are null, including between live entries.
	mnModuleTableWords = 3
	// mnClassObjectWords is the class object InspectJavaClass reads. Its first
	// word points inside itself, not to a next class in a linked list.
	mnClassObjectWords = 5
	// Bound both live classes and the sparse table before reading guest data.
	mnMaxClasses    = 4096
	mnMaxClassSlots = 8192
	// mnMaxImports bounds an import index the same way.
	mnMaxImports = 8192
	// mnHelperSlots is the dispatch table the load descriptor names.
	mnHelperSlots = 6
	// mnContextStackWords is the call-out stack the module switches to. Module
	// code reads it from [r11+0x34] before calling a host callback.
	mnContextStackWords = 4096
	// mnContextWords covers the context fields the module reads through r11.
	mnContextWords = 64
	// mnContextStackField is where that stack pointer lives.
	mnContextStackField = 0x34
	// mnContextJVMField is the base used to decode compact object class headers
	// before virtual dispatch, matching the ordinary KTF JvmContext ABI.
	mnContextJVMField = 0x38
)

type mnCallFrame struct {
	arguments      [2]uint32
	parameterWords int
}

// bootstrapMNModule brings up a relocatable client and registers its classes.
func (r *Runtime) bootstrapMNModule(ctx context.Context) (uint32, error) {
	descriptor, err := r.ReadWords(ImageBase, ktfRelocatableDescriptorWords)
	if err != nil {
		return 0, fmt.Errorf("read KTF load descriptor: %w", err)
	}
	entry, moduleTable := descriptor[9], descriptor[0]
	if entry == 0 || moduleTable == 0 {
		return 0, errors.New("KTF relocatable client has no entry point")
	}
	if err := r.prepareMNContext(descriptor[6]); err != nil {
		return 0, err
	}
	callback := r.RegisterHostCall("mn.get_interface", ktfMNGetInterface)
	table, err := r.AllocateWords(1)
	if err != nil {
		return 0, err
	}
	if err := r.writeWords(table, []uint32{callback}); err != nil {
		return 0, err
	}
	result, value, err := r.call(
		ctx,
		entry,
		[]uint32{table},
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"enter KTF module at PC 0x%08x after %d instructions: %w",
			result.PC,
			result.Instructions,
			err,
		)
	}
	if int32(value) != 0 {
		return 0, fmt.Errorf(
			"KTF module entry refused the interface: %d",
			int32(value),
		)
	}
	if err := r.registerMNClasses(moduleTable, descriptor[4]); err != nil {
		return 0, err
	}
	if err := r.installMNHelpers(descriptor[5]); err != nil {
		return 0, err
	}
	return moduleTable, nil
}

// prepareMNContext allocates the context module code reaches through r11 and
// records the global offset table the module was relocated around.
func (r *Runtime) prepareMNContext(globalOffsetTable uint32) error {
	stack, err := r.AllocateWords(mnContextStackWords)
	if err != nil {
		return err
	}
	context, err := r.AllocateWords(mnContextWords)
	if err != nil {
		return err
	}
	if err := r.WriteU32(
		context+mnContextStackField,
		stack+mnContextStackWords*4-0x40,
	); err != nil {
		return err
	}
	// GOT references to descriptor word 8 are the module's global VM-context
	// cell. The file carries an ABI marker there; the handset loader replaces it
	// with the live context before any Java code runs.
	if err := r.WriteU32(ImageBase+8*4, context); err != nil {
		return err
	}
	r.mnGOT, r.mnContext = globalOffsetTable, context
	// The ordinary path builds these in Initialize, which an MN module skips.
	// Java host handlers reach for both.
	if err := r.prepareMNExceptions(context); err != nil {
		return err
	}
	environment, err := r.AllocateWords(1)
	if err != nil {
		return err
	}
	if err := r.writeWords(environment, []uint32{r.exceptionContext}); err != nil {
		return err
	}
	r.javaEnvironment = environment
	jvm, err := r.AllocateWords(3 + 128)
	if err != nil {
		return err
	}
	r.JvmContext = jvm
	if err := r.WriteU32(context+mnContextJVMField, jvm); err != nil {
		return err
	}
	return nil
}

// registerMNClasses registers the non-null pointers in the module's sparse
// class table. Class objects need not follow the header or each other in memory.
// In particular, moduleTable+0x1c can contain unrelated string-object data.
func (r *Runtime) registerMNClasses(moduleTable, imports uint32) error {
	if !r.imagePointer(moduleTable, mnModuleTableWords*4) {
		return fmt.Errorf("KTF module table 0x%08x is outside client image", moduleTable)
	}
	header, err := r.ReadWords(moduleTable, mnModuleTableWords)
	if err != nil {
		return fmt.Errorf("read KTF module table: %w", err)
	}
	table, count, capacity := header[0], header[1], header[2]
	if count > mnMaxClasses || capacity > mnMaxClassSlots || count > capacity {
		return fmt.Errorf("invalid KTF module class table count %d capacity %d", count, capacity)
	}
	if count == 0 {
		return errors.New("KTF module registered no classes")
	}
	if !r.imagePointer(table, capacity*4) {
		return fmt.Errorf("KTF module class slots 0x%08x capacity %d are outside client image", table, capacity)
	}
	slots, err := r.ReadWords(table, int(capacity))
	if err != nil {
		return fmt.Errorf("read KTF module class slots: %w", err)
	}
	objects := make([]uint32, 0, count)
	classes := make([]JavaClass, 0, count)
	seen := make(map[uint32]bool, count)
	for index, object := range slots {
		if object == 0 {
			continue
		}
		if !r.imagePointer(object, mnClassObjectWords*4) {
			return fmt.Errorf("KTF module class slot %d object 0x%08x is outside client image", index, object)
		}
		if seen[object] {
			return fmt.Errorf("duplicate KTF module class object 0x%08x at slot %d", object, index)
		}
		seen[object] = true
		class, err := r.InspectJavaClass(object)
		if err != nil {
			return fmt.Errorf("inspect KTF module class slot %d object 0x%08x: %w", index, object, err)
		}
		if class.Name == "" {
			return fmt.Errorf("KTF module class slot %d object 0x%08x has no name", index, object)
		}
		objects = append(objects, object)
		classes = append(classes, class)
	}
	if uint32(len(objects)) != count {
		return fmt.Errorf("KTF module class count %d does not match %d occupied slots", count, len(objects))
	}
	for _, class := range classes {
		r.rememberRegisteredJavaClass(class.Name, class.Address)
		r.tracef("mn_class:%s@0x%08x", class.Name, class.Address)
	}
	// Parent imports can name another class carried by the same module. Publish
	// every module class before resolving any parent so a child that appears
	// first in the table does not make EnsureJavaClass synthesize a host
	// placeholder for its still-unseen parent.
	for _, object := range objects {
		if err := r.linkMNClassParent(object, imports); err != nil {
			return err
		}
		if err := r.linkMNClassExceptions(object, imports); err != nil {
			return err
		}
	}
	if err := r.linkMNClassLayouts(objects); err != nil {
		return err
	}
	r.trace(fmt.Sprintf("mn_classes:%d", len(objects)))
	return nil
}

// linkMNClassLayouts links fields and vtables parent-first even when the
// module's class records are ordered child-first. MN descriptors carry field
// sizes and offsets relative to the declaring class, while object allocation
// and getfield/putfield use one flat inherited field block. The handset loader
// rebases each child's instance fields after the complete parent footprint.
func (r *Runtime) linkMNClassLayouts(objects []uint32) error {
	moduleClasses := make(map[uint32]bool, len(objects))
	for _, object := range objects {
		moduleClasses[object] = true
	}
	states := make(map[uint32]uint8, len(objects))
	var link func(uint32) error
	link = func(object uint32) error {
		switch states[object] {
		case 1:
			return fmt.Errorf("cyclic KTF module class hierarchy at 0x%08x", object)
		case 2:
			return nil
		}
		if r.mnLinkedLayouts[object] {
			states[object] = 2
			return nil
		}
		states[object] = 1
		class, err := r.InspectJavaClass(object)
		if err != nil {
			return err
		}
		if moduleClasses[class.Parent] {
			if err := link(class.Parent); err != nil {
				return err
			}
			class, err = r.InspectJavaClass(object)
			if err != nil {
				return err
			}
		}
		if err := r.linkMNClassFields(class); err != nil {
			return err
		}
		class, err = r.InspectJavaClass(object)
		if err != nil {
			return err
		}
		if err := r.linkMNClassVTable(class); err != nil {
			return err
		}
		if r.mnLinkedLayouts == nil {
			r.mnLinkedLayouts = make(map[uint32]bool)
		}
		r.mnLinkedLayouts[object] = true
		states[object] = 2
		return nil
	}
	for _, object := range objects {
		if err := link(object); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) linkMNClassFields(class JavaClass) error {
	parentSize := uint32(0)
	if class.Parent != 0 {
		parent, err := r.InspectJavaClass(class.Parent)
		if err != nil {
			return err
		}
		parentSize = uint32(parent.FieldSize)
	}
	totalSize := parentSize + uint32(class.FieldSize)
	if totalSize > uint32(^uint16(0)) {
		return fmt.Errorf(
			"KTF module class %q field size %d exceeds limit",
			class.Name,
			totalSize,
		)
	}
	classWords, err := r.ReadWords(class.Address, 5)
	if err != nil {
		return err
	}
	descriptor, err := r.ReadWords(classWords[2], 9)
	if err != nil {
		return err
	}
	if descriptor[5] != 0 && parentSize != 0 {
		type fieldUpdate struct {
			address uint32
			offset  uint32
		}
		updates := make([]fieldUpdate, 0)
		terminated := false
		for index := uint32(0); index < 4096; index++ {
			field, err := r.ReadU32(descriptor[5] + index*4)
			if err != nil {
				return err
			}
			if field == 0 {
				terminated = true
				break
			}
			words, err := r.ReadWords(field, 4)
			if err != nil {
				return err
			}
			if words[0]&0x0008 != 0 {
				continue
			}
			_, fieldDescriptor, err := r.ReadJavaFullName(words[2])
			if err != nil {
				return err
			}
			fieldSize := uint32(4)
			if fieldDescriptor == "J" || fieldDescriptor == "D" {
				fieldSize = 8
			}
			ownSize := uint32(class.FieldSize)
			if words[3] > ownSize || fieldSize > ownSize-words[3] {
				return fmt.Errorf(
					"KTF module class %q field offset %d exceeds own footprint %d",
					class.Name,
					words[3],
					ownSize,
				)
			}
			updates = append(updates, fieldUpdate{
				address: field + 12,
				offset:  parentSize + words[3],
			})
		}
		if !terminated {
			return fmt.Errorf(
				"KTF module class %q field table exceeds 4096 entries",
				class.Name,
			)
		}
		for _, update := range updates {
			if err := r.WriteU32(update.address, update.offset); err != nil {
				return err
			}
		}
	}
	if err := r.WriteU32(
		classWords[2]+6*4,
		descriptor[6]&0x0000ffff|totalSize<<16,
	); err != nil {
		return err
	}
	r.tracef(
		"mn_class_fields:%s:parent=%d:own=%d:total=%d",
		class.Name,
		parentSize,
		class.FieldSize,
		totalSize,
	)
	return nil
}

// linkMNClassVTable performs the link step an MN module leaves to its loader.
// Module classes carry complete method records and their compiler-assigned
// vtable indexes, but their class vtable word is zero in the file. Virtual-call
// helpers decode an object's compact class header and index this table directly,
// so a merely registered class reaches a null method handle.
func (r *Runtime) linkMNClassVTable(class JavaClass) error {
	logicalSize := uint32(0)
	var inherited []uint32
	if class.Parent != 0 {
		parent, err := r.InspectJavaClass(class.Parent)
		if err != nil {
			return err
		}
		parentWords, err := r.ReadWords(parent.Address, 5)
		if err != nil {
			return err
		}
		logicalSize = parentWords[4] & 0xffff
		if parent.VTable != 0 && logicalSize != 0 {
			inherited, err = r.ReadWords(parent.VTable, int(logicalSize))
			if err != nil {
				return err
			}
		}
	}
	for _, method := range class.Methods {
		r.rememberMNCallMethod(method)
		if method.AccessFlags&0x0008 != 0 ||
			strings.HasPrefix(method.Name, "<") {
			continue
		}
		if size := uint32(method.VTableIndex) + 1; size > logicalSize {
			logicalSize = size
		}
	}
	if logicalSize > uint32(^uint16(0)) {
		return fmt.Errorf(
			"KTF module class %q vtable size %d exceeds limit",
			class.Name,
			logicalSize,
		)
	}
	if logicalSize == 0 {
		return nil
	}
	entries := make([]uint32, logicalSize)
	copy(entries, inherited)
	for _, method := range class.Methods {
		if method.AccessFlags&0x0008 != 0 ||
			strings.HasPrefix(method.Name, "<") {
			continue
		}
		entries[method.VTableIndex] = method.Address
	}
	vtable, err := r.AllocateWords(logicalSize)
	if err != nil {
		return err
	}
	if err := r.writeWords(vtable, entries); err != nil {
		return err
	}
	if err := r.WriteU32(class.Address+12, vtable); err != nil {
		return err
	}
	classWords, err := r.ReadWords(class.Address, 5)
	if err != nil {
		return err
	}
	if err := r.WriteU32(
		class.Address+16,
		classWords[4]&0xffff0000|logicalSize,
	); err != nil {
		return err
	}
	if _, err := r.ensureJavaVTableIndex(class.Address, vtable); err != nil {
		return err
	}
	r.javaVTableCapacity[class.Address] = logicalSize
	r.tracef(
		"mn_class_vtable:%s@0x%08x[%d]",
		class.Name,
		vtable,
		logicalSize,
	)
	return nil
}

// linkMNClassParent resolves a class's parent before the class is inspected.
//
// A module class descriptor holds its parent in the word an ordinary KTF class
// holds it in, but with two encodings: an even value is a pointer to another
// class the module carries, and an odd value is (index << 1) | 1 into the
// module's import table, naming either a module or platform class. All module
// classes are registered before this resolution pass, so either kind resolves
// through the same class registry. framework/FunnyAppMain
// carries 0x73, which is import 57 - org/kwis/msp/lcdui/Jlet - and
// framework/FunnyCanvas carries 0x139, import 156, org/kwis/msp/lcdui/Card.
// Resolving it to the platform class and writing it back is the link step a
// handset does when it loads the module.
func (r *Runtime) linkMNClassParent(object, imports uint32) error {
	classWords, err := r.ReadWords(object, 3)
	if err != nil {
		return err
	}
	descriptor := classWords[2]
	// Only module-owned descriptors carry encoded parent imports. Host classes
	// may have descriptors allocated outside the client image.
	if descriptor == 0 || !r.imagePointer(descriptor, 12) {
		return nil
	}
	parent, err := r.ReadU32(descriptor + 8)
	if err != nil {
		return err
	}
	if parent == 0 || parent&1 == 0 {
		return nil
	}
	name, err := r.mnImportName(imports, parent>>1)
	if err != nil || name == "" {
		return err
	}
	class, err := r.EnsureJavaClass(name)
	if err != nil {
		return err
	}
	r.tracef("mn_class_parent:0x%08x=%s", object, name)
	return r.WriteU32(descriptor+8, class)
}

// mnImportName answers the import table entry at index. Every entry is a tag
// byte, a Java descriptor, "+", and the name; a class import is just the name.
func (r *Runtime) mnImportName(imports, index uint32) (string, error) {
	if imports == 0 || index > mnMaxImports {
		return "", nil
	}
	address, err := r.ReadU32(imports + index*4)
	if err != nil || address == 0 {
		return "", err
	}
	text, err := r.readCString(address, 1024)
	if err != nil {
		return "", err
	}
	name := string(text)
	if strings.ContainsAny(name, "+()") {
		// A method import, not a class: this is not a parent reference.
		return "", nil
	}
	return name, nil
}

// installMNHelpers fills the small dispatch table the module tail-jumps
// through. The load descriptor names its address and the module ships it
// zeroed for the handset to populate.
func (r *Runtime) installMNHelpers(table uint32) error {
	if table == 0 {
		return nil
	}
	slots := make([]uint32, mnHelperSlots)
	for index := range slots {
		handler := ktfMNHelper(index)
		switch index {
		case 0:
			handler = ktfMNInvoke
		case 1:
			handler = ktfMNInvokeStatic
		}
		slots[index] = r.RegisterHostCall(
			fmt.Sprintf("mn.helper%d", index),
			handler,
		)
	}
	return r.writeWords(table, slots)
}

// ktfMNInvoke is the module's Java call trampoline. The module reaches it after
// pushing r2 and r3, with r0 holding either a method record or a resolved entry
// point and r1 the receiver. A handset helper invokes the method with the exact
// descriptor-shaped argument frame and removes the two scratch words before
// returning. Leaving it as a pass-through leaked stack and skipped every call.
func ktfMNInvoke(ctx context.Context, runtime *Runtime) (uint32, error) {
	rawTarget, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	receiver, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	stack, err := runtime.CPU.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return 0, err
	}
	savedFrame, hasSavedFrame := runtime.takeMNCallFrame(stack)
	var parameterWordsHint *int
	if hasSavedFrame {
		parameterWordsHint = &savedFrame.parameterWords
	}
	target, parameterWords, err := runtime.resolveMNCallTarget(
		rawTarget,
		parameterWordsHint,
	)
	if err != nil {
		return 0, err
	}
	frameWords := max(2, parameterWords)
	frame, err := runtime.ReadWords(stack, frameWords)
	if err != nil {
		return 0, err
	}
	arguments := [2]uint32{}
	copy(arguments[:], frame)
	if hasSavedFrame {
		arguments = savedFrame.arguments
	}
	callArgs := make([]uint32, 2, parameterWords+2)
	callArgs[1] = receiver
	for index := 0; index < parameterWords; index++ {
		value := frame[index]
		if index < len(arguments) {
			value = arguments[index]
		}
		callArgs = append(callArgs, value)
	}
	result, value, err := runtime.call(
		ctx,
		target,
		callArgs,
		ktfJavaNativeInstructionMax,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"invoke MN target 0x%08x at PC 0x%08x after %d instructions: %w",
			target,
			result.PC,
			result.Instructions,
			err,
		)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack+8); err != nil {
		return 0, err
	}
	runtime.tracef(
		"mn_invoke:target=0x%08x:receiver=0x%08x:arguments=%d",
		target,
		receiver,
		parameterWords,
	)
	return value, nil
}

// ktfMNInvokeStatic is helper slot 1, the static-method counterpart to slot 0.
// Static AOT methods reserve r0, put their first Java parameter in r1, and use
// r2/r3 plus the caller stack for the remaining words. The compiler veneer has
// pushed r2/r3 by the time it tail-jumps here, so the host must reconstruct the
// original register frame and remove those scratch words on return.
func ktfMNInvokeStatic(ctx context.Context, runtime *Runtime) (uint32, error) {
	rawTarget, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	firstArgument, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	stack, err := runtime.CPU.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return 0, err
	}
	savedFrame, hasSavedFrame := runtime.takeMNCallFrame(stack)
	var parameterWordsHint *int
	if hasSavedFrame {
		parameterWordsHint = &savedFrame.parameterWords
	}
	target, parameterWords, err := runtime.resolveMNCallTarget(
		rawTarget,
		parameterWordsHint,
	)
	if err != nil {
		return 0, err
	}
	frameWords := max(2, parameterWords-1)
	frame, err := runtime.ReadWords(stack, frameWords)
	if err != nil {
		return 0, err
	}
	arguments := [2]uint32{}
	copy(arguments[:], frame)
	if hasSavedFrame {
		arguments = savedFrame.arguments
	}
	callArgs := make([]uint32, 1, parameterWords+1)
	for index := 0; index < parameterWords; index++ {
		value := firstArgument
		if index != 0 {
			value = frame[index-1]
			if index-1 < len(arguments) {
				value = arguments[index-1]
			}
		}
		callArgs = append(callArgs, value)
	}
	result, value, err := runtime.call(
		ctx,
		target,
		callArgs,
		ktfJavaNativeInstructionMax,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"invoke static MN target 0x%08x at PC 0x%08x after %d instructions: %w",
			target,
			result.PC,
			result.Instructions,
			err,
		)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack+8); err != nil {
		return 0, err
	}
	runtime.tracef(
		"mn_invoke_static:target=0x%08x:arguments=%d",
		target,
		parameterWords,
	)
	return value, nil
}

func (r *Runtime) resolveMNCallTarget(
	rawTarget uint32,
	parameterWordsHint *int,
) (uint32, int, error) {
	if rawTarget == 0 {
		return 0, 0, errors.New("MN call target is null")
	}
	target := rawTarget
	// Some call sites hand the helper a method record, some an already-resolved
	// Thumb entry, and the shortest veneer hands it an even-addressed cell that
	// can contain either form. Follow at most two cells so malformed guest data
	// cannot form an infinite pointer chain.
	for depth := 0; depth < 3; depth++ {
		if target == 0 {
			return 0, 0, errors.New("MN member cell is unresolved")
		}
		if target&1 != 0 {
			if parameterWordsHint != nil {
				return target, *parameterWordsHint, nil
			}
			parameterWords, ok := r.mnCallParameterWords[target]
			if !ok {
				return 0, 0, fmt.Errorf(
					"MN direct call target 0x%08x has no descriptor arity",
					target,
				)
			}
			if parameterWords < 0 {
				return 0, 0, fmt.Errorf(
					"MN direct call target 0x%08x has ambiguous descriptor arity",
					target,
				)
			}
			return target, parameterWords, nil
		}
		if method, inspectErr := r.InspectJavaMethod(target); inspectErr == nil {
			r.rememberMNCallMethod(method)
			entry := method.Body
			if entry == 0 {
				entry = method.NativeBody
			}
			if entry == 0 {
				return 0, 0, errors.New("MN method has no executable entry")
			}
			parameterWords, ok := ktfJavaParameterWords(method.Descriptor)
			if !ok {
				return 0, 0, fmt.Errorf(
					"MN method has invalid descriptor %q",
					method.Descriptor,
				)
			}
			return entry, parameterWords, nil
		}
		next, err := r.ReadU32(target)
		if err != nil {
			return 0, 0, err
		}
		if next == target {
			return 0, 0, errors.New("MN member cell points to itself")
		}
		target = next
	}
	return 0, 0, fmt.Errorf(
		"MN member cell chain from 0x%08x exceeds limit",
		rawTarget,
	)
}

func (r *Runtime) rememberMNCallMethod(method JavaMethod) {
	parameterWords, ok := ktfJavaParameterWords(method.Descriptor)
	if !ok {
		return
	}
	if r.mnCallParameterWords == nil {
		r.mnCallParameterWords = make(map[uint32]int)
	}
	for _, target := range []uint32{method.Body, method.NativeBody} {
		if target == 0 {
			continue
		}
		if existing, found := r.mnCallParameterWords[target]; found &&
			existing != parameterWords {
			r.mnCallParameterWords[target] = -1
			continue
		}
		r.mnCallParameterWords[target] = parameterWords
	}
}

func (r *Runtime) takeMNCallFrame(stack uint32) (mnCallFrame, bool) {
	saved, ok := r.mnCallFrames[stack]
	if ok {
		delete(r.mnCallFrames, stack)
	}
	return saved, ok
}

func ktfMNHelper(index int) ktfHostHandler {
	return func(_ context.Context, runtime *Runtime) (uint32, error) {
		args := make([]uint32, 4)
		for i := range args {
			args[i], _ = runtime.parameter(uint32(i))
		}
		runtime.tracef(
			"mn_helper:%d:args=%08x,%08x,%08x,%08x",
			index,
			args[0],
			args[1],
			args[2],
			args[3],
		)
		return args[0], nil
	}
}

// ktfMNGetInterface answers the module's one bootstrap question.
func ktfMNGetInterface(_ context.Context, runtime *Runtime) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	name, err := runtime.readCString(nameAddress, 256)
	if err != nil {
		return 0, err
	}
	runtime.trace("mn_interface:" + string(name))
	if string(name) != "MNInterface" {
		return 0, nil
	}
	return runtime.ensureMNInterface()
}

// ensureMNInterface builds the callback table the module keeps. Relocatable MN
// modules use a related but distinct callback ABI from ordinary InterfaceInit.
// In particular, slot 8 is the non-returning throw-by-class-name callback used
// by the compiler's null, bounds, cast, and arithmetic failure veneers. Treating
// it as ordinary java_class_load lets those veneers return through a link
// register their internal call already replaced, eventually branching to zero.
func (r *Runtime) ensureMNInterface() (uint32, error) {
	if r.mnInterface != 0 {
		return r.mnInterface, nil
	}
	object, err := r.AllocateWords(32)
	if err != nil {
		return 0, err
	}
	fields := make([]uint32, 32)
	for index := range fields {
		fields[index] = r.RegisterHostCall(
			fmt.Sprintf("mn.slot%d", index),
			ktfMNUnmappedSlot(index),
		)
	}
	// Word 0 is not a callback but a pointer to a second table: module glue
	// reaches a service as interface -> [0] -> [slot], while the callbacks the
	// ordinary KTF path knows sit flat in the interface itself.
	fields[0] = object
	fields[1] = r.RegisterHostCall("java_throw", ktfJavaThrow)
	fields[2] = r.RegisterHostCall("java_throw_object", ktfJavaThrowObject)
	fields[4] = r.RegisterHostCall("java_check_type", ktfJavaCheckType)
	fields[5] = r.RegisterHostCall("java_new", ktfJavaNew)
	fields[6] = r.RegisterHostCall("java_array_new", ktfJavaArrayNew)
	fields[8] = r.RegisterHostCall("mn.throw", ktfJavaThrow)
	fields[10] = r.RegisterHostCall("java_string_copy", ktfJavaStringCopy)
	// Module allocation glue calls slot 14 as new(class, metadata, context) and
	// treats a non-zero result as the freshly allocated instance. The ordinary
	// KTF allocator already implements class initialization and object layout;
	// the two extra module arguments are bookkeeping the host does not need.
	fields[14] = r.RegisterHostCall("mn.new", ktfJavaNew)
	fields[15] = r.RegisterHostCall("mn.array_new", ktfMNArrayNew)
	fields[16] = r.RegisterHostCall("mn.resolve_class", ktfMNResolveClass)
	fields[17] = r.RegisterHostCall("mn.array_class", ktfMNObjectClass)
	fields[18] = r.RegisterHostCall("mn.check_type", ktfMNCheckType)
	// Slot 21 returns a field metadata record. The getstatic caller reads its
	// declaring class at +4 for slot-24 initialization, then its value at +12.
	// Returning the class itself makes Font constants read as vtable pointers.
	fields[21] = r.RegisterHostCall("mn.resolve_field", ktfGetJavaField)
	fields[24] = r.RegisterHostCall("mn.initialize_class", ktfMNInitializeClass)
	fields[25] = r.RegisterHostCall("mn.resolve_member", ktfMNResolveMember)
	fields[27] = r.RegisterHostCall("mn.resolve_array_class", ktfMNResolveArrayClass)
	fields[28] = r.RegisterHostCall("mn.primitive_array_new", ktfMNPrimitiveArrayNew)
	fields[29] = r.RegisterHostCall("mn.multi_array_new", ktfMNMultiArrayNew)
	fields[11] = r.RegisterHostCall("alloc", ktfAlloc)
	if err := r.writeWords(object, fields); err != nil {
		return 0, err
	}
	r.mnInterface = object
	return object, nil
}

func ktfMNMultiArrayNew(_ context.Context, runtime *Runtime) (uint32, error) {
	classAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	dimensions, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	sizesAddress, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	if dimensions == 0 || dimensions > 255 {
		return 0, fmt.Errorf("invalid MN array dimension count %d", dimensions)
	}
	sizes, err := runtime.ReadWords(sizesAddress, int(dimensions))
	if err != nil {
		return 0, err
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	var allocate func(string, uint32) (uint32, error)
	allocate = func(name string, depth uint32) (uint32, error) {
		if name == "" || name[0] != '[' {
			return 0, fmt.Errorf("MN multi-array class %q is not an array", name)
		}
		elementSize := uint32(4)
		if len(name) == 2 {
			_, elementSize, err = runtime.javaArrayClass(uint32(name[1]))
			if err != nil {
				return 0, err
			}
		}
		array, err := runtime.NewJavaArray(name, sizes[depth], elementSize)
		if err != nil {
			return 0, err
		}
		if depth+1 >= dimensions {
			return array, nil
		}
		childName := name[1:]
		fields, err := runtime.ReadU32(array)
		if err != nil {
			return 0, err
		}
		for index := uint32(0); index < sizes[depth]; index++ {
			child, err := allocate(childName, depth+1)
			if err != nil {
				return 0, err
			}
			if err := runtime.WriteU32(fields+8+index*4, child); err != nil {
				return 0, err
			}
		}
		return array, nil
	}
	array, err := allocate(class.Name, 0)
	if err != nil {
		return 0, err
	}
	runtime.tracef("mn_multi_array_new:%s:%v@0x%08x", class.Name, sizes, array)
	return array, nil
}

func ktfMNArrayNew(_ context.Context, runtime *Runtime) (uint32, error) {
	classAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	elementSize := uint32(4)
	if len(class.Name) == 2 && class.Name[0] == '[' {
		_, elementSize, err = runtime.javaArrayClass(uint32(class.Name[1]))
		if err != nil {
			return 0, err
		}
	}
	array, err := runtime.NewJavaArray(class.Name, count, elementSize)
	if err != nil {
		return 0, err
	}
	runtime.tracef("mn_array_new:%s[%d]@0x%08x", class.Name, count, array)
	return array, nil
}

func ktfMNResolveArrayClass(_ context.Context, runtime *Runtime) (uint32, error) {
	elementClass, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	className, _, err := runtime.javaArrayClass(elementClass)
	if err != nil {
		return 0, err
	}
	if elementClass > 0x100 {
		component, err := runtime.InspectJavaClass(elementClass)
		if err != nil {
			return 0, err
		}
		// MN slot 27 implements anewarray: an array component gains a
		// dimension. The ordinary multi-array helper deliberately does not.
		if strings.HasPrefix(component.Name, "[") {
			className = "[" + component.Name
		}
	}
	class, err := runtime.EnsureJavaClass(className)
	if err != nil {
		return 0, err
	}
	runtime.tracef("mn_array_class:%s@0x%08x", className, class)
	return class, nil
}

func ktfMNObjectClass(_ context.Context, runtime *Runtime) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if object == 0 {
		return 0, nil
	}
	return runtime.ReadU32(object + 4)
}

func ktfMNCheckType(ctx context.Context, runtime *Runtime) (uint32, error) {
	instance, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if instance == 0 {
		return 0, nil
	}
	compatibility, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	// MN array-store glue supplies its interface pointer as the non-zero third
	// argument. KTF's compatibility form accepts that store before consulting
	// the Java hierarchy, including compact module values that are not ordinary
	// heap object addresses.
	if compatibility != 0 {
		return 1, nil
	}
	return ktfJavaCheckType(ctx, runtime)
}

func ktfMNInitializeClass(ctx context.Context, runtime *Runtime) (uint32, error) {
	classAddress, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	if err := runtime.ensureJavaClassInitialized(ctx, class); err != nil {
		return 0, err
	}
	return classAddress, nil
}

// ktfMNPrimitiveArrayNew implements the module compiler's newarray helper. Its
// first argument is the JVM primitive-array type code scaled by one word, so
// int (T_INT = 10) arrives as 0x28; the second argument is the element count.
func ktfMNPrimitiveArrayNew(_ context.Context, runtime *Runtime) (uint32, error) {
	encodedType, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	count, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if encodedType%4 != 0 {
		return 0, fmt.Errorf("invalid MN primitive array type 0x%08x", encodedType)
	}
	descriptors := map[uint32]byte{
		4:  'Z',
		5:  'C',
		6:  'F',
		7:  'D',
		8:  'B',
		9:  'S',
		10: 'I',
		11: 'J',
	}
	descriptor, ok := descriptors[encodedType/4]
	if !ok {
		return 0, fmt.Errorf("unsupported MN primitive array type 0x%08x", encodedType)
	}
	className, elementSize, err := runtime.javaArrayClass(uint32(descriptor))
	if err != nil {
		return 0, err
	}
	array, err := runtime.NewJavaArray(className, count, elementSize)
	if err != nil {
		return 0, err
	}
	runtime.tracef("mn_primitive_array_new:%s[%d]@0x%08x", className, count, array)
	return array, nil
}

// ktfMNResolveClass answers the class a module names. The module passes the
// word to write the class into, the name, and its own import table, and reads
// the return as a status: zero is success, anything else sends it down its
// failure path.
func ktfMNResolveClass(_ context.Context, runtime *Runtime) (uint32, error) {
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
	class, err := runtime.EnsureJavaClass(string(name))
	if err != nil {
		return 0, err
	}
	if target != 0 {
		if err := runtime.WriteU32(target, class); err != nil {
			return 0, err
		}
	}
	runtime.tracef("mn_resolve_class:%s@0x%08x", string(name), class)
	return 0, nil
}

// ktfMNUnmappedSlot stands in for a callback whose meaning is not worked out
// yet. It records which slot was asked for, so a title that reaches one names
// it in a trace rather than dying later with a bad address, and answers with
// the value the module was carrying: these are continuation-shaped callbacks
// whose glue reads a zero as failure, so passing the argument through keeps a
// module going further than refusing does.
func ktfMNUnmappedSlot(index int) ktfHostHandler {
	return func(_ context.Context, runtime *Runtime) (uint32, error) {
		args := make([]uint32, 3)
		for position := range args {
			args[position], _ = runtime.parameter(uint32(position))
		}
		runtime.tracef(
			"mn_unmapped_slot:%d:args=0x%08x,0x%08x,0x%08x",
			index,
			args[0],
			args[1],
			args[2],
		)
		return args[0], nil
	}
}

// ktfMNResolveMember answers the member a module names against a class it has
// already resolved. It is called as resolveMember(class, member, out) and the
// module **branches through the word written to out**, so leaving that word
// alone is what sent it into its own stack: the slot still held the frame link
// the caller left there, and 0x7ffffeb4 is one 8-byte call-out frame past the
// 0x7ffffeac it was handed.
//
// The member is named the same way the ordinary KTF path names one - a tag
// byte, the descriptor, "+", and the name, as addHostJavaMethod writes it -
// so "H()V+<init>" against java/util/Vector is Vector.<init>()V.
func ktfMNResolveMember(_ context.Context, runtime *Runtime) (uint32, error) {
	classObject, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	member, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	target, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	handle, entry, err := runtime.mnResolveMember(classObject, member)
	if err != nil {
		runtime.tracef("mn_resolve_member_failed:%v", err)
		return 0, nil
	}
	if target != 0 {
		arguments, err := runtime.ReadWords(target, 2)
		if err != nil {
			return 0, err
		}
		method, err := runtime.InspectJavaMethod(handle)
		if err != nil {
			return 0, err
		}
		parameterWords, ok := ktfJavaParameterWords(method.Descriptor)
		if !ok {
			return 0, fmt.Errorf("invalid MN member descriptor %q", method.Descriptor)
		}
		if runtime.mnCallFrames == nil {
			runtime.mnCallFrames = make(map[uint32]mnCallFrame)
		}
		runtime.mnCallFrames[target] = mnCallFrame{
			arguments:      [2]uint32{arguments[0], arguments[1]},
			parameterWords: parameterWords,
		}
		if err := runtime.WriteU32(target, entry); err != nil {
			return 0, err
		}
	}
	// The glue caches the non-zero method handle in the member cell, while the
	// low-level call veneer branches through the executable entry written to the
	// scratch out-word. They are deliberately different values.
	return handle, nil
}

// mnResolveMember answers both the method metadata handle an MN module caches
// and the executable entry its first-call scratch frame branches through.
func (r *Runtime) mnResolveMember(
	classObject, member uint32,
) (uint32, uint32, error) {
	class, err := r.InspectJavaClass(classObject)
	if err != nil {
		return 0, 0, err
	}
	text, err := r.readCString(member, 512)
	if err != nil {
		return 0, 0, err
	}
	if len(text) == 0 {
		return 0, 0, errors.New("MN member name is empty")
	}
	// The first byte is the entry's tag - the same slot addHostJavaMethod
	// writes a zero into - and is not part of the descriptor.
	descriptor, name, found := strings.Cut(string(text[1:]), "+")
	if !found {
		return 0, 0, fmt.Errorf("MN member %q has no name", string(text))
	}
	key := class.Name + "." + name + descriptor
	handle := r.mnMembers[key]
	if handle == 0 {
		handle, err = r.resolveJavaMethod(classObject, name, descriptor)
		if err != nil {
			return 0, 0, err
		}
		if r.mnMembers == nil {
			r.mnMembers = map[string]uint32{}
		}
		r.mnMembers[key] = handle
	}
	method, err := r.InspectJavaMethod(handle)
	if err != nil {
		return 0, 0, err
	}
	r.rememberMNCallMethod(method)
	entry := method.Body
	if entry == 0 {
		entry = method.NativeBody
	}
	if entry == 0 {
		return 0, 0, fmt.Errorf("MN member %s has no executable entry", key)
	}
	r.tracef(
		"mn_resolve_member:%s:handle=0x%08x:entry=0x%08x",
		key,
		handle,
		entry,
	)
	return handle, entry, nil
}

// applyMNRegisters installs the two registers module code expects a caller to
// have set: the global offset table it was relocated around, and the context
// it reads its call-out stack from. Both are callee-saved, so the host has to
// supply them on every entry into module code.
func (r *Runtime) applyMNRegisters() error {
	if r.mnContext == 0 {
		return nil
	}
	if err := r.CPU.WriteRegister(cpu.RegisterR10, r.mnGOT); err != nil {
		return err
	}
	return r.CPU.WriteRegister(cpu.RegisterR11, r.mnContext)
}
