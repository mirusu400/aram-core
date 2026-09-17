package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestLoadSymbolWord(t *testing.T) {
	v, err := gvm.NewWithSymbols([]byte{4, 0, 0xff}, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0xcd, 0xab}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(2); err != nil || v.PC() != 3 || len(v.Stack()) != 1 || v.Stack()[0] != 0xabcd {
		t.Fatalf("load LE16: %v stack%x", err, v.Stack())
	}
	offset := uint32(6)
	v, err = gvm.NewWithSymbols([]byte{0x36, 0, 0x80, 4, 0, 0xff, 0, 0}, 0, []gvm.SymbolRegion{{ProgramOffset: &offset, Length: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(3); err != nil || v.Stack()[0] != 0xff80 {
		t.Fatalf("aliased store/load: %v stack%x", err, v.Stack())
	}
}

func TestSymbolLoadGuardPriority(t *testing.T) {
	var prefix []byte
	for i := 0; i < 65; i++ {
		prefix = append(prefix, 5, 1)
	}
	for _, tt := range []struct {
		suffix []byte
		want   error
	}{{[]byte{4}, gvm.ErrTruncated}, {[]byte{4, 255}, gvm.ErrStackOverflow}} {
		v := gvm.New(append(append([]byte(nil), prefix...), tt.suffix...))
		if err := v.Run(66); !errors.Is(err, tt.want) || v.PC() != 131 || len(v.Stack()) != 65 {
			t.Fatalf("guard priority %v", err)
		}
	}
}

func TestLoadSymbolSafety(t *testing.T) {
	for _, tt := range []struct {
		code    []byte
		regions []gvm.SymbolRegion
		want    error
	}{
		{[]byte{4}, nil, gvm.ErrTruncated},
		{[]byte{4, 0}, nil, gvm.ErrInvalidSymbol},
		{[]byte{4, 0}, []gvm.SymbolRegion{{Length: 1, Initial: []byte{9}}}, gvm.ErrInvalidSymbolRegion},
	} {
		v, err := gvm.NewWithSymbols(tt.code, 0, tt.regions)
		if err != nil {
			t.Fatal(err)
		}
		err = v.Step()
		if !errors.Is(err, tt.want) || v.PC() != 1 || len(v.Stack()) != 0 || v.Step() != err {
			t.Fatalf("load fault: %v pc%d", err, v.PC())
		}
	}
	var code []byte
	for i := 0; i < 66; i++ {
		code = append(code, 4, 0)
	}
	v, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{7, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(65); !errors.Is(err, gvm.ErrBudget) || len(v.Stack()) != 65 {
		t.Fatalf("capacity %v", err)
	}
	if err := v.Step(); !errors.Is(err, gvm.ErrStackOverflow) || len(v.Stack()) != 65 || v.PC() != 131 {
		t.Fatalf("overflow %v pc%d", err, v.PC())
	}
}
