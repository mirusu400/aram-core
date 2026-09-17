package gnex

import (
	"encoding/binary"
	"errors"
	"testing"
)

func executionFixture() []byte {
	b := make([]byte, 92)
	b[0], b[2], b[5], b[10] = 2, 12, 1, 'A'
	for offset, value := range map[int]uint16{0x1c: 54, 0x2c: 64, 0x2e: 76, 0x30: 80, 0x32: 88} {
		binary.LittleEndian.PutUint16(b[offset:], value)
	}
	b[54] = 0xff
	copy(b[64:], []byte{0, 1, 99, 0, 1, 1, 1, 0, 1, 1, 0, 0})
	copy(b[76:], []byte{0xaa, 0xbb, 1, 2})
	copy(b[80:], []byte{0, 7, 1, 0, 1, 9, 3, 0})
	copy(b[88:], []byte{0xcc, 1, 2, 3})
	return b
}

func TestDecodeExecutionImage(t *testing.T) {
	input := executionFixture()
	got, err := DecodeExecutionImage(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Entry != 54 || got.RAMBytes != 41 || len(got.Symbols) != 3 || len(got.Media) != 2 {
		t.Fatalf("layout %+v", got)
	}
	if got.Symbols[0].Mutable || !got.Symbols[1].Mutable || got.Symbols[0].Data[0] != 0xaa || got.Symbols[1].Data[1] != 2 || got.Symbols[2].Data[0] != 0 {
		t.Fatal("symbol initialization")
	}
	if got.Media[0].Mutable || !got.Media[1].Mutable || got.Media[1].Tag != 9 || len(got.Media[1].Data) != 3 {
		t.Fatal("media initialization")
	}
	if got.Symbols[0].BufferOffset != 76 || got.Symbols[1].BufferOffset != -1 || got.Media[0].BufferOffset != 88 || got.Media[1].BufferOffset != -1 || got.Symbols[1].Type != 1 {
		t.Fatal("descriptor type or alias offsets")
	}
	input[76] = 0
	if got.Symbols[0].Data[0] != 0xaa {
		t.Fatal("input ownership leaked")
	}
	got.Buffer[76] = 0x44
	if got.Symbols[0].Data[0] != 0x44 {
		t.Fatal("read-only alias did not share owned buffer")
	}
	got.Buffer[78] = 0x55
	if got.Symbols[1].Data[0] != 1 {
		t.Fatal("mutable symbol aliases file")
	}
	got.Buffer[89] = 0x66
	if got.Media[1].Data[0] != 1 {
		t.Fatal("mutable media aliases file")
	}
}

func TestExecutionImageVariantsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name        string
		change      func([]byte) []byte
		unsupported bool
	}{
		{"short", func(b []byte) []byte { return b[:40] }, false},
		{"version", func(b []byte) []byte { b[0] = 1; return b }, true},
		{"compression", func(b []byte) []byte { b[2] = 4; return b }, true},
		{"mode", func(b []byte) []byte { b[5] = 4; return b }, true},
		{"header_overlap", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x2c:], 48); return b }, false},
		{"misaligned", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x2e:], 75); return b }, false},
		{"order", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x30:], 70); return b }, false},
		{"symbol_data", func(b []byte) []byte { b[65] = 255; return b }, false},
		{"media_data", func(b []byte) []byte { b[86] = 255; return b }, false},
		{"entry", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x1c:], 500); return b }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeExecutionImage(tc.change(executionFixture()))
			if err == nil {
				t.Fatal("accepted")
			}
			if tc.unsupported && !errors.Is(err, ErrUnsupportedExecutionVariant) {
				t.Fatalf("wrong variant error: %v", err)
			}
		})
	}
	b := executionFixture()
	binary.LittleEndian.PutUint16(b[0x1c:], 0)
	if got, err := DecodeExecutionImage(b); err != nil || got.Entry != 0 {
		t.Fatalf("absent callback %v", err)
	}
}

func TestExecutionImageRAMLimit(t *testing.T) {
	b := make([]byte, 204)
	copy(b, executionFixture()[:64])
	for offset, value := range map[int]uint16{0x2c: 64, 0x2e: 204, 0x30: 204, 0x32: 204} {
		binary.LittleEndian.PutUint16(b[offset:], value)
	}
	for i := 64; i < 204; i += 4 {
		b[i], b[i+1] = 1, 255
	}
	if _, err := DecodeExecutionImage(b); err == nil {
		t.Fatal("RAM overflow accepted")
	}
}

func FuzzDecodeExecutionImage(f *testing.F) {
	f.Add(executionFixture())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = DecodeExecutionImage(b) })
}
