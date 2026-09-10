package ktf

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestKTFJavaCallPlacesExternalRequestAndEnds(t *testing.T) {
	runtime := newTestRuntime(t)
	number := newJavaString(t, runtime, "01012345678")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, number))
	_, err := runtime.handleCallMethod("place", "(Ljava/lang/String;)V")
	check(t, err)
	requests := runtime.Services.Device.Requests()
	if len(requests) != 1 || requests[0].Kind != shared.RequestPhone ||
		requests[0].Target != "01012345678" ||
		runtime.javaCall.state != ktfCallCalling ||
		runtime.javaCall.requestSequence != requests[0].Sequence {
		t.Fatalf("placed call = requests %+v, state %+v", requests, runtime.javaCall)
	}

	_, err = runtime.handleCallMethod("place0", "(Ljava/lang/String;)V")
	if err == nil || runtime.LastJavaThrowName != "java/io/IOException" {
		t.Fatalf("second place error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
	_, err = runtime.handleCallMethod("end0", "()V")
	check(t, err)
	if runtime.javaCall.state != ktfCallEnded {
		t.Fatalf("ended call state = %d", runtime.javaCall.state)
	}
	if len(runtime.eventQueueEvents) != 1 ||
		runtime.eventQueueEvents[0] != (ktfJavaEvent{7, 1, 0, 0}) {
		t.Fatalf("call end events = %+v", runtime.eventQueueEvents)
	}
}

func TestKTFJavaIncomingCallCanBeAcceptedOrRejected(t *testing.T) {
	runtime := newTestRuntime(t)
	queued, err := runtime.QueueIncomingCall("021234567")
	check(t, err)
	if !queued || runtime.javaCall.state != ktfCallIncoming ||
		len(runtime.eventQueueEvents) != 1 {
		t.Fatalf("incoming call = queued %v state %+v events %+v", queued, runtime.javaCall, runtime.eventQueueEvents)
	}
	event := runtime.eventQueueEvents[0]
	if event[0] != 7 || event[1] != 0 ||
		runtime.javaStringValue(event[2]) != "021234567" {
		t.Fatalf("incoming call event = %+v", event)
	}
	_, err = runtime.handleCallMethod("accept", "()V")
	check(t, err)
	if runtime.javaCall.state != ktfCallConnected {
		t.Fatalf("accepted call state = %d", runtime.javaCall.state)
	}
	_, err = runtime.handleCallMethod("end", "()V")
	check(t, err)

	runtime.eventQueueEvents = nil
	queued, err = runtime.QueueIncomingCall("0317654321")
	check(t, err)
	if !queued {
		t.Fatal("second incoming call was not queued")
	}
	_, err = runtime.handleCallMethod("reject0", "()V")
	check(t, err)
	if runtime.javaCall.state != ktfCallRejected ||
		len(runtime.eventQueueEvents) != 2 ||
		runtime.eventQueueEvents[1] != (ktfJavaEvent{7, 1, 0, 0}) {
		t.Fatalf("rejected call state/events = %+v/%+v", runtime.javaCall, runtime.eventQueueEvents)
	}
}

func TestKTFJavaCallRejectsInvalidTransitions(t *testing.T) {
	runtime := newTestRuntime(t)
	for _, method := range []string{"accept", "reject", "end"} {
		_, err := runtime.handleCallMethod(method, "()V")
		if err == nil || runtime.LastJavaThrowName != "java/io/IOException" {
			t.Fatalf("idle %s error=%v exception=%q", method, err, runtime.LastJavaThrowName)
		}
	}
	if queued, err := runtime.QueueIncomingCall(""); err == nil || queued {
		t.Fatalf("empty incoming call = %v/%v", queued, err)
	}
}

func TestKTFJavaCallSecuresPPPOnlyWhenNetworkIsAvailable(t *testing.T) {
	runtime := newTestRuntime(t)
	_, err := runtime.handleCallMethod("securePPPSession", "()V")
	if err == nil || runtime.LastJavaThrowName != "java/io/IOException" ||
		runtime.javaCall.ppp {
		t.Fatalf("offline PPP = error %v, call %+v", err, runtime.javaCall)
	}
	check(t, runtime.Services.Device.SetStatus(90, 80, true))
	_, err = runtime.handleCallMethod("securePPPSession0", "()V")
	check(t, err)
	if !runtime.javaCall.ppp {
		t.Fatal("online PPP session was not secured")
	}
}

func TestKTFJavaCallHostSpecDeclaresDocumentedMethods(t *testing.T) {
	spec := HostJavaClassSpecs["org/kwis/msp/handset/Call"]
	want := map[string]bool{
		"securePPPSession()V":         false,
		"accept()V":                   false,
		"reject()V":                   false,
		"end()V":                      false,
		"place(Ljava/lang/String;)V":  false,
		"securePPPSession0()V":        false,
		"accept0()V":                  false,
		"reject0()V":                  false,
		"end0()V":                     false,
		"place0(Ljava/lang/String;)V": false,
	}
	for _, method := range spec.methods {
		key := method.name + method.descriptor
		if _, ok := want[key]; ok && method.access&0x0008 != 0 {
			want[key] = true
		}
	}
	for method, found := range want {
		if !found {
			t.Errorf("Call.%s is absent from the host spec", method)
		}
	}
}

func TestKTFJavaCallStateSurvivesSaveRestore(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.javaCall = ktfCall{
		state: ktfCallConnected, number: "01076543210",
		requestSequence: 42, ppp: true,
	}
	var buffer bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&buffer)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	runtime.javaCall = ktfCall{}
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	if runtime.javaCall != (ktfCall{
		state: ktfCallConnected, number: "01076543210",
		requestSequence: 42, ppp: true,
	}) {
		t.Fatalf("restored Java call = %+v", runtime.javaCall)
	}
}
