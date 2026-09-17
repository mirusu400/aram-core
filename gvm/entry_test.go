package gvm_test

import (
	"errors"
	"github.com/mirusu400/aram-core/gvm"
	"testing"
)

func TestExplicitEntry(t *testing.T) {
	code := []byte{0xff, 0xee, 0x41, 0, 0}
	v, err := gvm.NewAt(code, 2)
	if err != nil {
		t.Fatal(err)
	}
	code[2] = 0xee
	if err = v.Run(2); err != nil || v.PC() != 1 || !v.Halted() {
		t.Fatalf("entry/base: %v pc %d", err, v.PC())
	}
	for _, entry := range []uint32{5, 6, ^uint32(0)} {
		if v, err := gvm.NewAt(code, entry); v != nil || !errors.Is(err, gvm.ErrInvalidTarget) {
			t.Fatalf("entry %d: %v", entry, err)
		}
	}
	if v, err := gvm.NewAt(nil, 0); v != nil || !errors.Is(err, gvm.ErrInvalidTarget) {
		t.Fatalf("empty: %v", err)
	}
}
