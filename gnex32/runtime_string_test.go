package gnex32

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestStrSubCopiesEucKrBytesAndTerminates(t *testing.T) {
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Code: []byte{0x86, 0}, Entry: 160,
		Symbols: symbols,
		Media: []gnex.GNEX32Media{
			{Flags: 0, Data: []byte{0xb0, 0xa1, 0xb0, 0xa2, 0, 'X'}},
			{Flags: 1},
		},
	}
	runtime, err := NewRuntime(image, Identity{MIN: "0111234567", UserID: "1234"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.service(0xa2, []uint32{1, 0, 2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 4 || len(result.Push) != 0 {
		t.Fatalf("StrSub stack result = %+v", result)
	}
	data, _, err := runtime.vm.media.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{0xb0, 0xa2, 0}) {
		t.Fatalf("StrSub destination = %x", data)
	}
	if _, err := runtime.service(0xa2, []uint32{1, 0, 1, 1}); err != nil {
		t.Fatal(err)
	}
	data, _, err = runtime.vm.media.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{0xa1, 0}) {
		t.Fatalf("StrSub byte index = %x", data)
	}
}
