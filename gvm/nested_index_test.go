package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestNestedIndexReadAndFixedElementCopy(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{
		0x01, 0, 1, 0, // push values[indices[0]]
		0x30, 0, 1, 2, // values[1] = source[0]
		0x03, 0, 1, 0xff,
	}, 0, []gvm.SymbolRegion{
		{Length: 6, Initial: []byte{0x10, 0, 0x20, 0, 0x30, 0}},
		{Length: 2, Initial: []byte{2, 0}},
		{Length: 2, Initial: []byte{0xef, 0xbe}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = vm.Run(4)
	stack := vm.Stack()
	if err != nil || len(stack) != 2 || stack[0] != 0x30 || stack[1] != 0xbeef {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}
