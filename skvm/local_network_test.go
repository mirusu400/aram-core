package skvm

import "testing"

func TestSocketConnectorReadsLocalServerResponse(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	connection := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/Connector", "open", "(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0,
		ReferenceValue(vm.NewString("socket://211.110.18.253:8501"))))
	output := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/SocketConnection", "openOutputStream", "()Ljava/io/OutputStream;", connection))
	input := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/SocketConnection", "openInputStream", "()Ljava/io/InputStream;", connection))
	for _, b := range []byte{2, 0, 0xf8, 0x2a} {
		invokeTestNative(t, vm, "java/io/OutputStream", "write", "(I)V", output, IntValue(int32(b)))
	}
	for _, b := range []byte{3, 0, 0xf9, 0x2a, 0} {
		got := mustInt(t, invokeTestNative(t, vm, "java/io/InputStream", "read", "()I", input))
		if got != int32(b) {
			t.Fatalf("read = %x, want %x", got, b)
		}
	}
	if got := mustInt(t, invokeTestNative(t, vm, "java/io/InputStream", "read", "()I", input)); got != -1 {
		t.Fatalf("duplicate response byte = %x", got)
	}
}
