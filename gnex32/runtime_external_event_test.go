package gnex32

import (
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestExternalSerialAndSMSReceivePopulateEventWords(t *testing.T) {
	symbols := make([]gnex.GNEX32Symbol, 10)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: []byte{
			0x86, 0x00, // main return
			0x04, 0x00, 0x00, 0x00, // push swData
			0x0b, 0x00, 0x06, 0x00, // symbol 6 = swData
			0x04, 0x00, 0x01, 0x00, // push swData1
			0x0b, 0x00, 0x07, 0x00, // symbol 7 = swData1
			0x86, 0x00,
			0x04, 0x00, 0x02, 0x00, // push swData2
			0x0b, 0x00, 0x08, 0x00, // symbol 8 = swData2
			0x04, 0x00, 0x03, 0x00, // push swData3
			0x0b, 0x00, 0x09, 0x00, // symbol 9 = swData3
			0x86, 0x00,
		},
		Symbols: symbols,
		Callbacks: gnex.GNEX32Callbacks{
			RSReceive:  162,
			SMSReceive: 180,
		},
	}, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}

	serial := EventData{Data: 0x11, Data1: 0x22, Data2: 0x33, Data3: 0x44}
	if err := runtime.InjectSerialReceive(serial, 5); err != nil {
		t.Fatal(err)
	}
	assertGNEX32Word(t, runtime, 6, serial.Data)
	assertGNEX32Word(t, runtime, 7, serial.Data1)

	sms := EventData{Data: 0xaa, Data1: 0xbb, Data2: 0xcc, Data3: 0xdd}
	if err := runtime.InjectSMSReceive(sms, 5); err != nil {
		t.Fatal(err)
	}
	assertGNEX32Word(t, runtime, 8, sms.Data2)
	assertGNEX32Word(t, runtime, 9, sms.Data3)
}

func TestExternalReceiveWithoutCallbackIsDropped(t *testing.T) {
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: []byte{0x86, 0x00}, Symbols: symbols,
	}, gnex32TestDeviceProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.InjectSerialReceive(EventData{Data: 1}, 1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.InjectSMSReceive(EventData{Data: 2}, 1); err != nil {
		t.Fatal(err)
	}
}

func assertGNEX32Word(t *testing.T, runtime *Runtime, symbol int, want uint32) {
	t.Helper()
	got, err := runtime.vm.memory.ReadWord(symbol, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("symbol %d = %#x, want %#x", symbol, got, want)
	}
}
