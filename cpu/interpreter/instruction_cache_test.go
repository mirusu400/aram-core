package interpreter

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestARM926InstructionCacheRetainsCodeUntilMVAInvalidation(t *testing.T) {
	backend := New()
	if err := backend.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	writeInstruction := func(instruction uint32) {
		var code [4]byte
		binary.LittleEndian.PutUint32(code[:], instruction)
		if err := backend.WriteMemory(0x1000, code[:]); err != nil {
			t.Fatal(err)
		}
	}
	writeInstruction(0xe3a00001) // MOV r0, #1
	backend.cp15.control = 1 << 12
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	writeInstruction(0xe3a00002) // MOV r0, #2 in backing memory only
	if err := backend.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("cached instruction result = %d, want 1", got)
	}
	if err := backend.writeCP15(7, 5, 1, 0x1000); err != nil {
		t.Fatal(err)
	}
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 2 {
		t.Fatalf("invalidated instruction result = %d, want 2", got)
	}
}

func TestARM926CP15PrefetchFillsInstructionCacheBeforeCodeIsOverwritten(t *testing.T) {
	backend := New()
	if err := backend.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [4]byte
	binary.LittleEndian.PutUint32(code[:], 0xe3a00001) // MOV r0, #1
	if err := backend.WriteMemory(0x1028, code[:]); err != nil {
		t.Fatal(err)
	}
	backend.cp15.control = 1 << 12
	if err := backend.writeCP15(7, 13, 1, 0x1028); err != nil {
		t.Fatal(err)
	}

	binary.LittleEndian.PutUint32(code[:], 0xe3a00002) // backing memory only
	if err := backend.WriteMemory(0x1028, code[:]); err != nil {
		t.Fatal(err)
	}
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
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3
	backend.cp15.control = 1 | 1<<12
	if err := backend.writeCP15(7, 13, 1, virtualBase); err != nil {
		t.Fatal(err)
	}
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
	if err := backend.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	var code [4]byte
	binary.LittleEndian.PutUint32(code[:], 0xe3a00001)
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	backend.cp15.control = 1 << 12
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	binary.LittleEndian.PutUint32(code[:], 0xe3a00002)
	if err := backend.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	state, err := backend.SaveContext()
	if err != nil {
		t.Fatal(err)
	}

	restored := New()
	if err := restored.Map(
		0x1000, 0x100,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := restored.WriteMemory(0x1000, code[:]); err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreContext(state); err != nil {
		t.Fatal(err)
	}
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
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3
	backend.cp15.control = 1 | 1<<12
	if result := backend.Run(context.Background(), virtualBase, cpu.ModeARM, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	bus.writeU32(physicalBase, 0xe3a00002)
	if err := backend.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
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
