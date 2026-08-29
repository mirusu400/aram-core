package interpreter

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestMMUSectionTranslatesInstructionAndDataAccesses(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		virtualBase  = uint32(0x80000000)
		physicalBase = uint32(0x00100000)
		tableBase    = uint32(0x00004000)
		domain       = uint32(2)
	)
	bus.writeU32(tableBase+(virtualBase>>20)*4, physicalBase|domain<<5|3<<10|2)
	bus.writeU32(physicalBase+0x1000, 0xe3a0002a) // MOV r0, #42
	bus.writeU32(physicalBase+0x2000, 0x11223344)

	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 1 << (domain * 2) // client
	backend.setCP15Control(1)
	result := backend.Run(context.Background(), virtualBase+0x1000, cpu.ModeARM, 1)
	if result.Err != nil || result.Instructions != 1 || register(t, backend, cpu.RegisterR0) != 42 {
		t.Fatalf("section execution result = %+v r0=%d", result, register(t, backend, cpu.RegisterR0))
	}
	value, err := backend.read32(virtualBase+0x2000, cpu.PermissionRead)
	if err != nil || value != 0x11223344 {
		t.Fatalf("section data read = %#x error %v", value, err)
	}
	if err := backend.write32(virtualBase+0x2000, 0xaabbccdd, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if value := bus.readU32(physicalBase + 0x2000); value != 0xaabbccdd {
		t.Fatalf("section data write = %#x", value)
	}
	var publicRead [4]byte
	if err := backend.ReadMemory(virtualBase+0x2000, publicRead[:]); err != nil {
		t.Fatal(err)
	}
	if value := uint32(publicRead[0]) | uint32(publicRead[1])<<8 |
		uint32(publicRead[2])<<16 | uint32(publicRead[3])<<24; value != 0xaabbccdd {
		t.Fatalf("public virtual read = %#x", value)
	}
}

func TestMMUCoarseSmallPageChecksDomainAndSubpagePermissions(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		virtual     = uint32(0x40003400)
		physical    = uint32(0x00301400)
		tableBase   = uint32(0x00010000)
		coarseBase  = uint32(0x00020000)
		domain      = uint32(3)
		smallPagePA = uint32(0x00301000)
	)
	bus.writeU32(tableBase+(virtual>>20)*4, coarseBase|domain<<5|1)
	secondAddress := coarseBase + ((virtual>>12)&0xff)*4
	bus.writeU32(secondAddress, smallPagePA|0x00000aa0|2) // AP=2 for all four subpages
	bus.writeU32(physical, 0x55667788)

	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 1 << (domain * 2) // client
	backend.setCP15Control(1)
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeUser)
	value, err := backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 0x55667788 {
		t.Fatalf("user page read = %#x error %v", value, err)
	}
	if err := backend.write32(virtual, 1, cpu.PermissionWrite); !errors.Is(err, ErrMMUPermissionFault) {
		t.Fatalf("user page write error = %v", err)
	}
	if backend.cp15.dataFaultStatus != domain<<4|0xf || backend.cp15.faultAddress != virtual {
		t.Fatalf("permission fault state = status %#x address %#x", backend.cp15.dataFaultStatus, backend.cp15.faultAddress)
	}

	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSupervisor)
	if err := backend.write32(virtual, 0x99aabbcc, cpu.PermissionWrite); err != nil {
		t.Fatalf("privileged page write: %v", err)
	}
	backend.cp15.domainAccessControl = 0
	if _, err := backend.read32(virtual, cpu.PermissionRead); !errors.Is(err, ErrMMUDomainFault) {
		t.Fatalf("page domain error = %v", err)
	}
	if backend.cp15.dataFaultStatus != domain<<4|0xb {
		t.Fatalf("domain fault status = %#x", backend.cp15.dataFaultStatus)
	}
}

func TestMMUFineTableTranslatesTinyPage(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		virtual   = uint32(0x510abc20)
		physical  = uint32(0x00700c20)
		tableBase = uint32(0x00004000)
		fineBase  = uint32(0x00008000)
		domain    = uint32(4)
	)
	bus.writeU32(tableBase+(virtual>>20)*4, fineBase|domain<<5|3)
	bus.writeU32(fineBase+((virtual>>10)&0x3ff)*4, physical&0xfffffc00|3<<4|3)
	bus.writeU32(physical, 0x89abcdef)

	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 1 << (domain * 2)
	backend.setCP15Control(1)
	value, err := backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 0x89abcdef {
		t.Fatalf("tiny-page read = %#x error %v", value, err)
	}
}

func TestMMULargePageSelectsSubpageAccessPermission(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		virtual   = uint32(0x62008000)
		physical  = uint32(0x00908000)
		tableBase = uint32(0x00004000)
		coarse    = uint32(0x00010000)
	)
	bus.writeU32(tableBase+(virtual>>20)*4, coarse|1)
	// The selected 16 KiB subpage is AP=2 (user read-only); the others are AP=0.
	bus.writeU32(coarse+((virtual>>12)&0xff)*4, physical&0xffff0000|2<<8|1)
	bus.writeU32(physical, 0x13579bdf)

	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 1
	backend.setCP15Control(1)
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeUser)
	value, err := backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 0x13579bdf {
		t.Fatalf("large-page user read = %#x error %v", value, err)
	}
	if err := backend.write32(virtual, 1, cpu.PermissionWrite); !errors.Is(err, ErrMMUPermissionFault) {
		t.Fatalf("large-page user write error = %v", err)
	}
}

func TestMMUTranslationFaultUpdatesInstructionFaultState(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = 0x4000
	backend.setCP15Control(1)
	_, err := backend.fetch32(0x90000000)
	if !errors.Is(err, ErrMMUTranslationFault) {
		t.Fatalf("instruction translation error = %v", err)
	}
	if backend.cp15.instructionFaultStatus != 5 || backend.cp15.faultAddress != 0x90000000 {
		t.Fatalf(
			"instruction fault state = status %#x address %#x",
			backend.cp15.instructionFaultStatus, backend.cp15.faultAddress,
		)
	}
}

func TestMMUTLBRetainsTranslationUntilInvalidated(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		virtual   = uint32(0x60000000)
		tableBase = uint32(0x00004000)
		firstPA   = uint32(0x01000000)
		secondPA  = uint32(0x02000000)
	)
	descriptorAddress := tableBase + (virtual>>20)*4
	bus.writeU32(descriptorAddress, firstPA|3<<10|2)
	bus.writeU32(firstPA, 1)
	bus.writeU32(secondPA, 2)
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3 // domain 0 manager
	backend.setCP15Control(1)
	value, err := backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 1 {
		t.Fatalf("initial TLB read = %d error %v", value, err)
	}
	bus.writeU32(descriptorAddress, secondPA|3<<10|2)
	value, err = backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 1 {
		t.Fatalf("cached TLB read = %d error %v", value, err)
	}
	if err := backend.writeCP15(8, 7, 0, 0); err != nil {
		t.Fatal(err)
	}
	value, err = backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 2 {
		t.Fatalf("invalidated TLB read = %d error %v", value, err)
	}
}

func TestMMUFCSEProcessIDModifiesLowVirtualAddresses(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	const (
		virtual      = uint32(0x00100000)
		processID    = uint32(0x06000000)
		modified     = virtual | processID
		tableBase    = uint32(0x00008000)
		physicalBase = uint32(0x00400000)
	)
	bus.writeU32(tableBase+(modified>>20)*4, physicalBase|3<<10|2)
	bus.writeU32(physicalBase, 0x12345678)
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3
	backend.cp15.processID = processID
	backend.setCP15Control(1)
	value, err := backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 0x12345678 {
		t.Fatalf("FCSE read = %#x error %v", value, err)
	}
}

func TestMMUDirectVirtualDataCacheHonorsGenerationPrivilegeAndInvalidation(t *testing.T) {
	const (
		virtual      = uint32(0x80000420)
		physicalBase = uint32(0x00100000)
		tableBase    = uint32(0x00004000)
	)
	bus := &directTestSystemBus{data: make([]byte, 0x300000)}
	binary.LittleEndian.PutUint32(
		bus.data[tableBase+(virtual>>20)*4:],
		physicalBase|1<<10|2, // client domain, privileged-only AP=1 section
	)
	physical := physicalBase | virtual&0x000fffff
	binary.LittleEndian.PutUint32(bus.data[physical:], 0x11223344)

	backend := NewJIT()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 1
	backend.setCP15Control(1)
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSupervisor)

	value, err := backend.read32(virtual, cpu.PermissionRead)
	if err != nil || value != 0x11223344 {
		t.Fatalf("cold direct virtual read = %#x error %v", value, err)
	}
	if _, _, ok := backend.virtualDataReadHit(virtual, 4); !ok {
		t.Fatal("successful direct virtual read did not install the Go TLB entry")
	}
	page := virtual >> 10
	entry := &backend.virtualData.read[page&(virtualDataCacheEntries-1)]
	if len(entry.data) != 0x400 {
		t.Fatalf("direct virtual cache span = %#x bytes, want one 1 KiB subpage", len(entry.data))
	}
	if err := backend.write32(virtual, 0x55667788, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := backend.virtualDataWriteHit(virtual, 4); !ok {
		t.Fatal("successful direct virtual write did not install its permission half")
	}
	if got := binary.LittleEndian.Uint32(bus.data[physical:]); got != 0x55667788 {
		t.Fatalf("direct virtual write = %#x", got)
	}

	// Privilege is the one permission input that can change without a mapping
	// generation. A privileged entry must not bypass the user AP check.
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeUser)
	if _, err := backend.read32(virtual, cpu.PermissionRead); !errors.Is(err, ErrMMUPermissionFault) {
		t.Fatalf("user read through privileged virtual cache = %v", err)
	}

	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSupervisor)
	oldGen := backend.mappingGen
	backend.invalidateTLB()
	if backend.mappingGen == oldGen {
		t.Fatal("TLB invalidation did not advance the virtual-cache generation")
	}
	if _, _, ok := backend.virtualDataReadHit(virtual, 4); ok {
		t.Fatal("mapping-generation change retained a virtual data hit")
	}

	if _, err := backend.read32(virtual, cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	bus.invalidate()
	if backend.virtualData != nil {
		t.Fatal("direct-memory invalidator retained virtual RAM slices")
	}
}

func TestARMJITBlockTransferUsesDirectMMUSubpage(t *testing.T) {
	const (
		virtualBase = uint32(0x80000000)
		tableBase   = uint32(0x00004000)
		code        = virtualBase + 0x1000
		data        = virtualBase + 0x2400
	)
	bus := &directTestSystemBus{data: make([]byte, 0x100000)}
	binary.LittleEndian.PutUint32(
		bus.data[tableBase+(virtualBase>>20)*4:],
		3<<10|2, // identity section, domain 0 manager permissions
	)
	for index, instruction := range []uint32{
		0xe8b0001e, // ldmia r0!, {r1-r4}
		0xe1200070, // bkpt
		0xe8a0001e, // stmia r0!, {r1-r4}
		0xe1200070, // bkpt
	} {
		binary.LittleEndian.PutUint32(bus.data[0x1000+index*4:], instruction)
	}
	for index, value := range []uint32{11, 22, 33, 44} {
		binary.LittleEndian.PutUint32(bus.data[0x2400+index*4:], value)
	}

	backend := NewJIT()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3
	backend.setCP15Control(1)

	// Scalar warmup installs both permission halves. The block transfer then
	// proves its complete range once and accesses the direct page itself.
	first, err := backend.read32(data, cpu.PermissionRead)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.write32(data, first, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := backend.armBlockTransferPage(data, 16, true); !ok {
		t.Fatal("warm LDM range did not hit the direct MMU subpage")
	}
	if _, _, ok := backend.armBlockTransferPage(data, 16, false); !ok {
		t.Fatal("warm STM range did not hit the direct MMU subpage")
	}
	if _, _, ok := backend.armBlockTransferPage(virtualBase+0x3fc, 8, true); ok {
		t.Fatal("cross-subpage transfer incorrectly used the direct range")
	}

	if err := backend.WriteRegister(cpu.RegisterR0, data); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), code, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 2 {
		t.Fatalf("direct LDM run = %+v", result)
	}
	for register, want := range []uint32{11, 22, 33, 44} {
		if got := registerValue(t, backend, uint32(register+1)); got != want {
			t.Fatalf("LDM r%d = %d, want %d", register+1, got, want)
		}
	}
	if got := registerValue(t, backend, cpu.RegisterR0); got != data+16 {
		t.Fatalf("LDM writeback = %#x, want %#x", got, data+16)
	}

	for register, value := range []uint32{101, 202, 303, 404} {
		if err := backend.WriteRegister(uint32(register+1), value); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.WriteRegister(cpu.RegisterR0, data); err != nil {
		t.Fatal(err)
	}
	result = backend.Run(context.Background(), code+8, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 2 {
		t.Fatalf("direct STM run = %+v", result)
	}
	for index, want := range []uint32{101, 202, 303, 404} {
		if got := binary.LittleEndian.Uint32(bus.data[0x2400+index*4:]); got != want {
			t.Fatalf("STM word %d = %d, want %d", index, got, want)
		}
	}
}
