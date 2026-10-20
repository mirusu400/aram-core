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
	}, gnex32TestDeviceProfile())
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

func TestRemainingStringServicesUseDocumentedMediaContracts(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 1, Data: []byte("ab\x00ignored")},
		{Flags: 0, Data: []byte("cd\x00tail")},
		{Flags: 1},
		{Flags: 0, Data: []byte(" \t-42xyz\x00")},
	})

	result, err := runtime.service(0xa3, []uint32{0, 1})
	if err != nil || result.Pop != 2 || len(result.Push) != 0 {
		t.Fatalf("StrCat result = %+v, %v", result, err)
	}
	if got := readStringMedia(t, runtime, 0); !bytes.Equal(got, []byte("abcd\x00")) {
		t.Fatalf("StrCat destination = %q", got)
	}
	if _, err := runtime.service(0xa3, []uint32{0, 0}); err != nil {
		t.Fatal(err)
	}
	if got := readStringMedia(t, runtime, 0); !bytes.Equal(got, []byte("abcdabcd\x00")) {
		t.Fatalf("aliased StrCat destination = %q", got)
	}

	result, err = runtime.service(0xa7, []uint32{3})
	if err != nil || result.Pop != 1 || len(result.Push) != 1 || int32(result.Push[0]) != -42 {
		t.Fatalf("AsciiToInt result = %+v, %v", result, err)
	}
	result, err = runtime.service(0xa8, []uint32{2, ^uint32(122)})
	if err != nil || result.Pop != 2 || len(result.Push) != 0 {
		t.Fatalf("IntToAscii result = %+v, %v", result, err)
	}
	if got := readStringMedia(t, runtime, 2); !bytes.Equal(got, []byte("-123\x00")) {
		t.Fatalf("IntToAscii destination = %q", got)
	}

	result, err = runtime.service(0x9d, []uint32{2})
	if err != nil || result.Pop != 1 || len(result.Push) != 1 || result.Push[0] != 5 {
		t.Fatalf("GetMediaSize result = %+v, %v", result, err)
	}
	result, err = runtime.service(0x9e, []uint32{2, 7})
	if err != nil || result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 1 {
		t.Fatalf("SetMediaSize result = %+v, %v", result, err)
	}
	if got := readStringMedia(t, runtime, 2); len(got) != 8 || !bytes.Equal(got[:5], []byte("-123\x00")) {
		t.Fatalf("rounded SetMediaSize destination = %x", got)
	}
	before := readStringMedia(t, runtime, 1)
	result, err = runtime.service(0x9e, []uint32{1, 10})
	if err != nil || len(result.Push) != 1 || result.Push[0] != 0 {
		t.Fatalf("read-only SetMediaSize result = %+v, %v", result, err)
	}
	if got := readStringMedia(t, runtime, 1); !bytes.Equal(got, before) {
		t.Fatal("failed SetMediaSize changed read-only media")
	}
}

func TestIntegerArrayByteServicesAreLittleEndianAndAtomic(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 0, Data: []byte("WXYZ")},
		{Flags: 1},
	})
	if err := runtime.vm.memory.WriteWord(0, 0, 0x01020304); err != nil {
		t.Fatal(err)
	}

	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	result, err := runtime.service(0xaa, []uint32{0, 0})
	if err != nil || result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 4 {
		t.Fatalf("GetByte result = %+v, %v", result, err)
	}
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	result, err = runtime.service(0xa9, []uint32{0, 1, 0xaa})
	if err != nil || result.Pop != 3 || len(result.Push) != 0 {
		t.Fatalf("PutByte result = %+v, %v", result, err)
	}
	if got, _ := runtime.vm.memory.ReadWord(0, 0); got != 0x0102aa04 {
		t.Fatalf("PutByte word = %#x", got)
	}

	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	result, err = runtime.service(0xab, []uint32{0, 0, 0, 4})
	if err != nil || result.Pop != 4 {
		t.Fatalf("PutBytes result = %+v, %v", result, err)
	}
	if got, _ := runtime.vm.memory.ReadWord(0, 0); got != 0x5a595857 {
		t.Fatalf("PutBytes word = %#x", got)
	}
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	result, err = runtime.service(0xac, []uint32{0, 0, 1, 4})
	if err != nil || result.Pop != 4 || !bytes.Equal(readStringMedia(t, runtime, 1), []byte("WXYZ")) {
		t.Fatalf("GetBytes result = %+v, %v, data %q", result, err, readStringMedia(t, runtime, 1))
	}

	before, _ := runtime.vm.memory.ReadWord(0, 0)
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	if _, err := runtime.service(0xab, []uint32{0, 2, 0, 4}); err == nil {
		t.Fatal("out-of-range PutBytes succeeded")
	}
	if after, _ := runtime.vm.memory.ReadWord(0, 0); after != before {
		t.Fatal("failed PutBytes changed symbol memory")
	}
}

func TestMakeStrServicesFormatBoundedByteStrings(t *testing.T) {
	runtime := newStringRuntime(t, []gnex.GNEX32Media{
		{Flags: 1},
		{Flags: 0, Data: []byte("D=%3d x=%04x X=%X c=%c %%\x00")},
		{Flags: 1},
		{Flags: 0, Data: []byte("[%s]\x00")},
		{Flags: 0, Data: []byte{0xb0, 0xa1, 0}},
		{Flags: 0, Data: []byte("bad=%q\x00")},
	})

	result, err := runtime.service(0xb1, []uint32{0, 1, 10, 0x2a, 0xbeef, 'A'})
	if err != nil || result.Pop != 6 || len(result.Push) != 0 {
		t.Fatalf("MakeStr4 result = %+v, %v", result, err)
	}
	if got := readStringMedia(t, runtime, 0); !bytes.Equal(got, []byte("D= 10 x=002a X=BEEF c=A %\x00")) {
		t.Fatalf("MakeStr4 destination = %q", got)
	}
	result, err = runtime.service(0xad, []uint32{2, 3, 4})
	if err != nil || result.Pop != 3 || !bytes.Equal(readStringMedia(t, runtime, 2), []byte{'[', 0xb0, 0xa1, ']', 0}) {
		t.Fatalf("MakeStrStr result = %+v, %v, data %x", result, err, readStringMedia(t, runtime, 2))
	}

	before := readStringMedia(t, runtime, 0)
	if _, err := runtime.service(0xae, []uint32{0, 5, 1}); err == nil {
		t.Fatal("MakeStr1 accepted unsupported format")
	}
	if got := readStringMedia(t, runtime, 0); !bytes.Equal(got, before) {
		t.Fatal("failed MakeStr changed destination")
	}
}
