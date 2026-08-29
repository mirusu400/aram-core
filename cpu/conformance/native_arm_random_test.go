//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package conformance

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestNativeARMRandomStraightLinePrograms is the native counterpart of
// TestARMJITRandomStraightLinePrograms. The host ARM emitter covers a much
// larger encoding surface than the fixed corpus reaches - shifted and
// register-specified shifter operands, carry arithmetic, multiply/accumulate,
// every LDR/STR indexing form, and the four LDM/STM addressing modes - and each
// of those is a hand-written host encoding whose only oracle is the
// interpreter. Randomizing over the families keeps a wrong flag, a wrong
// writeback, or a wrong transfer order from surviving because no fixed case
// happened to name it.
//
// Register roles are pinned so a discrepancy describes instruction semantics
// rather than two different early faults: r6 is the only transfer base and it
// always addresses the captured scratch region, and only r0..r5 are randomized
// destinations.
func TestNativeARMRandomStraightLinePrograms(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5E9D1A))
	var completed int
	for iteration := 0; iteration < 1500; iteration++ {
		words := make([]uint32, 0, 14)
		for range 12 {
			condition := uint32(rng.Intn(15)) << 28
			switch rng.Intn(12) {
			case 0, 1, 2, 3, 4, 5:
				words = append(words, condition|armRandomDataProcessing(rng))
			case 6:
				// MUL/MLA r3,r0,r1[,r2], optionally setting N/Z.
				words = append(words, condition|uint32(rng.Intn(2))<<21|
					uint32(rng.Intn(2))<<20|3<<16|2<<12|1<<8|0x90)
			case 7, 8:
				words = append(words, condition|armRandomSingleTransfer(rng))
			case 9:
				words = append(words, condition|armRandomHalfwordTransfer(rng))
			default:
				words = append(words, condition|armRandomBlockTransfer(rng))
			}
		}
		words = append(words, 0xe1200070) // bkpt
		regs := map[uint32]uint32{
			cpu.RegisterR6:   DataBase + 0x20,
			cpu.RegisterCPSR: uint32(rng.Intn(16))<<28 | 0x1f,
		}
		for register := uint32(0); register < 6; register++ {
			regs[register] = rng.Uint32()
		}
		data := make([]byte, 128)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		program := Program{
			Name:     fmt.Sprintf("native-arm-random/%d", iteration),
			Mode:     cpu.ModeARM,
			Code:     armCode(words...),
			Regs:     regs,
			Data:     map[uint32][]byte{DataBase: data},
			CaptureN: 128,
		}
		oracle, err := Execute(interp, program)
		if err != nil {
			t.Fatalf("%s oracle: %v", program.Name, err)
		}
		subject, err := Execute(newNative, program)
		if err != nil {
			t.Fatalf("%s native: %v", program.Name, err)
		}
		if difference := Diff(oracle, subject); difference != "" {
			t.Fatalf("%s diverged: %s\ncode %v", program.Name, difference, words)
		}
		if oracle.Reason == cpu.StopBreakpoint {
			completed++
		}
	}
	// A generator that drifts its transfer base out of the scratch region would
	// fault after two or three instructions and still pass, having compared
	// almost nothing. Require most programs to reach their terminating BKPT so
	// the corpus cannot silently degenerate.
	if completed < 1200 {
		t.Fatalf("only %d/1500 programs ran to completion", completed)
	}
}

// armRandomDataProcessing builds one data-processing instruction over all
// sixteen opcodes and all three shifter-operand forms.
func armRandomDataProcessing(rng *rand.Rand) uint32 {
	opcode := uint32(rng.Intn(16))
	rn := uint32(rng.Intn(6))
	rd := uint32(rng.Intn(6))
	setFlags := uint32(rng.Intn(2)) << 20
	var operand uint32
	switch rng.Intn(3) {
	case 0: // rotated immediate
		operand = 1<<25 | uint32(rng.Intn(16))<<8 | uint32(rng.Intn(256))
	case 1: // register-specified shift
		operand = uint32(rng.Intn(6)) | 1<<4 |
			uint32(rng.Intn(4))<<5 | uint32(rng.Intn(6))<<8
	default: // immediate register shift
		operand = uint32(rng.Intn(6)) | uint32(rng.Intn(4))<<5 |
			uint32(rng.Intn(32))<<7
	}
	return opcode<<21 | setFlags | rn<<16 | rd<<12 | operand
}

// armRandomSingleTransfer builds an LDR/STR{B} through r6 over the pre/post,
// up/down, writeback, and immediate/shifted-register offset forms. The offset
// range keeps every address inside the captured scratch window.
func armRandomSingleTransfer(rng *rand.Rand) uint32 {
	load := uint32(rng.Intn(2)) << 20
	byteTransfer := uint32(rng.Intn(2)) << 22
	preIndex := uint32(rng.Intn(2)) << 24
	up := uint32(rng.Intn(2)) << 23
	rd := uint32(rng.Intn(6))
	writeback := uint32(0)
	if preIndex != 0 {
		writeback = uint32(rng.Intn(2)) << 21
	}
	word := 1<<26 | preIndex | up | byteTransfer | writeback | load | 6<<16 | rd<<12
	if rng.Intn(2) == 0 {
		return word | uint32(rng.Intn(16))
	}
	// Shifted register offset: r7 holds a small index the caller never writes.
	return word | 1<<25 | 7 | uint32(rng.Intn(4))<<5 | uint32(rng.Intn(3))<<7
}

// armRandomHalfwordTransfer builds LDRH/STRH/LDRSB/LDRSH through r6.
func armRandomHalfwordTransfer(rng *rand.Rand) uint32 {
	load := uint32(rng.Intn(2)) << 20
	operation := uint32(1)
	if load != 0 {
		operation = uint32(1 + rng.Intn(3))
	}
	preIndex := uint32(rng.Intn(2)) << 24
	up := uint32(rng.Intn(2)) << 23
	rd := uint32(rng.Intn(6))
	writeback := uint32(0)
	if preIndex != 0 {
		writeback = uint32(rng.Intn(2)) << 21
	}
	word := preIndex | up | writeback | load | 6<<16 | rd<<12 | operation<<5 | 0x90
	if rng.Intn(2) == 0 {
		offset := uint32(rng.Intn(16))
		return word | 1<<22 | offset&0xf | offset&0xf0<<4
	}
	return word | 7 // register offset in r7
}

// armRandomBlockTransfer builds an LDM/STM through r6 in all four addressing
// modes, with and without writeback, over register lists confined to r0..r5.
func armRandomBlockTransfer(rng *rand.Rand) uint32 {
	list := uint32(rng.Intn(0x3f))
	if list == 0 {
		list = 1
	}
	load := uint32(rng.Intn(2)) << 20
	return 1<<27 | uint32(rng.Intn(2))<<24 | uint32(rng.Intn(2))<<23 |
		uint32(rng.Intn(2))<<21 | load | 6<<16 | list
}
