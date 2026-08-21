package interpreter

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbExecutesIntegerAndStackInstructions(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x07, 0x20, // movs r0, #7
		0x05, 0x30, // adds r0, #5
		0x41, 0x1c, // adds r1, r0, #1
		0x00, 0xb5, // push {lr}
		0x00, 0xbe, // bkpt #0
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x3000); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterLR, 0x12345679); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 16)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 5 {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 12 {
		t.Fatalf("r0 = %d, want 12", got)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 13 {
		t.Fatalf("r1 = %d, want 13", got)
	}
	if got := register(t, backend, cpu.RegisterSP); got != 0x2ffc {
		t.Fatalf("sp = 0x%x, want 0x2ffc", got)
	}
	var stacked [4]byte
	if err := backend.ReadMemory(0x2ffc, stacked[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(stacked[:]); got != 0x12345679 {
		t.Fatalf("stacked lr = 0x%x", got)
	}
}

func TestARMExecutesDataProcessing(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := make([]byte, 12)
	binary.LittleEndian.PutUint32(code[0:4], 0xe3a00007)  // mov r0, #7
	binary.LittleEndian.PutUint32(code[4:8], 0xe2800005)  // add r0, r0, #5
	binary.LittleEndian.PutUint32(code[8:12], 0xe1200070) // bkpt #0
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 16)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 3 {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 12 {
		t.Fatalf("r0 = %d, want 12", got)
	}
}

func TestExecutionBudgetAndContextRoundTrip(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	if err := backend.WriteMemory(0x1000, []byte{0x01, 0x20, 0x01, 0x30}); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
	if result.Reason != cpu.StopBudget || result.Instructions != 1 || result.PC != 0x1002 {
		t.Fatalf("Run result = %+v", result)
	}
	saved, err := backend.SaveContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 99); err != nil {
		t.Fatal(err)
	}
	if err := backend.RestoreContext(saved); err != nil {
		t.Fatal(err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("restored r0 = %d, want 1", got)
	}
}

func TestThumbLongBranchWithLinkExecutesAsOneInstruction(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x00, 0xf0, // BL prefix: high offset 0
		0x02, 0xf8, // BL suffix: branch to 0x1008
		0x00, 0xbe, // return address: BKPT
		0x00, 0x00, // padding
		0x2a, 0x20, // target: MOVS r0, #42
		0x00, 0xbe, // BKPT
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}

	first := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
	if first.Err != nil || first.Reason != cpu.StopBudget || first.PC != 0x1008 {
		t.Fatalf("BL result = %+v", first)
	}
	if got := register(t, backend, cpu.RegisterLR); got != 0x1005 {
		t.Fatalf("LR after BL = 0x%x, want 0x1005", got)
	}

	final := backend.Run(context.Background(), first.PC, cpu.ModeThumb, 2)
	if final.Err != nil || final.Reason != cpu.StopBreakpoint ||
		register(t, backend, cpu.RegisterR0) != 42 {
		t.Fatalf("branch target result = %+v", final)
	}
}

func TestThumbBranchExchangeWithLinkSetsReturnAddress(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	if err := backend.WriteMemory(0x1000, []byte{
		0x98, 0x47, // BLX r3
		0x00, 0xbe, // return address: BKPT
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1100, []byte{
		0x2a, 0x20, // MOVS r0, #42
		0x70, 0x47, // BX lr
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR3, 0x1101); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("BLX result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterLR); got != 0x1003 {
		t.Fatalf("LR after BLX = 0x%x, want 0x1003", got)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 42 {
		t.Fatalf("r0 after BLX target = %d, want 42", got)
	}
}

func TestARMBranchExchangeWithLinkSetsReturnAddress(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := make([]byte, 8)
	binary.LittleEndian.PutUint32(code[0:4], 0xe12fff33) // BLX r3
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // return address: BKPT
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1100, []byte{
		0x2a, 0x20, // MOVS r0, #42
		0x70, 0x47, // BX lr
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR3, 0x1101); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 4)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("ARM BLX result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterLR); got != 0x1004 {
		t.Fatalf("LR after ARM BLX = 0x%x, want 0x1004", got)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 42 {
		t.Fatalf("r0 after ARM BLX target = %d, want 42", got)
	}
}

func TestARMImmediateBranchExchangeWithLinkEntersThumb(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := make([]byte, 8)
	// BLX 0x1100: PC is 0x1008 and the signed immediate is 0xf8.
	binary.LittleEndian.PutUint32(code[0:4], 0xfa00003e)
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // return address: BKPT
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1100, []byte{
		0x2a, 0x20, // MOVS r0, #42
		0x70, 0x47, // BX lr
	}); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 4)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("ARM immediate BLX result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterLR); got != 0x1004 {
		t.Fatalf("LR after immediate BLX = 0x%x, want 0x1004", got)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 42 {
		t.Fatalf("r0 after immediate BLX target = %d, want 42", got)
	}
}

func TestARMInstructionCacheInvalidateIsCoherentNoOp(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := make([]byte, 8)
	binary.LittleEndian.PutUint32(code[0:4], 0xee070f15) // MCR p15,0,r0,c7,c5,0
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // BKPT
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("cache maintenance result = %+v", result)
	}
}

func TestARMBlockTransferPushesAndPopsRegisters(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	code := make([]byte, 20)
	binary.LittleEndian.PutUint32(code[0:4], 0xe92d4001)   // stmdb sp!, {r0, lr}
	binary.LittleEndian.PutUint32(code[4:8], 0xe3a00000)   // mov r0, #0
	binary.LittleEndian.PutUint32(code[8:12], 0xe8bd4001)  // ldmia sp!, {r0, lr}
	binary.LittleEndian.PutUint32(code[12:16], 0xe12fff1e) // bx lr
	binary.LittleEndian.PutUint32(code[16:20], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: 0x12345678,
		cpu.RegisterSP: 0x1ff0,
		cpu.RegisterLR: 0x1010,
	} {
		if err := backend.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("result = %+v", result)
	}
	if got, _ := backend.ReadRegister(cpu.RegisterR0); got != 0x12345678 {
		t.Fatalf("r0 = 0x%08x", got)
	}
	if got, _ := backend.ReadRegister(cpu.RegisterSP); got != 0x1ff0 {
		t.Fatalf("sp = 0x%08x", got)
	}
}

func TestARMDataProcessingSupportsImmediateRegisterShift(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		8,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [8]byte
	binary.LittleEndian.PutUint32(code[0:4], 0xe1b02f82) // movs r2, r2, lsl #31
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR2, 3); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("result = %+v", result)
	}
	if got, _ := backend.ReadRegister(cpu.RegisterR2); got != 0x80000000 {
		t.Fatalf("r2 = 0x%08x", got)
	}
	cpsr, err := backend.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		t.Fatal(err)
	}
	if cpsr&flagN == 0 || cpsr&flagC == 0 {
		t.Fatalf("CPSR = 0x%08x, want N and C", cpsr)
	}
}

func TestARMDataProcessingTSTControlsConditionalExecution(t *testing.T) {
	for _, test := range []struct {
		name string
		lr   uint32
		want uint32
	}{
		{name: "zero", lr: 0x1000, want: 1},
		{name: "nonzero", lr: 0x1001, want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := New()
			defer backend.Close()
			if err := backend.Map(
				0x1000,
				16,
				cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
			); err != nil {
				t.Fatal(err)
			}
			var code [16]byte
			binary.LittleEndian.PutUint32(code[0:4], 0xe31e0001)   // tst lr, #1
			binary.LittleEndian.PutUint32(code[4:8], 0x03a00001)   // moveq r0, #1
			binary.LittleEndian.PutUint32(code[8:12], 0x13a00002)  // movne r0, #2
			binary.LittleEndian.PutUint32(code[12:16], 0xe1200070) // bkpt
			if err := backend.WriteMemory(0x1000, code[:]); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteRegister(cpu.RegisterLR, test.lr); err != nil {
				t.Fatal(err)
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 4)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint {
				t.Fatalf("result = %+v", result)
			}
			if got, _ := backend.ReadRegister(cpu.RegisterR0); got != test.want {
				t.Fatalf("r0 = %d, want %d", got, test.want)
			}
		})
	}
}

func TestARMSingleDataTransferSupportsPostIndexAndCondition(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [8]byte
	binary.LittleEndian.PutUint32(code[0:4], 0x24913004) // ldrcs r3, [r1], #4
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	var value [4]byte
	binary.LittleEndian.PutUint32(value[:], 0x12345678)
	if err := backend.WriteMemory(0x1800, value[:]); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, 0x1800); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, flagC); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR3); got != 0x12345678 {
		t.Fatalf("r3 = 0x%08x", got)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 0x1804 {
		t.Fatalf("r1 = 0x%08x", got)
	}
}

func TestARMSingleDataTransferSupportsShiftedRegisterOffset(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [8]byte
	binary.LittleEndian.PutUint32(code[0:4], 0xe7913102) // ldr r3, [r1, r2, lsl #2]
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	var value [4]byte
	binary.LittleEndian.PutUint32(value[:], 0x89abcdef)
	if err := backend.WriteMemory(0x1810, value[:]); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, 0x1800); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR2, 4); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR3); got != 0x89abcdef {
		t.Fatalf("r3 = 0x%08x", got)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 0x1800 {
		t.Fatalf("r1 = 0x%08x", got)
	}
}

func TestThumbAddressGenerationFromPCAndSP(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x01, 0xa0, // ADD r0, PC, #4 => 0x1008
		0x01, 0xa9, // ADD r1, SP, #4 => 0x2804
		0x00, 0xbe, // BKPT
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x2800); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 3)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 0x1008 {
		t.Fatalf("r0 = 0x%x, want 0x1008", got)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 0x2804 {
		t.Fatalf("r1 = 0x%x, want 0x2804", got)
	}
}

func TestThumbLiteralLoadUsesAlignedArchitecturalPC(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x01, 0x48, // LDR r0, [PC, #4] => word at 0x1008
		0x00, 0xbe, // BKPT
		0x00, 0x00,
		0x00, 0x00,
		0x78, 0x56, 0x34, 0x12,
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 0x12345678 {
		t.Fatalf("literal value = 0x%x, want 0x12345678", got)
	}
}

func TestThumbCompareSetsSignFromResultMostSignificantBit(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	if err := backend.WriteMemory(0x1000, []byte{
		0x13, 0x28, // CMP r0, #0x13
		0x00, 0xbe,
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 0x1100); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Run result = %+v", result)
	}
	cpsr := register(t, backend, cpu.RegisterCPSR)
	if cpsr&flagN != 0 || cpsr&flagC == 0 {
		t.Fatalf("CPSR after positive subtraction = 0x%08x", cpsr)
	}
}

func TestThumbImmediateShifts(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x40, 0x00, // LSLS r0, r0, #1
		0x49, 0x08, // LSRS r1, r1, #1
		0x12, 0x10, // ASRS r2, r2, #32
		0x00, 0xbe, // BKPT
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: 0x80000001,
		cpu.RegisterR1: 3,
		cpu.RegisterR2: 0x80000000,
	} {
		if err := backend.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 2 {
		t.Fatalf("r0 = 0x%x, want 2", got)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 1 {
		t.Fatalf("r1 = 0x%x, want 1", got)
	}
	if got := register(t, backend, cpu.RegisterR2); got != 0xffffffff {
		t.Fatalf("r2 = 0x%x, want 0xffffffff", got)
	}
}

func TestThumbRegisterALUStackAdjustmentAndMultipleTransfer(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	alu := func(op, rs, rd uint16) uint16 {
		return 0x4000 | op<<6 | rs<<3 | rd
	}
	instructions := []uint16{
		alu(5, 1, 0), // ADC r0, r1
		alu(6, 3, 2), // SBC r2, r3
		0xb087,       // SUB SP, #28
		0xb007,       // ADD SP, #28
		0xc503,       // STMIA r5!, {r0, r1}
		0x3808,       // SUBS r0, #8 (restore zero below separately)
		0x2000,       // MOVS r0, #0
		0x2100,       // MOVS r1, #0
		0x3d08,       // SUBS r5, #8
		0xcd03,       // LDMIA r5!, {r0, r1}
		0xbe00,
	}
	code := make([]byte, len(instructions)*2)
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint16(code[index*2:], instruction)
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	for registerID, value := range map[uint32]uint32{
		cpu.RegisterR0:   0xffffffff,
		cpu.RegisterR1:   0,
		cpu.RegisterR2:   5,
		cpu.RegisterR3:   3,
		cpu.RegisterR5:   0x2000,
		cpu.RegisterSP:   0x2800,
		cpu.RegisterCPSR: cpu.StatusThumb | flagC,
	} {
		if err := backend.WriteRegister(registerID, value); err != nil {
			t.Fatal(err)
		}
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 32)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 0 {
		t.Fatalf("restored r0 = 0x%x, want 0", got)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 0 {
		t.Fatalf("restored r1 = 0x%x, want 0", got)
	}
	if got := register(t, backend, cpu.RegisterR2); got != 2 {
		t.Fatalf("SBC result r2 = 0x%x, want 2", got)
	}
	if got := register(t, backend, cpu.RegisterSP); got != 0x2800 {
		t.Fatalf("SP = 0x%x, want 0x2800", got)
	}
	if got := register(t, backend, cpu.RegisterR5); got != 0x2008 {
		t.Fatalf("LDM writeback r5 = 0x%x, want 0x2008", got)
	}
}

func TestThumbRegisterOffsetLoadsAndStores(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	encode := func(op, ro, rb, rd uint16) uint16 {
		return 0x5000 | op<<9 | ro<<6 | rb<<3 | rd
	}
	instructions := []uint16{
		encode(0, 1, 0, 2), // STR r2, [r0, r1]
		encode(4, 1, 0, 3), // LDR r3, [r0, r1]
		encode(2, 1, 0, 4), // STRB r4, [r0, r1]
		encode(6, 1, 0, 5), // LDRB r5, [r0, r1]
		encode(3, 1, 0, 6), // LDRSB r6, [r0, r1]
		encode(1, 1, 0, 2), // STRH r2, [r0, r1]
		encode(5, 1, 0, 4), // LDRH r4, [r0, r1]
		encode(7, 1, 0, 7), // LDRSH r7, [r0, r1]
		0xbe00,
	}
	code := make([]byte, len(instructions)*2)
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint16(code[index*2:], instruction)
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: 0x2000,
		cpu.RegisterR1: 4,
		cpu.RegisterR2: 0x8001,
		cpu.RegisterR4: 0x80,
	} {
		if err := backend.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 16)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR3); got != 0x8001 {
		t.Fatalf("r3 = 0x%x, want 0x8001", got)
	}
	if got := register(t, backend, cpu.RegisterR5); got != 0x80 {
		t.Fatalf("r5 = 0x%x, want 0x80", got)
	}
	if got := register(t, backend, cpu.RegisterR6); got != 0xffffff80 {
		t.Fatalf("r6 = 0x%x, want 0xffffff80", got)
	}
	if got := register(t, backend, cpu.RegisterR4); got != 0x8001 {
		t.Fatalf("r4 = 0x%x, want 0x8001", got)
	}
	if got := register(t, backend, cpu.RegisterR7); got != 0xffff8001 {
		t.Fatalf("r7 = 0x%x, want 0xffff8001", got)
	}
}

func TestThumbHighRegisterAddUsesArchitecturalPC(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x9f, 0x44, // ADD PC, r3: (0x1000 + 4) + 4 => 0x1008
		0x00, 0xde, // must be skipped
		0x00, 0xde, // must be skipped
		0x00, 0xde, // must be skipped
		0x2a, 0x20, // MOVS r0, #42
		0x00, 0xbe, // BKPT
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR3, 4); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 3)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		register(t, backend, cpu.RegisterR0) != 42 {
		t.Fatalf("Run result = %+v", result)
	}
}

func TestThumbImmediateLoadsAndStores(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x41, 0x60, // STR r1, [r0, #4]
		0x42, 0x68, // LDR r2, [r0, #4]
		0x41, 0x70, // STRB r1, [r0, #1]
		0x43, 0x78, // LDRB r3, [r0, #1]
		0x41, 0x80, // STRH r1, [r0, #2]
		0x44, 0x88, // LDRH r4, [r0, #2]
		0x01, 0x91, // STR r1, [SP, #4]
		0x01, 0x9d, // LDR r5, [SP, #4]
		0x00, 0xbe, // BKPT
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: 0x2000,
		cpu.RegisterR1: 0x12345678,
		cpu.RegisterSP: 0x2800,
	} {
		if err := backend.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 16)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Run result = %+v", result)
	}
	for registerID, want := range map[uint32]uint32{
		cpu.RegisterR2: 0x12345678,
		cpu.RegisterR3: 0x78,
		cpu.RegisterR4: 0x5678,
		cpu.RegisterR5: 0x12345678,
	} {
		if got := register(t, backend, registerID); got != want {
			t.Fatalf("r%d = 0x%x, want 0x%x", registerID, got, want)
		}
	}
}

func TestRestoreContextRejectsInvalidModeAtomically(t *testing.T) {
	backend := New()
	if err := backend.WriteRegister(cpu.RegisterR0, 7); err != nil {
		t.Fatal(err)
	}
	saved, err := backend.SaveContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 99); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(saved[len(saved)-4:], 99)
	if err := backend.RestoreContext(saved); err == nil {
		t.Fatal("RestoreContext accepted an invalid mode")
	}
	if got := register(t, backend, cpu.RegisterR0); got != 99 {
		t.Fatalf("failed RestoreContext changed r0 to %d", got)
	}
}

func TestMemoryPermissionsAndUnsupportedInstructionFault(t *testing.T) {
	backend := New()
	if err := backend.Map(0x1000, 2, cpu.PermissionRead|cpu.PermissionExecute); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{0, 0}); !errors.Is(err, cpu.ErrPermissionDenied) {
		t.Fatalf("WriteMemory error = %v", err)
	}

	writable := New()
	if err := writable.Map(0x1000, 2,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		t.Fatal(err)
	}
	if err := writable.WriteMemory(0x1000, []byte{0x00, 0xde}); err != nil {
		t.Fatal(err)
	}
	result := writable.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
	if !errors.Is(result.Err, cpu.ErrUnsupportedInstruction) {
		t.Fatalf("Run error = %v", result.Err)
	}
}

func TestScalarMemoryAccessCrossesAdjacentMappings(t *testing.T) {
	backend := New()
	defer backend.Close()
	permissions := cpu.PermissionRead | cpu.PermissionWrite
	if err := backend.Map(0x1000, 1, permissions); err != nil {
		t.Fatal(err)
	}
	if err := backend.Map(0x1001, 3, permissions); err != nil {
		t.Fatal(err)
	}
	if err := backend.write32(0x1000, 0x78563412, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	value, err := backend.read32(0x1000, cpu.PermissionRead)
	if err != nil {
		t.Fatal(err)
	}
	if value != 0x78563412 {
		t.Fatalf("cross-mapping value = 0x%08x", value)
	}
}

func TestRegionHintIsInvalidatedWhenMappingsAreSorted(t *testing.T) {
	backend := New()
	defer backend.Close()
	permissions := cpu.PermissionRead | cpu.PermissionWrite
	if err := backend.Map(0x2000, 4, permissions); err != nil {
		t.Fatal(err)
	}
	if err := backend.write32(0x2000, 0x22222222, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := backend.Map(0x1000, 4, permissions); err != nil {
		t.Fatal(err)
	}
	if err := backend.write32(0x1000, 0x11111111, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	for address, want := range map[uint32]uint32{
		0x1000: 0x11111111,
		0x2000: 0x22222222,
	} {
		got, err := backend.read32(address, cpu.PermissionRead)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("value at 0x%08x = 0x%08x, want 0x%08x", address, got, want)
		}
	}
}

func TestRunHonorsCanceledContextBeforeExecuting(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		2,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{0xfe, 0xe7}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := backend.Run(ctx, 0x1000, cpu.ModeThumb, 0)
	if !errors.Is(result.Err, context.Canceled) ||
		result.Reason != cpu.StopRequested ||
		result.Instructions != 0 {
		t.Fatalf("canceled Run result = %+v", result)
	}
}

func TestIdentityAndMemoryLimit(t *testing.T) {
	backend := NewWithMemoryLimit(0x1000)
	if err := backend.Identity().Validate(); err != nil {
		t.Fatal(err)
	}
	if backend.Identity().Name != BackendName ||
		backend.Identity().Version != BackendVersion {
		t.Fatalf("Identity = %+v", backend.Identity())
	}
	if err := backend.Map(0x1000, 0x1000, cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if err := backend.Map(0x3000, 1, cpu.PermissionRead); !errors.Is(err, cpu.ErrInvalidMapping) {
		t.Fatalf("Map beyond memory limit error = %v", err)
	}
}

// TestLazyFlagsPreserveCarryOverflowAcrossPartialSetter guards the deferred
// condition-flag path: an ADDS records N/Z/C/V lazily, and a following MOVS
// (which sets only N/Z) must materialize that pending update before overwriting
// N/Z, so C and V survive while N and Z take the MOVS result. If the partial
// setter failed to resolve first, the later CPSR read would replay the ADDS and
// wrongly report Z from the add instead of from the MOVS.
func TestLazyFlagsPreserveCarryOverflowAcrossPartialSetter(t *testing.T) {
	backend := New()
	defer backend.Close()
	mapCodeAndStack(t, backend)
	code := []byte{
		0x88, 0x18, // adds r0, r1, r2   ; 0x80000000+0x80000000 -> 0, C=1, V=1, Z=1
		0x05, 0x23, // movs r3, #5       ; N=0, Z=0, keeps C=1, V=1
		0x00, 0xbe, // bkpt #0
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	for reg, value := range map[uint32]uint32{
		cpu.RegisterR1: 0x80000000,
		cpu.RegisterR2: 0x80000000,
	} {
		if err := backend.WriteRegister(reg, value); err != nil {
			t.Fatal(err)
		}
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 3 {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 0 {
		t.Fatalf("r0 = 0x%x, want 0", got)
	}
	if got := register(t, backend, cpu.RegisterR3); got != 5 {
		t.Fatalf("r3 = %d, want 5", got)
	}
	cpsr := register(t, backend, cpu.RegisterCPSR)
	if cpsr&flagC == 0 || cpsr&flagV == 0 {
		t.Fatalf("C/V lost across partial setter: CPSR = 0x%08x", cpsr)
	}
	if cpsr&flagZ != 0 || cpsr&flagN != 0 {
		t.Fatalf("N/Z not taken from MOVS: CPSR = 0x%08x", cpsr)
	}
}

func mapCodeAndStack(t *testing.T, backend *Backend) {
	t.Helper()
	if err := backend.Map(0x1000, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		t.Fatal(err)
	}
	if err := backend.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
}

func register(t *testing.T, backend *Backend, id uint32) uint32 {
	t.Helper()
	value, err := backend.ReadRegister(id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestARMHalfwordAndSignedByteTransfers(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [20]byte
	binary.LittleEndian.PutUint32(code[0:4], 0xe1de30d0)  // ldrsb r3, [lr]
	binary.LittleEndian.PutUint32(code[4:8], 0xe1de40f2)  // ldrsh r4, [lr, #2]
	binary.LittleEndian.PutUint32(code[8:12], 0xe1de50b2) // ldrh r5, [lr, #2]
	binary.LittleEndian.PutUint32(code[12:16], 0xe0ce60b4)
	// strh r6, [lr], #4
	binary.LittleEndian.PutUint32(code[16:20], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1800, []byte{0x80, 0x00, 0x00, 0xff}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterLR, 0x1800); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR6, 0xdead1234); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("result = %+v", result)
	}
	for id, want := range map[uint32]uint32{
		cpu.RegisterR3: 0xffffff80,
		cpu.RegisterR4: 0xffffff00,
		cpu.RegisterR5: 0x0000ff00,
		cpu.RegisterLR: 0x1804,
	} {
		if got := register(t, backend, id); got != want {
			t.Fatalf("register %d = 0x%08x, want 0x%08x", id, got, want)
		}
	}
	var stored [2]byte
	if err := backend.ReadMemory(0x1800, stored[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(stored[:]); got != 0x1234 {
		t.Fatalf("stored halfword = 0x%04x", got)
	}
}

func TestARMMultipliesAndCountsLeadingZeros(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [20]byte
	binary.LittleEndian.PutUint32(code[0:4], 0xe0030291)   // mul r3, r1, r2
	binary.LittleEndian.PutUint32(code[4:8], 0xe0242391)   // mla r4, r1, r3, r2
	binary.LittleEndian.PutUint32(code[8:12], 0xe0c65291)  // smull r5, r6, r1, r2
	binary.LittleEndian.PutUint32(code[12:16], 0xe16f7f11) // clz r7, r1
	binary.LittleEndian.PutUint32(code[16:20], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, 0xfffffffe); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR2, 3); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("result = %+v", result)
	}
	for id, want := range map[uint32]uint32{
		cpu.RegisterR3: 0xfffffffa,
		cpu.RegisterR4: 0x0000000f,
		cpu.RegisterR5: 0xfffffffa,
		cpu.RegisterR6: 0xffffffff,
		cpu.RegisterR7: 0,
	} {
		if got := register(t, backend, id); got != want {
			t.Fatalf("register %d = 0x%08x, want 0x%08x", id, got, want)
		}
	}
}

func TestARMSwapExchangesMemoryAndRegister(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [8]byte
	binary.LittleEndian.PutUint32(code[0:4], 0xe1013092) // swp r3, r2, [r1]
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	var value [4]byte
	binary.LittleEndian.PutUint32(value[:], 0xaabbccdd)
	if err := backend.WriteMemory(0x1800, value[:]); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, 0x1800); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR2, 0x11223344); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 4)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR3); got != 0xaabbccdd {
		t.Fatalf("r3 = 0x%08x", got)
	}
	var swapped [4]byte
	if err := backend.ReadMemory(0x1800, swapped[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(swapped[:]); got != 0x11223344 {
		t.Fatalf("swapped word = 0x%08x", got)
	}
}

func TestPCHistoryKeepsConfiguredExecutionTail(t *testing.T) {
	backend := New()
	defer backend.Close()
	mapARMInstructions(t, backend,
		0xe1a00000, // MOV r0, r0
		0xe1a00000,
		0xe1a00000,
		0xe1a00000,
	)
	if err := backend.SetPCHistoryLimit(3); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 4)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("result = %+v", result)
	}
	if got, want := backend.PCHistory(), []uint32{0x1004, 0x1008, 0x100c}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PC history = %#v, want %#v", got, want)
	}
	if err := backend.SetPCHistoryLimit(0); err != nil {
		t.Fatal(err)
	}
	if got := backend.PCHistory(); len(got) != 0 {
		t.Fatalf("disabled PC history = %#v", got)
	}
	if err := backend.SetPCHistoryLimit(1<<20 + 1); err == nil {
		t.Fatal("oversized PC history was accepted")
	}
}
