package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func newThumbRecordSearchTestBackend(t *testing.T, backend *Backend) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	check(t, backend.Map(0x2000, 0x100, cpu.PermissionRead|cpu.PermissionWrite))
	code := make([]byte, 0x100)
	for index, word := range thumbRecordSearchWords {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	check(t, backend.WriteMemory(0x1000, code))
	data := make([]byte, 0x100)
	for index, value := range []uint32{120, 130, 160} {
		binary.LittleEndian.PutUint32(data[index*12:], value)
		binary.LittleEndian.PutUint32(data[index*12+4:], 100)
	}
	check(t, backend.WriteMemory(0x2000, data))
	for register, value := range map[uint32]uint32{
		0: 0, 1: 100, 2: 0x2000, 3: 0xdeadbeef, 4: 50, 12: 4,
		cpu.RegisterCPSR: 0xf0000020,
	} {
		check(t, backend.WriteRegister(register, value))
	}
	return backend
}

func TestThumbRecordSearchMatchesPreciseInterpreter(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*testing.T, *Backend)
		maxBudget uint64
	}{
		{"loop and value exit", nil, 22},
		{"count exit", func(t *testing.T, b *Backend) { check(t, b.WriteRegister(12, 1)) }, 10},
		{"first value exit", func(t *testing.T, b *Backend) {
			var word [4]byte
			binary.LittleEndian.PutUint32(word[:], 160)
			check(t, b.WriteMemory(0x2000, word[:]))
		}, 5},
		{"unaligned", func(t *testing.T, b *Backend) { check(t, b.WriteRegister(2, 0x2001)) }, 10},
		{"second read fault", func(t *testing.T, b *Backend) {
			var word [4]byte
			binary.LittleEndian.PutUint32(word[:], 120)
			check(t, b.WriteMemory(0x20fc, word[:]))
			check(t, b.WriteRegister(2, 0x20fc))
		}, 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			for budget := uint64(1); budget <= test.maxBudget; budget++ {
				t.Run(fmt.Sprint(budget), func(t *testing.T) {
					precise := newThumbRecordSearchTestBackend(t, New())
					translated := newThumbRecordSearchTestBackend(t, NewJIT())
					if test.change != nil {
						test.change(t, precise)
						test.change(t, translated)
					}
					assertThumbFillParity(t, precise, translated, 0x1000, budget)
				})
			}
		})
	}
}

func TestThumbRecordSearchRequiresExactCode(t *testing.T) {
	for index := range thumbRecordSearchWords {
		backend := newThumbRecordSearchTestBackend(t, NewJIT())
		address := 0x1000 + uint32(index)*2
		var word [2]byte
		check(t, backend.ReadMemory(address, word[:]))
		binary.LittleEndian.PutUint16(word[:], binary.LittleEndian.Uint16(word[:])^1)
		check(t, backend.WriteMemory(address, word[:]))
		if backend.classifyThumbRecordSearch(0x1000) != nil {
			t.Fatalf("changed instruction %d classified", index)
		}
	}
}

func TestThumbRecordSearchAccelerationAndObservationGuards(t *testing.T) {
	backend := newThumbRecordSearchTestBackend(t, NewJIT())
	loop := backend.classifyThumbRecordSearch(0x1000)
	backend.regs[cpu.RegisterPC] = 0x1000
	for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if retired := backend.accelerateThumbRecordSearch(loop, 9, flags[0], flags[1], flags[2]); retired != 0 {
			t.Fatalf("observed execution retired %d instructions", retired)
		}
	}
	if retired := backend.accelerateThumbRecordSearch(loop, 9, false, false, false); retired != 9 {
		t.Fatalf("search retired %d instructions, want 9", retired)
	}
	if backend.regs[cpu.RegisterPC] != 0x1000 || backend.regs[0] != 1 || backend.regs[1] != 100 || backend.regs[2] != 0x200c || backend.regs[3] != 20 {
		t.Fatalf("accelerated registers differ: r0=%d r1=%d r2=%x r3=%d pc=%x", backend.regs[0], backend.regs[1], backend.regs[2], backend.regs[3], backend.regs[cpu.RegisterPC])
	}
}
