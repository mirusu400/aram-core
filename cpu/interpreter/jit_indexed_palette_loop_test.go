package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func newThumbIndexedPaletteTestBackend(t *testing.T, backend *Backend, destination uint32) *Backend {
	t.Helper()
	backend = newThumbStackPaletteTestBackend(t, backend, 0x5000)
	code := make([]byte, 0x100)
	for index, word := range thumbIndexedPaletteLoopWords {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	check(t, backend.WriteMemory(0x1000, code))
	check(t, backend.WriteMemory(0x3000, []byte{0, 0, 1, 2, 3}))
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], 0x4000)
	check(t, backend.WriteMemory(0x6038, pointer[:]))
	for register, value := range map[uint32]uint32{
		0: 0x12345678, 1: 3, 2: destination, 3: 7,
		5: 0x3000, 12: 2,
	} {
		check(t, backend.WriteRegister(register, value))
	}
	return backend
}

func TestThumbIndexedPaletteLoopMatchesPreciseInterpreter(t *testing.T) {
	for _, budget := range []uint64{1, 2, 5, 10, 11, 12, 22, 32, 33, 34} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise := newThumbIndexedPaletteTestBackend(t, New(), 0x5000)
			translated := newThumbIndexedPaletteTestBackend(t, NewJIT(), 0x5000)
			assertThumbFillParity(t, precise, translated, 0x1000, budget)
			if budget == 33 {
				stats := translated.ExecutionStatistics()
				if stats.AcceleratedLoopIterations != 3 || stats.AcceleratedLoopInstructions != 33 {
					t.Fatalf("indexed palette statistics: %+v", stats)
				}
			}
		})
	}
}

func TestThumbIndexedPaletteLoopAliasesAndFaults(t *testing.T) {
	for _, destination := range []uint32{0x6038, 0x3002, 0x4002, 0x1000, 0x50fe, 0x8000, 0x5001} {
		t.Run(fmt.Sprintf("%08x", destination), func(t *testing.T) {
			precise := newThumbIndexedPaletteTestBackend(t, New(), destination)
			translated := newThumbIndexedPaletteTestBackend(t, NewJIT(), destination)
			assertThumbFillParity(t, precise, translated, 0x1000, 33)
		})
	}
}

func TestThumbIndexedPaletteLoopRequiresExactCode(t *testing.T) {
	for index := range thumbIndexedPaletteLoopWords {
		backend := newThumbIndexedPaletteTestBackend(t, NewJIT(), 0x5000)
		address := 0x1000 + uint32(index)*2
		var word [2]byte
		check(t, backend.ReadMemory(address, word[:]))
		binary.LittleEndian.PutUint16(word[:], binary.LittleEndian.Uint16(word[:])^1)
		check(t, backend.WriteMemory(address, word[:]))
		if backend.classifyThumbIndexedPaletteLoop(0x1000) != nil {
			t.Fatalf("changed instruction %d classified", index)
		}
	}
}

func TestThumbIndexedPaletteLoopObservationGuards(t *testing.T) {
	backend := newThumbIndexedPaletteTestBackend(t, NewJIT(), 0x5000)
	loop := backend.classifyThumbIndexedPaletteLoop(0x1000)
	for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if retired := backend.accelerateThumbIndexedPaletteLoop(loop, 33, flags[0], flags[1], flags[2]); retired != 0 {
			t.Fatalf("observed execution retired %d instructions", retired)
		}
	}
}
