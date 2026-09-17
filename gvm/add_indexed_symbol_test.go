package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestAddIndexedSymbol(t *testing.T) {
	for _, test := range []struct {
		name  string
		start uint16
		delta byte
		want  uint16
	}{
		{name: "positive", start: 10, delta: 7, want: 17},
		{name: "negative", start: 10, delta: 0xff, want: 9},
		{name: "wrap", start: 0xffff, delta: 1, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			initial := make([]byte, 6)
			binary.LittleEndian.PutUint16(initial[2:4], test.start)
			v, err := gvm.NewWithSymbols([]byte{0x39, 0, 1, test.delta, 0xff}, 0, []gvm.SymbolRegion{{Length: 6, Initial: initial}})
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Step(); err != nil || v.PC() != 4 || len(v.Stack()) != 0 {
				t.Fatalf("step: %v pc=%d stack=%x", err, v.PC(), v.Stack())
			}
			got, err := v.Symbol(0)
			if err != nil || binary.LittleEndian.Uint16(got[2:4]) != test.want {
				t.Fatalf("symbol=%x err=%v", got, err)
			}
		})
	}
}

func TestAddIndexedSymbolFailuresAreSticky(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   []byte
		region []byte
		cause  error
	}{
		{name: "truncated", code: []byte{0x39, 0, 0}, region: []byte{0, 0}, cause: gvm.ErrTruncated},
		{name: "symbol", code: []byte{0x39, 1, 0, 1}, region: []byte{0, 0}, cause: gvm.ErrInvalidSymbol},
		{name: "odd-region", code: []byte{0x39, 0, 0, 1}, region: []byte{0}, cause: gvm.ErrInvalidSymbolRegion},
		{name: "element", code: []byte{0x39, 0, 1, 1}, region: []byte{0, 0}, cause: gvm.ErrInvalidElement},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithSymbols(test.code, 0, []gvm.SymbolRegion{{Length: uint32(len(test.region)), Initial: test.region}})
			if err != nil {
				t.Fatal(err)
			}
			err = v.Step()
			if !errors.Is(err, test.cause) {
				t.Fatalf("err=%v", err)
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("failure was not sticky")
			}
		})
	}
}
