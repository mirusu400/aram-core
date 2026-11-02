package cheat

import (
	"context"
	machinecore "github.com/mirusu400/aram-core/core"
	"testing"
	"time"
)

type pointerMachine struct {
	machinecore.Machine
	events     []machinecore.PointerInputEvent
	hostEvents []machinecore.HostEvent
	requests   []machinecore.ExternalRequest
	readStart  chan struct{}
	readFinish chan struct{}
}

func (m *pointerMachine) SupportsPointer() bool { return true }
func (m *pointerMachine) QueuePointer(e machinecore.PointerInputEvent) error {
	m.events = append(m.events, e)
	return nil
}
func (m *pointerMachine) ReceiveHostEvent(e machinecore.HostEvent) error {
	m.hostEvents = append(m.hostEvents, e)
	return nil
}
func (m *pointerMachine) ExternalRequests() []machinecore.ExternalRequest {
	if m.readStart != nil {
		close(m.readStart)
		<-m.readFinish
	}
	// Deliberately return retained storage. The wrapper is responsible for
	// protecting callers even when an optional implementation is this lax.
	return m.requests
}
func (m *pointerMachine) ClearExternalRequests(through uint64) {
	for len(m.requests) != 0 && m.requests[0].Sequence <= through {
		m.requests = m.requests[1:]
	}
}
func TestMachineForwardsOptionalPointerInput(t *testing.T) {
	base := &pointerMachine{}
	wrapper := &Machine{machine: base, engine: &Engine{}}
	if !wrapper.SupportsPointer() {
		t.Fatal("wrapper hid pointer support")
	}
	event := machinecore.PointerInputEvent{X: 31, Y: 47, Pressed: true, At: time.Second}
	check(t, wrapper.QueuePointer(event))
	if len(base.events) != 1 || base.events[0] != event {
		t.Fatalf("input=%v", base.events)
	}
	wrapper.machine = &mutatingMachine{}
	if wrapper.SupportsPointer() || wrapper.QueuePointer(event) == nil {
		t.Fatal("keypad-only target accepted pointer")
	}
}

func TestMachineForwardsOptionalHostEvent(t *testing.T) {
	base := &pointerMachine{}
	wrapper := &Machine{machine: base, engine: &Engine{}}
	event := machinecore.HostEvent{
		Kind:  machinecore.HostEventSMSReceive,
		Words: [4]uint32{1, 2, 3, 4},
	}
	check(t, wrapper.ReceiveHostEvent(event))
	if len(base.hostEvents) != 1 ||
		base.hostEvents[0].Kind != event.Kind ||
		base.hostEvents[0].Words != event.Words ||
		len(base.hostEvents[0].Data) != 0 {
		t.Fatalf("host events = %v", base.hostEvents)
	}
	wrapper.machine = &mutatingMachine{}
	if wrapper.ReceiveHostEvent(event) == nil {
		t.Fatal("target without host ingress accepted event")
	}
}

func TestMachineForwardsExternalRequestOutbox(t *testing.T) {
	base := &pointerMachine{requests: []machinecore.ExternalRequest{{
		Sequence: 4,
		Kind:     machinecore.ExternalRequestBrowser,
		Target:   "https://example.invalid/game",
		Data:     []byte{1, 2, 3},
	}}}
	wrapper := &Machine{machine: base, engine: &Engine{}}
	requests := wrapper.ExternalRequests()
	if len(requests) != 1 ||
		requests[0].Sequence != base.requests[0].Sequence ||
		requests[0].Kind != base.requests[0].Kind ||
		requests[0].Target != base.requests[0].Target {
		t.Fatalf("external requests = %+v", requests)
	}
	requests[0].Data[0] = 0xff
	requests[0].Target = "mutated"
	if base.requests[0].Data[0] != 1 {
		t.Fatalf("external request data aliases wrapped machine: %v", base.requests[0].Data)
	}
	if base.requests[0].Target != "https://example.invalid/game" {
		t.Fatalf("external request slice aliases wrapped machine: %+v", base.requests[0])
	}
	wrapper.ClearExternalRequests(4)
	if len(base.requests) != 0 || len(wrapper.ExternalRequests()) != 0 {
		t.Fatalf("external request was not acknowledged: %+v", base.requests)
	}

	wrapper.machine = &mutatingMachine{}
	if requests := wrapper.ExternalRequests(); len(requests) != 0 {
		t.Fatalf("machine without an outbox exposed requests: %+v", requests)
	}
	wrapper.ClearExternalRequests(4)
}

func TestMachineSerializesExternalRequestSnapshotWithGuestExecution(t *testing.T) {
	readStart := make(chan struct{})
	readFinish := make(chan struct{})
	base := &pointerMachine{
		requests:   []machinecore.ExternalRequest{{Sequence: 1}},
		readStart:  readStart,
		readFinish: readFinish,
	}
	wrapper := &Machine{machine: base, engine: &Engine{}}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_ = wrapper.ExternalRequests()
	}()
	<-readStart

	executionStarted := make(chan struct{})
	executionAttempted := make(chan struct{})
	executionDone := make(chan struct{})
	go func() {
		defer close(executionDone)
		close(executionAttempted)
		_ = wrapper.engine.runMachine(func() error {
			close(executionStarted)
			return context.Canceled
		}, applyNone)
	}()
	<-executionAttempted
	select {
	case <-executionStarted:
		t.Fatal("guest execution overlapped external request snapshot")
	case <-time.After(25 * time.Millisecond):
	}
	close(readFinish)
	<-readDone
	<-executionStarted
	<-executionDone
}

func TestMachineSerializesExternalRequestSnapshotWithACK(t *testing.T) {
	readStart := make(chan struct{})
	readFinish := make(chan struct{})
	base := &pointerMachine{
		requests:   []machinecore.ExternalRequest{{Sequence: 1}},
		readStart:  readStart,
		readFinish: readFinish,
	}
	wrapper := &Machine{machine: base, engine: &Engine{}}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_ = wrapper.ExternalRequests()
	}()
	<-readStart

	ackAttempted := make(chan struct{})
	ackDone := make(chan struct{})
	go func() {
		defer close(ackDone)
		close(ackAttempted)
		wrapper.ClearExternalRequests(1)
	}()
	<-ackAttempted
	select {
	case <-ackDone:
		t.Fatal("external request ACK overlapped snapshot")
	case <-time.After(25 * time.Millisecond):
	}
	if len(base.requests) != 1 {
		t.Fatalf("ACK mutated request during snapshot: %+v", base.requests)
	}
	close(readFinish)
	<-readDone
	<-ackDone
	if len(base.requests) != 0 {
		t.Fatalf("serialized ACK did not clear request: %+v", base.requests)
	}
}
