package interpreter

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestArithmeticFlagsMatchWideSignedAndUnsignedOracles(t *testing.T) {
	values := []uint32{0, 1, 2, 0x7ffffffe, 0x7fffffff, 0x80000000, 0x80000001, 0xfffffffe, 0xffffffff}
	seed := uint32(0x12345678)
	for range 128 {
		seed = seed*1664525 + 1013904223
		values = append(values, seed)
	}
	backend := new(Backend)
	const controlBits = uint32(0x0100003f)
	for _, left := range values {
		for _, right := range values {
			for carryIn := uint32(0); carryIn <= 1; carryIn++ {
				wideUnsigned := uint64(left) + uint64(right) + uint64(carryIn)
				wideSigned := int64(int32(left)) + int64(int32(right)) + int64(carryIn)
				wantResult := uint32(wideUnsigned)
				wantCarry := wideUnsigned > 0xffffffff
				wantOverflow := wideSigned < -0x80000000 || wideSigned > 0x7fffffff
				result, carry, overflow := addWithCarry(left, right, carryIn)
				if result != wantResult || carry != wantCarry || overflow != wantOverflow {
					t.Fatalf("add(%08x,%08x,%d) = %08x,%t,%t; want %08x,%t,%t", left, right, carryIn, result, carry, overflow, wantResult, wantCarry, wantOverflow)
				}
				wantFlags := controlBits
				if int32(result) < 0 {
					wantFlags |= flagN
				}
				if result == 0 {
					wantFlags |= flagZ
				}
				if wantCarry {
					wantFlags |= flagC
				}
				if wantOverflow {
					wantFlags |= flagV
				}
				backend.regs[cpu.RegisterCPSR] = controlBits
				backend.setNZCV(result, carry, overflow)
				backend.resolveFlags()
				if got := backend.regs[cpu.RegisterCPSR]; got != wantFlags {
					t.Fatalf("CPSR = %08x, want %08x", got, wantFlags)
				}
			}
		}
	}
}

func TestNZBitsAcrossZeroAndSignBoundaries(t *testing.T) {
	backend := new(Backend)
	const preserved = flagC | flagV | uint32(0x0100003f)
	for _, test := range []struct{ value, want uint32 }{
		{0, flagZ}, {1, 0}, {0x7fffffff, 0}, {0x80000000, flagN}, {0xffffffff, flagN},
	} {
		if got := nzBits(test.value); got != test.want {
			t.Fatalf("nzBits(%08x) = %08x, want %08x", test.value, got, test.want)
		}
		backend.regs[cpu.RegisterCPSR] = preserved | flagN | flagZ
		backend.setNZ(test.value)
		backend.resolveFlags()
		if got := backend.regs[cpu.RegisterCPSR]; got != preserved|test.want {
			t.Fatalf("setNZ(%08x) CPSR = %08x, want %08x", test.value, got, preserved|test.want)
		}
	}
}
