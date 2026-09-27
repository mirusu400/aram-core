package ktf

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

func TestKTFLocalSocketStreamsAndSavedRequest(t *testing.T) {
	r := newTestRuntime(t)
	address, err := r.NewJavaString("socket://222.237.78.175:40240")
	check(t, err)
	socket, err := r.newOfflineMSFSocket(address)
	check(t, err)
	params := allocWords(t, r, 4)
	call := func(receiver uint32, args ...uint32) {
		t.Helper()
		check(t, r.writeWords(params, append([]uint32{receiver}, args...)))
		r.NativeParameterBase = params
	}
	call(socket)
	input, err := r.handleMSFSocketMethod("getInputStream", "()Ljava/io/InputStream;")
	check(t, err)
	output, err := r.handleMSFSocketMethod("getOutputStream", "()Ljava/io/OutputStream;")
	check(t, err)
	request, err := r.newJavaByteArray([]byte{0, 0, 0, 3, 'I', 'R', 9})
	check(t, err)
	call(output, request, 0, 2)
	_, err = r.handleOutputStreamMethod("write", "([BII)V")
	check(t, err)
	r.NativeParameterBase = 0
	var savedBytes bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&savedBytes)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(savedBytes.Bytes())}
	saved, err := ParseState(r, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	call(output, request, 2, 5)
	_, err = r.handleOutputStreamMethod("write", "([BII)V")
	check(t, err)
	buffer, err := r.newJavaByteArray(make([]byte, 32))
	check(t, err)
	call(input, buffer)
	n, err := r.handleInputStreamMethod(context.Background(), "read", "([B)I")
	check(t, err)
	got, err := r.readJavaByteArray(buffer)
	check(t, err)
	want := []byte{0, 0, 0, 12, 'I', 'R', 'O', 'K', 0, 0, 0, 0, 0, 0, 0, 0}
	if n != uint32(len(want)) || !bytes.Equal(got[:len(want)], want) {
		t.Fatalf("socket read = %d, %x", int32(n), got)
	}
	call(socket)
	_, err = r.handleMSFSocketMethod("close", "()V")
	check(t, err)
	call(output, request)
	if _, err = r.handleOutputStreamMethod("write", "([B)V"); err == nil {
		t.Fatal("closed socket accepted write")
	}
	r.NativeParameterBase = 0
	savedBytes.Reset()
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&savedBytes)))
}

func TestKTFLocalSocketLeavesOtherEndpointsOffline(t *testing.T) {
	for _, rawURL := range []string{"socket://127.0.0.1:40240", "socket://222.237.78.175:40241", "http://222.237.78.175:40240", "%"} {
		r := newTestRuntime(t)
		address, err := r.NewJavaString(rawURL)
		check(t, err)
		_, err = r.newOfflineMSFSocket(address)
		check(t, err)
		if len(r.socketServices) != 0 || len(r.Services.Network.Snapshot().Sockets) != 0 {
			t.Fatalf("unexpected local socket for %q", rawURL)
		}
	}
}
