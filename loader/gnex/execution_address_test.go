package gnex

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestExecutionAddressSpaceRegions(t *testing.T) {
	input := executionFixture()
	got, err := DecodeExecutionImage(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.SymbolFileStart != 76 || got.SymbolFileEnd != 80 {
		t.Fatalf("file span %d..%d", got.SymbolFileStart, got.SymbolFileEnd)
	}
	if !reflect.DeepEqual(got.SymbolRAM, []byte{1, 2, 0, 0}) {
		t.Fatalf("symbol RAM %x", got.SymbolRAM)
	}
	for i, want := range []int{-1, 0, 2} {
		if got.Symbols[i].RAMOffset != want {
			t.Fatalf("symbol %d RAM offset %d", i, got.Symbols[i].RAMOffset)
		}
	}
	got.SymbolRAM[0] = 9
	if got.Symbols[1].Data[0] != 9 {
		t.Fatal("RAM view disconnected")
	}
	got.Symbols[2].Data[1] = 7
	if got.SymbolRAM[3] != 7 {
		t.Fatal("symbol view disconnected")
	}
	got.Media[1].Data[0] = 99
	if len(got.SymbolRAM) != 4 || got.SymbolRAM[0] != 9 {
		t.Fatal("media in symbol region")
	}
	got.Buffer[78] = 88
	if got.SymbolRAM[0] != 9 {
		t.Fatal("initialized RAM aliases file")
	}
	if input[78] != 1 {
		t.Fatal("caller input modified")
	}
}

func TestExecutionAddressSpaceCursorsAndZeroSymbols(t *testing.T) {
	// Four symbols: zero-length RAM, zero-filled RAM, file, initialized RAM.
	b := make([]byte, 96)
	copy(b, executionFixture()[:64])
	for offset, value := range map[int]uint16{0x2c: 64, 0x2e: 80, 0x30: 88, 0x32: 92} {
		binary.LittleEndian.PutUint16(b[offset:], value)
	}
	copy(b[64:], []byte{1, 0, 0, 0, 1, 2, 0, 0, 0, 1, 0, 0, 2, 1, 1, 0})
	copy(b[80:], []byte{1, 2, 3, 4, 0xee, 0xee, 0xee, 0xee})
	copy(b[88:], []byte{1, 0, 4, 0})
	copy(b[92:], []byte{5, 6, 7, 8})
	got, err := DecodeExecutionImage(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.SymbolFileStart != 80 || got.SymbolFileEnd != 84 {
		t.Fatalf("file cursor includes padding/media %d..%d", got.SymbolFileStart, got.SymbolFileEnd)
	}
	if !reflect.DeepEqual(got.SymbolRAM, []byte{0, 0, 0, 0, 3, 4}) {
		t.Fatalf("RAM %x", got.SymbolRAM)
	}
	for i, want := range []int{0, 0, -1, 4} {
		if got.Symbols[i].RAMOffset != want {
			t.Fatalf("offset %d", i)
		}
	}
	if got.Symbols[2].BufferOffset != 80 || got.RAMBytes != 42 {
		t.Fatalf("legacy accounting %+v", got)
	}
	for _, s := range got.Symbols {
		if cap(s.Data) != len(s.Data) {
			t.Fatal("symbol capacity leaks adjacent region")
		}
	}
}

func TestExecutionAddressSpacePublicIntegration(t *testing.T) {
	input := executionFixture()
	copy(input[54:], []byte{0x4d, 0, 0x4d, 1, 0x4d, 2, 0xff})
	image, err := DecodeExecutionImage(input)
	if err != nil {
		t.Fatal(err)
	}
	space := gvm.AddressSpace{FileStart: uint32(image.SymbolFileStart), FileLength: uint32(image.SymbolFileEnd - image.SymbolFileStart), RAM: image.SymbolRAM}
	for _, s := range image.Symbols {
		binding := gvm.AddressSymbol{Region: gvm.AddressFile, Offset: uint32(s.BufferOffset - image.SymbolFileStart), Length: uint32(len(s.Data))}
		if s.Mutable {
			binding.Region = gvm.AddressRAM
			binding.Offset = uint32(s.RAMOffset)
		}
		space.Symbols = append(space.Symbols, binding)
	}
	v, err := gvm.NewWithAddressSpace(image.Buffer, uint32(image.Entry), space)
	if err != nil {
		t.Fatal(err)
	}
	image.Buffer[76] = 0
	image.SymbolRAM[0] = 0
	if err := v.Run(4); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Stack(), []uint16{0x4000, 0, 1}) {
		t.Fatalf("stack %x", v.Stack())
	}
	for address, want := range map[uint16]uint16{0x4000: 0xbbaa, 0x4001: 0x0201, 0: 0x0201, 1: 0} {
		if got, err := v.ReadWord(address); err != nil || got != want {
			t.Fatalf("address %x got %x %v", address, got, err)
		}
	}
	if _, err := v.ReadWord(2); err == nil {
		t.Fatal("media exposed by resolver")
	}
	if _, err := v.ReadWord(0x4002); err == nil {
		t.Fatal("media descriptors exposed by resolver")
	}
}
