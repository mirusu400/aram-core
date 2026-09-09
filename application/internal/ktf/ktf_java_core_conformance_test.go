package ktf

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
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
