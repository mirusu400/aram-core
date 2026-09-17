package gvm_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestCopyArrayElementToSymbol(t *testing.T) {
	source := make([]byte, 6)
	binary.LittleEndian.PutUint16(source[4:6], 0xbeef)
	vm, err := gvm.NewWithSymbols([]byte{0x34, 0, 1, 2, 0xff}, 0, []gvm.SymbolRegion{
		{Length: 2, Initial: make([]byte, 2)},
		{Length: 6, Initial: source},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(1); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	destination, _ := vm.Symbol(0)
	if got := binary.LittleEndian.Uint16(destination); got != 0xbeef || vm.PC() != 4 {
		t.Fatalf("destination=%04x pc=%d", got, vm.PC())
	}
}
