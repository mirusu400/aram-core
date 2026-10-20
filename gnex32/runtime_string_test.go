package gnex32

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func newStringRuntime(t *testing.T, media []gnex.GNEX32Media) *Runtime {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 10)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header:  gnex.Header{BodyOffset: 160},
		Code:    []byte{0x86, 0},
		Entry:   160,
		Symbols: symbols,
		Media:   media,
	}, Identity{MIN: "0111234567", UserID: "11234"})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func readStringMedia(t *testing.T, runtime *Runtime, index int) []byte {
	t.Helper()
	data, _, err := runtime.vm.media.Read(index)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStringServicesUseTerminatedByteStrings(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 0, Data: []byte{'a', 'b', 0, 'X'}},
		{Flags: 0, Data: []byte{'a', 'c', 0, 'Y'}},
		{Flags: 1, Data: []byte{'z'}},
		{Flags: 1},
		{Flags: 0, Data: []byte("raw")},
	})

	result, err := runtime.service(0xa0, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 1 || len(result.Push) != 1 || result.Push[0] != 2 {
		t.Fatalf("StrLen stack result = %+v", result)
	}

	result, err = runtime.service(0xa1, []uint32{2, 0})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 0 {
		t.Fatalf("StrCpy stack result = %+v", result)
	}
	if got := readStringMedia(t, runtime, 2); !bytes.Equal(got, []byte{'a', 'b', 0}) {
		t.Fatalf("StrCpy destination = %q", got)
	}

	if _, err := runtime.service(0xa1, []uint32{2, 2}); err != nil {
		t.Fatal(err)
	}
	if got := readStringMedia(t, runtime, 2); !bytes.Equal(got, []byte{'a', 'b', 0}) {
		t.Fatalf("aliased StrCpy destination = %q", got)
	}

	result, err = runtime.service(0xa4, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 1 || int32(result.Push[0]) != -1 {
		t.Fatalf("StrCmp less result = %+v", result)
	}
	result, err = runtime.service(0xa4, []uint32{0, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Push) != 1 || result.Push[0] != 0 {
		t.Fatalf("StrCmp equal result = %+v", result)
	}
	if _, err := runtime.service(0xa1, []uint32{2, 4}); err != nil {
		t.Fatal(err)
	}
	if got := readStringMedia(t, runtime, 2); !bytes.Equal(got, []byte{'r', 'a', 'w', 0}) {
		t.Fatalf("StrCpy unterminated source result = %q", got)
	}

	result, err = runtime.service(0x9f, []uint32{3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 0 {
		t.Fatalf("StrInit stack result = %+v", result)
	}
	if got := readStringMedia(t, runtime, 3); !bytes.Equal(got, make([]byte, 5)) {
		t.Fatalf("StrInit destination = %x, want five zero bytes", got)
	}
}

func TestStringServicesPreserveDestinationOnFailure(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 0, Data: []byte("constant\x00")},
		{Flags: 1, Data: []byte("mutable\x00")},
	})
	constantBefore := readStringMedia(t, runtime, 0)
	if _, err := runtime.service(0xa1, []uint32{0, 1}); err == nil {
		t.Fatal("StrCpy wrote a read-only destination")
	}
	if got := readStringMedia(t, runtime, 0); !bytes.Equal(got, constantBefore) {
		t.Fatalf("failed StrCpy changed destination to %q", got)
	}

	mutableBefore := readStringMedia(t, runtime, 1)
	if _, err := runtime.service(0xa2, []uint32{1, 0, ^uint32(0), 1}); err == nil {
		t.Fatal("StrSub accepted a negative start")
	}
	if got := readStringMedia(t, runtime, 1); !bytes.Equal(got, mutableBefore) {
		t.Fatalf("failed StrSub changed destination to %q", got)
	}
}

func TestGetCharCompatibilityBounds(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 0, Data: []byte{0xb0, 0xa1, 0}},
		{Flags: 1, Data: []byte{'A', 0}},
	})

	for name, stack := range map[string][]uint32{
		"negative":              {0, ^uint32(0)},
		"writable one past end": {1, 2},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := runtime.service(0xa5, stack)
			if err != nil {
				t.Fatal(err)
			}
			if result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 0 {
				t.Fatalf("GetChar stack result = %+v", result)
			}
		})
	}

	result, err := runtime.service(0xa5, []uint32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 0xa1 {
		t.Fatalf("GetChar CP949 byte result = %+v", result)
	}
	if _, err := runtime.service(0xa5, []uint32{0, 3}); err == nil {
		t.Fatal("GetChar accepted one-past-end read-only media")
	}
}

func TestStrSubCopiesEucKrBytesAndTerminates(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 0, Data: []byte{0xb0, 0xa1, 0xb0, 0xa2, 0, 'X'}},
		{Flags: 1},
	})
	result, err := runtime.service(0xa2, []uint32{1, 0, 2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 4 || len(result.Push) != 0 {
		t.Fatalf("StrSub stack result = %+v", result)
	}
	data := readStringMedia(t, runtime, 1)
	if !bytes.Equal(data, []byte{0xb0, 0xa2, 0}) {
		t.Fatalf("StrSub destination = %x", data)
	}
	if _, err := runtime.service(0xa2, []uint32{1, 0, 1, 1}); err != nil {
		t.Fatal(err)
	}
	data = readStringMedia(t, runtime, 1)
	if !bytes.Equal(data, []byte{0xa1, 0}) {
		t.Fatalf("StrSub byte index = %x", data)
	}

	if _, err := runtime.service(0xa2, []uint32{1, 1, 0, 1}); err != nil {
		t.Fatal(err)
	}
	if got := readStringMedia(t, runtime, 1); !bytes.Equal(got, []byte{0xa1, 0}) {
		t.Fatalf("aliased StrSub destination = %x", got)
	}
}
