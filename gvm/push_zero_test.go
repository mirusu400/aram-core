package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestPushZeroOpcode(t *testing.T) {
	vm := gvm.New([]byte{0x5a, 0xff})
	if err := vm.Run(2); err != nil {
		t.Fatal(err)
	}
	if stack := vm.Stack(); len(stack) != 1 || stack[0] != 0 {
		t.Fatalf("stack=%v", stack)
	}
}
