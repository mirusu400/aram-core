package gnex32

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32RuntimeDispatchesEveryNamedEventWithFourSystemValues(t *testing.T) {
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	callbacks := gnex.GNEX32Callbacks{
		Start: 160, End: 162, Timeout: 162, KeyPress: 162,
		Receive: 162, RSReceive: 162, SMSReceive: 162, Result: 162,
		Network: 162, RSEvent: 162, Extension: 162,
	}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Callbacks: callbacks, Code: []byte{0x86, 0, 0x86, 0}, Symbols: symbols,
	}, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}

	for event := gnex.GNEX32EventEnd; event <= gnex.GNEX32EventRSEvent; event++ {
		data := EventData{
			Data: uint32(event), Data1: uint32(event) + 10,
			Data2: uint32(event) + 20, Data3: uint32(event) + 30,
		}
		if !runtime.EventAvailable(event) {
			t.Fatalf("%s reported unavailable", event)
		}
		if err := runtime.DispatchEvent(event, data, 1); err != nil {
			t.Fatalf("dispatch %s: %v", event, err)
		}
		for symbol, want := range data.values() {
			if got, err := runtime.vm.memory.ReadWord(symbol, 0); err != nil || got != want {
				t.Fatalf("%s swData%d = %d, %v; want %d", event, symbol, got, err, want)
			}
		}
	}

	if !runtime.EventAvailable(gnex.GNEX32EventStart) {
		t.Fatal("start handler reported unavailable")
	}
	if err := runtime.DispatchEvent(gnex.GNEX32EventStart, EventData{}, 1); !errors.Is(err, ErrEventUnavailable) {
		t.Fatalf("duplicate start dispatch error = %v", err)
	}
	for _, event := range []gnex.GNEX32Event{gnex.GNEX32EventExtension, gnex.GNEX32EventCount} {
		if runtime.EventAvailable(event) {
			t.Fatalf("%s unexpectedly available", event)
		}
		if err := runtime.DispatchEvent(event, EventData{}, 1); !errors.Is(err, ErrEventUnavailable) {
			t.Fatalf("dispatch %s error = %v", event, err)
		}
	}
}
