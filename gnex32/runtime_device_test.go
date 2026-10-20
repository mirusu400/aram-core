package gnex32

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestBackToBrowserQueuesHostRequestAndHaltsGuest(t *testing.T) {
	const target = "https://example.invalid/game"
	runtime := newDeviceServiceRuntime(t, []byte{
		0x06, 0x00, 0x00, 0x00, // pushmi media 0
		0x92, 0x00, 0x07, 0x00, // BackToBrowser
		0xff, 0x00, // must not execute
	}, []gnex.GNEX32Media{{Data: append([]byte(target), 0, 'X')}})
	defer runtime.Close()

	if err := runtime.RunMain(10); err != nil {
		t.Fatal(err)
	}
	if !runtime.vm.Halted() || runtime.vm.PC() != 8 || runtime.vm.StackDepth() != 0 {
		t.Fatalf("BackToBrowser control state: halted=%v pc=%d stack=%v", runtime.vm.Halted(), runtime.vm.PC(), runtime.vm.stack)
	}
	requests := runtime.ExternalRequests()
	if len(requests) != 1 || requests[0].Sequence != 1 || requests[0].Owner != runtime.owner ||
		requests[0].Kind != shared.RequestBrowser || requests[0].Target != target ||
		requests[0].AtNS != 0 || len(requests[0].Data) != 0 {
		t.Fatalf("browser requests = %+v", requests)
	}
	if !runtime.TakeCloseRequest() || runtime.TakeCloseRequest() {
		t.Fatal("BackToBrowser close request was not consumed exactly once")
	}
	runtime.ClearExternalRequests(requests[0].Sequence)
	if requests := runtime.ExternalRequests(); len(requests) != 0 {
		t.Fatalf("acknowledged browser requests remain: %+v", requests)
	}
}

func TestBackToBrowserValidatesURLBeforeChangingState(t *testing.T) {
	tooLong := append(bytes.Repeat([]byte{'a'}, 256), 0)
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Data: []byte{0}},
		{Data: tooLong},
	})
	defer runtime.Close()

	for _, index := range []uint32{0, 1} {
		if _, err := runtime.service(0x07, []uint32{index}); err == nil {
			t.Fatalf("BackToBrowser accepted media %d", index)
		}
	}
	if _, err := runtime.service(0x07, nil); !errors.Is(err, ErrStackUnderflow) {
		t.Fatalf("BackToBrowser empty stack error = %v", err)
	}
	if runtime.TakeCloseRequest() || len(runtime.ExternalRequests()) != 0 {
		t.Fatal("invalid BackToBrowser call changed device state")
	}
}

func TestStartAndStopVibUseSecondsOnGuestClock(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	defer runtime.Close()

	result, err := runtime.service(0x8e, []uint32{2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 1 || len(result.Push) != 0 || result.Halt {
		t.Fatalf("StartVib stack result = %+v", result)
	}
	if level, until := runtime.Vibration(); level != 100 || until != 2*time.Second {
		t.Fatalf("StartVib state = (%d, %v)", level, until)
	}

	result, err = runtime.service(0x8f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 0 || len(result.Push) != 0 || result.Halt {
		t.Fatalf("StopVib stack result = %+v", result)
	}
	if level, until := runtime.Vibration(); level != 0 || until != 0 {
		t.Fatalf("StopVib state = (%d, %v)", level, until)
	}

	if _, err := runtime.service(0x8e, []uint32{^uint32(0)}); err == nil {
		t.Fatal("StartVib accepted a negative duration")
	}
	if _, err := runtime.service(0x8e, nil); !errors.Is(err, ErrStackUnderflow) {
		t.Fatalf("StartVib empty stack error = %v", err)
	}
	if level, until := runtime.Vibration(); level != 0 || until != 0 {
		t.Fatal("failed StartVib changed vibration state")
	}
}

func TestVibrationAndBrowserExitRoundTripInRuntimeState(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{{Data: []byte("https://example.invalid/\x00")}})
	defer runtime.Close()
	if _, err := runtime.service(0x8e, []uint32{3}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.service(0x07, []uint32{0}); err != nil {
		t.Fatal(err)
	}
	encoded, err := runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored := newStringRuntime(t, []gnex.GNEX32Media{{Data: []byte("https://example.invalid/\x00")}})
	defer restored.Close()
	if err := restored.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	if level, until := restored.Vibration(); level != 100 || until != 3*time.Second {
		t.Fatalf("restored vibration = (%d, %v)", level, until)
	}
	if !restored.TakeCloseRequest() {
		t.Fatal("restored runtime lost close request")
	}
	requests := restored.ExternalRequests()
	if len(requests) != 1 || requests[0].Kind != shared.RequestBrowser {
		t.Fatalf("restored browser requests = %+v", requests)
	}
}

func newDeviceServiceRuntime(t *testing.T, code []byte, media []gnex.GNEX32Media) *Runtime {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 10)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: code, Symbols: symbols, Media: media,
	}, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}
