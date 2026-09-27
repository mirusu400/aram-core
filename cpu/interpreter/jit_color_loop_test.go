package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func newThumbColorTestBackend(t *testing.T, backend *Backend, seed int64) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = backend.Close() })
	for _, address := range []uint32{0x1000, 0x2000, 0x3000} {
		size := uint32(64)
		perms := cpu.PermissionRead | cpu.PermissionWrite
		if address == 0x1000 {
			size = 0x104
			perms |= cpu.PermissionExecute
		} else if address == 0x2000 {
			size = 16
		}
		check(t, backend.Map(address, size, perms))
	}
	words := thumbColorLoopWords
	words[1], words[27] = 0x4b3f, 0x4c32 // both literals at 0x1100
	code := make([]byte, 0x104)
	for index, word := range words {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	binary.LittleEndian.PutUint16(code[96:], 0xbe00)
	rng := rand.New(rand.NewSource(seed))
	redMask := uint32(0xfffff800)
	if seed != 0 {
		redMask = rng.Uint32()
	}
	binary.LittleEndian.PutUint32(code[0x100:], redMask)
	check(t, backend.WriteMemory(0x1000, code))
	pixels, stack := make([]byte, 16), make([]byte, 64)
	_, _ = rng.Read(pixels)
	_, _ = rng.Read(stack)
	check(t, backend.WriteMemory(0x2000, pixels))
	check(t, backend.WriteMemory(0x3000, stack))
	for register := uint32(0); register < cpu.RegisterPC; register++ {
		check(t, backend.WriteRegister(register, rng.Uint32()))
	}
	check(t, backend.WriteRegister(5, 4))
	check(t, backend.WriteRegister(6, 0x2000))
	check(t, backend.WriteRegister(cpu.RegisterSP, 0x3000))
	check(t, backend.WriteRegister(cpu.RegisterCPSR, 0xf0000020))
	return backend
}

func assertThumbColorParity(t *testing.T, precise, translated *Backend, budget uint64) {
	t.Helper()
	want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
	got := translated.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
	if got.Reason != want.Reason || got.Instructions != want.Instructions ||
		got.PC != want.PC || !sameError(got.Err, want.Err) {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
		actual, err := translated.ReadRegister(register)
		check(t, err)
		expected, err := precise.ReadRegister(register)
		check(t, err)
		if actual != expected {
			t.Fatalf("register %d = %08x, want %08x", register, actual, expected)
		}
	}
	for address, size := range map[uint32]int{0x1000: 0x104, 0x2000: 16, 0x3000: 64} {
		actual, expected := make([]byte, size), make([]byte, size)
		check(t, translated.ReadMemory(address, actual))
		check(t, precise.ReadMemory(address, expected))
		if !bytes.Equal(actual, expected) {
			t.Fatalf("memory at %08x differs: %x, want %x", address, actual, expected)
		}
	}
}

func TestThumbColorLoopMatchesPreciseInterpreter(t *testing.T) {
	for seed := int64(0); seed < 12; seed++ {
		for budget := uint64(1); budget <= 200; budget++ {
			t.Run(fmt.Sprintf("%d/%d", seed, budget), func(t *testing.T) {
				precise := newThumbColorTestBackend(t, New(), seed)
				translated := newThumbColorTestBackend(t, NewJIT(), seed)
				assertThumbColorParity(t, precise, translated, budget)
				if budget >= 48 && translated.ExecutionStatistics().AcceleratedLoopIterations == 0 {
					t.Fatal("color loop was not accelerated")
				}
			})
		}
	}
}

func TestThumbColorLoopGuardsAndFaults(t *testing.T) {
	for _, tt := range []struct {
		name    string
		address uint32
		trace   bool
	}{
		{"stack alias", 0x3008, false},
		{"literal alias", 0x1100, false},
		{"executable pixel", 0x1000, false},
		{"tracing", 0x2000, true},
		{"fault after one pixel", 0x200e, false},
		{"unmapped pixel", 0x4000, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			precise := newThumbColorTestBackend(t, New(), 42)
			translated := newThumbColorTestBackend(t, NewJIT(), 42)
			check(t, precise.WriteRegister(6, tt.address))
			check(t, translated.WriteRegister(6, tt.address))
			if tt.trace {
				check(t, precise.SetPCHistoryLimit(512))
				check(t, translated.SetPCHistoryLimit(512))
			}
			budget := uint64(96)
			if tt.address == 0x1000 || tt.address == 0x1100 || tt.address == 0x3008 {
				budget = 48
			}
			assertThumbColorParity(t, precise, translated, budget)
			if tt.name != "fault after one pixel" &&
				translated.ExecutionStatistics().AcceleratedLoopIterations != 0 {
				t.Fatal("guarded loop was accelerated")
			}
		})
	}
}

func TestThumbColorLoopRequiresExactSequence(t *testing.T) {
	for index := range thumbColorLoopWords {
		backend := newThumbColorTestBackend(t, NewJIT(), 0)
		var encoded [2]byte
		check(t, backend.ReadMemory(0x1000+uint32(index)*2, encoded[:]))
		binary.LittleEndian.PutUint16(encoded[:], binary.LittleEndian.Uint16(encoded[:])^1)
		check(t, backend.WriteMemory(0x1000+uint32(index)*2, encoded[:]))
		if backend.classifyThumbColorLoop(0x1000) != nil {
			t.Fatalf("changed instruction %d still classified", index)
		}
	}
}

func TestThumbColorLoopAllRGB565Inputs(t *testing.T) {
	precise := newThumbColorTestBackend(t, New(), 101)
	translated := newThumbColorTestBackend(t, NewJIT(), 101)
	pixels := make([]byte, 65536*2)
	for color := 0; color < 65536; color++ {
		binary.LittleEndian.PutUint16(pixels[color*2:], uint16(color))
	}
	for _, backend := range []*Backend{precise, translated} {
		check(t, backend.Map(0x10000, uint32(len(pixels)), cpu.PermissionRead|cpu.PermissionWrite))
		check(t, backend.WriteMemory(0x10000, pixels))
		check(t, backend.WriteRegister(5, 65536))
		check(t, backend.WriteRegister(6, 0x10000))
	}
	assertThumbColorParity(t, precise, translated, 65536*48+1)
	want, got := make([]byte, len(pixels)), make([]byte, len(pixels))
	check(t, precise.ReadMemory(0x10000, want))
	check(t, translated.ReadMemory(0x10000, got))
	if !bytes.Equal(got, want) {
		t.Fatal("exhaustive RGB565 output differs")
	}
}

func TestThumbColorLoopZeroCounterAndShortSlices(t *testing.T) {
	precise := newThumbColorTestBackend(t, New(), 777)
	translated := newThumbColorTestBackend(t, NewJIT(), 777)
	check(t, precise.WriteRegister(5, 0))
	check(t, translated.WriteRegister(5, 0))
	for _, budget := range []uint64{47, 1, 48, 95, 1} {
		wantPC, err := precise.ReadRegister(cpu.RegisterPC)
		check(t, err)
		if wantPC == 0 {
			wantPC = 0x1000
		}
		want := precise.Run(context.Background(), wantPC, cpu.ModeThumb, budget)
		got := translated.Run(context.Background(), wantPC, cpu.ModeThumb, budget)
		if got.Reason != want.Reason || got.Instructions != want.Instructions ||
			got.PC != want.PC || !sameError(got.Err, want.Err) {
			t.Fatalf("slice %d result = %+v, want %+v", budget, got, want)
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			actual, err := translated.ReadRegister(register)
			check(t, err)
			expected, err := precise.ReadRegister(register)
			check(t, err)
			if actual != expected {
				t.Fatalf("slice %d register %d = %08x, want %08x", budget, register, actual, expected)
			}
		}
	}
}
