package gvm_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestHashQualifiedPopOnly(t *testing.T) {
	for _, op := range []byte{0x96, 0x97} {
		t.Run(fmt.Sprintf("%02x", op), func(t *testing.T) {
			v := gvm.New([]byte{5, 9, 6, 0x80, 0, op, 0xff})
			if err := v.Run(3); !errors.Is(err, gvm.ErrBudget) || v.PC() != 6 || !reflect.DeepEqual(v.Stack(), []uint16{9}) {
				t.Fatalf("pop-only: %v pc%d stack%x", err, v.PC(), v.Stack())
			}
			if err := v.Run(1); err != nil || !v.Halted() {
				t.Fatalf("exit: %v", err)
			}
			v = gvm.New([]byte{op})
			err := v.Step()
			if !errors.Is(err, gvm.ErrStackUnderflow) || v.PC() != 1 || len(v.Stack()) != 0 || v.Halted() || v.Step() != err {
				t.Fatalf("underflow: %v pc%d", err, v.PC())
			}
		})
	}
}
