package gvm_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestIndexedSymbolStore(t *testing.T) {
	offset := uint32(4)
	v, err := gvm.NewWithSymbols([]byte{0x31, 0, 1, 0xff, 0, 0, 0, 0, 0xff}, 0, []gvm.SymbolRegion{{ProgramOffset: &offset, Length: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Step(); err != nil || v.PC() != 4 || len(v.Stack()) != 0 {
		t.Fatalf("store: %v pc %d", err, v.PC())
	}
	s, _ := v.Symbol(0)
	if !bytes.Equal(s, []byte{0, 0, 0xff, 0xff}) {
		t.Fatalf("LE16 indexed: %x", s)
	}
	if err := v.Run(3); err != nil || v.PC() != 7 {
		t.Fatalf("aliased fetch: %v pc %d", err, v.PC())
	}
	v, err = gvm.NewWithSymbols([]byte{0x31, 0, 254, 0x80, 0xff}, 0, []gvm.SymbolRegion{{Length: 510, Initial: make([]byte, 510)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(2); err != nil {
		t.Fatal(err)
	}
	s, _ = v.Symbol(0)
	if !bytes.Equal(s[508:], []byte{0x80, 0xff}) {
		t.Fatalf("last word: %x", s[508:])
	}
}

func TestIndexedStoreSafety(t *testing.T) {
	for _, tt := range []struct {
		name string
		code []byte
		size int
		want error
	}{
		{"missing symbol", []byte{0x31}, 2, gvm.ErrTruncated},
		{"missing element", []byte{0x31, 0}, 2, gvm.ErrTruncated},
		{"missing immediate", []byte{0x31, 0, 0}, 2, gvm.ErrTruncated},
		{"invalid symbol", []byte{0x31, 1, 0, 1}, 2, gvm.ErrInvalidSymbol},
		{"empty region", []byte{0x31, 0, 0, 1}, 0, gvm.ErrInvalidElement},
		{"past last", []byte{0x31, 0, 1, 1}, 2, gvm.ErrInvalidElement},
		{"index255", []byte{0x31, 0, 255, 1}, 510, gvm.ErrInvalidElement},
		{"odd region", []byte{0x31, 0, 0, 1}, 3, gvm.ErrInvalidSymbolRegion},
		{"oversized region", []byte{0x31, 0, 0, 1}, 512, gvm.ErrInvalidSymbolRegion},
	} {
		t.Run(tt.name, func(t *testing.T) {
			initial := make([]byte, tt.size)
			v, err := gvm.NewWithSymbols(tt.code, 0, []gvm.SymbolRegion{{Length: uint32(tt.size), Initial: initial}})
			if err != nil {
				t.Fatal(err)
			}
			err = v.Step()
			if !errors.Is(err, tt.want) || v.PC() != 1 || v.Step() != err {
				t.Fatalf("fault %v pc %d", err, v.PC())
			}
			s, _ := v.Symbol(0)
			if !bytes.Equal(s, initial) || !bytes.Equal(v.Program(), tt.code) {
				t.Fatal("fault changed memory")
			}
		})
	}
}
