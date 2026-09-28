package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func newThumbStackPaletteTestBackend(t *testing.T, backend *Backend, destination uint32) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = backend.Close() })
	for _, address := range []uint32{0x1000, 0x2000, 0x3000, 0x4000, 0x5000, 0x6000, 0x7000} {
		perms := cpu.PermissionRead | cpu.PermissionWrite
		if address == 0x1000 {
			perms |= cpu.PermissionExecute
		}
		check(t, backend.Map(address, 0x100, perms))
	}
	code := make([]byte, 0x100)
	for index, word := range thumbStackPaletteLoopWords {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	check(t, backend.WriteMemory(0x1000, code))
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], 0x4000)
	check(t, backend.WriteMemory(0x2008, pointer[:]))
	check(t, backend.WriteMemory(0x3000, []byte{1, 2, 3}))
	palette := make([]byte, 8)
	binary.LittleEndian.PutUint16(palette[2:], 0x1234)
	binary.LittleEndian.PutUint16(palette[4:], 0xabcd)
	binary.LittleEndian.PutUint16(palette[6:], 0x5a68)
	check(t, backend.WriteMemory(0x4000, palette))
	binary.LittleEndian.PutUint32(pointer[:], destination)
	check(t, backend.WriteMemory(0x6028, pointer[:]))
	binary.LittleEndian.PutUint32(pointer[:], 0x2000)
	check(t, backend.WriteMemory(0x6038, pointer[:]))
	for register, value := range map[uint32]uint32{
		1: 0xdeadbeef, 2: 0xcafebabe, 3: 7, 4: 0x3000,
		5: 0x7000, 6: 3, cpu.RegisterSP: 0x6000,
	} {
		check(t, backend.WriteRegister(register, value))
	}
	return backend
}

func TestThumbStackPaletteLoopMatchesPreciseInterpreter(t *testing.T) {
	for _, budget := range []uint64{1, 2, 3, 16, 17, 18, 34, 35, 50, 51, 52} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise := newThumbStackPaletteTestBackend(t, New(), 0x5000)
			translated := newThumbStackPaletteTestBackend(t, NewJIT(), 0x5000)
			assertThumbFillParity(t, precise, translated, 0x1000, budget)
			if budget == 51 {
				stats := translated.ExecutionStatistics()
				if stats.AcceleratedLoopIterations != 3 || stats.AcceleratedLoopInstructions != 51 {
					t.Fatalf("palette loop statistics: %+v", stats)
				}
			}
		})
	}
}

func TestThumbStackPaletteLoopFallsBackForAliasingAndFaults(t *testing.T) {
	for _, destination := range []uint32{0x6028, 0x1000, 0x3000, 0x4002, 0x50fe, 0x8000, 0x5001} {
		t.Run(fmt.Sprintf("%08x", destination), func(t *testing.T) {
			precise := newThumbStackPaletteTestBackend(t, New(), destination)
			translated := newThumbStackPaletteTestBackend(t, NewJIT(), destination)
			assertThumbFillParity(t, precise, translated, 0x1000, 54)
		})
	}
}

func TestThumbStackPaletteLoopFlagBranchUsesPrecisePath(t *testing.T) {
	precise := newThumbStackPaletteTestBackend(t, New(), 0x5000)
	translated := newThumbStackPaletteTestBackend(t, NewJIT(), 0x5000)
	var one [4]byte
	binary.LittleEndian.PutUint32(one[:], 1)
	check(t, precise.WriteMemory(0x7074, one[:]))
	check(t, translated.WriteMemory(0x7074, one[:]))
	assertThumbFillParity(t, precise, translated, 0x1000, 10)
	if translated.ExecutionStatistics().AcceleratedLoopIterations != 0 {
		t.Fatal("nonzero flag accelerated")
	}
}

func TestThumbStackPaletteLoopRequiresExactCode(t *testing.T) {
	for index := range thumbStackPaletteLoopWords {
		backend := newThumbStackPaletteTestBackend(t, NewJIT(), 0x5000)
		address := 0x1000 + uint32(index)*2
		var word [2]byte
		check(t, backend.ReadMemory(address, word[:]))
		binary.LittleEndian.PutUint16(word[:], binary.LittleEndian.Uint16(word[:])^1)
		check(t, backend.WriteMemory(address, word[:]))
		if backend.classifyThumbStackPaletteLoop(0x1000) != nil {
			t.Fatalf("changed instruction %d classified", index)
		}
	}
}

func TestThumbStackPaletteLoopObservationGuards(t *testing.T) {
	backend := newThumbStackPaletteTestBackend(t, NewJIT(), 0x5000)
	loop := backend.classifyThumbStackPaletteLoop(0x1000)
	for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if retired := backend.accelerateThumbStackPaletteLoop(loop, 51, flags[0], flags[1], flags[2]); retired != 0 {
			t.Fatalf("observed execution retired %d instructions", retired)
		}
	}
}
