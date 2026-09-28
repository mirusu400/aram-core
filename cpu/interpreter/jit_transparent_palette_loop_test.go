package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func newThumbTransparentPaletteTestBackend(t *testing.T, backend *Backend, reverse bool, destination uint32) *Backend {
	t.Helper()
	backend = newThumbStackPaletteTestBackend(t, backend, 0x5000)
	words := thumbTransparentPaletteLoopWords
	if reverse {
		words[10] = 0x3a02
	}
	code := make([]byte, 0x100)
	for index, word := range words {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	check(t, backend.WriteMemory(0x1000, code))
	check(t, backend.WriteMemory(0x3000, []byte{1, 0, 2}))
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], 0x4000)
	check(t, backend.WriteMemory(0x6048, pointer[:]))
	for register, value := range map[uint32]uint32{
		0: 0xdeadbeef, 2: destination, 3: 7, 4: 3, 6: 0, 12: 0x3000,
	} {
		check(t, backend.WriteRegister(register, value))
	}
	return backend
}

func TestThumbTransparentPaletteLoopMatchesPreciseInterpreter(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, budget := range []uint64{1, 4, 9, 12, 13, 14, 22, 23, 34, 35, 36} {
			t.Run(fmt.Sprintf("reverse=%t/budget=%d", reverse, budget), func(t *testing.T) {
				destination := uint32(0x5000)
				if reverse {
					destination = 0x5004
				}
				precise := newThumbTransparentPaletteTestBackend(t, New(), reverse, destination)
				translated := newThumbTransparentPaletteTestBackend(t, NewJIT(), reverse, destination)
				assertThumbFillParity(t, precise, translated, 0x1000, budget)
				if budget == 35 {
					stats := translated.ExecutionStatistics()
					if stats.AcceleratedLoopIterations != 3 || stats.AcceleratedLoopInstructions != 35 {
						t.Fatalf("transparent palette statistics: %+v", stats)
					}
				}
			})
		}
	}
}

func TestThumbTransparentPaletteLoopAliasesAndFaults(t *testing.T) {
	for _, destination := range []uint32{0x6048, 0x3000, 0x4002, 0x1000, 0x50fe, 0x8000, 0x5001} {
		t.Run(fmt.Sprintf("%08x", destination), func(t *testing.T) {
			precise := newThumbTransparentPaletteTestBackend(t, New(), false, destination)
			translated := newThumbTransparentPaletteTestBackend(t, NewJIT(), false, destination)
			assertThumbFillParity(t, precise, translated, 0x1000, 35)
		})
	}
}

func TestThumbTransparentPaletteLoopRequiresExactCode(t *testing.T) {
	for index := range thumbTransparentPaletteLoopWords {
		backend := newThumbTransparentPaletteTestBackend(t, NewJIT(), false, 0x5000)
		address := 0x1000 + uint32(index)*2
		var word [2]byte
		check(t, backend.ReadMemory(address, word[:]))
		binary.LittleEndian.PutUint16(word[:], binary.LittleEndian.Uint16(word[:])^1)
		check(t, backend.WriteMemory(address, word[:]))
		if backend.classifyThumbTransparentPaletteLoop(0x1000) != nil {
			t.Fatalf("changed instruction %d classified", index)
		}
	}
}

func TestThumbTransparentPaletteLoopObservationGuards(t *testing.T) {
	backend := newThumbTransparentPaletteTestBackend(t, NewJIT(), false, 0x5000)
	loop := backend.classifyThumbTransparentPaletteLoop(0x1000)
	for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if retired := backend.accelerateThumbTransparentPaletteLoop(loop, 35, flags[0], flags[1], flags[2]); retired != 0 {
			t.Fatalf("observed execution retired %d instructions", retired)
		}
	}
}
