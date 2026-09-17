package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestStoreTopToSymbolPreservesStack(t *testing.T) {
	vm, err := gvm.NewWithSymbols([]byte{0x05, 0xfe, 0x4b, 0, 0xff}, 0, []gvm.SymbolRegion{{Length: 2, Initial: make([]byte, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	region, _ := vm.Symbol(0)
	if got := int16(binary.LittleEndian.Uint16(region)); got != -2 || len(vm.Stack()) != 1 || int16(vm.Stack()[0]) != -2 {
		t.Fatalf("symbol=%d stack=%v", got, vm.Stack())
	}
}
