package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSignedMinimum(t *testing.T) {
	for _, tc := range []struct {
		name string
		code []byte
		want uint16
	}{
		{"top", []byte{0x05, 10, 0x05, 0xfe, 0xaf}, 0xfffe},
		{"below", []byte{0x05, 0xfe, 0x05, 10, 0xaf}, 0xfffe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := gvm.New(tc.code)
			if err := v.Run(3); !errors.Is(err, gvm.ErrBudget) {
				t.Fatalf("Run = %v", err)
			}
			if got := v.Stack(); !reflect.DeepEqual(got, []uint16{tc.want}) {
				t.Fatalf("stack = %v", got)
			}
		})
	}
}

func TestSignedMaximum(t *testing.T) {
	v := gvm.New([]byte{0x05, 0xfe, 0x05, 10, 0xad})
	if err := v.Run(3); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if got := v.Stack(); !reflect.DeepEqual(got, []uint16{10}) {
		t.Fatalf("stack = %v", got)
	}
}
