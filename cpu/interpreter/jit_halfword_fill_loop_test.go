package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func newThumbHalfwordFillTestBackend(t *testing.T, backend *Backend, destination uint32) *Backend {
	t.Helper()
	backend = newThumbStackPaletteTestBackend(t, backend, 0x5000)
	code := make([]byte, 0x100)
	for index, word := range thumbHalfwordFillLoopWords {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	check(t, backend.WriteMemory(0x1000, code))
	for register, value := range map[uint32]uint32{
		3: 0x12345678, 4: 3, 5: destination, 6: 0xabcd, 7: 0x7000,
	} {
		check(t, backend.WriteRegister(register, value))
	}
	return backend
}

func TestThumbHalfwordFillLoopMatchesPreciseInterpreter(t *testing.T) {
	for _, budget := range []uint64{1, 2, 3, 9, 10, 11, 20, 21, 39, 40, 41} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise := newThumbHalfwordFillTestBackend(t, New(), 0x5000)
			translated := newThumbHalfwordFillTestBackend(t, NewJIT(), 0x5000)
			assertThumbFillParity(t, precise, translated, 0x1000, budget)
			if budget == 40 {
				stats := translated.ExecutionStatistics()
				if stats.AcceleratedLoopIterations != 4 || stats.AcceleratedLoopInstructions != 40 {
					t.Fatalf("halfword fill statistics: %+v", stats)
				}
			}
		})
	}
}

func TestThumbHalfwordFillLoopAliasesAndFaults(t *testing.T) {
	for _, destination := range []uint32{0x7074, 0x50fe, 0x8000, 0x5001} {
		t.Run(fmt.Sprintf("%08x", destination), func(t *testing.T) {
			precise := newThumbHalfwordFillTestBackend(t, New(), destination)
			translated := newThumbHalfwordFillTestBackend(t, NewJIT(), destination)
			assertThumbFillParity(t, precise, translated, 0x1000, 40)
		})
	}
}

func TestThumbHalfwordFillLoopCounterReachesMinusOne(t *testing.T) {
	precise := newThumbHalfwordFillTestBackend(t, New(), 0x5000)
	translated := newThumbHalfwordFillTestBackend(t, NewJIT(), 0x5000)
	check(t, precise.WriteRegister(4, 0))
	check(t, translated.WriteRegister(4, 0))
	assertThumbFillParity(t, precise, translated, 0x1000, 10)
}

func TestThumbHalfwordFillLoopRequiresExactCode(t *testing.T) {
	for index := range thumbHalfwordFillLoopWords {
		backend := newThumbHalfwordFillTestBackend(t, NewJIT(), 0x5000)
		address := 0x1000 + uint32(index)*2
		var word [2]byte
		check(t, backend.ReadMemory(address, word[:]))
		binary.LittleEndian.PutUint16(word[:], binary.LittleEndian.Uint16(word[:])^1)
		check(t, backend.WriteMemory(address, word[:]))
		if backend.classifyThumbHalfwordFillLoop(0x1000) != nil {
			t.Fatalf("changed instruction %d classified", index)
		}
	}
}

func TestThumbHalfwordFillLoopFlagAndObservationGuards(t *testing.T) {
	backend := newThumbHalfwordFillTestBackend(t, NewJIT(), 0x5000)
	loop := backend.classifyThumbHalfwordFillLoop(0x1000)
	for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if retired := backend.accelerateThumbHalfwordFillLoop(loop, 40, flags[0], flags[1], flags[2]); retired != 0 {
			t.Fatalf("observed execution retired %d instructions", retired)
		}
	}
	var one [4]byte
	binary.LittleEndian.PutUint32(one[:], 1)
	check(t, backend.WriteMemory(0x7074, one[:]))
	if retired := backend.accelerateThumbHalfwordFillLoop(loop, 40, false, false, false); retired != 0 {
		t.Fatalf("nonzero flag retired %d instructions", retired)
	}
}
