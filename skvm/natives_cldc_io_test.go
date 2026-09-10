package skvm

import (
	"testing"
	"unicode/utf16"
)

func TestCLDCInputStreamReaderDecodesEUC_KR(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	encoded, err := vm.services.Text.Encode("가A", "euc-kr")
	check(t, err)
	input := vm.NewObject("java/io/ByteArrayInputStream", &inputStreamState{data: encoded})
	reader := vm.NewObject("java/io/InputStreamReader", nil)
	invokeTestNative(t, vm, "java/io/InputStreamReader", "<init>", "(Ljava/io/InputStream;)V", reader, ReferenceValue(input))
	destination := vm.newCharArray(make([]uint16, 4))
	result := invokeTestNative(t, vm, "java/io/InputStreamReader", "read", "([CII)I", reader, ReferenceValue(destination), IntValue(0), IntValue(4))
	count, err := result.Int()
	check(t, err)
	if count != 2 {
		t.Fatalf("read count = %d", count)
	}
	object, _ := vm.Object(destination)
	units := []uint16{uint16(mustInt(t, object.Array.Elements[0])), uint16(mustInt(t, object.Array.Elements[1]))}
	if got := string(utf16.Decode(units)); got != "가A" {
		t.Fatalf("decoded text = %q", got)
	}
}

func TestCLDCOutputStreamWriterAndPrintStreamWrite(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	output := vm.NewObject("java/io/ByteArrayOutputStream", &outputStreamState{})
	writer := vm.NewObject("java/io/OutputStreamWriter", nil)
	invokeTestNative(t, vm, "java/io/OutputStreamWriter", "<init>", "(Ljava/io/OutputStream;)V", writer, ReferenceValue(output))
	text := vm.NewString("한글")
	invokeTestNative(t, vm, "java/io/OutputStreamWriter", "write", "(Ljava/lang/String;II)V", writer, ReferenceValue(text), IntValue(0), IntValue(2))
	state, err := vm.outputStream(output)
	check(t, err)
	decoded, err := vm.services.Text.Decode(state.data, "euc-kr")
	check(t, err)
	if decoded != "한글" {
		t.Fatalf("writer output = %q", decoded)
	}
	printer := vm.NewObject("java/io/PrintStream", nil)
	invokeTestNative(t, vm, "java/io/PrintStream", "<init>", "(Ljava/io/OutputStream;)V", printer, ReferenceValue(output))
	invokeTestNative(t, vm, "java/io/PrintStream", "println", "(I)V", printer, IntValue(42))
	if got := string(state.data[len(state.data)-3:]); got != "42\n" {
		t.Fatalf("PrintStream suffix = %q", got)
	}
}

func mustInt(t *testing.T, value Value) int32 {
	t.Helper()
	result, err := value.Int()
	check(t, err)
	return result
}
