package skvm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

const DefaultInstructionLimit = uint64(10_000_000)

const (
	applicationRootClass      = "javax/microedition/midlet/MIDlet"
	applicationRootField      = "__aramActiveApplication"
	applicationRootDescriptor = "Ljava/lang/Object;"
	callSeriallyEventName     = "skvm.display.callSerially"
)

var (
	ErrInstructionLimit = errors.New("SKVM instruction limit reached")
	ErrHalted           = errors.New("SKVM halted")
	ErrMethodNotFound   = errors.New("SKVM method not found")
)

type NativeFunc func(
	context.Context,
	*VM,
	uint32,
	[]Value,
) (Value, bool, error)

type TraceEvent struct {
	Class      string
	Method     string
	Descriptor string
	PC         int
	Opcode     byte
	Depth      int
	Target     string
}

type TraceHook func(TraceEvent) error

type Object struct {
	Class  string
	Fields map[string]Value
	Array  *Array
	Native any
}

type Array struct {
	Descriptor string
	Elements   []Value
}

type runtimeClass struct {
	class     *Class
	static    map[string]Value
	initState classInitState
}

type classInitState uint8

const (
	classUninitialized classInitState = iota
	classInitializing
	classInitialized
	classFailed
)

type nativeKey struct {
	class      string
	name       string
	descriptor string
}

type VM struct {
	classes          map[string]*runtimeClass
	heap             map[uint32]*Object
	nextReference    uint32
	natives          map[nativeKey]NativeFunc
	hostSupers       map[string]string
	hostStatic       map[string]Value
	hook             TraceHook
	frames           []*frame
	InstructionLimit uint64
	Instructions     uint64
	ScreenWidth      int
	ScreenHeight     int
	displayReference uint32
	currentDisplay   uint32
	properties       map[string]string
	screenGraphics   uint32
	services         *shared.Services
	serviceOwner     shared.OwnerID
	screenSurface    shared.ServiceID
	defaultFont      shared.ServiceID
	classDigest      [sha256.Size]byte
	runningThread    uint32
	threadFrameBase  int
}

type frame struct {
	class    *Class
	method   Method
	locals   []Value
	stack    []Value
	pc       int
	invokePC int
}

type thrown struct {
	reference uint32
	class     string
	message   string
}

func (e *thrown) Error() string {
	if e.message != "" {
		return fmt.Sprintf("SKVM %s thrown: %s", e.class, e.message)
	}
	return fmt.Sprintf("SKVM %s object %d thrown", e.class, e.reference)
}

func New(classData map[string][]byte) (*VM, error) {
	services, err := shared.NewServices(shared.Config{})
	if err != nil {
		return nil, fmt.Errorf("initialize SKVM services: %w", err)
	}
	return NewWithServices(classData, services, 1)
}

// NewWithServices constructs an SKVM interpreter adapter over the shared
// deterministic runtime services. Java references remain VM-private and map to
// guest-neutral service IDs through native payloads.
func NewWithServices(
	classData map[string][]byte,
	services *shared.Services,
	owner shared.OwnerID,
) (*VM, error) {
	if services == nil {
		return nil, fmt.Errorf("initialize SKVM: shared services are nil")
	}
	vm := &VM{
		classes:          make(map[string]*runtimeClass, len(classData)),
		heap:             make(map[uint32]*Object),
		nextReference:    1,
		natives:          make(map[nativeKey]NativeFunc),
		hostSupers:       defaultHostSupers(),
		hostStatic:       make(map[string]Value),
		InstructionLimit: DefaultInstructionLimit,
		ScreenWidth:      int(services.Config.Device.ScreenWidth),
		ScreenHeight:     int(services.Config.Device.ScreenHeight),
		properties:       make(map[string]string),
		services:         services,
		serviceOwner:     owner,
		classDigest:      digestClassData(classData),
	}
	names := make([]string, 0, len(classData))
	for name := range classData {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, suppliedName := range names {
		class, err := ParseClass(suppliedName+".class", classData[suppliedName])
		if err != nil {
			return nil, err
		}
		if suppliedName != class.Name &&
			strings.TrimSuffix(suppliedName, ".class") != class.Name {
			return nil, fmt.Errorf(
				"SKVM class key %q does not match declared name %q",
				suppliedName,
				class.Name,
			)
		}
		if _, duplicate := vm.classes[class.Name]; duplicate {
			return nil, fmt.Errorf("duplicate SKVM class %q", class.Name)
		}
		runtime := &runtimeClass{
			class:  class,
			static: make(map[string]Value),
		}
		for _, field := range class.Fields {
			if field.AccessFlags&AccessStatic == 0 {
				continue
			}
			value, err := zeroValue(field.Descriptor)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", class.Name, field.Name, err)
			}
			if field.ConstantIndex != 0 {
				constant, constantErr := class.Constant(field.ConstantIndex)
				if constantErr != nil {
					return nil, fmt.Errorf("%s.%s: %w", class.Name, field.Name, constantErr)
				}
				value, constantErr = vm.constantValue(constant)
				if constantErr != nil {
					return nil, fmt.Errorf("%s.%s: %w", class.Name, field.Name, constantErr)
				}
			}
			runtime.static[fieldStorageKey(class.Name, field.Name, field.Descriptor)] = value
		}
		vm.classes[class.Name] = runtime
	}
	screen, err := services.Graphics.CreateSurface(owner, shared.SurfaceDescriptor{
		Width:  int32(vm.ScreenWidth),
		Height: int32(vm.ScreenHeight),
		Format: shared.PixelRGBA8888,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize SKVM screen: %w", err)
	}
	if err := services.Graphics.SetScreen(owner, screen); err != nil {
		_ = services.Graphics.DestroySurface(owner, screen)
		return nil, fmt.Errorf("select SKVM screen: %w", err)
	}
	font, err := services.Text.CreateFont(owner, shared.FontDescriptor{
		Family: "aram-fallback",
		// The MIDP default is the system face at medium size. Keep this in
		// sync with the medium branch in newFontObject.
		Size: 12,
	})
	if err != nil {
		_ = services.Graphics.SetScreen(owner, 0)
		_ = services.Graphics.DestroySurface(owner, screen)
		return nil, fmt.Errorf("initialize SKVM font: %w", err)
	}
	vm.screenSurface = screen
	vm.defaultFont = font
	vm.installCoreNatives()
	return vm, nil
}

func digestClassData(classData map[string][]byte) [sha256.Size]byte {
	names := make([]string, 0, len(classData))
	for name := range classData {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	var encoded [8]byte
	for _, name := range names {
		binary.LittleEndian.PutUint64(encoded[:], uint64(len(name)))
		_, _ = hash.Write(encoded[:])
		_, _ = hash.Write([]byte(name))
		data := classData[name]
		binary.LittleEndian.PutUint64(encoded[:], uint64(len(data)))
		_, _ = hash.Write(encoded[:])
		_, _ = hash.Write(data)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func (vm *VM) SetTraceHook(hook TraceHook) {
	vm.hook = hook
}

func (vm *VM) RegisterNative(
	class, name, descriptor string,
	native NativeFunc,
) {
	key := nativeKey{class: class, name: name, descriptor: descriptor}
	if native == nil {
		delete(vm.natives, key)
		return
	}
	vm.natives[key] = native
}

func (vm *VM) RegisterHostClass(class, super string) {
	vm.hostSupers[class] = super
}

func (vm *VM) RegisterStaticField(
	class, name, descriptor string,
	value Value,
) {
	vm.hostStatic[fieldStorageKey(class, name, descriptor)] = value
}

func (vm *VM) SetProperties(properties map[string]string) {
	vm.properties = make(map[string]string, len(properties))
	for name, value := range properties {
		vm.properties[name] = value
	}
}

func (vm *VM) SetResources(resources map[string][]byte) {
	_ = vm.SetResourcesChecked(resources)
}

// SetResourcesChecked mounts validated package resources in the shared
// read-only namespace and reports malformed paths or quota failures.
func (vm *VM) SetResourcesChecked(resources map[string][]byte) error {
	if err := vm.services.Storage.MountPackage(resources); err != nil {
		return err
	}
	return nil
}

func (vm *VM) resource(name string) ([]byte, bool) {
	data, err := vm.services.Storage.ReadFile(shared.NamespacePackage, name)
	return data, err == nil
}

func (vm *VM) CurrentDisplay() uint32 {
	return vm.currentDisplay
}

func (vm *VM) FrameRGBA() []byte {
	pixels, err := vm.services.Graphics.RGBA(vm.serviceOwner, vm.screenSurface)
	if err != nil {
		return nil
	}
	return pixels
}

func (vm *VM) Services() *shared.Services {
	return vm.services
}

func (vm *VM) ServiceOwner() shared.OwnerID {
	return vm.serviceOwner
}

func (vm *VM) ScreenSurface() shared.ServiceID {
	return vm.screenSurface
}

func (vm *VM) Object(reference uint32) (*Object, bool) {
	if reference == 0 {
		return nil, false
	}
	object, ok := vm.heap[reference]
	return object, ok
}

func (vm *VM) NewObject(class string, native any) uint32 {
	reference := vm.nextReference
	vm.nextReference++
	vm.heap[reference] = &Object{
		Class:  class,
		Fields: make(map[string]Value),
		Native: native,
	}
	return reference
}

func (vm *VM) NewString(value string) uint32 {
	return vm.NewObject("java/lang/String", value)
}

func (vm *VM) String(reference uint32) (string, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return "", fmt.Errorf("invalid string reference %d", reference)
	}
	value, ok := object.Native.(string)
	if !ok || object.Class != "java/lang/String" {
		return "", fmt.Errorf("object %d is not a java/lang/String", reference)
	}
	return value, nil
}

func (vm *VM) NewByteArray(data []byte) uint32 {
	elements := make([]Value, len(data))
	for index, value := range data {
		elements[index] = IntValue(int32(int8(value)))
	}
	return vm.newArray("[B", elements)
}

func (vm *VM) ByteArray(reference uint32) ([]byte, error) {
	object, ok := vm.Object(reference)
	if !ok || object.Array == nil || object.Array.Descriptor != "[B" {
		return nil, fmt.Errorf("object %d is not a byte array", reference)
	}
	result := make([]byte, len(object.Array.Elements))
	for index, value := range object.Array.Elements {
		integer, err := value.Int()
		if err != nil {
			return nil, err
		}
		result[index] = byte(integer)
	}
	return result, nil
}

func (vm *VM) IsInstance(reference uint32, class string) bool {
	if reference == 0 {
		return false
	}
	object, ok := vm.Object(reference)
	if !ok {
		return false
	}
	if strings.HasPrefix(class, "[") {
		return object.Array != nil && object.Array.Descriptor == class
	}
	return vm.classAssignable(object.Class, class)
}

func (vm *VM) classAssignable(actual, target string) bool {
	pending := []string{actual}
	visited := make(map[string]struct{})
	for len(pending) != 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current == "" {
			continue
		}
		if _, seen := visited[current]; seen {
			continue
		}
		visited[current] = struct{}{}
		if current == target {
			return true
		}
		if runtime := vm.classes[current]; runtime != nil {
			pending = append(pending, runtime.class.SuperName)
			pending = append(pending, runtime.class.Interfaces...)
			continue
		}
		pending = append(pending, vm.hostSupers[current])
		pending = append(pending, hostInterfaces(current)...)
	}
	return target == "java/lang/Object"
}

// SupportsNativeReference reports whether a host method reference can resolve
// to a registered native directly or through a registered host implementor.
// It is intended for inspectors and compatibility audits, not invocation.
func (vm *VM) SupportsNativeReference(class, name, descriptor string) bool {
	for current := class; current != ""; current = vm.superName(current) {
		if _, ok := vm.natives[nativeKey{current, name, descriptor}]; ok {
			return true
		}
	}
	for key := range vm.natives {
		if key.name == name && key.descriptor == descriptor &&
			vm.classAssignable(key.class, class) {
			return true
		}
	}
	return false
}

// SupportsHostFieldReference reports whether a host static field is registered.
// Guest instance fields do not need registration because their storage is
// allocated lazily on each object.
func (vm *VM) SupportsHostFieldReference(class, name, descriptor string) bool {
	for current := class; current != ""; current = vm.superName(current) {
		if _, ok := vm.hostStatic[fieldStorageKey(current, name, descriptor)]; ok {
			return true
		}
	}
	return false
}

func hostInterfaces(class string) []string {
	switch class {
	case "javax/microedition/io/InputConnection",
		"javax/microedition/io/OutputConnection":
		return []string{"javax/microedition/io/Connection"}
	case "javax/microedition/io/SocketConnection":
		return []string{"javax/microedition/io/StreamConnection"}
	case "javax/microedition/io/HttpConnection":
		return []string{"javax/microedition/io/ContentConnection"}
	case "javax/microedition/io/ContentConnection":
		return []string{"javax/microedition/io/StreamConnection"}
	case "javax/microedition/io/StreamConnection":
		return []string{
			"javax/microedition/io/InputConnection",
			"javax/microedition/io/OutputConnection",
		}
	case "java/io/DataInputStream":
		return []string{"java/io/DataInput"}
	case "java/io/DataOutputStream":
		return []string{"java/io/DataOutput"}
	default:
		return nil
	}
}

func (vm *VM) InvokeStatic(
	ctx context.Context,
	className, name, descriptor string,
	args ...Value,
) (Value, bool, error) {
	budget := vm.remainingBudget()
	if err := vm.ensureInitialized(ctx, className, &budget); err != nil {
		return Value{}, false, err
	}
	class, method, err := vm.resolveDeclaredMethod(className, name, descriptor)
	if err != nil {
		if native, ok := vm.natives[nativeKey{className, name, descriptor}]; ok {
			return native(ctx, vm, 0, args)
		}
		return Value{}, false, err
	}
	if !method.Static() {
		return Value{}, false, fmt.Errorf("%s.%s%s is not static", className, name, descriptor)
	}
	return vm.execute(ctx, class, method, 0, args, &budget)
}

func (vm *VM) InvokeVirtual(
	ctx context.Context,
	receiver uint32,
	name, descriptor string,
	args ...Value,
) (Value, bool, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return Value{}, false, fmt.Errorf("SKVM invalid receiver %d", receiver)
	}
	class, method, err := vm.resolveVirtualMethod(object.Class, name, descriptor)
	if err != nil {
		return Value{}, false, fmt.Errorf(
			"%w: %s.%s%s",
			ErrMethodNotFound,
			object.Class,
			name,
			descriptor,
		)
	}
	budget := vm.remainingBudget()
	return vm.execute(ctx, class, method, receiver, args, &budget)
}

func (vm *VM) ShowCurrent(ctx context.Context) error {
	if vm.currentDisplay == 0 {
		return fmt.Errorf("SKVM has no current Displayable")
	}
	_, _, err := vm.InvokeVirtual(ctx, vm.currentDisplay, "showNotify", "()V")
	if errors.Is(err, ErrMethodNotFound) {
		return nil
	}
	return err
}

func (vm *VM) PaintCurrent(ctx context.Context) error {
	if vm.currentDisplay == 0 {
		return fmt.Errorf("SKVM has no current Displayable")
	}
	if err := vm.resetScreenGraphics(); err != nil {
		return fmt.Errorf("reset SKVM paint graphics: %w", err)
	}
	_, _, err := vm.InvokeVirtual(
		ctx,
		vm.currentDisplay,
		"paint",
		"(Ljavax/microedition/lcdui/Graphics;)V",
		ReferenceValue(vm.ScreenGraphics()),
	)
	return err
}

func (vm *VM) KeyEvent(ctx context.Context, key int32, pressed bool) error {
	if vm.currentDisplay == 0 {
		return fmt.Errorf("SKVM has no current Displayable")
	}
	method := "keyReleased"
	if pressed {
		method = "keyPressed"
	}
	_, _, err := vm.InvokeVirtual(
		ctx,
		vm.currentDisplay,
		method,
		"(I)V",
		IntValue(key),
	)
	if errors.Is(err, ErrMethodNotFound) {
		return nil
	}
	return err
}

// Advance is the SKVM adapter's explicit coordinator operation. It advances
// shared virtual time, delivers timer callbacks through the Java invocation
// gate, and offers other ready events to the adapter callback.
func (vm *VM) Advance(
	ctx context.Context,
	delta time.Duration,
	handle func(shared.Event) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := vm.services.Advance(vm.serviceOwner, delta); err != nil {
		return err
	}
	if err := vm.runReadyThreads(ctx); err != nil {
		return err
	}
	now := vm.services.Clock.Monotonic()
	for {
		event, ok := vm.services.Events.PopReady(now)
		if !ok {
			return nil
		}
		if event.Owner != vm.serviceOwner {
			if handle == nil {
				return fmt.Errorf(
					"SKVM event %d for owner %d has no adapter",
					event.Sequence,
					event.Owner,
				)
			}
			if err := handle(event); err != nil {
				return err
			}
			continue
		}
		if event.Kind == shared.EventTimer {
			taskReference := uint32(event.Value)
			task, err := vm.timerTask(taskReference)
			if err != nil || task.timer != event.ServiceID || task.cancelled {
				if err != nil {
					return err
				}
				continue
			}
			if _, _, err := vm.InvokeVirtual(
				ctx,
				taskReference,
				"run",
				"()V",
			); err != nil && !errors.Is(err, ErrMethodNotFound) {
				return err
			}
			continue
		}
		if event.Kind == shared.EventApplication &&
			event.Name == callSeriallyEventName {
			if event.Value <= 0 || uint64(event.Value) > uint64(^uint32(0)) {
				return fmt.Errorf("invalid SKVM callSerially reference %d", event.Value)
			}
			if _, _, err := vm.InvokeVirtual(
				ctx,
				uint32(event.Value),
				"run",
				"()V",
			); err != nil {
				return err
			}
			continue
		}
		if handle != nil {
			if err := handle(event); err != nil {
				return err
			}
		}
	}
}

// Start constructs the descriptor main class and executes its MIDlet
// startApp lifecycle method. The returned reference identifies the MIDlet
// object and may be retained by a scheduler or debugger.
func (vm *VM) Start(ctx context.Context, mainClass string) (uint32, error) {
	mainClass = strings.ReplaceAll(mainClass, ".", "/")
	budget := vm.remainingBudget()
	if err := vm.ensureInitialized(ctx, mainClass, &budget); err != nil {
		return 0, err
	}
	reference, err := vm.allocateObject(mainClass)
	if err != nil {
		return 0, err
	}
	class, constructor, err := vm.resolveDeclaredMethod(mainClass, "<init>", "()V")
	if err != nil {
		return 0, err
	}
	if _, _, err := vm.execute(ctx, class, constructor, reference, nil, &budget); err != nil {
		return 0, err
	}
	class, start, err := vm.resolveVirtualMethod(mainClass, "startApp", "()V")
	if err == nil {
		if _, _, err := vm.execute(ctx, class, start, reference, nil, &budget); err != nil {
			return 0, err
		}
	} else {
		class, start, err = vm.resolveVirtualMethod(
			mainClass,
			"startApp",
			"([Ljava/lang/String;)V",
		)
		if err != nil {
			return 0, err
		}
		arguments := vm.newArray("[Ljava/lang/String;", nil)
		if _, _, err := vm.execute(
			ctx,
			class,
			start,
			reference,
			[]Value{ReferenceValue(arguments)},
			&budget,
		); err != nil {
			return 0, err
		}
	}
	vm.hostStatic[fieldStorageKey(
		applicationRootClass,
		applicationRootField,
		applicationRootDescriptor,
	)] = ReferenceValue(reference)
	return reference, nil
}

func (vm *VM) remainingBudget() uint64 {
	if vm.InstructionLimit == 0 {
		return ^uint64(0)
	}
	if vm.Instructions >= vm.InstructionLimit {
		return 0
	}
	return vm.InstructionLimit - vm.Instructions
}

func (vm *VM) ensureInitialized(
	ctx context.Context,
	className string,
	budget *uint64,
) error {
	runtime, ok := vm.classes[className]
	if !ok {
		return nil
	}
	switch runtime.initState {
	case classInitialized, classInitializing:
		return nil
	case classFailed:
		return fmt.Errorf("SKVM class %q initialization previously failed", className)
	}
	runtime.initState = classInitializing
	if runtime.class.SuperName != "" {
		if err := vm.ensureInitialized(ctx, runtime.class.SuperName, budget); err != nil {
			runtime.initState = classFailed
			return err
		}
	}
	if initializer, exists := runtime.class.Method("<clinit>", "()V"); exists {
		if _, _, err := vm.execute(ctx, runtime.class, initializer, 0, nil, budget); err != nil {
			runtime.initState = classFailed
			return err
		}
	}
	runtime.initState = classInitialized
	return nil
}

func (vm *VM) allocateObject(className string) (uint32, error) {
	if _, ok := vm.classes[className]; !ok {
		if _, host := vm.hostSupers[className]; !host {
			return 0, fmt.Errorf("SKVM class %q is unavailable", className)
		}
	}
	reference := vm.NewObject(className, nil)
	object := vm.heap[reference]
	for current := className; current != ""; current = vm.superName(current) {
		runtime, ok := vm.classes[current]
		if !ok {
			continue
		}
		for _, field := range runtime.class.Fields {
			if field.AccessFlags&AccessStatic != 0 {
				continue
			}
			value, err := zeroValue(field.Descriptor)
			if err != nil {
				return 0, err
			}
			object.Fields[fieldStorageKey(current, field.Name, field.Descriptor)] = value
		}
	}
	return reference, nil
}

func (vm *VM) newArray(descriptor string, elements []Value) uint32 {
	reference := vm.NewObject(descriptor, nil)
	vm.heap[reference].Array = &Array{
		Descriptor: descriptor,
		Elements:   elements,
	}
	return reference
}

func (vm *VM) resolveDeclaredMethod(
	className, name, descriptor string,
) (*Class, Method, error) {
	runtime, ok := vm.classes[className]
	if !ok {
		return nil, Method{}, fmt.Errorf("SKVM class %q is unavailable", className)
	}
	method, ok := runtime.class.Method(name, descriptor)
	if !ok {
		return nil, Method{}, fmt.Errorf(
			"%w: %s.%s%s",
			ErrMethodNotFound,
			className,
			name,
			descriptor,
		)
	}
	return runtime.class, method, nil
}

func (vm *VM) resolveVirtualMethod(
	className, name, descriptor string,
) (*Class, Method, error) {
	for current := className; current != ""; current = vm.superName(current) {
		runtime, ok := vm.classes[current]
		if !ok {
			continue
		}
		if method, exists := runtime.class.Method(name, descriptor); exists {
			return runtime.class, method, nil
		}
	}
	return nil, Method{}, fmt.Errorf(
		"%w: %s.%s%s",
		ErrMethodNotFound,
		className,
		name,
		descriptor,
	)
}

func (vm *VM) superName(className string) string {
	if runtime, ok := vm.classes[className]; ok {
		return runtime.class.SuperName
	}
	return vm.hostSupers[className]
}

func (vm *VM) constantValue(constant Constant) (Value, error) {
	switch constant.Kind {
	case ConstantInteger:
		return IntValue(constant.Integer), nil
	case ConstantFloat:
		return FloatValue(constant.Float), nil
	case ConstantLong:
		return LongValue(constant.Long), nil
	case ConstantDouble:
		return DoubleValue(constant.Double), nil
	case ConstantString:
		return ReferenceValue(vm.NewString(constant.String)), nil
	case ConstantClass:
		return ReferenceValue(vm.NewObject("java/lang/Class", constant.Class)), nil
	default:
		return Value{}, fmt.Errorf("unsupported constant kind %q", constant.Kind)
	}
}

func (vm *VM) newThrowable(class string, message string) *thrown {
	reference := vm.NewObject(class, message)
	return &thrown{reference: reference, class: class, message: message}
}

func (vm *VM) throwableMatches(reference uint32, catchType string) bool {
	return catchType == "" || vm.IsInstance(reference, catchType)
}

func fieldStorageKey(class, name, descriptor string) string {
	return class + "\x00" + name + "\x00" + descriptor
}

func defaultHostSupers() map[string]string {
	return map[string]string{
		"java/lang/Object":                            "",
		"java/lang/Class":                             "java/lang/Object",
		"java/lang/String":                            "java/lang/Object",
		"java/lang/StringBuffer":                      "java/lang/Object",
		"java/lang/Thread":                            "java/lang/Object",
		"java/lang/Runtime":                           "java/lang/Object",
		"java/lang/Integer":                           "java/lang/Object",
		"java/lang/Byte":                              "java/lang/Object",
		"java/lang/Long":                              "java/lang/Object",
		"java/util/Random":                            "java/lang/Object",
		"java/util/Vector":                            "java/lang/Object",
		"java/util/Stack":                             "java/util/Vector",
		"java/util/Hashtable":                         "java/lang/Object",
		"java/util/Enumeration":                       "java/lang/Object",
		"java/util/Date":                              "java/lang/Object",
		"java/util/Calendar":                          "java/lang/Object",
		"java/util/TimeZone":                          "java/lang/Object",
		"java/util/Timer":                             "java/lang/Object",
		"java/util/TimerTask":                         "java/lang/Object",
		"java/io/InputStream":                         "java/lang/Object",
		"java/io/OutputStream":                        "java/lang/Object",
		"java/io/PrintStream":                         "java/io/OutputStream",
		"java/io/ByteArrayInputStream":                "java/io/InputStream",
		"java/io/ByteArrayOutputStream":               "java/io/OutputStream",
		"java/io/DataInputStream":                     "java/io/InputStream",
		"java/io/DataOutputStream":                    "java/io/OutputStream",
		"java/io/InputStreamReader":                   "java/lang/Object",
		"javax/microedition/io/Connection":            "java/lang/Object",
		"javax/microedition/io/InputConnection":       "java/lang/Object",
		"javax/microedition/io/OutputConnection":      "java/lang/Object",
		"javax/microedition/io/StreamConnection":      "java/lang/Object",
		"javax/microedition/io/ContentConnection":     "java/lang/Object",
		"javax/microedition/io/HttpConnection":        "java/lang/Object",
		"javax/microedition/io/SocketConnection":      "java/lang/Object",
		"javax/microedition/midlet/MIDlet":            "java/lang/Object",
		"javax/microedition/lcdui/Display":            "java/lang/Object",
		"javax/microedition/lcdui/Displayable":        "java/lang/Object",
		"javax/microedition/lcdui/Canvas":             "javax/microedition/lcdui/Displayable",
		"javax/microedition/lcdui/Graphics":           "java/lang/Object",
		"javax/microedition/lcdui/Image":              "java/lang/Object",
		"javax/microedition/lcdui/Font":               "java/lang/Object",
		"javax/microedition/rms/RecordStore":          "java/lang/Object",
		"com/skt/m/AudioClip":                         "java/lang/Object",
		"com/skt/m/Graphics2D":                        "java/lang/Object",
		"com/skt/m/ProgressBar":                       "java/lang/Object",
		"com/sun/midp/lcdui/InputMethodHandler":       "java/lang/Object",
		"com/xce/io/XFile":                            "java/lang/Object",
		"com/xce/io/FileInputStream":                  "java/io/InputStream",
		"com/xce/io/FileOutputStream":                 "java/io/OutputStream",
		"com/xce/io/ByteToCharConverter":              "java/lang/Object",
		"com/xce/io/ByteToCharEUC_KR":                 "com/xce/io/ByteToCharConverter",
		"com/xce/lcdui/XTextField":                    "java/lang/Object",
		"com/xce/lcdui/TextComponentHandler":          "java/lang/Object",
		"org/kwis/msp/io/File":                        "java/lang/Object",
		"org/kwis/msp/io/FileSystem":                  "java/lang/Object",
		"org/kwis/msp/lcdui/Jlet":                     "java/lang/Object",
		"org/kwis/msp/lcdui/Card":                     "java/lang/Object",
		"org/kwis/msp/lcdui/Display":                  "java/lang/Object",
		"org/kwis/msp/lcdui/Graphics":                 "java/lang/Object",
		"org/kwis/msp/lcdui/Image":                    "java/lang/Object",
		"org/kwis/msp/lcdui/Font":                     "java/lang/Object",
		"org/kwis/msf/io/Network":                     "java/lang/Object",
		"org/kwis/msf/io/Socket":                      "java/lang/Object",
		"org/kwis/msf/io/URL":                         "java/lang/Object",
		"java/lang/Throwable":                         "java/lang/Object",
		"java/lang/Exception":                         "java/lang/Throwable",
		"java/lang/RuntimeException":                  "java/lang/Exception",
		"java/lang/NullPointerException":              "java/lang/RuntimeException",
		"java/lang/ArithmeticException":               "java/lang/RuntimeException",
		"java/lang/ArrayIndexOutOfBoundsException":    "java/lang/RuntimeException",
		"java/lang/NegativeArraySizeException":        "java/lang/RuntimeException",
		"java/lang/ClassCastException":                "java/lang/RuntimeException",
		"java/lang/ArrayStoreException":               "java/lang/RuntimeException",
		"java/lang/IllegalArgumentException":          "java/lang/RuntimeException",
		"java/lang/NumberFormatException":             "java/lang/IllegalArgumentException",
		"java/lang/IllegalStateException":             "java/lang/RuntimeException",
		"java/lang/IllegalThreadStateException":       "java/lang/IllegalArgumentException",
		"java/lang/UnsupportedOperationException":     "java/lang/RuntimeException",
		"java/util/NoSuchElementException":            "java/lang/RuntimeException",
		"java/lang/IndexOutOfBoundsException":         "java/lang/RuntimeException",
		"java/lang/StringIndexOutOfBoundsException":   "java/lang/IndexOutOfBoundsException",
		"java/io/IOException":                         "java/lang/Exception",
		"java/io/UnsupportedEncodingException":        "java/io/IOException",
		"javax/microedition/rms/RecordStoreException": "java/lang/Exception",
	}
}
