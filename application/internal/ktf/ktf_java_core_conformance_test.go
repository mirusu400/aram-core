package ktf

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/internal/ime"
)

func TestKTFStringImplementsCLDC11OverloadsAndIntern(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	buffer := newHostObject(t, runtime, "java/lang/StringBuffer")
	runtime.stringBuffers[buffer] = "source"
	result := newHostObject(t, runtime, "java/lang/String")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, result))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, buffer))
	_, err := runtime.handleStringMethod(
		"<init>",
		"(Ljava/lang/StringBuffer;)V",
	)
	check(t, err)
	if got := runtime.javaStringValue(result); got != "source" {
		t.Fatalf("String(StringBuffer) = %q, want source", got)
	}

	value := newJavaString(t, runtime, "A😀B")
	prefix := newJavaString(t, runtime, "😀")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, value))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, prefix))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 1))
	matched, err := runtime.handleStringMethod(
		"startsWith",
		"(Ljava/lang/String;I)Z",
	)
	check(t, err)
	if matched != 1 {
		t.Fatal("String.startsWith at UTF-16 offset 1 did not match")
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 2))
	matched, err = runtime.handleStringMethod(
		"startsWith",
		"(Ljava/lang/String;I)Z",
	)
	check(t, err)
	if matched != 0 {
		t.Fatal("String.startsWith matched in the middle of a surrogate pair")
	}

	check(t, runtime.CPU.WriteRegister(
		cpu.RegisterR1,
		math.Float32bits(1.5),
	))
	formatted, err := runtime.handleStringMethod(
		"valueOf",
		"(F)Ljava/lang/String;",
	)
	check(t, err)
	if got := runtime.javaStringValue(formatted); got != "1.5" {
		t.Fatalf("String.valueOf(float) = %q, want 1.5", got)
	}

	bits := math.Float64bits(math.Inf(1))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, uint32(bits)))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32(bits>>32)))
	formatted, err = runtime.handleStringMethod(
		"valueOf",
		"(D)Ljava/lang/String;",
	)
	check(t, err)
	if got := runtime.javaStringValue(formatted); got != "Infinity" {
		t.Fatalf("String.valueOf(double) = %q, want Infinity", got)
	}

	first := newJavaString(t, runtime, "canonical")
	second := newJavaString(t, runtime, "canonical")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, first))
	canonical, err := runtime.handleStringMethod(
		"intern",
		"()Ljava/lang/String;",
	)
	check(t, err)
	if canonical != first {
		t.Fatalf("first intern = 0x%08x, want 0x%08x", canonical, first)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, second))
	canonical, err = runtime.handleStringMethod(
		"intern",
		"()Ljava/lang/String;",
	)
	check(t, err)
	if canonical != first {
		t.Fatalf("second intern = 0x%08x, want 0x%08x", canonical, first)
	}
}

func TestKTFStringBufferModelsUTF16CapacityAndNumericOverloads(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	buffer := newHostObject(t, runtime, "java/lang/StringBuffer")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, buffer))
	_, err := runtime.handleStringBufferMethod("<init>", "()V")
	check(t, err)

	capacity, err := runtime.handleStringBufferMethod("capacity", "()I")
	check(t, err)
	if capacity != 16 {
		t.Fatalf("initial StringBuffer capacity = %d, want 16", capacity)
	}

	characters, err := runtime.newJavaCharArray("A😀")
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, characters))
	returned, err := runtime.handleStringBufferMethod(
		"append",
		"([C)Ljava/lang/StringBuffer;",
	)
	check(t, err)
	if returned != buffer {
		t.Fatalf("append(char[]) returned 0x%08x, want receiver", returned)
	}
	length, err := runtime.handleStringBufferMethod("length", "()I")
	check(t, err)
	if length != 3 {
		t.Fatalf("UTF-16 StringBuffer length = %d, want 3", length)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, math.Float32bits(1.5)))
	_, err = runtime.handleStringBufferMethod(
		"append",
		"(F)Ljava/lang/StringBuffer;",
	)
	check(t, err)
	bits := math.Float64bits(2.25)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32(bits)))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, uint32(bits>>32)))
	_, err = runtime.handleStringBufferMethod(
		"append",
		"(D)Ljava/lang/StringBuffer;",
	)
	check(t, err)
	if got := runtime.stringBuffers[buffer]; got != "A😀1.52.25" {
		t.Fatalf("numeric append result = %q", got)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 40))
	_, err = runtime.handleStringBufferMethod("ensureCapacity", "(I)V")
	check(t, err)
	capacity, err = runtime.handleStringBufferMethod("capacity", "()I")
	check(t, err)
	if capacity != 40 {
		t.Fatalf("ensured StringBuffer capacity = %d, want 40", capacity)
	}

	runtime.stringBuffers[buffer] = "A😀B"
	_, err = runtime.handleStringBufferMethod(
		"reverse",
		"()Ljava/lang/StringBuffer;",
	)
	check(t, err)
	if got := runtime.stringBuffers[buffer]; got != "B😀A" {
		t.Fatalf("UTF-16 StringBuffer.reverse = %q, want B😀A", got)
	}
}

func TestKTFPrintStreamWritesToWrappedOutputStream(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	target := newHostObject(t, runtime, "java/io/ByteArrayOutputStream")
	stream := newHostObject(t, runtime, "java/io/PrintStream")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, stream))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, target))
	_, err := runtime.handlePrintStreamMethod(
		"<init>",
		"(Ljava/io/OutputStream;)V",
	)
	check(t, err)

	text := newJavaString(t, runtime, "score=")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, text))
	_, err = runtime.handlePrintStreamMethod(
		"print",
		"(Ljava/lang/String;)V",
	)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 7))
	_, err = runtime.handlePrintStreamMethod("println", "(I)V")
	check(t, err)

	array, err := runtime.newJavaByteArray([]byte{0x12, 0x34, 0x56})
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, array))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 1))
	stack := allocWords(t, runtime, 4)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	check(t, runtime.writeWords(
		stack,
		[]uint32{1},
	))
	_, err = runtime.handlePrintStreamMethod("write", "([BII)V")
	check(t, err)
	if want := []byte("score=7\n4"); !bytes.Equal(runtime.outputStreams[target], want) {
		t.Fatalf(
			"PrintStream output = %q, want %q",
			runtime.outputStreams[target],
			want,
		)
	}
	errorState, err := runtime.handlePrintStreamMethod("checkError", "()Z")
	check(t, err)
	if errorState != 0 {
		t.Fatalf("PrintStream.checkError = %d, want false", errorState)
	}
}

func TestKTFSystemPropertiesAndIntegerParsingFollowCLDC(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	getProperty := HostJavaMethod(
		"java/lang/System",
		"getProperty",
		"(Ljava/lang/String;)Ljava/lang/String;",
	)
	key := newJavaString(t, runtime, "microedition.configuration")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, key))
	value, err := getProperty(context.Background(), runtime)
	check(t, err)
	if got := runtime.javaStringValue(value); got != "CLDC-1.1" {
		t.Fatalf("microedition.configuration = %q, want CLDC-1.1", got)
	}

	unknown := newJavaString(t, runtime, "missing.property")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, unknown))
	value, err = getProperty(context.Background(), runtime)
	check(t, err)
	if value != 0 {
		t.Fatalf("unknown System property = 0x%08x, want null", value)
	}

	invalid := newJavaString(t, runtime, " 12 ")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, invalid))
	_, err = runtime.handleIntegerMethod(
		"parseInt",
		"(Ljava/lang/String;)I",
	)
	if err == nil {
		t.Fatal("parseInt accepted surrounding whitespace")
	}
	if runtime.LastJavaThrowName != "java/lang/NumberFormatException" {
		t.Fatalf(
			"parseInt whitespace exception = %q",
			runtime.LastJavaThrowName,
		)
	}
}

func TestKTFClassReportsArraysAndInterfaces(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	arrayClass, err := runtime.EnsureJavaClass("[I")
	check(t, err)
	arrayObject, err := runtime.javaClassObject(arrayClass)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, arrayObject))
	isArray, err := runtime.handleClassMethod(context.Background(), "isArray", "()Z")
	check(t, err)
	if isArray != 1 {
		t.Fatalf("int[].class.isArray = %d, want true", isArray)
	}

	interfaceClass, err := runtime.EnsureJavaClass("org/kwis/msp/media/Player")
	check(t, err)
	interfaceObject, err := runtime.javaClassObject(interfaceClass)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, interfaceObject))
	isInterface, err := runtime.handleClassMethod(
		context.Background(),
		"isInterface",
		"()Z",
	)
	check(t, err)
	if isInterface != 1 {
		t.Fatalf("Player.class.isInterface = %d, want true", isInterface)
	}

	_, err = runtime.handleClassMethod(
		context.Background(),
		"newInstance",
		"()Ljava/lang/Object;",
	)
	if err == nil {
		t.Fatal("Class.newInstance instantiated an interface")
	}
	if runtime.LastJavaThrowName != "java/lang/InstantiationException" {
		t.Fatalf(
			"interface newInstance exception = %q",
			runtime.LastJavaThrowName,
		)
	}

	missing := newJavaString(t, runtime, "missing.Class")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, missing))
	_, err = runtime.handleClassMethod(
		context.Background(),
		"forName",
		"(Ljava/lang/String;)Ljava/lang/Class;",
	)
	if err == nil {
		t.Fatal("Class.forName returned normally for a missing class")
	}
	if runtime.LastJavaThrowName != "java/lang/ClassNotFoundException" {
		t.Fatalf(
			"Class.forName exception = %q",
			runtime.LastJavaThrowName,
		)
	}
}

func TestKTFEventQueueCopiesPostsAndDispatchesListeners(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	runtime.DeferThreads = true
	queue := newHostObject(t, runtime, "org/kwis/msp/lcdui/EventQueue")
	source, err := runtime.NewJavaArray("[I", 4, 4)
	check(t, err)
	sourceFields := readU32(t, runtime, source)
	posted := ktfJavaEvent{0x5000, 11, 22, 33}
	check(t, runtime.writeWords(sourceFields+8, posted[:]))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, queue))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, source))
	accepted, err := runtime.handleEventQueueMethod(
		context.Background(),
		"postEvent",
		"([I)Z",
	)
	check(t, err)
	if accepted != 1 || len(runtime.eventQueueEvents) != 1 {
		t.Fatalf(
			"EventQueue.postEvent accepted=%d queued=%d",
			accepted,
			len(runtime.eventQueueEvents),
		)
	}
	check(t, runtime.writeWords(sourceFields+8, []uint32{0, 0, 0, 0}))

	destination, err := runtime.NewJavaArray("[I", 4, 4)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, destination))
	_, err = runtime.handleEventQueueMethod(
		context.Background(),
		"getNextEvent",
		"([I)V",
	)
	check(t, err)
	destinationFields := readU32(t, runtime, destination)
	if got := readWords(t, runtime, destinationFields+8, 4); !equalWords(got, posted[:]) {
		t.Fatalf("EventQueue copied event = %v, want %v", got, posted)
	}

	listener := newHostObject(t, runtime, "org/kwis/msp/lcdui/JletEventListener")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 0x5000))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, listener))
	_, err = runtime.handleEventQueueMethod(
		context.Background(),
		"hookEvent",
		"(ILorg/kwis/msp/lcdui/JletEventListener;)V",
	)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, queue))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, destination))
	_, err = runtime.handleEventQueueMethod(
		context.Background(),
		"dispatchEvent",
		"([I)V",
	)
	check(t, err)
	if len(runtime.Tasks) != 1 || runtime.Tasks[0] == nil || runtime.Tasks[0].Done {
		t.Fatalf("EventQueue listener task = %+v", runtime.Tasks)
	}
}

func TestKTFDisplayRegistersAndReleasesGrabbedKeys(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	runtime.DeferThreads = true
	display := newHostObject(t, runtime, "org/kwis/msp/lcdui/Display")
	listener := newHostObject(t, runtime, "org/kwis/msp/lcdui/JletEventListener")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, display))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32('5')))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, listener))
	_, err := runtime.handleDisplayMethod(
		context.Background(),
		"grabKey",
		"(ILorg/kwis/msp/lcdui/JletEventListener;)V",
	)
	check(t, err)
	queued, err := runtime.QueueKeyEvent(true, '5')
	check(t, err)
	if !queued || len(runtime.Tasks) != 1 {
		t.Fatalf("grabbed key queued=%t tasks=%d", queued, len(runtime.Tasks))
	}
	if !runtime.CanQueueKeyEventFor('5') {
		t.Fatal("grabbed key was rejected without a display Card")
	}
	if runtime.CanQueueKeyEventFor('6') {
		t.Fatal("ungrabbed key was accepted without a display Card")
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32('5')))
	_, err = runtime.handleDisplayMethod(
		context.Background(),
		"ungrabKey",
		"(I)V",
	)
	check(t, err)
	if _, ok := runtime.grabbedKeys['5']; ok {
		t.Fatal("Display.ungrabKey left the listener registered")
	}
}

func TestKTFInputMethodHandlerComposesAndNotifies(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	handler := newHostObject(t, runtime, "org/kwis/msp/lcdui/InputMethodHandler")
	listener := newHostObject(t, runtime, "org/kwis/msp/lcdui/InputMethodListener")

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, handler))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32(ktfInputConstraintAny)))
	_, err := runtime.handleInputMethodHandlerMethod("<init>", "(I)V")
	check(t, err)
	mode, err := runtime.handleInputMethodHandlerMethod("getCurrentMode", "()I")
	check(t, err)
	if mode != uint32(ime.ModeKorean) {
		t.Fatalf("initial ANY input mode = %d, want Korean", mode)
	}
	code, err := runtime.handleInputMethodHandlerMethod(
		"getCurrentModeCode",
		"()Ljava/lang/String;",
	)
	check(t, err)
	if got := runtime.javaStringValue(code); got != "KO" {
		t.Fatalf("initial input mode code = %q, want KO", got)
	}

	_, err = runtime.handleInputMethodHandlerMethod("changeCurrentModeToNext", "()V")
	check(t, err)
	mode, err = runtime.handleInputMethodHandlerMethod("getCurrentInputMode", "()I")
	check(t, err)
	if mode != uint32(ime.ModeENLower) {
		t.Fatalf("next input mode = %d, want EN/S", mode)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, listener))
	_, err = runtime.handleInputMethodHandlerMethod(
		"setInputMethodListener",
		"(Lorg/kwis/msp/lcdui/InputMethodListener;)V",
	)
	check(t, err)

	// Force callbacks into PendingJavaCalls so their arguments can be checked
	// without executing an artificial interface method body.
	runtime.Tasks = make([]*Task, ktfBackgroundTaskLimit)
	for index := range runtime.Tasks {
		runtime.Tasks[index] = &Task{}
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32('2')))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, KeyPressed))
	handled, err := runtime.handleInputMethodHandlerMethod("notifyKeyInput", "(II)Z")
	check(t, err)
	if handled != 1 || len(runtime.PendingJavaCalls) != 1 {
		t.Fatalf("first IME press handled=%d callbacks=%d", handled, len(runtime.PendingJavaCalls))
	}
	first := runtime.PendingJavaCalls[0]
	if first.instance != listener || first.name != "notifyTextChanged" ||
		first.descriptor != "([CII)V" || len(first.args) != 3 {
		t.Fatalf("first IME callback = %+v", first)
	}
	text, err := runtime.readJavaCharArrayRange(first.args[0], 0, first.args[1])
	check(t, err)
	if text != "a" || int32(first.args[2]) != -1 {
		t.Fatalf("first IME edit = %q mode=%d, want insert a", text, int32(first.args[2]))
	}

	handled, err = runtime.handleInputMethodHandlerMethod("notifyKeyInput", "(II)Z")
	check(t, err)
	if handled != 1 || len(runtime.PendingJavaCalls) != 2 {
		t.Fatalf("second IME press handled=%d callbacks=%d", handled, len(runtime.PendingJavaCalls))
	}
	second := runtime.PendingJavaCalls[1]
	text, err = runtime.readJavaCharArrayRange(second.args[0], 0, second.args[1])
	check(t, err)
	if text != "b" || int32(second.args[2]) != 0 {
		t.Fatalf("second IME edit = %q mode=%d, want replace b", text, int32(second.args[2]))
	}
}

func TestKTFInputMethodHandlerEnforcesConstraintAndBounds(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	handler := newHostObject(t, runtime, "org/kwis/msp/lcdui/InputMethodHandler")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, handler))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32(ktfInputConstraintPhone)))
	_, err := runtime.handleInputMethodHandlerMethod("<init>", "(I)V")
	check(t, err)

	mode, err := runtime.handleInputMethodHandlerMethod("getCurrentMode", "()I")
	check(t, err)
	if mode != uint32(ime.ModeNumeric) {
		t.Fatalf("phone input mode = %d, want numeric", mode)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32(ime.ModeKorean)))
	_, err = runtime.handleInputMethodHandlerMethod("setCurrentMode", "(I)Z")
	if err == nil || runtime.LastJavaThrowName != "java/lang/IllegalArgumentException" {
		t.Fatalf("unsupported input mode error=%v exception=%q", err, runtime.LastJavaThrowName)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, handler))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 0))
	stack := allocWords(t, runtime, 2)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	check(t, runtime.writeWords(stack, []uint32{0, 10}))
	_, err = runtime.handleInputMethodHandlerMethod("setSymbolPosition", "(IIII)V")
	if err == nil || runtime.LastJavaThrowName != "java/lang/IllegalArgumentException" {
		t.Fatalf("invalid symbol bounds error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
}

func TestKTFWrapperAndMathStaticConstantsUseJavaBits(t *testing.T) {
	runtime := newTestRuntime(t)
	tests := []struct {
		class, name, descriptor string
		want                    uint64
	}{
		{"java/lang/Byte", "MIN_VALUE", "B", uint64(uint32(0xffffff80))},
		{"java/lang/Character", "MAX_RADIX", "I", 36},
		{"java/lang/Integer", "MIN_VALUE", "I", 0x80000000},
		{"java/lang/Long", "MAX_VALUE", "J", 0x7fffffffffffffff},
		{"java/lang/Float", "NaN", "F", 0x7fc00000},
		{"java/lang/Double", "NEGATIVE_INFINITY", "D", 0xfff0000000000000},
		{"java/lang/Math", "PI", "D", 0x400921fb54442d18},
	}
	for _, test := range tests {
		class := inspectClass(t, runtime, ensureClass(t, runtime, test.class))
		field, err := runtime.ResolveJavaField(class, test.name, test.descriptor)
		check(t, err)
		low := readU32(t, runtime, field+12)
		got := uint64(low)
		if test.descriptor == "J" || test.descriptor == "D" {
			got |= uint64(readU32(t, runtime, field+16)) << 32
		}
		if got != test.want {
			t.Errorf("%s.%s bits = 0x%x, want 0x%x", test.class, test.name, got, test.want)
		}
	}
}

func TestKTFCharacterUsesUnicodeCaseAndDigits(t *testing.T) {
	runtime := newTestRuntime(t)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, uint32('\u00c4')))
	lower, err := runtime.handleCharacterMethod("toLowerCase", "(C)C")
	check(t, err)
	if lower != uint32('\u00e4') {
		t.Fatalf("Character.toLowerCase(Ä) = U+%04X, want U+00E4", lower)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, uint32('\u0665')))
	digit, err := runtime.handleCharacterMethod("isDigit", "(C)Z")
	check(t, err)
	if digit != 1 {
		t.Fatal("Character.isDigit(Arabic-Indic five) = false")
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 10))
	value, err := runtime.handleCharacterMethod("digit", "(CI)I")
	check(t, err)
	if value != 5 {
		t.Fatalf("Character.digit(Arabic-Indic five, 10) = %d, want 5", value)
	}
}

func equalWords(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
