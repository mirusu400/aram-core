package application

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32BackToBrowserStopsMachineAndExposesOutbox(t *testing.T) {
	const target = "https://example.invalid/game"
	machine := newGNEX32DeviceMachine(t, []byte{
		0x05, 0x00, 0x02, 0x00,
		0x92, 0x00, 0x8e, 0x00,
		0x06, 0x00, 0x00, 0x00,
		0x92, 0x00, 0x07, 0x00,
		0xff, 0x00,
	}, []gnex.GNEX32Media{{Data: append([]byte(target), 0)}})
	defer machine.Close()

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped {
		t.Fatalf("machine state = %s, want stopped", machine.State())
	}
	if level, remaining := machine.Vibration(); level != 0 || remaining != 0 {
		t.Fatalf("browser-exit vibration = (%d, %v)", level, remaining)
	}
	requests := machine.ExternalRequests()
	if len(requests) != 1 || requests[0].Kind != machinecore.ExternalRequestBrowser || requests[0].Target != target {
		t.Fatalf("host browser requests = %+v", requests)
	}
	machine.ClearExternalRequests(requests[0].Sequence)
	if requests := machine.ExternalRequests(); len(requests) != 0 {
		t.Fatalf("cleared browser requests = %+v", requests)
	}
}

func TestGNEX32CloseHidesUnacknowledgedExternalRequests(t *testing.T) {
	const target = "https://example.invalid/closed"
	machine := newGNEX32DeviceMachine(t, []byte{
		0x05, 0x00, 0x00, 0x00,
		0x92, 0x00, 0x07, 0x00,
		0xff, 0x00,
	}, []gnex.GNEX32Media{{Data: append([]byte(target), 0)}})
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests := machine.ExternalRequests()
	if len(requests) != 1 {
		t.Fatalf("browser requests before close = %+v", requests)
	}
	if err := machine.Close(); err != nil {
		t.Fatal(err)
	}
	if requests := machine.ExternalRequests(); len(requests) != 0 {
		t.Fatalf("closed machine exposed stale browser requests: %+v", requests)
	}
	if len(machine.requestIDs.hostByGuest) != 0 {
		t.Fatalf("closed machine retained %d request IDs", len(machine.requestIDs.hostByGuest))
	}
	machine.ClearExternalRequests(requests[0].Sequence)
}

func TestGNEX32FailedTimelineChangesPreserveExternalRequestToken(t *testing.T) {
	const target = "https://example.invalid/failure"
	newMachine := func(t *testing.T) *gnex32Machine {
		t.Helper()
		return newGNEX32DeviceMachine(t, []byte{
			0x05, 0x00, 0x00, 0x00,
			0x92, 0x00, 0x07, 0x00,
			0xff, 0x00,
		}, []gnex.GNEX32Media{{Data: append([]byte(target), 0)}})
	}
	for _, test := range []struct {
		name   string
		change func(*gnex32Machine) error
	}{
		{
			name: "canceled reset",
			change: func(machine *gnex32Machine) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return machine.Reset(ctx)
			},
		},
		{
			name: "invalid load state",
			change: func(machine *gnex32Machine) error {
				return machine.LoadState(strings.NewReader("not a save state"))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			machine := newMachine(t)
			defer machine.Close()
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			before := machine.ExternalRequests()
			if len(before) != 1 {
				t.Fatalf("requests before failed change = %+v", before)
			}
			if err := test.change(machine); err == nil {
				t.Fatal("timeline change unexpectedly succeeded")
			}
			after := machine.ExternalRequests()
			if len(after) != 1 || after[0].Sequence != before[0].Sequence {
				t.Fatalf("failed change replaced request token: before=%+v after=%+v", before, after)
			}
			machine.ClearExternalRequests(before[0].Sequence)
			if requests := machine.ExternalRequests(); len(requests) != 0 {
				t.Fatalf("preserved token did not ACK request: %+v", requests)
			}
		})
	}
}

func TestGNEX32ResetRejectsACKFromPreviousRequestSession(t *testing.T) {
	const target = "https://example.invalid/reset"
	machine := newGNEX32DeviceMachine(t, []byte{
		0x05, 0x00, 0x00, 0x00,
		0x92, 0x00, 0x07, 0x00,
		0xff, 0x00,
	}, []gnex.GNEX32Media{{Data: append([]byte(target), 0)}})
	defer machine.Close()

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := machine.ExternalRequests()
	if len(first) != 1 {
		t.Fatalf("first-session requests = %+v", first)
	}
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := machine.ExternalRequests()
	if len(second) != 1 || second[0].Sequence <= first[0].Sequence {
		t.Fatalf("second-session requests = %+v after %+v", second, first)
	}

	machine.ClearExternalRequests(first[0].Sequence)
	remaining := machine.ExternalRequests()
	if len(remaining) != 1 || remaining[0].Sequence != second[0].Sequence {
		t.Fatalf("stale ACK consumed current request: %+v", remaining)
	}
	machine.ClearExternalRequests(second[0].Sequence)
	if remaining := machine.ExternalRequests(); len(remaining) != 0 {
		t.Fatalf("current request remained after ACK: %+v", remaining)
	}
}

func TestGNEX32LoadStateRejectsACKFromPreviousRequestSession(t *testing.T) {
	const target = "https://example.invalid/load-state"
	machine := newGNEX32DeviceMachine(t, []byte{
		0x05, 0x00, 0x00, 0x00,
		0x92, 0x00, 0x07, 0x00,
		0xff, 0x00,
	}, []gnex.GNEX32Media{{Data: append([]byte(target), 0)}})
	defer machine.Close()
	machine.source.SHA256 = strings.Repeat("a", 64)

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := machine.ExternalRequests()
	if len(first) != 1 {
		t.Fatalf("saved-session requests = %+v", first)
	}
	var state bytes.Buffer
	if err := machine.SaveState(&state); err != nil {
		t.Fatal(err)
	}
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	intermediate := machine.ExternalRequests()
	if len(intermediate) != 1 || intermediate[0].Sequence <= first[0].Sequence {
		t.Fatalf("intermediate-session requests = %+v after %+v", intermediate, first)
	}
	if err := machine.LoadState(bytes.NewReader(state.Bytes())); err != nil {
		t.Fatal(err)
	}
	restored := machine.ExternalRequests()
	if len(restored) != 1 || restored[0].Sequence <= intermediate[0].Sequence {
		t.Fatalf("restored-session requests = %+v after %+v", restored, intermediate)
	}

	machine.ClearExternalRequests(intermediate[0].Sequence)
	remaining := machine.ExternalRequests()
	if len(remaining) != 1 || remaining[0].Sequence != restored[0].Sequence {
		t.Fatalf("pre-load ACK consumed restored request: %+v", remaining)
	}
	machine.ClearExternalRequests(restored[0].Sequence)
	if remaining := machine.ExternalRequests(); len(remaining) != 0 {
		t.Fatalf("restored request remained after ACK: %+v", remaining)
	}
}

func TestGNEX32MachineExposesVibration(t *testing.T) {
	machine := newGNEX32DeviceMachine(t, []byte{
		0x05, 0x00, 0x02, 0x00,
		0x92, 0x00, 0x8e, 0x00,
		0xff, 0x00,
	}, nil)
	defer machine.Close()

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if level, remaining := machine.Vibration(); level != 100 || remaining != 2*time.Second {
		t.Fatalf("host vibration = (%d, %v)", level, remaining)
	}
	if err := machine.Stop(); err != nil {
		t.Fatal(err)
	}
	if level, remaining := machine.Vibration(); level != 0 || remaining != 0 {
		t.Fatalf("stopped host vibration = (%d, %v)", level, remaining)
	}
}

func TestGNEX32ExitDispatchesEndBeforeStoppingMachine(t *testing.T) {
	const endOffset = 6
	machine := newGNEX32DeviceMachineForImage(t, gnex.GNEX32Image{
		Header:    gnex.Header{BodyOffset: 160},
		Entry:     160,
		Callbacks: gnex.GNEX32Callbacks{End: 160 + endOffset},
		Code: []byte{
			0x92, 0x00, 0xe1, 0x00, // Exit
			0xff, 0x00, // must not execute
			0x75, 0x00, 0x06, 0x00, 0x2a, 0x00, // EVENT_END: symbol 6 = 42
			0xff, 0x00,
		},
		Symbols: gnex32DeviceTestSymbols(),
	})
	defer machine.Close()
	machine.EnableGNEX32ExecutionTrace()

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped {
		t.Fatalf("machine state = %s, want stopped", machine.State())
	}
	trace := machine.GNEX32ExecutionTrace()
	for _, opcode := range trace.Opcodes {
		for _, offset := range opcode.Offsets {
			if offset == endOffset {
				return
			}
		}
	}
	t.Fatalf("EVENT_END offset %d was not executed: %+v", endOffset, trace.Opcodes)
}

func newGNEX32DeviceMachine(t *testing.T, code []byte, media []gnex.GNEX32Media) *gnex32Machine {
	t.Helper()
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: code, Symbols: gnex32DeviceTestSymbols(), Media: media,
	}
	return newGNEX32DeviceMachineForImage(t, image)
}

func newGNEX32DeviceMachineForImage(t *testing.T, image gnex.GNEX32Image) *gnex32Machine {
	t.Helper()
	profile := gnex32.DeviceProfile{
		ID: "gnex32/test/device", ScreenWidth: 120, ScreenHeight: 149,
		Identity:       gnex32.Identity{MIN: "0111234567", UserID: "TEST"},
		Platform:       "WIPI 1.2",
		KeyToneEnabled: true, MMIBacklightEnabled: true,
	}
	runtime, err := gnex32.NewRuntime(image, profile)
	if err != nil {
		t.Fatal(err)
	}
	return &gnex32Machine{
		state: machinecore.StateReady, image: image, profile: profile,
		runtime: runtime, budget: 100, audioGeneration: 1,
		mediaOutputRevision: runtime.AudioOutputRevision(),
	}
}

func gnex32DeviceTestSymbols() []gnex.GNEX32Symbol {
	symbols := make([]gnex.GNEX32Symbol, 10)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	return symbols
}
