package interpreter

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestARMConditionTableCoversEveryNZCVState(t *testing.T) {
	backend := New()
	for condition := uint8(0); condition < 16; condition++ {
		for nzcv := uint32(0); nzcv < 16; nzcv++ {
			backend.regs[cpu.RegisterCPSR] = nzcv << 28
			got := backend.conditionPassed(condition)
			n := nzcv&8 != 0
			z := nzcv&4 != 0
			c := nzcv&2 != 0
			v := nzcv&1 != 0
			var want bool
			switch condition {
			case 0x0:
				want = z
			case 0x1:
				want = !z
			case 0x2:
				want = c
			case 0x3:
				want = !c
			case 0x4:
				want = n
			case 0x5:
				want = !n
			case 0x6:
				want = v
			case 0x7:
				want = !v
			case 0x8:
				want = c && !z
			case 0x9:
				want = !c || z
			case 0xa:
				want = n == v
			case 0xb:
				want = n != v
			case 0xc:
				want = !z && n == v
			case 0xd:
				want = z || n != v
			case 0xe:
				want = true
			}
			if got != want {
				t.Fatalf("condition %#x NZCV %#x = %v, want %v", condition, nzcv, got, want)
			}
		}
	}
}

func TestARMConditionTableResolvesDeferredFlags(t *testing.T) {
	backend := New()
	backend.setNZCV(0, true, true)
	if !backend.conditionPassed(0x0) || !backend.conditionPassed(0x2) || !backend.conditionPassed(0x6) {
		t.Fatal("deferred Z/C/V flags were not visible to condition lookup")
	}
}
