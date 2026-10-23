package cheat

import (
	machinecore "github.com/mirusu400/aram-core/core"
	"testing"
	"time"
)

type pointerMachine struct {
	machinecore.Machine
	events     []machinecore.PointerInputEvent
	hostEvents []machinecore.HostEvent
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
