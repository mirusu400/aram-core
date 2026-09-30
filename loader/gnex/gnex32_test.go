package gnex

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

func gnex32Fixture() []byte {
	const prefix, ds = 32, 0x100a0
	const ps, dm, pm = ds + 8, ds + 12, ds + 24
	b := make([]byte, pm+6)
	binary.LittleEndian.PutUint32(b, prefix)
	b[prefix], b[prefix+5] = 4, 4
	copy(b[prefix+10:], "Test")
	binary.LittleEndian.PutUint32(b[prefix+0x1c:], ds-prefix-4)
	binary.LittleEndian.PutUint32(b[prefix+0x24:], ds-prefix-8)
	for off, value := range map[int]int{0x48: ds, 0x4c: ps, 0x50: dm, 0x54: pm} {
		binary.LittleEndian.PutUint32(b[prefix+off:], uint32(value-prefix))
	}
	binary.LittleEndian.PutUint32(b[prefix+0x78:], uint32(len(b)-prefix))
	b[ds-4] = 0xff
	binary.LittleEndian.PutUint16(b[ds:], 1)
	binary.LittleEndian.PutUint16(b[ds+2:], 1)
	copy(b[ps:], []byte{1, 2, 3, 4})
	binary.LittleEndian.PutUint32(b[dm:], 0x100)
	binary.LittleEndian.PutUint32(b[dm+4:], 6)
	copy(b[pm:], []byte{1, 2, 3, 4, 5, 6})
	return b
}

func TestDecodeGNEX32Image(t *testing.T) {
	b := gnex32Fixture()
	image, err := DecodeGNEX32Image(b)
	if err != nil {
		t.Fatal(err)
	}
	if image.Header.FormatVersion != 4 || image.Header.Title != "Test" ||
		image.EntryField != 0x1007c || image.Entry != 0x1009c || len(image.Symbols) != 1 || len(image.Media) != 1 ||
		image.CodePointers[0] != image.Entry || image.CodePointers[2] != 0x10098 || image.CodePointers[1] != 0 ||
		image.Symbols[0].Flags != 1 || image.Symbols[0].Words != 1 ||
		image.Media[0].Flags != 0x100 || !bytes.Equal(image.Symbols[0].Data, []byte{1, 2, 3, 4}) {
		t.Fatalf("decoded layout: %+v", image.Header)
	}
	b[0x100a0+24] = 0
	if image.Media[0].Data[0] != 1 {
		t.Fatal("input buffer leaked into image")
	}
}

func TestGNEX32CodeAddressWordOrderAndBounds(t *testing.T) {
	b := gnex32Fixture()
	codeStart := 32 + 0x80
	// Address 0x10000 is encoded as high word 1, low word 0. Reading the
	// same four bytes as a little-endian uint32 would produce 1 instead.
	binary.LittleEndian.PutUint16(b[codeStart:], 1)
	binary.LittleEndian.PutUint16(b[codeStart+2:], 0)
	image, err := DecodeGNEX32Image(b)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := image.DecodeCodeAddress(0); err != nil || got != 0x10000-0x80 {
		t.Fatalf("code address = %#x, %v", got, err)
	}
	for _, tc := range []struct {
		name   string
		high   uint16
		low    uint16
		offset int
	}{
		{"before_code", 0, 0x7e, 0},
		{"odd_target", 0, 0x81, 0},
		{"after_code", 1, 0xa0 - 32, 0},
		{"truncated", 0, 0, len(image.Code) - 2},
		{"negative_offset", 0, 0, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary.LittleEndian.PutUint16(image.Code, tc.high)
			binary.LittleEndian.PutUint16(image.Code[2:], tc.low)
			if _, err := image.DecodeCodeAddress(tc.offset); err == nil {
				t.Fatal("accepted invalid code address")
			}
		})
	}
}

func TestGNEX32AddressInstructionLayouts(t *testing.T) {
	for _, tc := range []struct {
		opcode  uint16
		word    uint16
		hasWord bool
		next    int
	}{
		{0x7d, 0x1234, true, 8},
		{0x7f, 0x1234, true, 8},
		{0x80, 0x1234, true, 8},
		{0x81, 0, false, 6},
		{0x82, 0, false, 6},
		{0x83, 0, false, 6},
		{0x85, 0, false, 6},
	} {
		t.Run(fmt.Sprintf("%02x", tc.opcode), func(t *testing.T) {
			b := gnex32Fixture()
			start := 32 + 0x80
			binary.LittleEndian.PutUint16(b[start:], tc.opcode)
			addressOffset := start + 2
			if tc.hasWord {
				binary.LittleEndian.PutUint16(b[addressOffset:], tc.word)
				addressOffset += 2
			}
			binary.LittleEndian.PutUint16(b[addressOffset:], 1)
			binary.LittleEndian.PutUint16(b[addressOffset+2:], 0)
			image, err := DecodeGNEX32Image(b)
			if err != nil {
				t.Fatal(err)
			}
			got, err := image.DecodeAddressInstruction(0)
			if err != nil || got.Opcode != tc.opcode || got.Word != tc.word ||
				got.HasWord != tc.hasWord || got.Target != 0x10000-0x80 || got.Next != tc.next {
				t.Fatalf("instruction = %+v, %v", got, err)
			}
		})
	}
	image, err := DecodeGNEX32Image(gnex32Fixture())
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint16(image.Code[len(image.Code)-2:], 0x85)
	for _, offset := range []int{-1, 1, len(image.Code) - 2} {
		if _, err := image.DecodeAddressInstruction(offset); err == nil {
			t.Fatalf("accepted malformed instruction at %d", offset)
		}
	}
}

func TestInspectGNEX32Package(t *testing.T) {
	sgs := gnex32Fixture()
	for name, input := range map[string][]byte{
		"paired": buildZIP(t, map[string][]byte{"game.mod": []byte("manifest"), "game.SGS": sgs}),
		"raw":    sgs,
	} {
		t.Run(name, func(t *testing.T) {
			pkg, err := Inspect(input)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Header.FormatVersion != 4 || pkg.Header.Title != "Test" || len(pkg.SGS) != len(sgs) {
				t.Fatalf("recognized image: %+v", pkg.Header)
			}
		})
	}
}

func TestDecodeGNEX32ImageRejectsInvalidLayout(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"short", func(b []byte) []byte { return b[:159] }},
		{"prefix", func(b []byte) []byte { b[4] = 1; return b }},
		{"version", func(b []byte) []byte { b[32] = 2; return b }},
		{"length", func(b []byte) []byte { b[32+0x78]++; return b }},
		{"entry", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[32+0x1c:], 0xffffffff); return b }},
		{"callback", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[32+0x24:], 0xffffffff); return b }},
		{"order", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[32+0x4c:], 0); return b }},
		{"symbol_size", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x100a0+2:], 2); return b }},
		{"media_size", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x100a0+12+4:], 7); return b }},
		{"title", func(b []byte) []byte { copy(b[42:], bytes.Repeat([]byte{0xff}, 14)); return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.edit(gnex32Fixture())
			image, err := DecodeGNEX32Image(b)
			if err == nil || !bytes.Equal(image.Buffer, nil) {
				t.Fatalf("accepted malformed image: %v", err)
			}
			if tc.name == "version" || tc.name == "prefix" {
				if !errors.Is(err, ErrUnsupportedExecutionVariant) {
					t.Fatalf("wrong variant error: %v", err)
				}
			}
		})
	}
}
