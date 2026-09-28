package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func newThumbObjectLookupTestBackend(t *testing.T, backend *Backend) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = backend.Close() })
	for _, address := range []uint32{0x1000, 0x2000, 0x3000, 0x6000} {
		perms := cpu.PermissionRead | cpu.PermissionWrite
		if address == 0x1000 {
			perms |= cpu.PermissionExecute
		}
		check(t, backend.Map(address, 0x100, perms))
	}
	code := make([]byte, 0x100)
	for index, word := range thumbObjectLookupWords {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	check(t, backend.WriteMemory(0x1000, code))
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], 0x3000)
	check(t, backend.WriteMemory(0x2000, word[:]))
	binary.LittleEndian.PutUint32(word[:], 0x12345678)
	check(t, backend.WriteMemory(0x300c, word[:]))
	for register, value := range map[uint32]uint32{
		0: 0x2000, 3: 99, 4: 0xaabbccdd,
		cpu.RegisterSP: 0x6080, cpu.RegisterLR: 0x1041,
	} {
		check(t, backend.WriteRegister(register, value))
	}
	return backend
}

func TestThumbObjectLookupMatchesPreciseInterpreter(t *testing.T) {
	for _, budget := range []uint64{1, 2, 3, 6, 7, 8, 9, 10} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			precise := newThumbObjectLookupTestBackend(t, New())
			translated := newThumbObjectLookupTestBackend(t, NewJIT())
			assertThumbFillParity(t, precise, translated, 0x1000, budget)
		})
	}
}

func TestThumbObjectLookupGuardsAndFaults(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *Backend)
	}{
		{"tagged", func(t *testing.T, b *Backend) { check(t, b.WriteMemory(0x2000, []byte{1, 0, 0, 0})) }},
		{"stack alias", func(t *testing.T, b *Backend) { check(t, b.WriteRegister(0, 0x6078)) }},
		{"value alias", func(t *testing.T, b *Backend) { check(t, b.WriteMemory(0x2000, []byte{0x70, 0x60, 0, 0})) }},
		{"fault", func(t *testing.T, b *Backend) { check(t, b.WriteMemory(0x2000, []byte{0, 0x80, 0, 0})) }},
		{"arm return", func(t *testing.T, b *Backend) { check(t, b.WriteRegister(cpu.RegisterLR, 0x1040)) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			precise := newThumbObjectLookupTestBackend(t, New())
			translated := newThumbObjectLookupTestBackend(t, NewJIT())
			test.change(t, precise)
			test.change(t, translated)
			assertThumbFillParity(t, precise, translated, 0x1000, 9)
		})
	}
}

func TestThumbObjectLookupRequiresExactCode(t *testing.T) {
	for index := range thumbObjectLookupWords {
		backend := newThumbObjectLookupTestBackend(t, NewJIT())
		address := 0x1000 + uint32(index)*2
		var word [2]byte
		check(t, backend.ReadMemory(address, word[:]))
		binary.LittleEndian.PutUint16(word[:], binary.LittleEndian.Uint16(word[:])^1)
		check(t, backend.WriteMemory(address, word[:]))
		if backend.classifyThumbObjectLookup(0x1000) != nil {
			t.Fatalf("changed instruction %d classified", index)
		}
	}
}

func TestThumbObjectLookupAccelerationAndObservationGuards(t *testing.T) {
	backend := newThumbObjectLookupTestBackend(t, NewJIT())
	lookup := backend.classifyThumbObjectLookup(0x1000)
	backend.regs[cpu.RegisterPC] = 0x1000
	for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if retired := backend.accelerateThumbObjectLookup(lookup, 9, flags[0], flags[1], flags[2]); retired != 0 {
			t.Fatalf("observed execution retired %d instructions", retired)
		}
	}
	if retired := backend.accelerateThumbObjectLookup(lookup, 9, false, false, false); retired != 9 {
		t.Fatalf("lookup retired %d instructions, want 9", retired)
	}
}
