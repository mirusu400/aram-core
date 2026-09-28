package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"unsafe"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbImmediateMemoryTranslationKeepsCompactEncoding(t *testing.T) {
	if unsafe.Sizeof(thumbMicroInstr{}) != 8 {
		t.Fatal("cached Thumb instruction no longer has the compact encoding")
	}
	for raw := uint32(0x6000); raw < 0x8000; raw++ {
		instruction := uint16(raw)
		op, ends, ok := translateThumbMicroOp(instruction)
		want := thumbImmediateTransfer
		if !ok || ends || op != want {
			t.Fatalf("instruction %#04x: op=%d end=%v ok=%v, want %d", instruction, op, ends, ok, want)
		}
	}
}

func TestThumbMicroBlockPreservesInstructionPC(t *testing.T) {
	code := thumbTestCode(
		0x2001, // movs r0,#1
		0x4901, // ldr r1,[pc,#4] reads the word at 0x1008
		0x3101, // adds r1,#1
		0xe7fc, // b 0x1002
		0x5678, 0x1234,
	)
	for _, create := range []func() *Backend{New, NewJIT} {
		backend := create()
		check(t, backend.Map(0x1000, 4096, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		check(t, backend.WriteMemory(0x1000, code))
		result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 4 {
			t.Fatalf("four-instruction block: %+v", result)
		}
		for register, want := range map[uint32]uint32{
			cpu.RegisterR0: 1, cpu.RegisterR1: 0x12345679, cpu.RegisterPC: 0x1002,
		} {
			if got := immediateMemoryRegisterValue(t, backend, register); got != want {
				t.Fatalf("register %d = %#x, want %#x", register, got, want)
			}
		}
		check(t, backend.Close())
	}
}

func TestThumbImmediateMemoryAllEncodingsMatchInterpreter(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, backend := range []*Backend{precise, jit} {
		mapCodeAndStack(t, backend)
		t.Cleanup(func() { _ = backend.Close() })
	}
	seed := make([]byte, 1024)
	for index := range seed {
		seed[index] = byte(index*29 + 7)
	}
	var expected, actual [1024]byte
	ctx := context.Background()
	for raw := uint32(0x6000); raw < 0x8000; raw++ {
		for _, backend := range []*Backend{precise, jit} {
			check(t, backend.WriteMemory(0x1000, thumbTestCode(uint16(raw))))
			check(t, backend.WriteMemory(0x2400, seed))
			for register := uint32(0); register < 8; register++ {
				// Exercise all aliases and all four alignments. When rb == rd,
				// stores must read the old base value and loads must replace it.
				check(t, backend.WriteRegister(register, 0x2400+register*8+(raw&3)))
			}
			check(t, backend.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
			result := backend.Run(ctx, 0x1000, cpu.ModeThumb, 1)
			if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 1 {
				t.Fatalf("instruction %#04x: %+v", raw, result)
			}
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			if registerValue := immediateMemoryRegisterValue(t, precise, register); registerValue != immediateMemoryRegisterValue(t, jit, register) {
				t.Fatalf("instruction %#04x changed register %d", raw, register)
			}
		}
		check(t, precise.ReadMemory(0x2400, expected[:]))
		check(t, jit.ReadMemory(0x2400, actual[:]))
		if !bytes.Equal(expected[:], actual[:]) {
			t.Fatalf("instruction %#04x changed memory", raw)
		}
	}
}

func immediateMemoryRegisterValue(t *testing.T, backend *Backend, index uint32) uint32 {
	t.Helper()
	value, err := backend.ReadRegister(index)
	check(t, err)
	return value
}

func TestThumbImmediateMemoryFaultsMatchInterpreter(t *testing.T) {
	for _, instruction := range []uint16{0x6008, 0x6808, 0x7008, 0x7808} {
		for _, permissions := range []cpu.Permissions{cpu.PermissionRead, cpu.PermissionWrite, cpu.PermissionRead | cpu.PermissionWrite} {
			for _, address := range []uint32{0x1fff, 0x2000, 0x203c, 0x203f, 0x2040} {
				precise, jit := New(), NewJIT()
				results := make([]cpu.Result, 0, 2)
				for _, backend := range []*Backend{precise, jit} {
					check(t, backend.Map(0x1000, 4096, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					check(t, backend.Map(0x2000, 64, permissions))
					check(t, backend.WriteMemory(0x1000, thumbTestCode(instruction)))
					check(t, backend.WriteRegister(cpu.RegisterR0, 0xaabbccdd))
					check(t, backend.WriteRegister(cpu.RegisterR1, address))
					results = append(results, backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 1))
				}
				if results[0].Reason != results[1].Reason || results[0].Instructions != results[1].Instructions || fmt.Sprint(results[0].Err) != fmt.Sprint(results[1].Err) {
					t.Fatalf("instruction %#04x address %#x permissions %d: precise=%+v jit=%+v", instruction, address, permissions, results[0], results[1])
				}
				for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
					if immediateMemoryRegisterValue(t, precise, register) != immediateMemoryRegisterValue(t, jit, register) {
						t.Fatal("fault changed architectural registers")
					}
				}
				if !bytes.Equal(precise.regions[1].data, jit.regions[1].data) {
					t.Fatal("fault or permission denial changed guest memory")
				}
				check(t, precise.Close())
				check(t, jit.Close())
			}
		}
	}
}

func BenchmarkThumbImmediateMemoryGoJIT(b *testing.B) {
	backend := NewJIT()
	b.Cleanup(func() { _ = backend.Close() })
	check(b, backend.Map(0x1000, 4096, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	check(b, backend.Map(0x2000, 4096, cpu.PermissionRead|cpu.PermissionWrite))
	check(b, backend.WriteMemory(0x1000, thumbTestCode(
		0x680a, // ldr r2,[r1]
		0x3201, // adds r2,#1
		0x600a, // str r2,[r1]
		0x784b, // ldrb r3,[r1,#1]
		0x704b, // strb r3,[r1,#1]
		0xe7f9, // b 0x1000
	)))
	check(b, backend.WriteRegister(cpu.RegisterR1, 0x2000))
	ctx := context.Background()
	const budget = uint64(100_000)
	b.ResetTimer()
	for range b.N {
		result := backend.Run(ctx, 0x1000, cpu.ModeThumb, budget)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget {
			b.Fatalf("run result = %+v", result)
		}
	}
	b.ReportMetric(float64(b.N)*float64(budget)/b.Elapsed().Seconds(), "guest-insn/s")
}
