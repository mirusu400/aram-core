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
	if got := binary.LittleEndian.Uint32(saved[4:8]); got != 3 {
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

func TestARMLDMExceptionReturnRestoresCPSRAndBranches(t *testing.T) {
	backend := New()
	mapARMInstructions(t, backend,
		0xe8d0dfff, // LDMIA r0, {r0-r12,lr,pc}^
	)
	if err := backend.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}

	loaded := make([]uint32, 0, 15)
	for register := uint32(0); register <= cpu.RegisterR12; register++ {
		loaded = append(loaded, 0x11000000+register)
	}
	const loadedSupervisorLR = 0x2200000e
	const loadedPC = 0x3001
	loaded = append(loaded, loadedSupervisorLR, loadedPC)
	encoded := make([]byte, len(loaded)*4)
	for index, value := range loaded {
		binary.LittleEndian.PutUint32(encoded[index*4:], value)
	}
	if err := backend.WriteMemory(0x2000, encoded); err != nil {
		t.Fatal(err)
	}

	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSupervisor)
	backend.regs[cpu.RegisterR0] = 0x2000
	backend.banks.userStackLink = [2]uint32{0x3300000d, 0x3300000e}
	restored := flagN | flagC | cpu.StatusThumb | uint32(processorModeSystem)
	backend.spsr.supervisor = restored

	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("Run result = %+v", result)
	}
	if result.PC != loadedPC&^1 || backend.mode != cpu.ModeThumb {
		t.Fatalf("exception return = PC %#x mode %d, want PC %#x Thumb", result.PC, backend.mode, loadedPC&^1)
	}
	if got := register(t, backend, cpu.RegisterCPSR); got != restored {
		t.Fatalf("restored CPSR = %#x, want %#x", got, restored)
	}
	for register := uint32(0); register <= cpu.RegisterR12; register++ {
		if got := registerValue(t, backend, register); got != 0x11000000+register {
			t.Fatalf("r%d = %#x, want %#x", register, got, 0x11000000+register)
		}
	}
	if got := register(t, backend, cpu.RegisterSP); got != 0x3300000d {
		t.Fatalf("System sp = %#x, want %#x", got, 0x3300000d)
	}
	if got := register(t, backend, cpu.RegisterLR); got != 0x3300000e {
		t.Fatalf("System lr = %#x, want %#x", got, 0x3300000e)
	}
	if got := backend.banks.supervisor[1]; got != loadedSupervisorLR {
		t.Fatalf("banked Supervisor lr = %#x, want %#x", got, loadedSupervisorLR)
	}
}

func TestARMBlockTransferSBitUsesUserBank(t *testing.T) {
	t.Run("store", func(t *testing.T) {
		backend := New()
		mapARMInstructions(t, backend,
			0xe8c07f00, // STMIA r0, {r8-r14}^
		)
		if err := backend.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
			t.Fatal(err)
		}
		backend.regs[cpu.RegisterCPSR] = uint32(processorModeFIQ)
		backend.regs[cpu.RegisterR0] = 0x2000
		for index := range backend.banks.userHigh {
			backend.banks.userHigh[index] = 0x44000008 + uint32(index)
			backend.regs[cpu.RegisterR8+uint32(index)] = 0xf1000008 + uint32(index)
		}
		backend.banks.userStackLink = [2]uint32{0x4400000d, 0x4400000e}
		backend.regs[cpu.RegisterSP] = 0xf100000d
		backend.regs[cpu.RegisterLR] = 0xf100000e

		result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
		if result.Err != nil || result.Reason != cpu.StopBudget {
			t.Fatalf("Run result = %+v", result)
		}
		stored := make([]byte, 7*4)
		if err := backend.ReadMemory(0x2000, stored); err != nil {
			t.Fatal(err)
		}
		for index := uint32(0); index < 7; index++ {
			if got := binary.LittleEndian.Uint32(stored[index*4:]); got != 0x44000008+index {
				t.Fatalf("stored user r%d = %#x, want %#x", index+8, got, 0x44000008+index)
			}
		}
	})

	t.Run("load", func(t *testing.T) {
		backend := New()
		mapARMInstructions(t, backend,
			0xe8d07f00, // LDMIA r0, {r8-r14}^
		)
		if err := backend.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
			t.Fatal(err)
		}
		encoded := make([]byte, 7*4)
		for index := uint32(0); index < 7; index++ {
			binary.LittleEndian.PutUint32(encoded[index*4:], 0x55000008+index)
		}
		if err := backend.WriteMemory(0x2000, encoded); err != nil {
			t.Fatal(err)
		}
		backend.regs[cpu.RegisterCPSR] = uint32(processorModeFIQ)
		backend.regs[cpu.RegisterR0] = 0x2000
		for register := uint32(cpu.RegisterR8); register <= cpu.RegisterLR; register++ {
			backend.regs[register] = 0xf2000000 + register
		}

		result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
		if result.Err != nil || result.Reason != cpu.StopBudget {
			t.Fatalf("Run result = %+v", result)
		}
		for register := uint32(cpu.RegisterR8); register <= cpu.RegisterLR; register++ {
			if got := backend.readUserRegister(register); got != 0x55000000+register {
				t.Fatalf("loaded user r%d = %#x, want %#x", register, got, 0x55000000+register)
			}
			if got := registerValue(t, backend, register); got != 0xf2000000+register {
				t.Fatalf("visible FIQ r%d = %#x, want %#x", register, got, 0xf2000000+register)
			}
		}
	})
}

func registerValue(t *testing.T, backend *Backend, id uint32) uint32 {
	t.Helper()
	value, err := backend.ReadRegister(id)
	if err != nil {
		t.Fatal(err)
	}
	return value
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
