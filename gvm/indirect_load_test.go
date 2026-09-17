package gvm_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func indirectLoadVM(t *testing.T, code []byte, symbols [][]byte) *gvm.VM {
	t.Helper()
	regions := make([]gvm.SymbolRegion, len(symbols))
	for i := range symbols {
		regions[i] = gvm.SymbolRegion{Length: uint32(len(symbols[i])), Initial: symbols[i]}
	}
	v, err := gvm.NewWithSymbols(code, 0, regions)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestIndirectIndexedLoad(t *testing.T) {
	values := make([]byte, 8)
	for i, value := range []uint16{0x1111, 0x2222, 0xabcd, 0x4444} {
		binary.LittleEndian.PutUint16(values[2*i:], value)
	}
	index := []byte{2, 0}
	v := indirectLoadVM(t, []byte{0x05, 7, 0x02, 0, 1, 0xff}, [][]byte{values, index})
	if err := v.Run(2); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	if got := v.Stack(); !reflect.DeepEqual(got, []uint16{7, 0xabcd}) || v.PC() != 5 {
		t.Fatalf("pc=%d stack=%x", v.PC(), got)
	}
	if err := v.Step(); err != nil || !v.Halted() {
		t.Fatalf("halt: %v", err)
	}
}

func TestIndirectIndexedLoadFailuresAreSticky(t *testing.T) {
	tests := []struct {
		name    string
		code    []byte
		symbols [][]byte
		cause   error
	}{
		{name: "truncated", code: []byte{0x02, 0}, cause: gvm.ErrTruncated},
		{name: "value-symbol", code: []byte{0x02, 1, 0}, symbols: [][]byte{{0, 0}}, cause: gvm.ErrInvalidSymbol},
		{name: "index-symbol", code: []byte{0x02, 0, 1}, symbols: [][]byte{{0, 0}}, cause: gvm.ErrInvalidSymbol},
		{name: "short-index", code: []byte{0x02, 0, 1}, symbols: [][]byte{{0, 0}, {0}}, cause: gvm.ErrInvalidSymbolRegion},
		{name: "odd-values", code: []byte{0x02, 0, 1}, symbols: [][]byte{{0}, {0, 0}}, cause: gvm.ErrInvalidSymbolRegion},
		{name: "negative-index", code: []byte{0x02, 0, 1}, symbols: [][]byte{{0, 0}, {0xff, 0xff}}, cause: gvm.ErrInvalidElement},
		{name: "large-index", code: []byte{0x02, 0, 1}, symbols: [][]byte{{0, 0}, {1, 0}}, cause: gvm.ErrInvalidElement},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v := indirectLoadVM(t, test.code, test.symbols)
			before := v.Stack()
			err := v.Step()
			if !errors.Is(err, test.cause) || !reflect.DeepEqual(v.Stack(), before) {
				t.Fatalf("err=%v stack=%x", err, v.Stack())
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("failure was not sticky")
			}
		})
	}
}
