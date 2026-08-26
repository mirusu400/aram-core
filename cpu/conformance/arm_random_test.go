package conformance

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// TestARMJITRandomStraightLinePrograms differentially exercises the ARM block
// decoder over condition codes, all data-processing opcodes, immediate and
// register shifters, multiply/accumulate, and safe RAM transfers. Registers
// that hold control-flow/data bases are kept out of random destinations so
// every case reaches its BKPT and discrepancies describe instruction semantics
// rather than two equivalent early faults.
func TestARMJITRandomStraightLinePrograms(t *testing.T) {
	rng := rand.New(rand.NewSource(0xA4B10C))
	newJIT := func() cpu.Backend { return interpreter.NewJIT() }
	for iteration := 0; iteration < 1500; iteration++ {
		words := make([]uint32, 0, 14)
		for range 12 {
			condition := uint32(rng.Intn(15)) << 28
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5, 6:
				opcode := uint32(rng.Intn(16))
				rn := uint32(rng.Intn(6))
				rd := uint32(rng.Intn(6))
				setFlags := uint32(rng.Intn(2)) << 20
				var operand uint32
				if rng.Intn(2) == 0 {
					operand = 1<<25 | uint32(rng.Intn(16))<<8 | uint32(rng.Intn(256))
				} else if rng.Intn(3) == 0 {
					operand = uint32(rng.Intn(6)) | 1<<4 |
						uint32(rng.Intn(4))<<5 | uint32(rng.Intn(6))<<8
				} else {
					operand = uint32(rng.Intn(6)) | uint32(rng.Intn(4))<<5 |
						uint32(rng.Intn(32))<<7
				}
				words = append(words, condition|opcode<<21|setFlags|rn<<16|rd<<12|operand)
			case 7:
				// MUL/MLA r3,r0,r1[,r2], optionally setting N/Z.
				words = append(words, condition|uint32(rng.Intn(2))<<21|
					uint32(rng.Intn(2))<<20|3<<16|2<<12|1<<8|0x90)
			default:
				load := uint32(rng.Intn(2)) << 20
				byteTransfer := uint32(rng.Intn(2)) << 22
				rd := uint32(rng.Intn(6))
				offset := uint32(rng.Intn(64))
				// LDR/STR{B} rd,[r6,#offset], within captured RAM.
				words = append(words, condition|1<<26|1<<24|1<<23|
					byteTransfer|load|6<<16|rd<<12|offset)
			}
		}
		words = append(words, 0xe1200070) // bkpt
		regs := map[uint32]uint32{
			cpu.RegisterR6:   DataBase,
			cpu.RegisterCPSR: uint32(rng.Intn(16))<<28 | 0x1f,
		}
		for register := uint32(0); register < 6; register++ {
			regs[register] = rng.Uint32()
		}
		data := make([]byte, 64)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		program := Program{
			Name: fmt.Sprintf("arm-random/%d", iteration),
			Mode: cpu.ModeARM,
			Code: armCode(words...),
			Regs: regs,
			Data: map[uint32][]byte{DataBase: data},
		}
		oracle, err := Execute(interp, program)
		if err != nil {
			t.Fatalf("%s oracle: %v", program.Name, err)
		}
		subject, err := Execute(newJIT, program)
		if err != nil {
			t.Fatalf("%s JIT: %v", program.Name, err)
		}
		if difference := Diff(oracle, subject); difference != "" {
			t.Fatalf("%s diverged: %s", program.Name, difference)
		}
	}
}
