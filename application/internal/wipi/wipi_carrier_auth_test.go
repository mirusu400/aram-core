package wipi

import (
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

// TestAnswerOfflineCarrierAcknowledgesCarrierRequests pins the offline LGT
// carrier auth responder: an auth-gated title frames each request as
// [u32 len][u16 0xffff][u16 code][body] and blocks on its connecting screen
// until the carrier answers. With the servers shut down ARAM synthesizes that
// answer (a success reply carrying code+1, subtype 0) so the title proceeds.
func TestAnswerOfflineCarrierAcknowledgesCarrierRequests(t *testing.T) {
	r := &Runtime{}
	socket := &wipiSocket{descriptor: 3, readCallback: 0x1234, readParameter: 0x5678}
	// UserAuthentication request: length 0x30, marker 0xffff, code 0x2711.
	request := []byte{0x30, 0, 0, 0, 0xff, 0xff, 0x11, 0x27, 0x12, 0x00}
	r.answerOfflineCarrier(socket, request)

	want := []byte{9, 0, 0, 0, 0xff, 0xff, 0x12, 0x27, 0}
	if string(socket.readData) != string(want) {
		t.Fatalf("reply = % x, want % x", socket.readData, want)
	}
	// The registered read callback must fire so the title reads the reply.
	if socket.readCallback != 0 {
		t.Fatalf("read callback not consumed: 0x%x", socket.readCallback)
	}
	if len(r.PendingCallbacks) != 1 || r.PendingCallbacks[0].Procedure != 0x1234 ||
		r.PendingCallbacks[0].Args != [4]uint32{3, 0, 0x5678} {
		t.Fatalf("queued callback = %+v", r.PendingCallbacks)
	}
}

// TestAnswerOfflineCarrierIgnoresForeignFraming makes sure the responder never
// injects into a socket that is not speaking the carrier protocol, so ordinary
// socket traffic is untouched.
func TestAnswerOfflineCarrierIgnoresForeignFraming(t *testing.T) {
	r := &Runtime{}
	socket := &wipiSocket{descriptor: 1}
	for _, request := range [][]byte{
		{},                                // empty
		{1, 2, 3},                         // too short
		{0x10, 0, 0, 0, 0x00, 0x01, 5, 6}, // wrong marker
		{0x10, 0, 0, 0, 0xff, 0x00, 5, 6}, // half marker
	} {
		r.answerOfflineCarrier(socket, request)
	}
	if len(socket.readData) != 0 {
		t.Fatalf("foreign framing produced a reply: % x", socket.readData)
	}
	if len(r.PendingCallbacks) != 0 {
		t.Fatalf("foreign framing queued a callback: %+v", r.PendingCallbacks)
	}
}

// TestAnswerOfflineCarrierAnswersTextCarrierAuth covers the plaintext /
// length-prefixed carrier handshakes some LGT titles use instead of the 0xffff
// framing. Their success payload is server-defined and the servers are gone, so
// the responder echoes the request as a minimal acknowledgement — but only for
// requests that match a known carrier-auth signature, never ordinary traffic.
func TestAnswerOfflineCarrierAnswersTextCarrierAuth(t *testing.T) {
	// ENSLGT plaintext handshake -> echoed acknowledgement + woken callback.
	r := &Runtime{}
	socket := &wipiSocket{descriptor: 4, readCallback: 0xabc, readParameter: 0xde}
	req := []byte("ENSLGT 01000000000")
	r.answerOfflineCarrier(socket, req)
	if string(socket.readData) != string(req) {
		t.Fatalf("ENSLGT reply = % x, want echo % x", socket.readData, req)
	}
	if socket.readCallback != 0 || len(r.PendingCallbacks) != 1 {
		t.Fatalf("ENSLGT callback not fired: cb=0x%x pending=%d", socket.readCallback, len(r.PendingCallbacks))
	}

	// The 28-byte binary carrier frame (14 00 01 00 ...) is answered.
	r2 := &Runtime{}
	s2 := &wipiSocket{descriptor: 5}
	bin := make([]byte, 28)
	bin[0], bin[2] = 0x14, 0x01
	r2.answerOfflineCarrier(s2, bin)
	if len(s2.readData) != 28 {
		t.Fatalf("binary carrier reply len = %d, want 28", len(s2.readData))
	}

	// A 28-byte frame without the carrier signature is left alone.
	r3 := &Runtime{}
	s3 := &wipiSocket{descriptor: 6}
	other := make([]byte, 28)
	other[0] = 0x99
	r3.answerOfflineCarrier(s3, other)
	if len(s3.readData) != 0 {
		t.Fatalf("non-carrier 28-byte frame produced a reply: % x", s3.readData)
	}
}

// TestDeliverSocketReadWakesAWaitingReadCallback covers the async read path: a
// title that arms MC_netSetReadCB and waits for a server reply must be woken
// when bytes arrive after registration, not only when they were already
// buffered.
func TestDeliverSocketReadWakesAWaitingReadCallback(t *testing.T) {
	r := &Runtime{
		sockets:        map[int32]*wipiSocket{7: {descriptor: 7, readCallback: 0x900, readParameter: 0x5}},
		socketServices: map[int32]shared.ServiceID{7: 42},
	}
	if !r.DeliverSocketRead(42) {
		t.Fatal("DeliverSocketRead(42) = false, want a fired callback")
	}
	if got := r.sockets[7].readCallback; got != 0 {
		t.Fatalf("read callback not consumed: 0x%x", got)
	}
	if len(r.PendingCallbacks) != 1 || r.PendingCallbacks[0].Procedure != 0x900 ||
		r.PendingCallbacks[0].Args != [4]uint32{7, 0, 0x5} {
		t.Fatalf("queued callback = %+v", r.PendingCallbacks)
	}
	// A service id with no armed callback fires nothing.
	if r.DeliverSocketRead(42) {
		t.Fatal("second DeliverSocketRead fired without an armed callback")
	}
}
