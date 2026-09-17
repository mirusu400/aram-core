package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSignedAbsolute(t *testing.T) {
	vm := gvm.New([]byte{0x05, 0xfe, 0xa3, 0xff})
	if err := vm.Run(3); err != nil {
		t.Fatal(err)
	}
	if stack := vm.Stack(); len(stack) != 1 || stack[0] != 2 {
		t.Fatalf("stack=%v", stack)
	}
}
