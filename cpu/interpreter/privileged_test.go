package interpreter

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestARMStatusRegisterImmediateAndRead(t *testing.T) {
	backend := New()
	mapARMInstructions(t, backend,
		0xe321f0d3, // MSR CPSR_c, #0xd3 (SVC, IRQ/FIQ masked)
		0xe10f0000, // MRS r0, CPSR
	)
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got&0xff != 0xd3 {
		t.Fatalf("MRS CPSR control = %#x, want %#x", got&0xff, 0xd3)
	}
}

func TestARMProcessorModesBankStackAndLinkRegisters(t *testing.T) {
	backend := New()
	mapARMInstructions(t, backend,
		0xe321f0d3, // MSR CPSR_c, #0xd3 (SVC)
		0xe3a0d011, // MOV sp, #0x11
		0xe3a0e012, // MOV lr, #0x12
		0xe321f0d2, // MSR CPSR_c, #0xd2 (IRQ)
		0xe3a0d021, // MOV sp, #0x21
		0xe3a0e022, // MOV lr, #0x22
		0xe321f0d3, // MSR CPSR_c, #0xd3 (SVC)
	)
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 7)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterSP); got != 0x11 {
		t.Fatalf("restored SVC sp = %#x, want %#x", got, 0x11)
	}
	if got := register(t, backend, cpu.RegisterLR); got != 0x12 {
		t.Fatalf("restored SVC lr = %#x, want %#x", got, 0x12)
	}
}

func TestPrivilegedBanksSurviveContextRoundTrip(t *testing.T) {
	backend := New()
	mapARMInstructions(t, backend,
		0xe321f0d3, // SVC
		0xe3a0d011, // SVC sp
		0xe321f0d2, // IRQ
		0xe3a0d022, // IRQ sp
		0xe321f0d3, // SVC
		0xe321f0d2, // IRQ
	)
	first := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 5)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	saved, err := backend.SaveContext()
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(saved[4:8]); got != 2 {
		t.Fatalf("context version = %d, want 2", got)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x99); err != nil {
		t.Fatal(err)
	}
	if err := backend.RestoreContext(saved); err != nil {
		t.Fatal(err)
	}
	second := backend.Run(context.Background(), 0x1014, cpu.ModeARM, 1)
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	if got := register(t, backend, cpu.RegisterSP); got != 0x22 {
		t.Fatalf("restored IRQ sp = %#x, want %#x", got, 0x22)
	}
}

func mapARMInstructions(t *testing.T, backend *Backend, instructions ...uint32) {
	t.Helper()
	if err := backend.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, len(instructions)*4)
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint32(encoded[index*4:], instruction)
	}
	if err := backend.WriteMemory(0x1000, encoded); err != nil {
		t.Fatal(err)
	}
}
