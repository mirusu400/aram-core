package gnex32

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGetSysInfoUsesDeviceProfile(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 0, Data: []byte("PLATFORM\x00")},
		{Flags: 0, Data: []byte("VODEX\x00")},
		{Flags: 0, Data: []byte("UNKNOWN\x00")},
		{Flags: 1, Data: []byte("unchanged\x00")},
	})

	result, err := runtime.service(0x1d8, []uint32{0, 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 1 {
		t.Fatalf("GetSysInfo PLATFORM stack result = %+v", result)
	}
	if got := readStringMedia(t, runtime, 3); !bytes.Equal(got, []byte("WIPI 1.2\x00")) {
		t.Fatalf("GetSysInfo PLATFORM = %q", got)
	}

	if result, err = runtime.service(0x1d8, []uint32{1, 3}); err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 1 ||
		!bytes.Equal(readStringMedia(t, runtime, 3), []byte("Not Support\x00")) {
		t.Fatalf("GetSysInfo VODEX result = %+v, value %q", result, readStringMedia(t, runtime, 3))
	}

	before := readStringMedia(t, runtime, 3)
	if result, err = runtime.service(0x1d8, []uint32{2, 3}); err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 0 ||
		!bytes.Equal(readStringMedia(t, runtime, 3), before) {
		t.Fatalf("unknown GetSysInfo result = %+v, value %q", result, readStringMedia(t, runtime, 3))
	}
}

func TestGetSysInfoRejectsInvalidStackAndReadOnlyDestination(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 0, Data: []byte("PLATFORM\x00")},
		{Flags: 0, Data: []byte("constant\x00")},
	})
	if _, err := runtime.service(0x1d8, []uint32{0}); err != ErrStackUnderflow {
		t.Fatalf("one-argument GetSysInfo error = %v", err)
	}
	if _, err := runtime.service(0x1d8, []uint32{0, 1}); err == nil {
		t.Fatal("GetSysInfo wrote a read-only destination")
	}
	if got := readStringMedia(t, runtime, 1); !bytes.Equal(got, []byte("constant\x00")) {
		t.Fatalf("failed GetSysInfo changed destination to %q", got)
	}
}
