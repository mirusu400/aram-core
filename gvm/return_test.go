package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestNonemptyReturn(t *testing.T) {
	v := gvm.New([]byte{0x44, 0, 4, 0xff, 0x44, 0, 8, 0x45, 0x45})
	if err := v.Run(5); err != nil || !v.Halted() || v.PC() != 4 {
		t.Fatalf("nested return: %v pc%d", err, v.PC())
	}
	// A save immediately after the final byte is allowed by call, but a return
	// cannot fetch there. Bounds failure leaves the current return stack intact.
	v, err := gvm.NewAt([]byte{0x45, 0x44, 0, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	err = v.Step()
	if !errors.Is(err, gvm.ErrInvalidTarget) || v.PC() != 1 || v.Step() != err {
		t.Fatalf("bad return target: %v pc%d", err, v.PC())
	}
	v = gvm.New([]byte{0x45})
	err = v.Step()
	var unsupported *gvm.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) || unsupported.Opcode != 0x45 || unsupported.Offset != 0 || v.PC() != 1 || v.Halted() {
		t.Fatalf("empty return sentinel must remain unsupported: %v", err)
	}
}
