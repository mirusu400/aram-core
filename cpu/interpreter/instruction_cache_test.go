package interpreter

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestARM926InstructionCacheRetainsCodeUntilMVAInvalidation(t *testing.T) {
	backend := New()
	check(t, backend.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	))
	writeInstruction := func(instruction uint32) {
		var code [4]byte
		binary.LittleEndian.PutUint32(code[:], instruction)
		check(t, backend.WriteMemory(0x1000, code[:]))
	}
	writeInstruction(0xe3a00001) // MOV r0, #1
	backend.setCP15Control(1 << 12)
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	writeInstruction(0xe3a00002) // MOV r0, #2 in backing memory only
	check(t, backend.WriteRegister(cpu.RegisterR0, 0))
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("cached instruction result = %d, want 1", got)
	}
	check(t, backend.writeCP15(7, 5, 1, 0x1000))
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 2 {
		t.Fatalf("invalidated instruction result = %d, want 2", got)
	}
}

func TestARM926InstructionWindowTracksLineAndInvalidatesWithMappings(t *testing.T) {
	backend := New()
	check(t, backend.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	))
	code := make([]byte, instructionCacheLineSize)
	for offset := 0; offset < len(code); offset += 4 {
		binary.LittleEndian.PutUint32(code[offset:offset+4], 0xe1a00000) // MOV r0, r0
	}
	check(t, backend.WriteMemory(0x1000, code))
	backend.setCP15Control(1 << 12)
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Instructions != 8 {
		t.Fatalf("Run result = %+v", result)
	}
	if backend.instructionWindow == nil || backend.instructionWindowTag != (0x1000>>5)+1 {
		t.Fatalf("instruction window = %p tag %#x", backend.instructionWindow, backend.instructionWindowTag)
	}
	backend.invalidateTLB()
	if backend.instructionWindow != nil || backend.instructionWindowTag != 0 {
		t.Fatalf("mapping invalidation retained instruction window = %p tag %#x",
			backend.instructionWindow, backend.instructionWindowTag)
	}
}

func TestARM926CP15PrefetchFillsInstructionCacheBeforeCodeIsOverwritten(t *testing.T) {
	backend := New()
	check(t, backend.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	))
	var code [4]byte
	binary.LittleEndian.PutUint32(code[:], 0xe3a00001) // MOV r0, #1
	check(t, backend.WriteMemory(0x1028, code[:]))
	backend.setCP15Control(1 << 12)
	check(t, backend.writeCP15(7, 13, 1, 0x1028))

	binary.LittleEndian.PutUint32(code[:], 0xe3a00002) // backing memory only
	check(t, backend.WriteMemory(0x1028, code[:]))
	if result := backend.Run(context.Background(), 0x1028, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("prefetched instruction result = %d, want 1", got)
	}
}

func TestARM926CP15PrefetchDoesNotFillUncacheableSection(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		tableBase    = uint32(0x4000)
		virtualBase  = uint32(0x80000000)
		physicalBase = uint32(0x00100000)
	)
	bus.writeU32(tableBase+(virtualBase>>20)*4, physicalBase|3<<10|2)
	bus.writeU32(physicalBase, 0xe3a00001)
	backend := New()
	check(t, backend.AttachSystemBus(bus))
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3
	backend.setCP15Control(1 | 1<<12)
	check(t, backend.writeCP15(7, 13, 1, virtualBase))
	bus.writeU32(physicalBase, 0xe3a00002)
	if result := backend.Run(context.Background(), virtualBase, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 2 {
		t.Fatalf("uncacheable prefetched instruction result = %d, want 2", got)
	}
}

func TestARM926InstructionCacheStateRoundTripPreservesStaleLine(t *testing.T) {
	backend := New()
	check(t, backend.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	))
	var code [4]byte
	binary.LittleEndian.PutUint32(code[:], 0xe3a00001)
	check(t, backend.WriteMemory(0x1000, code[:]))
	backend.setCP15Control(1 << 12)
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	binary.LittleEndian.PutUint32(code[:], 0xe3a00002)
	check(t, backend.WriteMemory(0x1000, code[:]))
	state, err := backend.SaveContext()
	check(t, err)

	restored := New()
	check(t, restored.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	))
	check(t, restored.WriteMemory(0x1000, code[:]))
	check(t, restored.RestoreContext(state))
	if result := restored.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, restored, cpu.RegisterR0); got != 1 {
		t.Fatalf("restored cached instruction result = %d, want 1", got)
	}
}

func TestARM926InstructionCacheHonorsSectionCacheability(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		tableBase    = uint32(0x4000)
		virtualBase  = uint32(0x80000000)
		physicalBase = uint32(0x00100000)
	)
	// Manager-domain, cacheable section.
	bus.writeU32(tableBase+(virtualBase>>20)*4, physicalBase|3<<10|1<<3|2)
	bus.writeU32(physicalBase, 0xe3a00001)
	backend := New()
	check(t, backend.AttachSystemBus(bus))
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3
	backend.setCP15Control(1 | 1<<12)
	if result := backend.Run(context.Background(), virtualBase, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	bus.writeU32(physicalBase, 0xe3a00002)
	check(t, backend.WriteRegister(cpu.RegisterR0, 0))
	if result := backend.Run(context.Background(), virtualBase, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("cacheable section result = %d, want 1", got)
	}

	backend.invalidateInstructionCache()
	bus.writeU32(tableBase+(virtualBase>>20)*4, physicalBase|3<<10|2)
	backend.invalidateTLB()
	if result := backend.Run(context.Background(), virtualBase, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	bus.writeU32(physicalBase, 0xe3a00003)
	if result := backend.Run(context.Background(), virtualBase, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 3 {
		t.Fatalf("uncacheable section result = %d, want 3", got)
	}
}
