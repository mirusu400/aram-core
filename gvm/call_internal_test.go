package gvm

import (
	"errors"
	"testing"
)

// Supplemental internal check: no return/snapshot API is invented merely to
// expose saved PCs while empty-return sentinel semantics remain deferred.
func TestCallStoresPostOperandPC(t *testing.T) {
	v := New([]byte{0x44, 0, 3, 0x44, 0, 6, 0xff})
	if err := v.Run(3); err != nil {
		t.Fatal(err)
	}
	if v.returnDepth != 2 || v.returns[0] != 3 || v.returns[1] != 6 {
		t.Fatalf("saved PCs: %v depth %d", v.returns, v.returnDepth)
	}
	v = New([]byte{0x44, 0, 3})
	if err := v.Step(); !errors.Is(err, ErrInvalidTarget) || v.returnDepth != 0 {
		t.Fatalf("bad call changed return stack: %v", err)
	}
}
