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

func TestAttachedDirectMemoryBusBypassesDataCallsAfterColdFill(t *testing.T) {
	bus := &directTestSystemBus{data: make([]byte, 0x2000), base: 0x1000}
	binary.LittleEndian.PutUint32(bus.data[0x0000:], 0xe5901000) // LDR r1, [r0]
	binary.LittleEndian.PutUint32(bus.data[0x1000:], 41)
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 0x2000); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || result.Instructions != 1 {
		t.Fatalf("Run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 41 {
		t.Fatalf("r1 = %d, want 41", got)
	}
	if bus.directFills != 1 || bus.dataReads != 0 {
		t.Fatalf("direct fills = %d, ordinary data reads = %d", bus.directFills, bus.dataReads)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 0x2004); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(bus.data[0x1004:], 42)
	result = backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || register(t, backend, cpu.RegisterR1) != 42 {
		t.Fatalf("cached direct run = %+v r1=%d", result, register(t, backend, cpu.RegisterR1))
	}
	if bus.directFills != 1 || bus.dataReads != 0 {
		t.Fatalf("hot direct access refilled/called bus: fills=%d reads=%d", bus.directFills, bus.dataReads)
	}
	bus.invalidate()
	result = backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || register(t, backend, cpu.RegisterR1) != 42 {
		t.Fatalf("post-invalidation run = %+v r1=%d", result, register(t, backend, cpu.RegisterR1))
	}
	if bus.directFills != 2 || bus.dataReads != 0 {
		t.Fatalf("invalidated direct access fills=%d reads=%d, want one refill", bus.directFills, bus.dataReads)
	}
}

func TestContextSystemBusAttributesDataAccessesToGuestInstructions(t *testing.T) {
	bus := &contextTestSystemBus{testSystemBus: testSystemBus{memory: make(map[uint32]byte)}}
	code := []uint32{
		0xe59f0008, // LDR r0, [pc, #8] -> 0x2000
		0xe5901000, // LDR r1, [r0]
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
	if err := backend.WriteRegister(cpu.RegisterLR, 0x2221); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x1800); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 3)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("Run result = %+v", result)
	}
	if len(bus.dataContexts) != 3 {
		t.Fatalf("data contexts = %+v", bus.dataContexts)
	}
	want := []cpu.MemoryAccessContext{
		{InstructionAddress: 0x1000, LinkAddress: 0x2221, StackAddress: 0x1800, Mode: cpu.ModeARM, Attributed: true},
		{InstructionAddress: 0x1004, LinkAddress: 0x2221, StackAddress: 0x1800, Mode: cpu.ModeARM, Attributed: true},
		{InstructionAddress: 0x1008, LinkAddress: 0x2221, StackAddress: 0x1800, Mode: cpu.ModeARM, Attributed: true},
	}
	for index := range want {
		if bus.dataContexts[index] != want[index] {
			t.Fatalf("data context %d = %+v, want %+v", index, bus.dataContexts[index], want[index])
		}
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

type contextTestSystemBus struct {
	testSystemBus
	dataContexts []cpu.MemoryAccessContext
}

type directTestSystemBus struct {
	data        []byte
	base        uint32
	dataReads   int
	directFills int
	invalidate  func()
}

type alternatingDirectTestBus struct {
	first          []byte
	second         []byte
	directAttempts int
	directFills    int
}

func (b *alternatingDirectTestBus) Read(
	address uint32,
	destination []byte,
	permission cpu.Permissions,
) error {
	region, ok := b.directMemoryRegion(address, len(destination), permission)
	if !ok {
		return cpu.ErrInvalidAddress
	}
	copy(destination, region.Data[address-region.Address:])
	return nil
}

func (b *alternatingDirectTestBus) Write(
	address uint32,
	source []byte,
	permission cpu.Permissions,
) error {
	region, ok := b.directMemoryRegion(address, len(source), permission)
	if !ok {
		return cpu.ErrInvalidAddress
	}
	copy(region.Data[address-region.Address:], source)
	return nil
}

func (b *alternatingDirectTestBus) DirectMemoryRegion(
	address uint32,
	size int,
	permission cpu.Permissions,
) (cpu.DirectMemoryRegion, bool) {
	b.directAttempts++
	region, ok := b.directMemoryRegion(address, size, permission)
	if ok {
		b.directFills++
	}
	return region, ok
}

func TestDirectDataCacheRetainsNonDirectPageMiss(t *testing.T) {
	backend := NewJIT()
	defer func() { _ = backend.Close() }()
	bus := &alternatingDirectTestBus{
		first:  make([]byte, 0x1000),
		second: make([]byte, 0x1000),
	}
	backend.directBus = bus
	for range 3 {
		if _, _, _, ok := backend.directData(0x5000, 4, cpu.PermissionRead); ok {
			t.Fatal("unmapped page was exposed as direct memory")
		}
	}
	if bus.directAttempts != 1 {
		t.Fatalf("repeated non-direct page attempts = %d, want 1", bus.directAttempts)
	}
	backend.clearDataCaches()
	if _, _, _, ok := backend.directData(0x5000, 4, cpu.PermissionRead); ok {
		t.Fatal("invalidated unmapped page was exposed as direct memory")
	}
	if bus.directAttempts != 2 {
		t.Fatalf("post-invalidation direct attempts = %d, want 2", bus.directAttempts)
	}
}

func (b *alternatingDirectTestBus) directMemoryRegion(
	address uint32,
	size int,
	permission cpu.Permissions,
) (cpu.DirectMemoryRegion, bool) {
	if permission&cpu.PermissionExecute != 0 {
		return cpu.DirectMemoryRegion{}, false
	}
	for _, candidate := range []struct {
		address uint32
		data    []byte
	}{{0x1000, b.first}, {0x3000, b.second}} {
		if address >= candidate.address &&
			uint64(address-candidate.address)+uint64(size) <= uint64(len(candidate.data)) {
			return cpu.DirectMemoryRegion{
				Address: candidate.address,
				Data:    candidate.data,
				Permissions: cpu.PermissionRead | cpu.PermissionWrite |
					cpu.PermissionExecute,
			}, true
		}
	}
	return cpu.DirectMemoryRegion{}, false
}

func (*alternatingDirectTestBus) SetDirectMemoryInvalidator(func()) {}

func TestDirectDataCacheRetainsAlternatingRegions(t *testing.T) {
	backend := NewJIT()
	defer func() { _ = backend.Close() }()
	bus := &alternatingDirectTestBus{
		first:  make([]byte, 0x1000),
		second: make([]byte, 0x1000),
	}
	backend.directBus = bus
	for _, address := range []uint32{0x1010, 0x3010, 0x1020, 0x3020} {
		if _, _, _, ok := backend.directData(address, 4, cpu.PermissionRead); !ok {
			t.Fatalf("direct data miss at %#x", address)
		}
	}
	if bus.directFills != 2 {
		t.Fatalf("alternating direct region fills = %d, want 2", bus.directFills)
	}
}

func (b *directTestSystemBus) Read(address uint32, destination []byte, permission cpu.Permissions) error {
	if permission != cpu.PermissionExecute {
		b.dataReads++
	}
	offset := int(address - b.base)
	copy(destination, b.data[offset:offset+len(destination)])
	return nil
}

func (b *directTestSystemBus) Write(address uint32, source []byte, _ cpu.Permissions) error {
	offset := int(address - b.base)
	copy(b.data[offset:offset+len(source)], source)
	return nil
}

func (b *directTestSystemBus) DirectMemoryRegion(
	address uint32,
	size int,
	permission cpu.Permissions,
) (cpu.DirectMemoryRegion, bool) {
	if permission&cpu.PermissionExecute != 0 || address < b.base ||
		uint64(address-b.base)+uint64(size) > uint64(len(b.data)) {
		return cpu.DirectMemoryRegion{}, false
	}
	b.directFills++
	return cpu.DirectMemoryRegion{
		Address: b.base, Data: b.data,
		Permissions: cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute,
	}, true
}

func (b *directTestSystemBus) SetDirectMemoryInvalidator(invalidate func()) {
	b.invalidate = invalidate
}

func (b *contextTestSystemBus) ReadContext(
	context cpu.MemoryAccessContext,
	address uint32,
	destination []byte,
	permission cpu.Permissions,
) error {
	if permission != cpu.PermissionExecute {
		b.dataContexts = append(b.dataContexts, context)
	}
	return b.Read(address, destination, permission)
}

func (b *contextTestSystemBus) WriteContext(
	context cpu.MemoryAccessContext,
	address uint32,
	source []byte,
	permission cpu.Permissions,
) error {
	b.dataContexts = append(b.dataContexts, context)
	return b.Write(address, source, permission)
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
