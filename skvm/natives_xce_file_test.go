package skvm

import (
	"context"
	"errors"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func xceOutput(t *testing.T, vm *VM, path string, appendFlag ...int32) uint32 {
	t.Helper()
	stream := vm.NewObject("com/xce/io/FileOutputStream", nil)
	args := []Value{ReferenceValue(vm.NewString(path))}
	descriptor := "(Ljava/lang/String;)V"
	if len(appendFlag) != 0 {
		descriptor = "(Ljava/lang/String;Z)V"
		args = append(args, IntValue(appendFlag[0]))
	}
	invokeTestNative(t, vm, "com/xce/io/FileOutputStream", "<init>", descriptor, stream, args...)
	return stream
}

func xceInvoke(t *testing.T, vm *VM, stream uint32, name, descriptor string, args ...Value) (Value, bool, error) {
	t.Helper()
	object, ok := vm.Object(stream)
	if !ok {
		t.Fatal("missing stream")
	}
	native, ok := vm.resolveNative(object.Class, Reference{Class: object.Class, Name: name, Descriptor: descriptor}, 0xb6)
	if !ok {
		t.Fatalf("missing native %s.%s%s", object.Class, name, descriptor)
	}
	return native(context.Background(), vm, stream, args)
}

func xceCall(t *testing.T, vm *VM, stream uint32, name, descriptor string, args ...Value) Value {
	t.Helper()
	value, _, err := xceInvoke(t, vm, stream, name, descriptor, args...)
	check(t, err)
	return value
}

func xceStored(t *testing.T, vm *VM, path, want string) {
	t.Helper()
	got, err := vm.services.Storage.ReadFile(shared.NamespacePrivate, path)
	check(t, err)
	if string(got) != want {
		t.Fatalf("file %q = %q, want %q", path, got, want)
	}
}

func TestXCEFileOutputAppendTruncateRoundtrip(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	out := xceOutput(t, vm, "file:///save/test.dat", 1) // append creates missing file
	xceStored(t, vm, "/save/test.dat", "")
	xceCall(t, vm, out, "write", "(I)V", IntValue(0x141))
	data := ReferenceValue(vm.NewByteArray([]byte("xBCy")))
	xceCall(t, vm, out, "write", "([BII)V", data, IntValue(1), IntValue(2))
	xceCall(t, vm, out, "flush", "()V")
	xceStored(t, vm, "/save/test.dat", "ABC")
	second := xceOutput(t, vm, `\save\test.dat`, 1)
	xceCall(t, vm, second, "write", "([B)V", ReferenceValue(vm.NewByteArray([]byte("D"))))
	xceCall(t, vm, out, "write", "(I)V", IntValue('E'))
	xceStored(t, vm, "/save/test.dat", "ABCDE")
	xceCall(t, vm, out, "close", "()V")
	xceCall(t, vm, out, "close", "()V")
	_, _, err = xceInvoke(t, vm, out, "write", "(I)V", IntValue('!'))
	requireXCEIOException(t, vm, err)
	input := vm.NewObject("com/xce/io/FileInputStream", nil)
	invokeTestNative(t, vm, "com/xce/io/FileInputStream", "<init>", "(Ljava/lang/String;)V", input, ReferenceValue(vm.NewString("/save/test.dat")))
	bulk := vm.NewObject("com/xce/io/FileInputStream", nil)
	invokeTestNative(t, vm, "com/xce/io/FileInputStream", "<init>", "(Ljava/lang/String;)V", bulk, ReferenceValue(vm.NewString("/save/test.dat")))
	destination := vm.NewByteArray(make([]byte, 7))
	if got := mustInt(t, xceCall(t, vm, bulk, "read", "([BII)I", ReferenceValue(destination), IntValue(1), IntValue(5))); got != 5 {
		t.Fatalf("bulk read = %d", got)
	}
	readback, err := vm.ByteArray(destination)
	check(t, err)
	if string(readback) != "\x00ABCDE\x00" {
		t.Fatalf("bulk data = %q", readback)
	}
	for _, want := range []int32{'A', 'B', 'C', 'D', 'E', -1} {
		if got := mustInt(t, xceCall(t, vm, input, "read", "()I")); got != want {
			t.Fatalf("read = %d, want %d", got, want)
		}
	}
	xceCall(t, vm, input, "close", "()V")
	_, _, err = xceInvoke(t, vm, input, "read", "()I")
	requireXCEIOException(t, vm, err)
	for _, flag := range [][]int32{{0}, nil} {
		out = xceOutput(t, vm, "/save/test.dat", flag...)
		xceStored(t, vm, "/save/test.dat", "")
		xceCall(t, vm, out, "write", "([B)V", ReferenceValue(vm.NewByteArray([]byte("reset"))))
		xceStored(t, vm, "/save/test.dat", "reset")
	}
}

func requireXCEIOException(t *testing.T, vm *VM, err error) {
	t.Helper()
	var thrown *thrown
	if !errors.As(err, &thrown) {
		t.Fatalf("expected guest IOException, got %v", err)
	}
	object, ok := vm.Object(thrown.reference)
	if !ok || object.Class != "java/io/IOException" {
		t.Fatalf("expected IOException, got %#v", object)
	}
}

func TestXCEFileOutputSnapshot(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	out := xceOutput(t, vm, "/save/state", 0)
	xceCall(t, vm, out, "write", "(I)V", IntValue('A'))
	appender := xceOutput(t, vm, "/save/state", 1)
	closed := xceOutput(t, vm, "/save/closed")
	xceCall(t, vm, closed, "close", "()V")
	snapshot, err := vm.MarshalBinary()
	check(t, err)
	xceCall(t, vm, out, "write", "(I)V", IntValue('!'))
	check(t, vm.UnmarshalBinary(snapshot))
	xceStored(t, vm, "/save/state", "A")
	xceCall(t, vm, appender, "write", "(I)V", IntValue('B'))
	xceCall(t, vm, out, "write", "(I)V", IntValue('C')) // restored position overwrites B
	xceStored(t, vm, "/save/state", "AC")
	_, _, err = xceInvoke(t, vm, closed, "write", "(I)V", IntValue('!'))
	requireXCEIOException(t, vm, err)
}

func TestXCEFileOutputXFileAndWriteBounds(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	file := vm.NewObject("com/xce/io/XFile", nil)
	invokeTestNative(t, vm, "com/xce/io/XFile", "<init>", "(Ljava/lang/String;I)V", file, ReferenceValue(vm.NewString("/xfile")), IntValue(1))
	out := vm.NewObject("com/xce/io/FileOutputStream", nil)
	invokeTestNative(t, vm, "com/xce/io/FileOutputStream", "<init>", "(Lcom/xce/io/XFile;)V", out, ReferenceValue(file))
	xceCall(t, vm, out, "write", "([B)V", ReferenceValue(vm.NewByteArray([]byte("AB"))))
	xceCall(t, vm, out, "write", "([BII)V", ReferenceValue(vm.NewByteArray(nil)), IntValue(0), IntValue(0))
	_, _, err = xceInvoke(t, vm, out, "write", "([BII)V", ReferenceValue(vm.NewByteArray([]byte("!"))), IntValue(1), IntValue(1))
	var exception *thrown
	if !errors.As(err, &exception) || exception.class != "java/lang/IndexOutOfBoundsException" {
		t.Fatalf("invalid slice error = %v", err)
	}
	xceStored(t, vm, "/xfile", "AB")
	xceCall(t, vm, out, "close", "()V")
	snapshot, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(snapshot))
	_, _, err = xceInvoke(t, vm, out, "write", "(I)V", IntValue('!'))
	requireXCEIOException(t, vm, err)
}

func TestXCEFileOutputSandboxAndFailure(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	for _, path := range []string{"../../escape", "file:///../escape", "bad\x00name", "C:/guest-only.dat"} {
		stream := vm.NewObject("com/xce/io/FileOutputStream", nil)
		_, _, err := nativeXCEFileOutputInit(context.Background(), vm, stream, []Value{ReferenceValue(vm.NewString(path)), IntValue(1)})
		requireXCEIOException(t, vm, err)
	}
	// Absolute guest paths are still only VFS names.
	out := xceOutput(t, vm, "/guest-only.dat")
	xceCall(t, vm, out, "write", "(I)V", IntValue('V'))
	xceStored(t, vm, "/guest-only.dat", "V")
	config := shared.DefaultConfig()
	config.Limits.Storage.MaxFileBytes = 1
	services, err := shared.NewServices(config)
	check(t, err)
	vm, err = NewWithServices(map[string][]byte{}, services, 1)
	check(t, err)
	out = xceOutput(t, vm, "/limit")
	xceCall(t, vm, out, "write", "(I)V", IntValue('A'))
	_, _, err = xceInvoke(t, vm, out, "write", "([B)V", ReferenceValue(vm.NewByteArray([]byte("BC"))))
	requireXCEIOException(t, vm, err)
	xceStored(t, vm, "/limit", "A")
	state, err := vm.outputStream(out)
	check(t, err)
	if len(state.data) != 1 {
		t.Fatal("failed write advanced position")
	}
}
