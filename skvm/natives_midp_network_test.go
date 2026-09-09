package skvm

import "testing"

func mustReference(t *testing.T, value Value) uint32 {
	t.Helper()
	reference, err := value.Reference()
	check(t, err)
	return reference
}

func mustStringValue(t *testing.T, vm *VM, value Value) string {
	t.Helper()
	text, err := vm.String(mustReference(t, value))
	check(t, err)
	return text
}

func TestMIDPHttpsURLResponseAndSecurityInfo(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	connection := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/Connector", "open", "(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0,
		ReferenceValue(vm.NewString("https://example.com:8443/game?q=1#top"))))
	object, _ := vm.Object(connection)
	if object.Class != "javax/microedition/io/HttpsConnection" {
		t.Fatalf("HTTPS class = %q", object.Class)
	}
	if got := mustStringValue(t, vm, invokeTestNative(t, vm, "javax/microedition/io/HttpConnection", "getHost", "()Ljava/lang/String;", connection)); got != "example.com" {
		t.Fatalf("host = %q", got)
	}
	if got := mustStringValue(t, vm, invokeTestNative(t, vm, "javax/microedition/io/HttpConnection", "getFile", "()Ljava/lang/String;", connection)); got != "/game?q=1" {
		t.Fatalf("file = %q", got)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/io/HttpsConnection", "getPort", "()I", connection)); got != 8443 {
		t.Fatalf("port = %d", got)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/io/HttpConnection", "getResponseCode", "()I", connection)); got != 204 {
		t.Fatalf("response code = %d", got)
	}
	security := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/HttpsConnection", "getSecurityInfo", "()Ljavax/microedition/io/SecurityInfo;", connection))
	certificate := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/SecurityInfo", "getServerCertificate", "()Ljavax/microedition/pki/Certificate;", security))
	if got := mustStringValue(t, vm, invokeTestNative(t, vm, "javax/microedition/pki/Certificate", "getSubject", "()Ljava/lang/String;", certificate)); got != "CN=example.com" {
		t.Fatalf("certificate subject = %q", got)
	}
}

func TestMIDPSocketOptionsAndCommBaudRate(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	socket := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/Connector", "open", "(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0,
		ReferenceValue(vm.NewString("socket://127.0.0.1:9000"))))
	invokeTestNative(t, vm, "javax/microedition/io/SocketConnection", "setSocketOption", "(BI)V", socket, IntValue(4), IntValue(32768))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/io/SocketConnection", "getSocketOption", "(B)I", socket, IntValue(4))); got != 32768 {
		t.Fatalf("send buffer = %d", got)
	}
	if got := mustStringValue(t, vm, invokeTestNative(t, vm, "javax/microedition/io/SocketConnection", "getAddress", "()Ljava/lang/String;", socket)); got != "127.0.0.1" {
		t.Fatalf("remote address = %q", got)
	}
	comm := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/Connector", "open", "(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0,
		ReferenceValue(vm.NewString("comm:1;baudrate=19200"))))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/io/CommConnection", "getBaudRate", "()I", comm)); got != 19200 {
		t.Fatalf("baud rate = %d", got)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/io/CommConnection", "setBaudRate", "(I)I", comm, IntValue(57600))); got != 57600 {
		t.Fatalf("new baud rate = %d", got)
	}
}

func TestMIDPPushRegistryRoundTrip(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	connection := vm.NewString("socket://:1234")
	midlet := vm.NewString("game.Main")
	filter := vm.NewString("*")
	invokeTestNative(t, vm, "javax/microedition/io/PushRegistry", "registerConnection", "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)V", 0,
		ReferenceValue(connection), ReferenceValue(midlet), ReferenceValue(filter))
	if got := mustStringValue(t, vm, invokeTestNative(t, vm, "javax/microedition/io/PushRegistry", "getMIDlet", "(Ljava/lang/String;)Ljava/lang/String;", 0, ReferenceValue(connection))); got != "game.Main" {
		t.Fatalf("registered MIDlet = %q", got)
	}
	listed := mustReference(t, invokeTestNative(t, vm, "javax/microedition/io/PushRegistry", "listConnections", "(Z)[Ljava/lang/String;", 0, IntValue(0)))
	array, _ := vm.Object(listed)
	if array.Array == nil || len(array.Array.Elements) != 1 {
		t.Fatalf("listed connections = %#v", array.Array)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/io/PushRegistry", "unregisterConnection", "(Ljava/lang/String;)Z", 0, ReferenceValue(connection))); got != 1 {
		t.Fatalf("unregister = %d", got)
	}
}

func TestMIDletPlatformRequestAndLifecycle(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	midlet := vm.NewObject("javax/microedition/midlet/MIDlet", nil)
	invokeTestNative(t, vm, "javax/microedition/midlet/MIDlet", "notifyPaused", "()V", midlet)
	object, _ := vm.Object(midlet)
	if state, _ := object.Fields[midletLifecycleField].Int(); state != 1 {
		t.Fatalf("MIDlet lifecycle = %d", state)
	}
	invokeTestNative(t, vm, "javax/microedition/midlet/MIDlet", "resumeRequest", "()V", midlet)
	invokeTestNative(t, vm, "javax/microedition/midlet/MIDlet", "platformRequest", "(Ljava/lang/String;)Z", midlet, ReferenceValue(vm.NewString("https://example.com")))
	requests := vm.services.Device.Requests()
	if len(requests) != 1 || requests[0].Target != "https://example.com" {
		t.Fatalf("platform requests = %#v", requests)
	}
}
