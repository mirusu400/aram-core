package gnex32

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
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
