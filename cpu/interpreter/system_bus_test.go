package interpreter

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestAttachedSystemBusExecutesCodeAndDispatchesDataAccess(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	code := []uint32{
		0xe59f0008, // LDR r0, [pc, #8] -> 0x2000
		0xe5901000, // LDR r1, [r0]
		0xe2811001, // ADD r1, r1, #1
		0xe5801000, // STR r1, [r0]
		0x00002000,
	}
	for index, instruction := range code {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], instruction)
		bus.writeRaw(0x1000+uint32(index*4), encoded[:])
	}
	bus.writeU32(0x2000, 41)

	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 4)
	if result.Reason != cpu.StopBudget || result.Instructions != 4 {
		t.Fatalf("Run result = %+v", result)
	}
	if got := bus.readU32(0x2000); got != 42 {
		t.Fatalf("bus value = %d, want 42", got)
	}
	if bus.executeReads == 0 || bus.dataReads == 0 || bus.dataWrites == 0 {
		t.Fatalf("bus access counts = execute %d read %d write %d", bus.executeReads, bus.dataReads, bus.dataWrites)
	}
}

func TestAttachSystemBusRejectsExistingMappings(t *testing.T) {
	backend := New()
	if err := backend.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := backend.AttachSystemBus(&testSystemBus{}); err == nil {
		t.Fatal("AttachSystemBus accepted an existing private mapping")
	}
}

func TestExecutionTrapStopsBeforeGuestInstructionWithoutPatchingMemory(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	code := []uint32{
		0xe3a00001, // MOV r0, #1
		0xe2800001, // ADD r0, r0, #1 -- trapped before execution
		0xe1200070, // BKPT
	}
	for index, instruction := range code {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], instruction)
		bus.writeRaw(0x1000+uint32(index*4), encoded[:])
	}
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetExecutionTraps([]cpu.ExecutionTrap{
		{Address: 0x1004, Mode: cpu.ModeARM},
	}); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopExecutionTrap ||
		result.Instructions != 1 || result.PC != 0x1004 {
		t.Fatalf("execution-trap result = %+v", result)
	}
	if value, _ := backend.ReadRegister(cpu.RegisterR0); value != 1 {
		t.Fatalf("r0 after trap = %d", value)
	}
	if got := bus.readU32(0x1004); got != code[1] {
		t.Fatalf("trapped instruction changed from %#x to %#x", code[1], got)
	}
	if err := backend.SetExecutionTraps(nil); err != nil {
		t.Fatal(err)
	}
	result = backend.Run(context.Background(), result.PC, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 2 {
		t.Fatalf("post-trap result = %+v", result)
	}
	if value, _ := backend.ReadRegister(cpu.RegisterR0); value != 2 {
		t.Fatalf("r0 after resume = %d", value)
	}
}

func TestExecutionTrapConfigurationRejectsInvalidAndDuplicateEntries(t *testing.T) {
	backend := New()
	if err := backend.SetExecutionTraps([]cpu.ExecutionTrap{
		{Address: 0x1002, Mode: cpu.ModeARM},
	}); err == nil {
		t.Fatal("accepted unaligned ARM execution trap")
	}
	trap := cpu.ExecutionTrap{Address: 0x1000, Mode: cpu.ModeARM}
	if err := backend.SetExecutionTraps([]cpu.ExecutionTrap{trap, trap}); err == nil {
		t.Fatal("accepted duplicate execution trap")
	}
}

func TestSystemCapabilitiesAdvertiseInterruptLinesWithoutCompleteExceptions(t *testing.T) {
	capabilities := New().SystemCapabilities()
	for _, capability := range []cpu.SystemCapability{
		cpu.CapabilityPhysicalBus,
		cpu.CapabilityPrivilegedModes,
		cpu.CapabilityCP15Control,
		cpu.CapabilityMMU,
		cpu.CapabilityInterruptLines,
		cpu.CapabilityExecutionTraps,
	} {
		if !capabilities.Has(capability) {
			t.Fatalf("missing system capability %#x", capability)
		}
	}
	if capabilities.Has(cpu.CapabilityExceptions) {
		t.Fatalf("overclaimed complete exception capability %#x", capabilities)
	}
}

type testSystemBus struct {
	memory       map[uint32]byte
	executeReads int
	dataReads    int
	dataWrites   int
}

func (b *testSystemBus) Read(address uint32, destination []byte, permission cpu.Permissions) error {
	if permission == cpu.PermissionExecute {
		b.executeReads++
	} else {
		b.dataReads++
	}
	for index := range destination {
		destination[index] = b.memory[address+uint32(index)]
	}
	return nil
}

func (b *testSystemBus) Write(address uint32, source []byte, permission cpu.Permissions) error {
	b.dataWrites++
	b.writeRaw(address, source)
	return nil
}

func (b *testSystemBus) writeRaw(address uint32, source []byte) {
	if b.memory == nil {
		b.memory = make(map[uint32]byte)
	}
	for index, value := range source {
		b.memory[address+uint32(index)] = value
	}
}

func (b *testSystemBus) writeU32(address, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	b.writeRaw(address, encoded[:])
}

func (b *testSystemBus) readU32(address uint32) uint32 {
	var encoded [4]byte
	for index := range encoded {
		encoded[index] = b.memory[address+uint32(index)]
	}
	return binary.LittleEndian.Uint32(encoded[:])
}
