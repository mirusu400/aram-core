package skvm

import (
	"context"
	"testing"
)

func TestLGTOfflineHTTPInputFailsAtConnection(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	connectionValue := invokeTestNative(
		t, vm, "javax/microedition/io/Connector", "open",
		"(Ljava/lang/String;)Ljavax/microedition/io/Connection;", 0,
		ReferenceValue(vm.NewString("http://example.invalid/game")),
	)
	connection, err := connectionValue.Reference()
	check(t, err)
	open := vm.natives[nativeKey{
		class: "javax/microedition/io/HttpConnection",
		name:  "openInputStream", descriptor: "()Ljava/io/InputStream;",
	}]
	if open == nil {
		t.Fatal("HTTP input native is unavailable")
	}
	stream, _, err := open(context.Background(), vm, connection, nil)
	check(t, err)
	streamReference, err := stream.Reference()
	check(t, err)
	read := vm.natives[nativeKey{
		class: "java/io/InputStream", name: "read", descriptor: "()I",
	}]
	_, _, err = read(context.Background(), vm, streamReference, nil)
	requireXCEIOException(t, vm, err)

	// An attached network may supply a response through the normal service path.
	vm.services.Device.SetNetworkAvailable(true)
	stream, _, err = read(context.Background(), vm, streamReference, nil)
	check(t, err)
	if value, valueErr := stream.Int(); valueErr != nil || value != -1 {
		t.Fatalf("online empty HTTP read = %d, %v", value, valueErr)
	}
}
