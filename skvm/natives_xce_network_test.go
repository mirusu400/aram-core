package skvm

import "testing"

func TestXCEPPPCloseReleasesNetworkAfterRestore(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicySKT)
	socketValue := invokeTestNative(t, vm, "javax/microedition/io/Connector", "open",
		"(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0,
		ReferenceValue(vm.NewString("socket://127.0.0.1:7821")))
	socketRef, err := socketValue.Reference()
	check(t, err)
	httpValue := invokeTestNative(t, vm, "javax/microedition/io/Connector", "open",
		"(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0,
		ReferenceValue(vm.NewString("https://example.test/game")))
	httpRef, err := httpValue.Reference()
	check(t, err)
	_, err = vm.services.Network.OpenSocket(vm.serviceOwner, 2, 1) // interrupted constructor
	check(t, err)
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	for i := 0; i < 2; i++ {
		invokeTestNative(t, vm, "com/xce/net/Socket", "PPPClose", "()V", 0)
	}
	if state, err := vm.socketConnection(socketRef); err != nil || !state.closed || state.socket != 0 {
		t.Fatalf("socket after PPP close = %+v, %v", state, err)
	}
	httpObject, ok := vm.Object(httpRef)
	if !ok {
		t.Fatal("HTTP object disappeared after PPP close")
	}
	httpState, ok := httpObject.Native.(*httpConnectionState)
	if !ok || !httpState.closed || httpState.request != 0 {
		t.Fatalf("HTTP after PPP close = %+v", httpState)
	}
	if network := vm.services.Network.Snapshot(); len(network.Sockets) != 0 || len(network.HTTP) != 0 {
		t.Fatalf("network handles remain after PPP close: %d sockets, %d HTTP", len(network.Sockets), len(network.HTTP))
	}
	other := policyRegressionVM(t, NativePolicyJ2ME)
	if other.SupportsNativeReference("com/xce/net/Socket", "PPPClose", "()V") {
		t.Fatal("XCE PPP method leaked to generic J2ME")
	}
}
