package application

import (
	"context"
	"errors"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32MachineStopDispatchesEndEventOnce(t *testing.T) {
	machine := newGNEX32EndEventMachine(t, []byte{0x86, 0})
	if err := machine.Stop(); err != nil {
		t.Fatal(err)
	}
	if machine.state != machinecore.StateStopped || machine.runtime.PC() != 4 {
		t.Fatalf("stop state=%s end pc=%d", machine.state, machine.runtime.PC())
	}
	if err := machine.Stop(); err != nil {
		t.Fatal(err)
	}
	if machine.runtime.PC() != 4 {
		t.Fatalf("second stop redispatched end event: pc=%d", machine.runtime.PC())
	}
}

func TestGNEX32MachineResetPropagatesEndEventFault(t *testing.T) {
	machine := newGNEX32EndEventMachine(t, []byte{0xfe, 0})
	err := machine.Reset(context.Background())
	var unsupported *gnex32.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("reset end error = %v", err)
	}
	if machine.state != machinecore.StateFaulted {
		t.Fatalf("reset end state = %s", machine.state)
	}
}

func TestGNEX32MachineCloseCleansUpAfterEndEventFault(t *testing.T) {
	machine := newGNEX32EndEventMachine(t, []byte{0xfe, 0})
	err := machine.Close()
	var unsupported *gnex32.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("close end error = %v", err)
	}
	if !machine.closed || machine.state != machinecore.StateStopped {
		t.Fatalf("close flags: closed=%v state=%s", machine.closed, machine.state)
	}
	if machine.audioGeneration != 2 || !machine.audioGenerationPending {
		t.Fatalf("close audio generation = %d pending=%v",
			machine.audioGeneration, machine.audioGenerationPending)
	}
	if err := machine.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
}

func newGNEX32EndEventMachine(t *testing.T, end []byte) *gnex32Machine {
	t.Helper()
	code := append([]byte{0x86, 0}, end...)
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: code, Symbols: symbols,
		Callbacks: gnex.GNEX32Callbacks{Start: 160, End: 162},
	}
	runtime, err := gnex32.NewRuntime(image, gnex32VirusProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}
	machine := &gnex32Machine{
		state: machinecore.StateRunning, image: image, profile: gnex32VirusProfile,
		runtime: runtime, budget: 1, audioGeneration: 1,
		mediaOutputRevision: runtime.AudioOutputRevision(),
	}
	t.Cleanup(func() { _ = machine.Close() })
	return machine
}
