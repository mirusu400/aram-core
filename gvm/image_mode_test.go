package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestImageModeConsumesTopWord(t *testing.T) {
	v := gvm.New([]byte{0x05, 0x02, 0xba, 0xff})
	if err := v.Run(4); err != nil {
		t.Fatal(err)
	}
	if !v.Halted() || len(v.Stack()) != 0 {
		t.Fatalf("halted=%t stack=%v", v.Halted(), v.Stack())
	}
}

func TestImageModeRequiresWord(t *testing.T) {
	v := gvm.New([]byte{0xba})
	if err := v.Step(); !errors.Is(err, gvm.ErrStackUnderflow) {
		t.Fatalf("Step = %v", err)
	}
}
