package application

import (
	"context"
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

func newGNEX32DeviceMachine(t *testing.T, code []byte, media []gnex.GNEX32Media) *gnex32Machine {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 10)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: code, Symbols: symbols, Media: media,
	}
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
