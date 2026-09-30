package gnex

import (
	"encoding/binary"
	"testing"
)

func TestGNEX32SymbolMemoryCopiesBothInitializerClasses(t *testing.T) {
	mutable := []byte{1, 0, 0, 0, 2, 0, 0, 0}
	constant := []byte{3, 0, 0, 0}
	image := GNEX32Image{Symbols: []GNEX32Symbol{
		{Flags: 1, Words: 2, Data: mutable},
		{Flags: 0x100, Words: 1, Data: constant},
	}}
	state, err := image.NewSymbolMemory()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		symbol, element int
		want            uint32
	}{{0, 0, 1}, {0, 1, 2}, {1, 0, 3}} {
		got, err := state.ReadWord(tc.symbol, tc.element)
		if err != nil || got != tc.want {
			t.Fatalf("ReadWord(%d, %d) = %d, %v; want %d", tc.symbol, tc.element, got, err, tc.want)
		}
	}
	if err := state.WriteWord(0, 1, 0x12345678); err != nil {
		t.Fatal(err)
	}
	if got, err := state.ReadWord(0, 1); err != nil || got != 0x12345678 {
		t.Fatalf("mutable word = %#x, %v", got, err)
	}
	if binary.LittleEndian.Uint32(mutable[4:]) != 2 {
		t.Fatal("runtime write modified image initializer")
	}
	if err := state.WriteWord(1, 0, 4); err != nil {
		t.Fatalf("initialized symbol write = %v", err)
	}
	if got, err := state.ReadWord(1, 0); err != nil || got != 4 {
		t.Fatalf("initialized symbol = %d, %v", got, err)
	}
	if binary.LittleEndian.Uint32(constant) != 3 {
		t.Fatal("runtime write modified initialized image data")
	}
	second, err := image.NewSymbolMemory()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.ReadWord(0, 1); err != nil || got != 2 {
		t.Fatalf("new state inherited prior write: %#x, %v", got, err)
	}
	if got, err := second.ReadWord(1, 0); err != nil || got != 3 {
		t.Fatalf("new state inherited initialized symbol write: %#x, %v", got, err)
	}
}

func TestGNEX32SymbolMemoryRejectsInvalidAccessAndFlags(t *testing.T) {
	image := GNEX32Image{Symbols: []GNEX32Symbol{{Flags: 1, Words: 1, Data: make([]byte, 4)}}}
	state, err := image.NewSymbolMemory()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ symbol, element int }{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		if _, err := state.ReadWord(tc.symbol, tc.element); err == nil {
			t.Fatalf("ReadWord(%d, %d) accepted", tc.symbol, tc.element)
		}
		if err := state.WriteWord(tc.symbol, tc.element, 1); err == nil {
			t.Fatalf("WriteWord(%d, %d) accepted", tc.symbol, tc.element)
		}
	}
	image.Symbols[0].Flags = 2
	if _, err := image.NewSymbolMemory(); err == nil {
		t.Fatal("accepted unknown symbol flags")
	}
	image.Symbols[0].Flags = 1
	image.Symbols[0].Data = nil
	if _, err := image.NewSymbolMemory(); err == nil {
		t.Fatal("accepted truncated initializer")
	}
}
