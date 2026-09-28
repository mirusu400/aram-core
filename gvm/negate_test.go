package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestNegateTopWord(t *testing.T) {
	vm := gvm.New([]byte{0x05, 7, 0x10, 0xff})
	if err := vm.Run(3); err != nil || len(vm.Stack()) != 1 || vm.Stack()[0] != 0xfff9 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
	if err := gvm.New([]byte{0x10}).Step(); !errors.Is(err, gvm.ErrStackUnderflow) {
		t.Fatalf("empty stack error = %v", err)
	}
}
