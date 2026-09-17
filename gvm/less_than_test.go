package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSignedLessThan(t *testing.T) {
	tests := []struct {
		name string
		a    uint16
		b    uint16
		want uint16
	}{
		{name: "positive", a: 1, b: 2, want: 1},
		{name: "reverse", a: 2, b: 1},
		{name: "equal", a: 7, b: 7},
		{name: "negative-positive", a: 0xffff, b: 0, want: 1},
		{name: "positive-negative", a: 0, b: 0xffff},
		{name: "minimum-maximum", a: 0x8000, b: 0x7fff, want: 1},
		{name: "maximum-minimum", a: 0x7fff, b: 0x8000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code := icPush(nil, 0x55aa)
			code = icPush(code, test.a)
			code = icPush(code, test.b)
			code = append(code, 0x1e, 0xff)
			v := gvm.New(code)
			if err := v.Run(4); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			if got := v.Stack(); !reflect.DeepEqual(got, []uint16{0x55aa, test.want}) {
				t.Fatalf("stack=%x want=[55aa %x]", got, test.want)
			}
			if err := v.Step(); err != nil || !v.Halted() {
				t.Fatalf("halt: %v", err)
			}
		})
	}
}

func TestSignedLessThanStickyUnderflow(t *testing.T) {
	for _, code := range [][]byte{{0x1e}, {0x05, 1, 0x1e}} {
		v := gvm.New(code)
		if len(code) > 1 {
			if err := v.Step(); err != nil {
				t.Fatal(err)
			}
		}
		before := v.Stack()
		err := v.Step()
		if !errors.Is(err, gvm.ErrStackUnderflow) || !reflect.DeepEqual(v.Stack(), before) {
			t.Fatalf("err=%v stack=%x", err, v.Stack())
		}
		if v.Step() != err || v.Run(1) != err {
			t.Fatal("underflow was not sticky")
		}
	}
}

func TestSignedLessOrEqual(t *testing.T) {
	for _, test := range []struct {
		a, b uint16
		want uint16
	}{
		{a: 1, b: 2, want: 1},
		{a: 2, b: 2, want: 1},
		{a: 3, b: 2},
		{a: 0xffff, b: 0, want: 1},
		{a: 0, b: 0xffff},
		{a: 0x8000, b: 0x7fff, want: 1},
	} {
		code := icPush(nil, test.a)
		code = icPush(code, test.b)
		code = append(code, 0x20)
		v := gvm.New(code)
		if err := v.Run(3); err != gvm.ErrBudget || !reflect.DeepEqual(v.Stack(), []uint16{test.want}) {
			t.Fatalf("a=%x b=%x err=%v stack=%x", test.a, test.b, err, v.Stack())
		}
	}
}
