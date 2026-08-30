package raptor

import (
	"testing"

	"github.com/mirusu400/aram-core/netauth"
)

// TestServiceAuthCompletionDeliversTheCarrierResponse pins how the runtime
// emulates the LGT carrier's asynchronous DRM/server response: a backend arms a
// completion, and after its delay the runtime posts it to the clet's
// HandleEvent with the payload written to a reused guest buffer. LGT titles
// block on "접속중"/"서버 접속중" until this event arrives.
func TestServiceAuthCompletionDeliversTheCarrierResponse(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	runtime.Clet.HandleEvent = 0x00001665

	runtime.armAuthCompletion(&netauth.Completion{
		Event:       1800,
		Arg1:        7,
		Response:    []byte{1, 2, 3, 4},
		DelayFrames: 3,
	})

	// The event waits out its delay before it is posted.
	for frame := 0; frame < 2; frame++ {
		if err := runtime.ServiceAuthCompletion(); err != nil {
			t.Fatal(err)
		}
		if len(public.PendingCallbacks) != 0 {
			t.Fatalf("completion posted early on frame %d", frame)
		}
	}
	if err := runtime.ServiceAuthCompletion(); err != nil {
		t.Fatal(err)
	}
	if len(public.PendingCallbacks) != 1 {
		t.Fatalf("pending callbacks = %d, want 1", len(public.PendingCallbacks))
	}
	callback := public.PendingCallbacks[0]
	if callback.Procedure != runtime.Clet.HandleEvent {
		t.Fatalf("callback procedure = 0x%08x, want the clet HandleEvent", callback.Procedure)
	}
	if callback.Args[0] != 1800 || callback.Args[1] != 7 {
		t.Fatalf("callback args = %v, want event 1800 status 7", callback.Args)
	}
	if payload, err := public.ReadU32(callback.Args[2]); err != nil || payload != 0x04030201 {
		t.Fatalf("response buffer = 0x%08x (err %v), want the armed bytes", payload, err)
	}

	// A serviced completion is not delivered a second time.
	if err := runtime.ServiceAuthCompletion(); err != nil {
		t.Fatal(err)
	}
	if len(public.PendingCallbacks) != 1 {
		t.Fatalf("completion re-delivered: callbacks = %d", len(public.PendingCallbacks))
	}
}

// TestServiceAuthCompletionIsInertWithoutABackend keeps titles that never
// request the carrier handshake (or run with no auth backend) unaffected.
func TestServiceAuthCompletionIsInertWithoutABackend(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	runtime.Clet.HandleEvent = 0x00001665
	if err := runtime.ServiceAuthCompletion(); err != nil {
		t.Fatal(err)
	}
	if len(public.PendingCallbacks) != 0 {
		t.Fatalf("posted a completion with none armed: %d", len(public.PendingCallbacks))
	}
}
