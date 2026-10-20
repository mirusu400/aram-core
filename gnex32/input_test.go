package gnex32

import (
	"testing"
	"time"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32PhysicalKeyRangeAndReleaseSentinel(t *testing.T) {
	keys := []uint16{
		Key1, Key2, Key3, Key4, Key5, Key6, Key7, Key8, Key9,
		Key0, KeyAsterisk, KeyPound, KeyClear, KeySend, KeyEnd,
		KeyLeft, KeyRight, KeyUp, KeyDown, KeyOK, KeySoft1, KeySoft2,
	}
	for index, key := range keys {
		want := uint16(index + 1)
		if key != want || !ValidPhysicalKey(key) {
			t.Fatalf("key %d = %#x, valid=%v; want %#x", index, key, ValidPhysicalKey(key), want)
		}
	}
	for _, code := range []uint16{0, KeySoft2 + 1, KeyRelease, 0xffff} {
		if ValidPhysicalKey(code) {
			t.Fatalf("non-physical code %#x reported valid", code)
		}
	}
	if KeyRelease != 0xff {
		t.Fatalf("release sentinel = %#x", KeyRelease)
	}
}

func TestGNEX32QueueKeyRepeatsAndReleaseStopsRepeat(t *testing.T) {
	runtime := newGNEX32InputRuntime(t, true)
	if err := runtime.QueueKey(KeyUp, true, 0, 1); err != nil {
		t.Fatal(err)
	}
	if got := readGNEX32InputData(t, runtime); got != uint32(KeyUp) {
		t.Fatalf("press swData = %#x", got)
	}
	if runtime.keyToneClip == 0 {
		t.Fatal("physical press did not start key tone")
	}

	writeGNEX32InputData(t, runtime, 0xdeadbeef)
	for frame := 0; frame < 30; frame++ {
		if err := runtime.StepFrame(1); err != nil {
			t.Fatalf("pre-repeat frame %d: %v", frame, err)
		}
	}
	if got := readGNEX32InputData(t, runtime); got != 0xdeadbeef {
		t.Fatalf("key repeated before 500 ms: swData=%#x", got)
	}
	if runtime.keyToneClip != 0 {
		t.Fatalf("initial key tone still active at 500 ms: %d", runtime.keyToneClip)
	}

	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if got := readGNEX32InputData(t, runtime); got != uint32(KeyUp) {
		t.Fatalf("first repeat swData = %#x", got)
	}
	if runtime.keyToneClip != 0 {
		t.Fatalf("repeat incorrectly started key tone %d", runtime.keyToneClip)
	}
	writeGNEX32InputData(t, runtime, 0x12345678)
	for frame := 0; frame < 2; frame++ {
		if err := runtime.StepFrame(1); err != nil {
			t.Fatal(err)
		}
	}
	if got := readGNEX32InputData(t, runtime); got != 0x12345678 {
		t.Fatalf("key repeated before the next 50 ms period: swData=%#x", got)
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if got := readGNEX32InputData(t, runtime); got != uint32(KeyUp) {
		t.Fatalf("periodic repeat swData = %#x", got)
	}

	if err := runtime.QueueKey(KeyUp, false, runtime.AudioTime(), 1); err != nil {
		t.Fatal(err)
	}
	if got := readGNEX32InputData(t, runtime); got != uint32(KeyRelease) {
		t.Fatalf("release swData = %#x", got)
	}
	writeGNEX32InputData(t, runtime, 0xcafebabe)
	for frame := 0; frame < 12; frame++ {
		if err := runtime.StepFrame(1); err != nil {
			t.Fatalf("post-release frame %d: %v", frame, err)
		}
	}
	if got := readGNEX32InputData(t, runtime); got != 0xcafebabe {
		t.Fatalf("released key repeated: swData=%#x", got)
	}
}

func TestGNEX32QueueKeyHonorsFutureGuestTimestamp(t *testing.T) {
	runtime := newGNEX32InputRuntime(t, false)
	if err := runtime.QueueKey(KeySoft1, true, 100*time.Millisecond, 1); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 6; frame++ {
		if err := runtime.StepFrame(1); err != nil {
			t.Fatal(err)
		}
	}
	if got := readGNEX32InputData(t, runtime); got != 0 {
		t.Fatalf("future press arrived early: swData=%#x", got)
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if got := readGNEX32InputData(t, runtime); got != uint32(KeySoft1) {
		t.Fatalf("future press swData = %#x", got)
	}
}

func TestGNEX32QueueKeyRejectsInvalidPhysicalCode(t *testing.T) {
	runtime := newGNEX32InputRuntime(t, false)
	for _, code := range []uint16{0, KeyRelease, 0xffff} {
		if err := runtime.QueueKey(code, true, 0, 1); err == nil {
			t.Fatalf("QueueKey accepted code %#x", code)
		}
	}
}

func TestParseGNEX32InputControl(t *testing.T) {
	if got, err := parseGNEX32InputControl("gnex32-key/0012"); err != nil || got != 0x12 {
		t.Fatalf("parse valid control = %#x, %v", got, err)
	}
	for _, control := range []string{"0012", "gnex32-key/12", "gnex32-key/zzzz", "gnex32-key/10000"} {
		if _, err := parseGNEX32InputControl(control); err == nil {
			t.Fatalf("parsed invalid control %q", control)
		}
	}
}

func newGNEX32InputRuntime(t *testing.T, keyTone bool) *Runtime {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: []byte{0x86, 0, 0x86, 0}, Symbols: symbols,
		Callbacks: gnex.GNEX32Callbacks{KeyPress: 162},
	}, DeviceProfile{
		ID: "gnex32/input-test", ScreenWidth: 120, ScreenHeight: 149,
		Identity:       Identity{MIN: "0111234567", UserID: "98B49700F"},
		Platform:       "WIPI 1.2",
		KeyToneEnabled: keyTone, MMIBacklightEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.RunMain(1); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func readGNEX32InputData(t *testing.T, runtime *Runtime) uint32 {
	t.Helper()
	value, err := runtime.vm.memory.ReadWord(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeGNEX32InputData(t *testing.T, runtime *Runtime, value uint32) {
	t.Helper()
	if err := runtime.vm.memory.WriteWord(0, 0, value); err != nil {
		t.Fatal(err)
	}
}
