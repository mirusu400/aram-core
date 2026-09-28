package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestNotEqualComparesRawWords(t *testing.T) {
	vm := gvm.New([]byte{0x05, 1, 0x05, 2, 0x22, 0x05, 1, 0x05, 1, 0x22, 0xff})
	if err := vm.Run(7); err != nil || len(vm.Stack()) != 2 || vm.Stack()[0] != 1 || vm.Stack()[1] != 0 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}
