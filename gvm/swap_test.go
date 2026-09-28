package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestSwapTopWords(t *testing.T) {
	vm := gvm.New([]byte{0x05, 7, 0x05, 9, 0x11, 0xff})
	if err := vm.Run(4); err != nil {
		t.Fatal(err)
	}
	if got, want := vm.Stack(), []uint16{9, 7}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stack = %v, want %v", got, want)
	}
	for _, program := range [][]byte{{0x11}, {0x05, 1, 0x11}} {
		vm := gvm.New(program)
		if err := vm.Run(3); !errors.Is(err, gvm.ErrStackUnderflow) {
			t.Fatalf("program %x error = %v", program, err)
		}
	}
}
