package gnex_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
	"github.com/mirusu400/aram-core/loader/gnex"
)

// This independently authored buffer is a decoder/kernel contract, not a MIDlet,
// ordinary product launch, or an assertion that reserved GVM services exist.
func TestExecutionImageKernelMemoryContract(t *testing.T) {
	for _, allocated := range []bool{false, true} {
		name := "alias"
		if allocated {
			name = "allocated"
		}
		t.Run(name, func(t *testing.T) {
			b := make([]byte, 136)
			b[0], b[2], b[5], b[10] = 2, 12, 1, 'A'
			for o, v := range map[int]uint16{0x1c: 54, 0x2c: 128, 0x2e: 132, 0x30: 136, 0x32: 136} {
				binary.LittleEndian.PutUint16(b[o:], v)
			}
			copy(b[54:], []byte{0x44, 0, 100, 0xff})
			copy(b[100:], []byte{0x31, 0, 1, 0x7f, 0x45})
			if allocated {
				b[128] = 1
			}
			b[129], b[130] = 2, 1
			copy(b[132:], []byte{0x11, 0x22, 0x33, 0x44})
			original := bytes.Clone(b)
			image, err := gnex.DecodeExecutionImage(b)
			if err != nil {
				t.Fatal(err)
			}
			symbol := image.Symbols[0]
			region := gvm.SymbolRegion{Length: uint32(len(symbol.Data))}
			if symbol.BufferOffset >= 0 {
				offset := uint32(symbol.BufferOffset)
				region.ProgramOffset = &offset
			} else {
				region.Initial = symbol.Data
			}
			vm, err := gvm.NewWithSymbols(image.Buffer, uint32(image.Entry), []gvm.SymbolRegion{region})
			if err != nil {
				t.Fatal(err)
			}
			if err = vm.Run(3); !errors.Is(err, gvm.ErrBudget) {
				t.Fatalf("before ff: %v", err)
			}
			if err = vm.Run(1); err != nil || !vm.Halted() || vm.PC() != 58 {
				t.Fatalf("dispatch exit pc=%d err=%v", vm.PC(), err)
			}
			actual, err := vm.Symbol(0)
			if err != nil {
				t.Fatal(err)
			}
			want := []byte{0x11, 0x22, 0x7f, 0}
			if !bytes.Equal(actual, want) {
				t.Fatalf("symbol %x", actual)
			}
			program := vm.Program()
			if allocated {
				if !bytes.Equal(program[132:], original[132:]) {
					t.Fatal("RAM store modified program")
				}
			} else if !bytes.Equal(program[132:], want) {
				t.Fatal("program alias lost across decoder boundary")
			}
			if !bytes.Equal(b, original) || !bytes.Equal(image.Buffer, original) {
				t.Fatal("kernel mutated caller input")
			}
		})
	}
}
