package gnex32

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

func newNVRuntime(t *testing.T, words int) *Runtime {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	symbols[0] = gnex.GNEX32Symbol{Flags: 1, Words: uint16(words), Data: make([]byte, words*4)}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: []byte{0x86, 0}, Entry: 160,
		Symbols: symbols,
	}, Identity{MIN: "0111234567", UserID: "11234"})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestPutUserNVRoundTripsThroughGetUserNV(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	want := []uint32{1, 2, 0x80000000, 0xffffffff}
	for index, value := range want {
		if err := runtime.vm.memory.WriteWord(0, index, value); err != nil {
			t.Fatal(err)
		}
	}
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	result, err := runtime.service(0x94, []uint32{want[0], uint32(len(want))})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 0 {
		t.Fatalf("PutUserNV stack result = %+v", result)
	}

	for index := range want {
		if err := runtime.vm.memory.WriteWord(0, index, 0); err != nil {
			t.Fatal(err)
		}
	}
	result, err = runtime.service(0x93, []uint32{0, uint32(len(want))})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 0 {
		t.Fatalf("GetUserNV stack result = %+v", result)
	}
	for index, expected := range want {
		got, err := runtime.vm.memory.ReadWord(0, index)
		if err != nil {
			t.Fatal(err)
		}
		if got != expected {
			t.Fatalf("word %d = %#x, want %#x", index, got, expected)
		}
	}
}

func TestUserNVCaptureRestoreCarriesServiceWritesAcrossRuntimes(t *testing.T) {
	source := newNVRuntime(t, 4)
	want := []uint32{0x01020304, 7, 0x80000000, 0xffffffff}
	for index, value := range want {
		if err := source.vm.memory.WriteWord(0, index, value); err != nil {
			t.Fatal(err)
		}
	}
	source.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	if _, err := source.service(0x94, []uint32{want[0], uint32(len(want))}); err != nil {
		t.Fatal(err)
	}

	target := newNVRuntime(t, 4)
	target.RestoreUserNV(source.CaptureUserNV())
	target.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	if _, err := target.service(0x93, []uint32{0, uint32(len(want))}); err != nil {
		t.Fatal(err)
	}
	if got := gnex32TestSymbolWords(t, target, 0, len(want)); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored UserNV = %#v, want %#v", got, want)
	}
}

func TestPutUserNVRejectsInvalidInputWithoutChangingNV(t *testing.T) {
	runtime := newNVRuntime(t, 17)
	runtime.nv[0] = 7
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	if _, err := runtime.service(0x94, []uint32{0, 17}); err == nil {
		t.Fatal("PutUserNV accepted more than 16 words")
	}
	if runtime.nv[0] != 7 {
		t.Fatalf("oversized PutUserNV changed NV to %d", runtime.nv[0])
	}

	delete(runtime.vm.refs, 0)
	if _, err := runtime.service(0x94, []uint32{0, 1}); !errors.Is(err, ErrInvalidLValue) {
		t.Fatalf("PutUserNV invalid lvalue error = %v", err)
	}
	if runtime.nv[0] != 7 {
		t.Fatalf("invalid PutUserNV changed NV to %d", runtime.nv[0])
	}
}

func TestDateAndTimeUseDeterministicHandsetClock(t *testing.T) {
	runtime := newNVRuntime(t, 4)
	const epoch = int64(1167609600000) // 2007-01-01T00:00:00Z
	if err := runtime.services.Clock.Restore(shared.ClockState{
		WallEpochMillis:    epoch,
		TimezoneOffsetMins: 9 * 60,
		Locale:             "ko-KR",
	}); err != nil {
		t.Fatal(err)
	}
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}

	if _, err := runtime.service(4, []uint32{0}); err != nil {
		t.Fatal(err)
	}
	if got := gnex32TestSymbolWords(t, runtime, 0, 4); !reflect.DeepEqual(got, []uint32{2007, 1, 1, 1}) {
		t.Fatalf("GetDate = %v, want 2007-01-01 Monday", got)
	}
	if err := runtime.services.Clock.Advance(1234 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	before := runtime.services.Clock.Snapshot()
	if _, err := runtime.service(5, []uint32{0}); err != nil {
		t.Fatal(err)
	}
	if got := gnex32TestSymbolWords(t, runtime, 0, 4); !reflect.DeepEqual(got, []uint32{9, 0, 1, 234}) {
		t.Fatalf("GetTime = %v, want 09:00:01.234", got)
	}
	if after := runtime.services.Clock.Snapshot(); after != before {
		t.Fatalf("time query mutated clock: before=%+v after=%+v", before, after)
	}
}

func gnex32TestSymbolWords(t *testing.T, runtime *Runtime, symbol, words int) []uint32 {
	t.Helper()
	values := make([]uint32, words)
	for index := range values {
		value, err := runtime.vm.memory.ReadWord(symbol, index)
		if err != nil {
			t.Fatal(err)
		}
		values[index] = value
	}
	return values
}
