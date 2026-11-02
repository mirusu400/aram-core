package gvm

import (
	"bytes"
	"errors"
	"testing"
)

func TestVolatileStateRoundTripAndAtomicValidation(t *testing.T) {
	v := New([]byte{0x05, 7, 0xff})
	if err := v.Step(); err != nil {
		t.Fatal(err)
	}
	saved, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Step(); err != nil || !v.Halted() {
		t.Fatalf("mutate: halted=%v err=%v", v.Halted(), err)
	}
	if err := v.Restore(saved); err != nil {
		t.Fatal(err)
	}
	if v.PC() != 2 || v.Halted() || !bytes.Equal(v.Program(), []byte{0x05, 7, 0xff}) ||
		len(v.Stack()) != 1 || v.Stack()[0] != 7 {
		t.Fatalf("restored VM pc=%d halted=%v stack=%v program=%x", v.PC(), v.Halted(), v.Stack(), v.Program())
	}
	invalid := saved
	invalid.Program = invalid.Program[:2]
	if err := v.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("invalid restore: %v", err)
	}
	if v.PC() != 2 || v.Halted() || v.Stack()[0] != 7 {
		t.Fatal("invalid restore partially mutated VM")
	}
	if err := v.Step(); err != nil || !v.Halted() {
		t.Fatalf("resumed VM halted=%v err=%v", v.Halted(), err)
	}
}

func TestVolatileStatePreservesGrowingRestoreBacking(t *testing.T) {
	// Save a two-word top, pop the second word through the verified ret-only
	// opcode, snapshot, then prove that 0c can re-expose it after restoration.
	code := []byte{0x06, 0x11, 0x11, 0x06, 0x22, 0x22, 0x0b, 0x96, 0x0c, 0xff}
	source := New(code)
	if err := source.Run(4); err != ErrBudget {
		t.Fatalf("prime hidden stack: %v", err)
	}
	if got := source.Stack(); len(got) != 1 || got[0] != 0x1111 {
		t.Fatalf("popped public stack = %x", got)
	}
	saved, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	restored := New(code)
	if err := restored.Restore(saved); err != nil {
		t.Fatal(err)
	}
	if err := restored.Step(); err != nil {
		t.Fatal(err)
	}
	if got := restored.Stack(); len(got) != 2 || got[0] != 0x1111 || got[1] != 0x2222 {
		t.Fatalf("restored growing stack = %x", got)
	}
}
