package application

import (
	"context"
	"errors"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32MachineReceivesBackendNeutralHostEvent(t *testing.T) {
	const callbackOffset = 2
	machine := newGNEX32DeviceMachineForImage(t, gnex.GNEX32Image{
		Header:    gnex.Header{BodyOffset: 160},
		Entry:     160,
		Callbacks: gnex.GNEX32Callbacks{SMSReceive: 160 + callbackOffset},
		Code: []byte{
			0x86, 0x00, // main return
			0x86, 0x00, // SMS callback return
		},
		Symbols: gnex32DeviceTestSymbols(),
	})
	defer machine.Close()
	machine.EnableGNEX32ExecutionTrace()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var receiver machinecore.HostEventReceiver = machine
	if err := receiver.ReceiveHostEvent(machinecore.HostEvent{
		Kind:  machinecore.HostEventSMSReceive,
		Words: [4]uint32{0x11, 0x22, 0x33, 0x44},
	}); err != nil {
		t.Fatal(err)
	}
	trace := machine.GNEX32ExecutionTrace()
	for _, opcode := range trace.Opcodes {
		for _, offset := range opcode.Offsets {
			if offset == callbackOffset {
				return
			}
		}
	}
	t.Fatalf("SMS callback offset %d was not executed: %+v", callbackOffset, trace.Opcodes)
}

func TestGNEX32NetworkProviderMistakeDoesNotFaultGuest(t *testing.T) {
	machine := newGNEX32DeviceMachine(t, []byte{0x86, 0x00}, nil)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := machine.ReceiveHostEvent(machinecore.HostEvent{
		Kind: machinecore.HostEventNetworkReceive,
		Data: []byte("packet-before-connect"),
	})
	if !errors.Is(err, gnex32.ErrNetworkReceiveUnavailable) {
		t.Fatalf("receive error = %v", err)
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatalf("machine state = %s, want running", machine.State())
	}
}
