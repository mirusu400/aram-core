package gvm_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSymbolStoreSelfAliasAndHighIndex(t *testing.T) {
	offset := uint32(1)
	v, err := gvm.NewWithSymbols([]byte{0x36, 0, 0x80, 0xff}, 0, []gvm.SymbolRegion{{ProgramOffset: &offset, Length: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(2); err != nil || !bytes.Equal(v.Program(), []byte{0x36, 0x80, 0xff, 0xff}) || v.PC() != 4 {
		t.Fatalf("self alias: %v %x", err, v.Program())
	}
	// An unsigned index 255 selects exactly the caller's 256th binding.
	regions := make([]gvm.SymbolRegion, 256)
	regions[255] = gvm.SymbolRegion{Length: 2, Initial: []byte{1, 2}}
	v, err = gvm.NewWithSymbols([]byte{0x36, 0xff, 0, 0xff}, 0, regions)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(2); err != nil {
		t.Fatal(err)
	}
	s, err := v.Symbol(255)
	if err != nil || !bytes.Equal(s, []byte{0, 0}) {
		t.Fatalf("unsigned index: %v %x", err, s)
	}
}

func FuzzSymbolAliasDeterminism(f *testing.F) {
	f.Add([]byte{0x36, 0, 0xff, 0, 0, 0xff}, uint16(3), uint8(10))
	f.Add([]byte{0x36, 0, 0x80, 0xff}, uint16(1), uint8(10))
	f.Fuzz(func(t *testing.T, code []byte, rawOffset uint16, budget uint8) {
		if len(code) == 0 || len(code) > 4096 {
			t.Skip()
		}
		offset := uint32(int(rawOffset) % (len(code) + 1))
		regions := []gvm.SymbolRegion{{ProgramOffset: &offset, Length: uint32(len(code)) - offset}, {Length: 2, Initial: []byte{0, 0}}}
		a, ea := gvm.NewWithSymbols(code, 0, regions)
		b, eb := gvm.NewWithSymbols(code, 0, regions)
		if ea != nil || eb != nil {
			t.Fatalf("valid binding rejected %v %v", ea, eb)
		}
		ea = a.Run(uint64(budget))
		eb = b.Run(uint64(budget))
		if fmt.Sprint(ea) != fmt.Sprint(eb) || a.PC() != b.PC() || a.Halted() != b.Halted() || !bytes.Equal(a.Program(), b.Program()) {
			t.Fatal("nondeterministic alias execution")
		}
		for i := uint8(0); i < 2; i++ {
			sa, _ := a.Symbol(i)
			sb, _ := b.Symbol(i)
			if !bytes.Equal(sa, sb) {
				t.Fatal("nondeterministic symbols")
			}
		}
		if a.PC() < 0 || a.PC() > len(code) || len(a.Stack()) > 65 {
			t.Fatal("unbounded state")
		}
	})
}
