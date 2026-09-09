package skvm

import "testing"

func TestCLDCDatagramSendUsesModeledNetwork(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	name := vm.NewString("datagram://127.0.0.1:9000")
	connectionValue := invokeTestNative(t, vm, "javax/microedition/io/Connector", "open", "(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0, ReferenceValue(name))
	connection, err := connectionValue.Reference()
	check(t, err)
	payload := vm.NewByteArray([]byte("packet"))
	datagramValue := invokeTestNative(t, vm, "javax/microedition/io/DatagramConnection", "newDatagram", "([BI)Ljavax/microedition/io/Datagram;", connection, ReferenceValue(payload), IntValue(6))
	datagram, err := datagramValue.Reference()
	check(t, err)
	invokeTestNative(t, vm, "javax/microedition/io/DatagramConnection", "send", "(Ljavax/microedition/io/Datagram;)V", connection, ReferenceValue(datagram))
	state, err := vm.openSocketConnection(connection)
	check(t, err)
	written, err := vm.services.Network.SocketWritten(vm.serviceOwner, state.socket)
	check(t, err)
	if string(written) != "packet" {
		t.Fatalf("datagram payload = %q", written)
	}
}
