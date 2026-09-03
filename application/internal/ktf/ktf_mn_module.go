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
// comes back is the callback table the ordinary path passes to InterfaceInit,
// at the same indices, with three differences: word 0 points back at the table,
// because module glue reaches a callback both flat and as *(interface)[slot];
// the class lookup takes the name first and answers the class; and the import
// resolution takes the word to write the class into and answers a status.
//
// It also carries its classes with it. The module table the descriptor names
// holds a linked list of class objects in exactly the layout InspectJavaClass
// already reads, so the runtime registers them by name instead of calling into
// the module for each one - which it could not do anyway, since an MN module
// has no WipiExe and so no class lookup procedure for LoadClass to call.
//
// This is a partial bring-up: it takes a module as far as its main class and
// its class registry, and a callback whose meaning is not worked out yet
// records itself as mn_unmapped_slot rather than being guessed at.
const (
	// mnModuleTableClasses is the offset in the module table of the first
	// class record. The two words before it are the class count and a size.
	mnModuleTableClasses = 0x1c
	// mnClassRecordWords is one entry of that list: a tag, the class object,
	// a reserved word, flags, and the next entry.
	mnClassRecordWords = 5
	// mnMaxClasses bounds the walk so a malformed list cannot spin.
	mnMaxClasses = 4096
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
)

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
	r.mnGOT, r.mnContext = globalOffsetTable, context
	// The ordinary path builds these in Initialize, which an MN module skips.
	// Java host handlers reach for both.
	exceptionContext, err := r.AllocateWords(ktfJavaEnvironmentWords)
	if err != nil {
		return err
	}
	r.exceptionContext = exceptionContext
	environment, err := r.AllocateWords(1)
	if err != nil {
		return err
	}
	if err := r.writeWords(environment, []uint32{exceptionContext}); err != nil {
		return err
	}
	r.javaEnvironment = environment
	jvm, err := r.AllocateWords(3 + 128)
	if err != nil {
		return err
	}
	r.JvmContext = jvm
	return nil
}

// registerMNClasses walks the module's class list and registers every class by
// name. The objects are already in the layout InspectJavaClass reads.
func (r *Runtime) registerMNClasses(moduleTable, imports uint32) error {
	record := moduleTable + mnModuleTableClasses
	registered := 0
	for seen := 0; record != 0 && seen < mnMaxClasses; seen++ {
		words, err := r.ReadWords(record, mnClassRecordWords)
		if err != nil {
			return fmt.Errorf("read KTF module class record: %w", err)
		}
		// The class object is the record itself minus its tag word: the tag
		// sits where the object's second word does.
		object := record - 4
		if err := r.linkMNClassParent(object, imports); err != nil {
			return err
		}
		class, err := r.InspectJavaClass(object)
		if err == nil && class.Name != "" {
			r.rememberRegisteredJavaClass(class.Name, object)
			registered++
			r.tracef("mn_class:%s@0x%08x", class.Name, object)
		}
		record = words[4]
	}
	if registered == 0 {
		return errors.New("KTF module registered no classes")
	}
	r.trace(fmt.Sprintf("mn_classes:%d", registered))
	return nil
}

// linkMNClassParent resolves a class's parent before the class is inspected.
//
// A module class descriptor holds its parent in the word an ordinary KTF class
// holds it in, but with two encodings: an even value is a pointer to another
// class the module carries, and an odd value is (index << 1) | 1 into the
// module's import table, naming a platform class. framework/FunnyAppMain
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
	if descriptor == 0 {
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
		slots[index] = r.RegisterHostCall(
			fmt.Sprintf("mn.helper%d", index),
			ktfMNHelper(index),
		)
	}
	return r.writeWords(table, slots)
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

// ensureMNInterface builds the callback table the module keeps. It is the
// ordinary KTF InterfaceInit table at the same indices; only the class lookup
// differs, so only that one has its own adapter.
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
	fields[8] = r.RegisterHostCall("mn.class_load", ktfMNClassLoad)
	fields[10] = r.RegisterHostCall("java_string_copy", ktfJavaStringCopy)
	fields[16] = r.RegisterHostCall("mn.resolve_class", ktfMNResolveClass)
	fields[25] = r.RegisterHostCall("mn.resolve_member", ktfMNResolveMember)
	fields[11] = r.RegisterHostCall("alloc", ktfAlloc)
	if err := r.writeWords(object, fields); err != nil {
		return 0, err
	}
	r.mnInterface = object
	return object, nil
}

// ktfMNResolveClass answers the class a module names in its second argument.
// The module passes the name and its own import table; the runtime already
// knows every class the module carries and every platform class.
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
	stub, err := runtime.mnResolveMember(classObject, member)
	if err != nil {
		runtime.tracef("mn_resolve_member_failed:%v", err)
		return 0, nil
	}
	if target != 0 {
		if err := runtime.WriteU32(target, stub); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

// mnResolveMember answers a callable entry point for the member a module names
// as "<tag><descriptor>+<name>" against the class object it passes.
func (r *Runtime) mnResolveMember(classObject, member uint32) (uint32, error) {
	class, err := r.InspectJavaClass(classObject)
	if err != nil {
		return 0, err
	}
	text, err := r.readCString(member, 512)
	if err != nil {
		return 0, err
	}
	if len(text) == 0 {
		return 0, errors.New("MN member name is empty")
	}
	// The first byte is the entry's tag - the same slot addHostJavaMethod
	// writes a zero into - and is not part of the descriptor.
	descriptor, name, found := strings.Cut(string(text[1:]), "+")
	if !found {
		return 0, fmt.Errorf("MN member %q has no name", string(text))
	}
	key := class.Name + "." + name + descriptor
	if stub := r.mnMembers[key]; stub != 0 {
		return stub, nil
	}
	// One stub per member: the host-call page is small and a module resolves
	// the same member every time it reaches the call site.
	stub := r.RegisterHostCall(
		"java.method."+key,
		HostJavaMethod(class.Name, name, descriptor),
	)
	if r.mnMembers == nil {
		r.mnMembers = map[string]uint32{}
	}
	r.mnMembers[key] = stub
	r.tracef("mn_resolve_member:%s@0x%08x", key, stub)
	return stub, nil
}

func ktfMNClassLoad(_ context.Context, runtime *Runtime) (uint32, error) {
	nameAddress, err := runtime.parameter(0)
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
	runtime.tracef("mn_class_load:%s@0x%08x", string(name), class)
	return class, nil
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
