package systemmachine

import (
	"bytes"
	"crypto/sha512"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/samsung"
	"github.com/mirusu400/aram-core/system"
)

type interruptCascadeProbe struct {
	lines [2]bool
	calls []struct {
		line     cpu.InterruptLine
		asserted bool
	}
}

func (p *interruptCascadeProbe) SetInterruptLine(line cpu.InterruptLine, asserted bool) error {
	p.lines[line] = asserted
	p.calls = append(p.calls, struct {
		line     cpu.InterruptLine
		asserted bool
	}{line: line, asserted: asserted})
	return nil
}

func TestQualcommLegacyInterruptCascadeRoutesThroughVectoredGroup(t *testing.T) {
	profile := system.SPHW4200DC17BoardProfile()
	probe := &interruptCascadeProbe{}
	vectored, err := system.NewQualcommVectoredInterruptController(
		*profile.VectoredInterrupt,
		probe,
	)
	check(t, err)
	cascade := profile.LegacyInterruptCascade
	legacy := system.NewQualcommInterruptController(qualcommInterruptCascadeSink{
		target:       vectored,
		source:       cascade.VectoredSource,
		statusOffset: cascade.GroupStatusOffset,
		mask:         cascade.GroupMask,
	})

	// Source 17 is reverse-packed as bank 1 bit 6. UIM is group child bit 1
	// and legacy QIC source 55 is bank 1 bit 23.
	check(t, vectored.Write(0x18, system.Width32, 0x02))
	check(t, vectored.Write(0x34, system.Width32, 1<<6))
	check(t, legacy.Write(0x18, system.Width32, 1<<23))
	check(t, legacy.SetSource(55, true))
	if !probe.lines[cpu.InterruptIRQ] || probe.lines[cpu.InterruptFIQ] {
		t.Fatalf("cascaded outputs IRQ=%v FIQ=%v", probe.lines[cpu.InterruptIRQ], probe.lines[cpu.InterruptFIQ])
	}
	if status, readErr := vectored.Read(0x8c, system.Width32); readErr != nil || status != 2 {
		t.Fatalf("cascaded group status = %#x error %v", status, readErr)
	}
	if vector, readErr := vectored.Read(0x9c, system.Width32); readErr != nil || vector != 31 {
		t.Fatalf("cascaded vector = %#x error %v", vector, readErr)
	}
	check(t, legacy.SetSource(55, false))
	check(t, legacy.Write(0x04, system.Width32, 1<<23))
	if status, _ := vectored.Read(0x8c, system.Width32); status != 0 {
		t.Fatalf("deasserted group status = %#x", status)
	}
}

func TestCompatibleCPUContextIdentityAllowsInterpreterTiers(t *testing.T) {
	precise := interpreter.New().Identity()
	jit := interpreter.NewJIT().Identity()
	jitLoops := interpreter.NewJITWithOptions(interpreter.JITOptions{LoopAcceleration: true}).Identity()
	if !compatibleCPUContextIdentity(precise, jit) ||
		!compatibleCPUContextIdentity(jit, precise) ||
		!compatibleCPUContextIdentity(precise, jitLoops) ||
		!compatibleCPUContextIdentity(jitLoops, jit) {
		t.Fatalf(
			"interpreter tier contexts are not portable: precise=%+v jit=%+v jit-loops=%+v",
			precise, jit, jitLoops,
		)
	}
	if compatibleCPUContextIdentity(precise, cpu.Identity{
		Name: "different-backend", Version: precise.Version, Architecture: precise.Architecture,
	}) {
		t.Fatal("unrelated backend was accepted as context-compatible")
	}
	wrongVersion := jit
	wrongVersion.Version = "different"
	if compatibleCPUContextIdentity(precise, wrongVersion) {
		t.Fatal("different interpreter context version was accepted")
	}
}

func TestSCHW830BatteryResponsesStayDL21Specific(t *testing.T) {
	dl21 := schw830BoardProfile(samsung.SCHW830DL21ProfileID)
	if len(dl21.BootControlSBIReadResponses) == 0 {
		t.Fatal("DL21 board profile has no battery SBI responses")
	}
	da18 := schw830BoardProfile(samsung.SCHW830DA18ProfileID)
	if len(da18.BootControlSBIReadResponses) != 0 {
		t.Fatalf("unevidenced DA18 battery SBI responses = %#v", da18.BootControlSBIReadResponses)
	}
}

func TestSamsungQualcommVerifiedPBLHandlerReturnsLoaderSuccess(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	for _, region := range []struct {
		address uint32
		size    uint32
	}{
		{address: 0x00080000, size: 0x00010000},
		{address: 0x00500000, size: 0x00100000},
		{address: 0x01880000, size: 0x00010000},
	} {
		check(t, backend.Map(region.address, region.size, cpu.PermissionRead|cpu.PermissionWrite))
	}
	qcsbl := make([]byte, samsungW320QCSBLUsedSize)
	for index := range qcsbl {
		qcsbl[index] = byte(index*17 + 3)
	}
	check(t, backend.WriteMemory(samsungW320QCSBLLoadAddress, qcsbl))
	check(t, backend.WriteMemory(samsungW320PBLVerifiedStatus, []byte{0xff}))
	handler := samsungQualcommHLEHandlers()[system.HLEContractQualcommPBLVerifiedLoaderState]
	if handler == nil {
		t.Fatal("verified PBL loader-state handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != 0x10 {
		t.Fatalf("verified PBL loader-state result = %#x, error %v", value, err)
	}
	verifiedCopy := make([]byte, len(qcsbl))
	check(t, backend.ReadMemory(samsungW320PBLVerifiedCopy, verifiedCopy))
	if !bytes.Equal(verifiedCopy, qcsbl) {
		t.Fatal("verified PBL QCSBL copy differs from its exact input")
	}
	record := make([]byte, 6+sha512.Size)
	check(t, backend.ReadMemory(samsungW320PBLVerifiedRecord, record))
	digest := sha512.Sum512(qcsbl)
	if binary.BigEndian.Uint32(record[:4]) != samsungW320QCSBLUsedSize ||
		record[4] != 0 || record[5] != 0 || !bytes.Equal(record[6:], digest[:]) {
		t.Fatal("verified PBL record does not describe the exact QCSBL")
	}
	status := []byte{0xff}
	check(t, backend.ReadMemory(samsungW320PBLVerifiedStatus, status))
	if status[0] != 0 {
		t.Fatalf("verified PBL status = %#x", status[0])
	}
}

func TestSCHW320ResetHandoffSeedsVerifiedPBLLoaderState(t *testing.T) {
	qcsblBytes := make([]byte, samsungW320QCSBLUsedSize+0x20)
	for index := range qcsblBytes {
		qcsblBytes[index] = byte(index*29 + 7)
	}
	handoff := system.BootHandoff{
		ID:    "synthetic-w320-pbl-handoff",
		Entry: samsungW320QCSBLLoadAddress,
		Mode:  cpu.ModeARM,
		Memory: []system.MemorySeed{{
			Address: samsungW320QCSBLLoadAddress,
			Bytes:   append([]byte(nil), qcsblBytes...),
		}},
	}
	qcsbl := samsung.BootImage{
		ID:          "qcsbl",
		LoadAddress: samsungW320QCSBLLoadAddress,
		UsedSize:    samsungW320QCSBLUsedSize,
		Bytes:       qcsblBytes,
	}
	check(t, appendSamsungW320VerifiedPBLState(&handoff, qcsbl))
	if err := handoff.Validate(); err != nil {
		t.Fatalf("seeded W320 reset handoff is invalid: %v", err)
	}
	if len(handoff.Memory) != 4 {
		t.Fatalf("W320 reset handoff memory seeds = %d, want 4", len(handoff.Memory))
	}
	verified := handoff.Memory[1]
	if verified.Address != samsungW320PBLVerifiedCopy ||
		!bytes.Equal(verified.Bytes, qcsblBytes[:samsungW320QCSBLUsedSize]) {
		t.Fatal("W320 reset handoff does not retain the exact used QCSBL")
	}
	record := handoff.Memory[2]
	digest := sha512.Sum512(qcsblBytes[:samsungW320QCSBLUsedSize])
	if record.Address != samsungW320PBLVerifiedRecord ||
		len(record.Bytes) != 6+sha512.Size ||
		binary.BigEndian.Uint32(record.Bytes[:4]) != samsungW320QCSBLUsedSize ||
		record.Bytes[4] != 0 || record.Bytes[5] != 0 ||
		!bytes.Equal(record.Bytes[6:], digest[:]) {
		t.Fatal("W320 reset handoff verification record is malformed")
	}
	status := handoff.Memory[3]
	if status.Address != samsungW320PBLVerifiedStatus || !bytes.Equal(status.Bytes, []byte{0}) {
		t.Fatalf("W320 reset handoff status seed = %#x/%#v", status.Address, status.Bytes)
	}
	qcsblBytes[0] ^= 0xff
	if verified.Bytes[0] == qcsblBytes[0] {
		t.Fatal("W320 reset handoff aliases the reconstructed QCSBL buffer")
	}
}

func TestSamsungQualcommVerifiedBootstrapHandlerReturnsSuccess(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	handler := samsungQualcommHLEHandlers()[system.HLEContractQualcommBootstrapVerifiedFirmware]
	if handler == nil {
		t.Fatal("verified bootstrap handler is missing")
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0xffffffff))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != 0 {
		t.Fatalf("verified bootstrap result = %#x, error %v", value, err)
	}
}

func TestSamsungQualcommResidentBootHandlerPreservesCallRegisters(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	handler := samsungQualcommHLEHandlers()[system.HLEContractQualcommResidentBootCallback]
	if handler == nil {
		t.Fatal("resident boot callback handler is missing")
	}
	const sentinel = uint32(0x04460c8c)
	check(t, backend.WriteRegister(cpu.RegisterR0, sentinel))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != sentinel {
		t.Fatalf("resident boot callback result = %#x, error %v", value, err)
	}
}

func TestSamsungOptionalPreloadFileHandlerReturnsAbsent(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungOptionalPreloadFile]
	if handler == nil {
		t.Fatal("optional preload file handler is missing")
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0xffffffff))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != 0 {
		t.Fatalf("optional preload file result = %#x, error %v", value, err)
	}
}

func TestSamsungQualcommRetainedPBLNANDHandlers(t *testing.T) {
	flash, err := system.NewErasedCOWFlash(0x8000, 0x4000, strings.Repeat("a", 64))
	check(t, err)
	page := bytes.Repeat([]byte{0x5a}, 0x200)
	check(t, flash.ProgramAt(page, 0x400))
	board := system.BoardProfile{
		NANDPageSize: 0x200, NANDEraseBlockSize: 0x4000,
		NANDFactoryBadBlocks: []uint32{1},
	}
	handlers := samsungQualcommMachineHLEHandlers(flash, nil, board, samsung.ProgressiveELF{}, nil, nil)
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.Map(0x2000, 0x2000, cpu.PermissionRead|cpu.PermissionWrite))
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: 2,
		cpu.RegisterR1: 0x200,
		cpu.RegisterR2: 2,
		cpu.RegisterR3: 0x2000,
		cpu.RegisterSP: 0x3000,
	} {
		check(t, backend.WriteRegister(register, value))
	}
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], 1)
	check(t, backend.WriteMemory(0x3000, count[:]))
	readHandler := handlers[system.HLEContractQualcommPBLNANDRead]
	if readHandler == nil {
		t.Fatal("retained PBL NAND read handler is missing")
	}
	check(t, readHandler.InvokeHLE(system.HLECallContext{CPU: backend}))
	output := make([]byte, len(page))
	check(t, backend.ReadMemory(0x2000, output))
	if !bytes.Equal(output, page) {
		t.Fatal("retained PBL NAND read did not copy the requested page")
	}
	if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != 1 {
		t.Fatalf("retained PBL NAND read result = %#x, error %v", value, err)
	}

	badBlockHandler := handlers[system.HLEContractQualcommPBLNANDBadBlock]
	if badBlockHandler == nil {
		t.Fatal("retained PBL NAND bad-block handler is missing")
	}
	for block, want := range map[uint32]uint32{0: 0, 1: 1} {
		check(t, backend.WriteRegister(cpu.RegisterR0, block))
		check(t, badBlockHandler.InvokeHLE(system.HLECallContext{CPU: backend}))
		if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != want {
			t.Fatalf("retained PBL NAND block %#x result = %#x, error %v", block, value, err)
		}
	}
	if err := handlers[system.HLEContractQualcommPBLFatal].InvokeHLE(
		system.HLECallContext{CPU: backend},
	); err == nil {
		t.Fatal("retained PBL fatal handler returned success")
	}
}

func TestSamsungProgressiveAMSSLoaderCopiesOnlyMappedSegments(t *testing.T) {
	flash, err := system.NewErasedCOWFlash(0x1000, 0x100, strings.Repeat("b", 64))
	check(t, err)
	check(t, flash.ProgramAt([]byte{0x11, 0x22, 0x33, 0x44}, 0x100))
	check(t, flash.ProgramAt([]byte{0x55, 0x66}, 0x200))

	progressive := samsung.ProgressiveELF{
		Entry: 0x1000,
		ProgramHeaders: []samsung.ELF32ProgramHeader{
			{Type: 1, Offset: 0x100, PhysicalAddress: 0x1000, FileSize: 4, MemorySize: 8},
			{Type: 1, Offset: 0x200, PhysicalAddress: 0x5000, FileSize: 2, MemorySize: 2},
		},
	}
	handlers := samsungQualcommMachineHLEHandlers(
		flash,
		nil,
		system.BoardProfile{},
		progressive,
		[]samsung.Partition{{Name: "0:AMSS", Start: 0, Size: 0x1000}},
		nil,
	)
	handler := handlers[system.HLEContractSamsungProgressiveAMSSLoad]
	if handler == nil {
		t.Fatal("progressive AMSS loader handler is missing")
	}

	bus := system.NewBus()
	check(t, bus.MapRAM("amss", 0x1000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteMemory(0x1000, bytes.Repeat([]byte{0xaa}, 8)))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))

	loaded := make([]byte, 8)
	check(t, backend.ReadMemory(0x1000, loaded))
	if !bytes.Equal(loaded, []byte{0x11, 0x22, 0x33, 0x44, 0, 0, 0, 0}) {
		t.Fatalf("loaded progressive segment = %x", loaded)
	}
	if entry, readErr := backend.ReadRegister(cpu.RegisterR0); readErr != nil || entry != progressive.Entry {
		t.Fatalf("progressive entry = %#x, error %v", entry, readErr)
	}
}

func TestSPHW4200ProgressiveAMSSLoaderUsesPhysicalBootSegmentPlacement(t *testing.T) {
	flash, err := system.NewErasedCOWFlash(0x1000, 0x100, strings.Repeat("c", 64))
	check(t, err)
	check(t, flash.ProgramAt([]byte{0xde, 0xca, 0xfb, 0xad}, 0x100))
	check(t, flash.ProgramAt([]byte{0x11, 0x22, 0x33, 0x44}, 0x200))
	progressive := samsung.ProgressiveELF{
		ProgramHeaders: []samsung.ELF32ProgramHeader{{
			Type: 1, Offset: 0x100, PhysicalAddress: 0x200,
			FileSize: 4, MemorySize: 4, Flags: 0x05600005,
		}},
	}
	handlers := samsungQualcommMachineHLEHandlers(
		flash, nil,
		system.BoardProfile{ID: "samsung.sph-w4200"},
		progressive,
		[]samsung.Partition{{Name: "0:AMSS", Start: 0, Size: 0x1000}},
		nil,
	)
	bus := system.NewBus()
	check(t, bus.MapRAM("amss", 0, 0x2000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, handlers[system.HLEContractSamsungProgressiveAMSSLoad].InvokeHLE(
		system.HLECallContext{CPU: backend, Bus: bus},
	))
	loaded := make([]byte, 4)
	check(t, backend.ReadMemory(0x200, loaded))
	if !bytes.Equal(loaded, []byte{0x11, 0x22, 0x33, 0x44}) {
		t.Fatalf("W4200 physical progressive segment = %x", loaded)
	}
}

func TestSPHW4200AMSSResetCookieMatchesVectorContract(t *testing.T) {
	if samsungW4200AMSSResetCookieStore != 0xffffdfc0+cpu.RegisterR12*4 {
		t.Fatalf("W4200 AMSS reset cookie address = %#08x", samsungW4200AMSSResetCookieStore)
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], samsungW4200AMSSResetCookie)
	if !bytes.Equal(encoded[:], []byte{0x78, 0x56, 0x34, 0x12}) {
		t.Fatalf("W4200 AMSS reset cookie = %x", encoded)
	}
}

func TestSCHW340RetainedLoaderSeedsMatchNativeContracts(t *testing.T) {
	staticModules := samsungW340StaticModuleListSeed()
	if staticModules.Address != samsungW340StaticModuleList || len(staticModules.Bytes) != int(samsungW340StaticModuleCount)*4 ||
		len(samsungW340StaticModuleEntries) != 128 ||
		binary.LittleEndian.Uint32(staticModules.Bytes) != samsungW340StaticModuleEntries[0] ||
		binary.LittleEndian.Uint32(staticModules.Bytes[(len(samsungW340StaticModuleEntries)-1)*4:]) != samsungW340StaticModuleEntries[len(samsungW340StaticModuleEntries)-1] ||
		binary.LittleEndian.Uint32(staticModules.Bytes[len(samsungW340StaticModuleEntries)*4:]) != 0 ||
		staticModules.Address+uint32(len(staticModules.Bytes)) != samsungW340StaticClassList {
		t.Fatalf("W340 static-module list seed = %#08x/%x", staticModules.Address, staticModules.Bytes)
	}
	wantedModules := map[uint32]bool{
		samsungW340IdleModuleEntry:      false,
		samsungW340MainModuleEntry:      false,
		samsungW340Class7003ModuleEntry: false,
		samsungW340Class7017ModuleEntry: false,
	}
	for index, entry := range samsungW340StaticModuleEntries {
		if entry&1 == 0 || binary.LittleEndian.Uint32(staticModules.Bytes[index*4:]) != entry {
			t.Fatalf("W340 static-module entry %d = %#08x", index, entry)
		}
		if _, ok := wantedModules[entry]; ok {
			wantedModules[entry] = true
		}
	}
	for entry, found := range wantedModules {
		if !found {
			t.Fatalf("W340 static-module entry %#08x is missing", entry)
		}
	}
	classRecords := samsungW340DownloadableClassRecordSeeds()
	if len(classRecords) != len(samsungW340DownloadableClassRecords) {
		t.Fatalf("W340 downloadable-class record count = %d", len(classRecords))
	}
	for index, classRecord := range classRecords {
		want := samsungW340DownloadableClassRecords[index]
		if classRecord.Address != want.address || len(classRecord.Bytes) != 0x14 ||
			binary.LittleEndian.Uint32(classRecord.Bytes) != want.classID ||
			!bytes.Equal(classRecord.Bytes[4:], make([]byte, 0x10)) {
			t.Fatalf("W340 downloadable-class record %d = %#08x/%x", index, classRecord.Address, classRecord.Bytes)
		}
	}
	oemClasses := make([]byte, int(samsungW340OEMClassCount)*0x10)
	binary.LittleEndian.PutUint32(oemClasses[0x00:], 0x01006388)
	binary.LittleEndian.PutUint32(oemClasses[0x04:], 0xffff0000)
	binary.LittleEndian.PutUint32(oemClasses[0x08:], 0x01c9db15)
	binary.LittleEndian.PutUint32(oemClasses[0x0c:], 0x015d7a59)
	staticClasses, err := samsungW340StaticClassSeeds(oemClasses)
	check(t, err)
	if samsungW340OEMClassTable != 0x01fa7804 ||
		len(staticClasses) != 2 || staticClasses[0].Address != samsungW340StaticClassBacking ||
		staticClasses[1].Address != samsungW340StaticClassList ||
		binary.LittleEndian.Uint32(staticClasses[1].Bytes) != samsungW340StaticClassBacking ||
		binary.LittleEndian.Uint32(staticClasses[0].Bytes[0x00:]) != 0x01006388 ||
		binary.LittleEndian.Uint32(staticClasses[0].Bytes[0x04:]) != 0xffff0000 ||
		binary.LittleEndian.Uint32(staticClasses[0].Bytes[0x08:]) != 0x01c9db15 ||
		binary.LittleEndian.Uint32(staticClasses[0].Bytes[0x0c:]) != 0x015d7a59 ||
		!bytes.Equal(staticClasses[0].Bytes[len(staticClasses[0].Bytes)-0x10:], make([]byte, 0x10)) {
		t.Fatalf("W340 static-class seeds = %#v", staticClasses)
	}
	retainedService := samsungW340RetainedServiceSeeds()
	if len(retainedService) != 5 ||
		retainedService[0].Address != samsungW340RetainedServiceObject ||
		binary.LittleEndian.Uint32(retainedService[0].Bytes) != samsungW340RetainedServiceVTable ||
		binary.LittleEndian.Uint32(retainedService[0].Bytes[4:]) != 1 ||
		retainedService[1].Address != samsungW340RetainedServiceVTable ||
		binary.LittleEndian.Uint32(retainedService[1].Bytes) != samsungW340RetainedServiceAddRef ||
		binary.LittleEndian.Uint32(retainedService[1].Bytes[4:]) != samsungW340RetainedServiceRelease ||
		binary.LittleEndian.Uint32(retainedService[1].Bytes[0x3c:]) != samsungW340RetainedServiceNoop|1 ||
		!bytes.Equal(retainedService[2].Bytes, []byte{0x00, 0x20, 0x70, 0x47}) ||
		retainedService[3].Address != samsungW340RetainedServiceIdentity ||
		!bytes.Equal(retainedService[3].Bytes, []byte{0x70, 0x47}) ||
		binary.LittleEndian.Uint32(retainedService[4].Bytes) != samsungW340RetainedServiceObject {
		t.Fatalf("W340 retained-service seeds = %#v", retainedService)
	}
	connectionManager := samsungW340ConnectionManagerSeeds()
	if len(connectionManager) != 3 ||
		connectionManager[0].Address != samsungW340ConnectionManagerObject ||
		binary.LittleEndian.Uint32(connectionManager[0].Bytes) != samsungW340ConnectionManagerVTable ||
		binary.LittleEndian.Uint32(connectionManager[0].Bytes[4:]) != 1 ||
		connectionManager[1].Address != samsungW340ConnectionManagerVTable ||
		len(connectionManager[1].Bytes) != 0x100 ||
		binary.LittleEndian.Uint32(connectionManager[1].Bytes) != samsungW340RetainedServiceNoop|1 ||
		binary.LittleEndian.Uint32(connectionManager[1].Bytes[0xfc:]) != samsungW340RetainedServiceNoop|1 ||
		connectionManager[2].Address != samsungW340ConnectionManagerStore ||
		binary.LittleEndian.Uint32(connectionManager[2].Bytes) != samsungW340ConnectionManagerObject {
		t.Fatalf("W340 connection-manager seeds = %#v", connectionManager)
	}
	displayProvider := samsungW340DisplayProviderSeeds()
	if len(displayProvider) != 10 ||
		displayProvider[0].Address != samsungW340DisplayProviderObject ||
		binary.LittleEndian.Uint32(displayProvider[0].Bytes) != samsungW340DisplayProviderVTable ||
		binary.LittleEndian.Uint32(displayProvider[0].Bytes[8:]) != 1 ||
		displayProvider[1].Address != samsungW340DisplayProviderVTable ||
		binary.LittleEndian.Uint32(displayProvider[1].Bytes[8:]) != samsungW340DisplayProviderCreate ||
		binary.LittleEndian.Uint32(displayProvider[1].Bytes[0x2c:]) != samsungW340DisplayProviderLookup|1 ||
		displayProvider[3].Address != samsungW340DisplayProviderLookup ||
		binary.LittleEndian.Uint32(displayProvider[3].Bytes[len(displayProvider[3].Bytes)-8:]) != 0x8000 ||
		binary.LittleEndian.Uint32(displayProvider[3].Bytes[len(displayProvider[3].Bytes)-4:]) != samsungW340FontInterfaceObject ||
		binary.LittleEndian.Uint32(displayProvider[4].Bytes) != samsungW340DisplayProviderObject ||
		displayProvider[5].Address != samsungW340FontInterfaceObject ||
		binary.LittleEndian.Uint32(displayProvider[5].Bytes) != samsungW340FontInterfaceVTable ||
		displayProvider[6].Address != samsungW340FontInterfaceVTable ||
		binary.LittleEndian.Uint32(displayProvider[6].Bytes[0x0c:]) != samsungW340FontInterfaceDraw|1 ||
		binary.LittleEndian.Uint32(displayProvider[6].Bytes[0x10:]) != samsungW340FontInterfaceMeasure|1 ||
		binary.LittleEndian.Uint32(displayProvider[6].Bytes[0x14:]) != samsungW340FontInterfaceInfo|1 {
		t.Fatalf("W340 display-provider seeds = %#v", displayProvider)
	}
	brewNVM := samsungW340BREWNVMSeeds()
	if len(brewNVM) != 2 ||
		brewNVM[0].Address != samsungW340BREWNVMFileTable || len(brewNVM[0].Bytes) != 12 ||
		binary.LittleEndian.Uint32(brewNVM[0].Bytes) != samsungW340BREWNVMFilename ||
		!bytes.Equal(brewNVM[0].Bytes[4:], make([]byte, 8)) ||
		brewNVM[1].Address != samsungW340BREWNVMFilename ||
		!bytes.Equal(brewNVM[1].Bytes, []byte("prefs.dat\x00")) {
		t.Fatalf("W340 BREW NVM seeds = %#v", brewNVM)
	}
	posDetTimer := samsungW340PosDetTimerSeeds()
	if len(posDetTimer) != 3 ||
		posDetTimer[0].Address != samsungW340PosDetTimerOwnerStore ||
		binary.LittleEndian.Uint32(posDetTimer[0].Bytes) != samsungW340PosDetTimerOwnerSlot ||
		posDetTimer[1].Address != samsungW340PosDetTimerOwnerSlot ||
		binary.LittleEndian.Uint32(posDetTimer[1].Bytes) != samsungW340PosDetTimerManager ||
		posDetTimer[2].Address != samsungW340PosDetTimerManager ||
		!bytes.Equal(posDetTimer[2].Bytes, make([]byte, 0x18)) {
		t.Fatalf("W340 position timer seeds = %+v", posDetTimer)
	}
	moduleInitializer := samsungW340ModuleInitializerSeed()
	if moduleInitializer.Address != samsungW340ModuleInitializerStore || len(moduleInitializer.Bytes) != 16*4 {
		t.Fatalf("W340 module initializer seed = %#08x/%d", moduleInitializer.Address, len(moduleInitializer.Bytes))
	}
	for offset := 0; offset < len(moduleInitializer.Bytes); offset += 4 {
		if got := binary.LittleEndian.Uint32(moduleInitializer.Bytes[offset:]); got != samsungW340RetainedServiceNoop|1 {
			t.Fatalf("W340 module initializer slot %#x = %#08x", offset, got)
		}
	}
	for name, seed := range map[string]system.MemorySeed{
		"device callback": samsungW340EFS2DeviceCallbackSeed(),
		"IRQ handler":     samsungW340IRQHandlerSeed(),
		"BCX pool":        samsungW340BCXPoolPointerSeed(),
		"BCIM_AV signals": samsungW340BCIMAVSignalMaskSeed(),
		"BCIM signals":    samsungW340BCIMSignalMaskSeed(),
	} {
		if len(seed.Bytes) != 4 {
			t.Fatalf("W340 %s seed length = %d", name, len(seed.Bytes))
		}
	}
	if seed := samsungW340EFS2DeviceCallbackSeed(); seed.Address != samsungW340EFS2DeviceCallback ||
		binary.LittleEndian.Uint32(seed.Bytes) != samsungW340EFS2AbsentDeviceHandler|1 {
		t.Fatalf("W340 device callback seed = %#08x/%x", seed.Address, seed.Bytes)
	}
	absentDeviceHandler := samsungW340EFS2AbsentDeviceHandlerSeed()
	if absentDeviceHandler.Address != samsungW340EFS2AbsentDeviceHandler ||
		len(absentDeviceHandler.Bytes) != 0x20 ||
		!bytes.Equal(absentDeviceHandler.Bytes[0x0e:0x12], []byte{0x00, 0x22, 0x22, 0x71}) {
		t.Fatalf("W340 absent-device handler does not release its message slot: %#08x/%x", absentDeviceHandler.Address, absentDeviceHandler.Bytes)
	}
	if seed := samsungW340BCXPoolPointerSeed(); seed.Address != samsungW340BCXPoolPointer ||
		binary.LittleEndian.Uint32(seed.Bytes) != samsungW340BCXPoolBacking ||
		samsungW340BCXPoolBacking+samsungW340BCXPoolSize != samsungW340AMSSHeapBase {
		t.Fatalf("W340 BCX pool seed = %#08x/%x", seed.Address, seed.Bytes)
	}
	if seed := samsungW340BCIMAVSignalMaskSeed(); seed.Address != samsungW340BCIMAVSignalMaskStore ||
		binary.LittleEndian.Uint32(seed.Bytes) != samsungW340BCIMAVSignalMask {
		t.Fatalf("W340 BCIM_AV signal-mask seed = %#08x/%x", seed.Address, seed.Bytes)
	}
	if seed := samsungW340BCIMSignalMaskSeed(); seed.Address != samsungW340BCIMSignalMaskStore ||
		binary.LittleEndian.Uint32(seed.Bytes) != samsungW340BCIMSignalMask {
		t.Fatalf("W340 BCIM signal-mask seed = %#08x/%x", seed.Address, seed.Bytes)
	}
	bcxInterfaces := samsungW340BCXInterfaceSeeds()
	if len(bcxInterfaces) != 3 {
		t.Fatalf("W340 BCX interface seed count = %d", len(bcxInterfaces))
	}
	for index, seed := range bcxInterfaces {
		wantAddress := samsungW340BCXInterfaceBase + uint32(index)*samsungW340BCXInterfaceStride + 4
		if seed.Address != wantAddress || len(seed.Bytes) != 8 ||
			binary.LittleEndian.Uint32(seed.Bytes[0:]) != samsungW340BCXUnavailableCallback ||
			binary.LittleEndian.Uint32(seed.Bytes[4:]) != samsungW340BCXUnavailableCallback {
			t.Fatalf("W340 BCX interface seed %d = %#08x/%x", index, seed.Address, seed.Bytes)
		}
	}
	if seed := samsungW340IRQHandlerSeed(); seed.Address != samsungW340IRQHandlerStore ||
		binary.LittleEndian.Uint32(seed.Bytes) != samsungW340IRQHandler {
		t.Fatalf("W340 IRQ handler seed = %#08x/%x", seed.Address, seed.Bytes)
	}
	interruptTable := samsungW340InterruptTableSeed()
	if interruptTable.Address != samsungW340InterruptTable || len(interruptTable.Bytes) != 49*0x34 {
		t.Fatalf("W340 interrupt-table seed = %#08x/%d", interruptTable.Address, len(interruptTable.Bytes))
	}
	timeTick := interruptTable.Bytes[21*0x34 : 22*0x34]
	for offset, want := range map[int]uint32{
		0x00: 0x01041147,
		0x04: 0x00000101,
		0x08: 0x00000049,
		0x18: 0x80000478,
		0x1c: 0x80000434,
		0x20: 0x80000404,
		0x24: 0x8000045c,
		0x28: 0x8000042c,
	} {
		if got := binary.LittleEndian.Uint32(timeTick[offset:]); got != want {
			t.Fatalf("W340 time-tick interrupt[%#x] = %#08x, want %#08x", offset, got, want)
		}
	}
	sleepClock := interruptTable.Bytes[13*0x34 : 14*0x34]
	if got := binary.LittleEndian.Uint32(sleepClock); got != 0x020b9989 {
		t.Fatalf("W340 sleep-clock callback = %#08x", got)
	}
	if got := binary.LittleEndian.Uint32(sleepClock[0x18:]); got != 0x80000478 {
		t.Fatalf("W340 sleep-clock status pointer = %#08x", got)
	}
	uimInterrupt := samsungW340UIMGroupedInterruptSeed()
	if uimInterrupt.Address != samsungW340GroupedInterruptTable ||
		len(uimInterrupt.Bytes) != 24*0x3c {
		t.Fatalf("W340 UIM grouped-interrupt seed = %#08x/%d", uimInterrupt.Address, len(uimInterrupt.Bytes))
	}
	if got := binary.LittleEndian.Uint32(uimInterrupt.Bytes[3*0x3c+0x24:]); got != 0x8000048c {
		t.Fatalf("W340 UIM group status pointer = %#08x", got)
	}
	uimRecord := uimInterrupt.Bytes[4*0x3c : 5*0x3c]
	for offset, want := range map[int]uint32{
		0x00: samsungW340DefaultInterruptHandler,
		0x08: 0x00000002,
		0x10: 0x00000002,
		0x1c: 0x00000011,
		0x20: 0x80000418,
		0x24: 0x8000048c,
		0x28: 0x80000460,
		0x30: 0x00000001,
	} {
		if got := binary.LittleEndian.Uint32(uimRecord[offset:]); got != want {
			t.Fatalf("W340 UIM grouped-interrupt[%#x] = %#08x, want %#08x", offset, got, want)
		}
	}
	vtable := samsungW340EFS2DeviceVTableSeed()
	if vtable.Address != samsungW340EFS2DeviceVTable || len(vtable.Bytes) != 18*4 {
		t.Fatalf("W340 EFS2 vtable seed = %#08x/%d", vtable.Address, len(vtable.Bytes))
	}
	for offset, want := range map[int]uint32{
		0x00: 0x0206f8bb,
		0x14: 0x0206fadf,
		0x18: 0x0206fb2b,
		0x28: 0x00040000,
		0x2c: 0x08000000,
		0x44: 0xffffffff,
	} {
		if got := binary.LittleEndian.Uint32(vtable.Bytes[offset:]); got != want {
			t.Fatalf("W340 EFS2 vtable[%#x] = %#08x, want %#08x", offset, got, want)
		}
	}
	fileVTable := samsungW340POSIXFileVTableSeed()
	if fileVTable.Address != samsungW340POSIXFileVTable || len(fileVTable.Bytes) != 3*4 {
		t.Fatalf("W340 POSIX file vtable seed = %#08x/%d", fileVTable.Address, len(fileVTable.Bytes))
	}
	for offset, want := range map[int]uint32{
		0x00: 0x0052825d,
		0x04: 0x00528133,
		0x08: 0x00528171,
	} {
		if got := binary.LittleEndian.Uint32(fileVTable.Bytes[offset:]); got != want {
			t.Fatalf("W340 POSIX file vtable[%#x] = %#08x, want %#08x", offset, got, want)
		}
	}
	charset := samsungW340CharsetSeeds()
	if len(charset) != 5 || charset[0].Address != samsungW340CharsetStore ||
		binary.LittleEndian.Uint32(charset[0].Bytes) != samsungW340CharsetDescriptor {
		t.Fatalf("W340 character-set seeds = %+v", charset)
	}
	descriptor := charset[1].Bytes
	for offset, want := range map[int]uint32{
		0x04: 1,
		0x10: samsungW340CharsetDecodeTable,
		0x14: samsungW340CharsetEncodeTable,
		0x18: samsungW340CharsetEncodeOffsets,
	} {
		if got := binary.LittleEndian.Uint32(descriptor[offset:]); got != want {
			t.Fatalf("W340 character-set descriptor[%#x] = %#08x, want %#08x", offset, got, want)
		}
	}
	if got := binary.LittleEndian.Uint16(charset[2].Bytes['a'*2:]); got != 'a' ||
		charset[3].Bytes['a'] != 'a' {
		t.Fatalf("W340 character-set ASCII mapping = %#x/%#x", got, charset[3].Bytes['a'])
	}
	rexObjectList := samsungW340REXObjectListSeed()
	if rexObjectList.Address != samsungW340REXObjectListHeadStore ||
		binary.LittleEndian.Uint32(rexObjectList.Bytes) != samsungW340REXObjectListAnchor {
		t.Fatalf("W340 REX object-list seed = %+v", rexObjectList)
	}
	cleanup := samsungW340CleanupListSeeds()
	if len(cleanup) != 3 || cleanup[0].Address != samsungW340CleanupListHeadStore ||
		binary.LittleEndian.Uint32(cleanup[0].Bytes) != samsungW340CleanupListSentinel ||
		binary.LittleEndian.Uint32(cleanup[1].Bytes[8:]) != samsungW340CleanupListNextStub|1 ||
		binary.LittleEndian.Uint32(cleanup[2].Bytes[4:]) != samsungW340CleanupListSentinel {
		t.Fatalf("W340 cleanup-list seeds = %+v", cleanup)
	}
	scriptPointer := samsungW340SDSSScriptPointerSeed()
	if scriptPointer.Address != samsungW340SDSSScriptStore ||
		binary.LittleEndian.Uint32(scriptPointer.Bytes) != samsungW340SDSSScriptBacking ||
		samsungW340SDSSScriptBacking+0x140 > samsungW340NVBufferBacking {
		t.Fatalf("W340 SD script-state seed = %+v", scriptPointer)
	}
	nvBuffer := samsungW340NVBufferPointerSeed()
	if nvBuffer.Address != samsungW340NVBufferStore ||
		binary.LittleEndian.Uint32(nvBuffer.Bytes) != samsungW340NVBufferBacking ||
		samsungW340NVBufferBacking+0x80 > samsungW340FontGlyphMap {
		t.Fatalf("W340 NV buffer seed = %+v", nvBuffer)
	}
	deviceSeeds := samsungW340DeviceTableSeeds()
	if len(deviceSeeds) != 2 || deviceSeeds[0].Address != samsungW340DeviceTable ||
		len(deviceSeeds[0].Bytes) != 3*0x18 ||
		deviceSeeds[1].Address != samsungW340DeviceObjectBacking ||
		len(deviceSeeds[1].Bytes) != 3*int(samsungW340DeviceObjectSize) {
		t.Fatalf("W340 device-table seeds = %+v", deviceSeeds)
	}
	for index, wantCallback := range []uint32{
		samsungW340DeviceCallback0,
		samsungW340DeviceCallback1,
		samsungW340DeviceCallback2,
	} {
		record := deviceSeeds[0].Bytes[index*0x18:]
		if got := binary.LittleEndian.Uint32(record[0x00:]); got != uint32(index) {
			t.Fatalf("W340 device table[%d].index = %#08x", index, got)
		}
		if got := binary.LittleEndian.Uint32(record[0x04:]); got != samsungW340DeviceObjectBacking+uint32(index)*samsungW340DeviceObjectSize {
			t.Fatalf("W340 device table[%d].object = %#08x", index, got)
		}
		if got := binary.LittleEndian.Uint32(record[0x08:]); got != samsungW340DeviceCommonCallback {
			t.Fatalf("W340 device table[%d].constructor = %#08x", index, got)
		}
		if got := binary.LittleEndian.Uint32(record[0x0c:]); got != wantCallback {
			t.Fatalf("W340 device table[%d].callback = %#08x", index, got)
		}
	}
	uiInitialization := samsungW340UIInitializationSeed()
	if uiInitialization.Address != samsungW340UIInitializationFlag ||
		len(uiInitialization.Bytes) != 1 || uiInitialization.Bytes[0] != 1 {
		t.Fatalf("W340 UI initialization seed = %+v", uiInitialization)
	}
	brewTableOffset := samsungW340BREWTableOffsetSeed()
	if brewTableOffset.Address != samsungW340BREWResourceOffsetsStore ||
		len(brewTableOffset.Bytes) != 16 ||
		binary.LittleEndian.Uint32(brewTableOffset.Bytes[0:]) != samsungW340BREWUTFOffset ||
		binary.LittleEndian.Uint32(brewTableOffset.Bytes[4:]) != samsungW340BREWDictionaryOffset ||
		binary.LittleEndian.Uint32(brewTableOffset.Bytes[8:]) != samsungW340BREWTableOffset ||
		binary.LittleEndian.Uint32(brewTableOffset.Bytes[12:]) != 0 ||
		samsungW340BREWUTFOffset != 0x02dc4b30 ||
		samsungW340BREWDictionaryOffset != 0x0000135c ||
		samsungW340BREWTableOffset != 0x00b2fcb8 {
		t.Fatalf("W340 BREW resource-offset seed = %+v", brewTableOffset)
	}
	lsmCallbacks := samsungW340LSMCallbackTableSeed()
	if lsmCallbacks.Address != samsungW340LSMCallbackTableStore ||
		binary.LittleEndian.Uint32(lsmCallbacks.Bytes) != samsungW340LSMCallbackTable {
		t.Fatalf("W340 LSM callback-table seed = %+v", lsmCallbacks)
	}
	dispatchObject := samsungW340DefaultDispatchObjectSeed()
	if dispatchObject.Address != samsungW340DefaultDispatchObject ||
		len(dispatchObject.Bytes) != int(samsungW340DefaultDispatchSlots*4) {
		t.Fatalf("W340 default dispatch-object seed = %#08x/%d", dispatchObject.Address, len(dispatchObject.Bytes))
	}
	for offset := uint32(0); offset < samsungW340DefaultDispatchSlots; offset++ {
		if got := binary.LittleEndian.Uint32(dispatchObject.Bytes[offset*4:]); got != samsungW340BCXUnavailableCallback {
			t.Fatalf("W340 default dispatch-object slot %d = %#08x", offset, got)
		}
	}
	diagBuffer := samsungW340DIAGBufferPointerSeed()
	if diagBuffer.Address != samsungW340DIAGBufferStore ||
		binary.LittleEndian.Uint32(diagBuffer.Bytes) != samsungW340DIAGBufferBacking ||
		samsungW340DIAGBufferBacking != 0x02ffb360+0x38 {
		t.Fatalf("W340 DIAG buffer seed = %+v", diagBuffer)
	}
	radioState := samsungW340RadioStatePointerSeed()
	if radioState.Address != samsungW340RadioStateStore ||
		binary.LittleEndian.Uint32(radioState.Bytes) != samsungW340RadioStateBacking ||
		samsungW340RadioStateBacking != 0x0422b1a1+3 {
		t.Fatalf("W340 radio-state seed = %+v", radioState)
	}
	radioWorkspace := samsungW340RadioWorkspaceSeed()
	if radioWorkspace.Address != samsungW340RadioWorkspace || len(radioWorkspace.Bytes) != 0x40 {
		t.Fatalf("W340 radio-workspace seed = %#08x/%d", radioWorkspace.Address, len(radioWorkspace.Bytes))
	}
	for offset, want := range map[int]uint32{
		0x00: 0x00010101,
		0x04: samsungW340RadioWorkspacePrimary,
		0x08: samsungW340RadioWorkspaceBuffer,
		0x0c: samsungW340RadioWorkspacePrimary + 8,
		0x28: samsungW340RadioWorkspaceBuffer,
		0x2c: 1,
		0x30: samsungW340RadioWorkspaceBuffer,
		0x34: 1,
		0x38: samsungW340RadioWorkspaceTail,
		0x3c: samsungW340RadioWorkspaceBuffer,
	} {
		if got := binary.LittleEndian.Uint32(radioWorkspace.Bytes[offset:]); got != want {
			t.Fatalf("W340 radio-workspace[%#x] = %#08x, want %#08x", offset, got, want)
		}
	}
	mgpQueuePool := samsungW340MGPQueuePoolPointerSeed()
	if mgpQueuePool.Address != samsungW340MGPQueuePoolStore ||
		binary.LittleEndian.Uint32(mgpQueuePool.Bytes) != samsungW340MGPQueuePoolBacking ||
		samsungW340MGPQueuePoolBacking+samsungW340MGPQueuePoolSize > samsungW340FontDescriptor {
		t.Fatalf("W340 MGP queue-pool seed = %+v", mgpQueuePool)
	}
}

func TestSCHW340MGPFrameCounterHandlerAdvancesSelectedClock(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	permissions := cpu.PermissionRead | cpu.PermissionWrite
	for _, region := range []struct {
		address uint32
		size    uint32
	}{
		{0x00ed7000, 0x1000},
		{0x015ab000, 0x2000},
		{0x90108000, 0x2000},
	} {
		check(t, backend.Map(region.address, region.size, permissions))
	}
	writeWord := func(address, value uint32) {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], value)
		check(t, backend.WriteMemory(address, encoded[:]))
	}
	writeWord(0x00ed703c, 0x015ab1c0)
	writeWord(0x015ab1d0, 0x9010802c)
	writeWord(0x9010802c, 0x90109bd4)
	check(t, backend.WriteMemory(0x90109bd7, []byte{1}))
	writeWord(0x90109bdc, 41)

	handler := samsungQualcommMachineHLEHandlers(
		nil, nil, system.SCHW340DC18BoardProfile(), samsung.ProgressiveELF{}, nil, nil,
	)[system.HLEContractSamsungW340MGPFrameCounter]
	if handler == nil {
		t.Fatal("W340 MGP frame-counter handler is missing")
	}
	for _, want := range []uint32{42, 43} {
		check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
		got, err := backend.ReadRegister(cpu.RegisterR0)
		check(t, err)
		if got != want {
			t.Fatalf("W340 MGP frame-counter result = %d, want %d", got, want)
		}
	}
	var encoded [4]byte
	check(t, backend.ReadMemory(0x90109bdc, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != 43 {
		t.Fatalf("W340 selected MGP frame counter = %d, want 43", got)
	}
}

func TestSCHW340PointerAccessPolicyApprovesMappedMemory(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })

	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340PointerAccessPolicy]
	if handler == nil {
		t.Fatal("W340 pointer-access-policy handler is missing")
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != 1 {
		t.Fatalf("W340 pointer access result = %d, want 1", got)
	}
}

func TestSCHW340ConnectionManagerPublishesOfflineSingleton(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-connection-manager-store", 0x03c55000, 0x1000))
	check(t, bus.MapRAM("w340-connection-manager-object", 0x07fd1000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340ConnectionManager]
	if handler == nil {
		t.Fatal("W340 connection-manager handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != samsungW340ConnectionManagerObject {
		t.Fatalf("W340 connection-manager result = %#08x", got)
	}
	var encoded [4]byte
	check(t, backend.ReadMemory(samsungW340ConnectionManagerStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340ConnectionManagerObject {
		t.Fatalf("W340 connection-manager store = %#08x", got)
	}
}

func TestSCHW340MainAppletLifecyclePublishesReadyCallback(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-main-applet", 0x06100000, 0x1000))
	check(t, bus.MapRAM("w340-main-lifecycle-callbacks", 0x07fd1000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	owner := uint32(0x06100100)
	object := make([]byte, 0x2c)
	binary.LittleEndian.PutUint32(object[0x04:], 0x01007002)
	binary.LittleEndian.PutUint32(object[0x18:], 0x012bac79)
	binary.LittleEndian.PutUint32(object[0x1c:], 0x012bac15)
	check(t, backend.WriteMemory(owner, object))
	check(t, backend.WriteRegister(cpu.RegisterR4, owner))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340MainAppletLifecycle]
	if handler == nil {
		t.Fatal("W340 MainApp-lifecycle handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	check(t, backend.ReadMemory(owner, object))
	if got := binary.LittleEndian.Uint32(object[0x20:]); got != 7 {
		t.Fatalf("W340 MainApp lifecycle state = %d, want 7", got)
	}
	if got := binary.LittleEndian.Uint32(object[0x28:]); got != samsungW340MainLifecycleCallbacks {
		t.Fatalf("W340 MainApp lifecycle callbacks = %#08x", got)
	}
	var table [8]byte
	check(t, backend.ReadMemory(samsungW340MainLifecycleCallbacks, table[:]))
	if got := binary.LittleEndian.Uint32(table[0:]); got != samsungW340RetainedServiceNoop|1 {
		t.Fatalf("W340 MainApp lifecycle attach callback = %#08x", got)
	}
	if got := binary.LittleEndian.Uint32(table[4:]); got != samsungW340DisplayProviderRelease|1 {
		t.Fatalf("W340 MainApp lifecycle event callback = %#08x", got)
	}
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != 7 {
		t.Fatalf("W340 MainApp lifecycle result = %d, want 7", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR6, 0xdeadbeef))
	check(t, handler.InvokeHLE(system.HLECallContext{
		Call: system.HLECallProfile{Address: 0x012b3692},
		CPU:  backend,
	}))
	got, err = backend.ReadRegister(cpu.RegisterR6)
	check(t, err)
	if got != 0 {
		t.Fatalf("W340 MainApp prestart r6 = %#08x, want 0", got)
	}
}

func TestSCHW340RetainedHomeCommitsBitmapAndUprightPanelFrame(t *testing.T) {
	profile := system.SCHW340DC18BoardProfile()
	controller, err := system.NewDCSPanelController(profile.Panel)
	check(t, err)
	panel, err := system.NewParallelPanelInterfaceWithController(controller)
	check(t, err)
	commandPort, err := system.NewParallelPanelCommandPort(panel)
	check(t, err)
	dataPort, err := system.NewParallelPanelDataPort(panel)
	check(t, err)

	bus := system.NewBus()
	check(t, bus.MapRAM("w340-retained-framebuffer", 0x02d90000, 0x40000))
	check(t, bus.MapMMIO("w340-panel-command", samsungW340PanelCommandPort, 0x80, commandPort))
	check(t, bus.MapMMIO("w340-panel-data", samsungW340PanelDataPort, 0x80, dataPort))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))

	check(t, renderSamsungW340RetainedHome(system.HLECallContext{CPU: backend, Bus: bus}))
	want := samsungW340RetainedHomeFrame()
	encoded := make([]byte, len(want)*2)
	for index, value := range want {
		binary.LittleEndian.PutUint16(encoded[index*2:], value)
	}
	gotBitmap := make([]byte, len(encoded))
	check(t, backend.ReadMemory(samsungW340DisplayBitmap, gotBitmap))
	if !bytes.Equal(gotBitmap, encoded) {
		t.Fatal("W340 retained guest framebuffer differs from composed home")
	}
	gotFrame := controller.FrameRGB565()
	if len(gotFrame) != len(want) {
		t.Fatalf("W340 retained panel pixels = %d, want %d", len(gotFrame), len(want))
	}
	for index := range want {
		if gotFrame[index] != want[index] {
			t.Fatalf("W340 retained panel pixel %d = %#04x, want %#04x", index, gotFrame[index], want[index])
		}
	}
	pixels, updates := controller.WriteCounts()
	if pixels != samsungW340HomeWidth*samsungW340HomeHeight || updates != 1 {
		t.Fatalf("W340 retained panel counts = %d/%d", pixels, updates)
	}
	if want[10*samsungW340HomeWidth+10] == want[160*samsungW340HomeWidth+120] ||
		want[160*samsungW340HomeWidth+120] == want[300*samsungW340HomeWidth+10] {
		t.Fatal("W340 retained home lacks distinct status, carousel, and clock regions")
	}
}

func TestSCHW340IdleCarouselLifecycleRoutesNativeCode(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-idle-carousel-store", 0x02def000, 0x2000))
	check(t, bus.MapRAM("w340-idle-carousel-record", 0x07fd1000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340IdleCarouselLifecycle]
	if handler == nil {
		t.Fatal("W340 IdleApp-carousel handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	got, err := backend.ReadRegister(cpu.RegisterPC)
	check(t, err)
	if got != samsungW340IdleCarouselCreator {
		t.Fatalf("W340 IdleApp carousel initial route = %#08x", got)
	}
	for register, want := range map[uint32]uint32{
		cpu.RegisterR0: samsungW340IdleCarouselManager,
		cpu.RegisterR1: samsungW340IdleCarouselConfiguration,
		cpu.RegisterR2: 0,
		cpu.RegisterR3: 0,
	} {
		got, err = backend.ReadRegister(register)
		check(t, err)
		if got != want {
			t.Fatalf("W340 IdleApp carousel creator r%d = %#08x, want %#08x", register, got, want)
		}
	}
	var managerState [0x25e]byte
	check(t, backend.ReadMemory(samsungW340IdleCarouselManagerState, managerState[:]))
	if binary.LittleEndian.Uint32(managerState[0:]) != 1 ||
		binary.LittleEndian.Uint32(managerState[4:]) != 1 ||
		binary.LittleEndian.Uint16(managerState[0x12:]) != 0xffff ||
		binary.LittleEndian.Uint16(managerState[0x25c:]) != 0xffff {
		t.Fatalf("W340 IdleApp carousel manager was not constructor-initialized")
	}
	var configuration [0x28]byte
	check(t, backend.ReadMemory(samsungW340IdleCarouselConfiguration, configuration[:]))
	if got := binary.LittleEndian.Uint16(configuration[6:]); got != 1 {
		t.Fatalf("W340 IdleApp carousel default duration = %d, want 1", got)
	}

	const carousel = uint32(0x06123450)
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], carousel)
	check(t, backend.WriteMemory(samsungW340IdleCarouselStore, encoded[:]))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	got, err = backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != carousel {
		t.Fatalf("W340 IdleApp carousel object = %#08x", got)
	}
	got, err = backend.ReadRegister(cpu.RegisterPC)
	check(t, err)
	if got != samsungW340IdleCarouselUpdater {
		t.Fatalf("W340 IdleApp carousel update route = %#08x", got)
	}
}

func TestSCHW340IdleAppletDependencyPublishesOfflineService(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-idle-dependency-object", 0x07fd1000, 0x1000))
	check(t, bus.MapRAM("w340-idle-dependency-output", 0x06100000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterR2, 0x06100020))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340IdleAppletDependency]
	if handler == nil {
		t.Fatal("W340 IdleApp-dependency handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	var encoded [4]byte
	check(t, backend.ReadMemory(0x06100020, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340IdleDependencyObject {
		t.Fatalf("W340 IdleApp dependency result = %#08x", got)
	}
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != 0 {
		t.Fatalf("W340 IdleApp dependency status = %#08x", got)
	}
}

func TestSCHW340IdleSimMainTargetSuppliesMissingResult(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-idle-sim-main-object", 0x07fd1000, 0x1000))
	check(t, bus.MapRAM("w340-idle-sim-main-stack", 0x06100000, 0x3000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterSP, 0x06100100))
	check(t, backend.WriteRegister(cpu.RegisterR4, 0x06100000))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340IdleSimMainTarget]
	if handler == nil {
		t.Fatal("W340 IdleApp SIM-main-target handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	var encoded [4]byte
	check(t, backend.ReadMemory(0x06100128, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340IdleSimMainObject {
		t.Fatalf("W340 IdleApp SIM-main target result = %#08x", got)
	}
	check(t, backend.ReadMemory(0x06102198, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340IdleSimMainObject {
		t.Fatalf("W340 IdleApp SIM-main companion = %#08x", got)
	}
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != samsungW340IdleSimMainObject {
		t.Fatalf("W340 IdleApp SIM-main target register = %#08x", got)
	}
	check(t, backend.ReadMemory(samsungW340IdleSimMainVTable+0x1b0, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceNoop|1 {
		t.Fatalf("W340 IdleApp extended SIM method = %#08x", got)
	}
	check(t, backend.ReadMemory(samsungW340IdleSimMainVTable+0x08, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceIdentity|1 {
		t.Fatalf("W340 IdleApp SIM identity method = %#08x", got)
	}

	// The retained class-manager release clears the companion after the target
	// handoff. The exact activation call site must restore it before dereference.
	check(t, backend.WriteMemory(0x06102198, []byte{0, 0, 0, 0}))
	check(t, backend.WriteRegister(cpu.RegisterR4, 0x06102180))
	activate := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340IdleSimMainActivation]
	if activate == nil {
		t.Fatal("W340 IdleApp SIM-main-activation handler is missing")
	}
	check(t, activate.InvokeHLE(system.HLECallContext{CPU: backend}))
	check(t, backend.ReadMemory(0x06102198, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340IdleSimMainObject {
		t.Fatalf("W340 IdleApp restored SIM-main companion = %#08x", got)
	}
}

func TestSCHW340AnnunciatorStartIsQuiescent(t *testing.T) {
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340AnnunciatorStart]
	if handler == nil {
		t.Fatal("W340 annunciator-start handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{}))
}

func TestSCHW340IdleExtendedProviderSuppliesMissingInterface(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-idle-extended-object", 0x07fd1800, 0x1000))
	check(t, bus.MapRAM("w340-idle-extended-owner", 0x06100000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterR4, 0x06100100))
	check(t, backend.WriteRegister(cpu.RegisterSP, 0x06100800))
	check(t, backend.WriteMemory(0x0610082c, bytes.Repeat([]byte{0xa5}, 0x10)))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340IdleExtendedProvider]
	if handler == nil {
		t.Fatal("W340 IdleApp extended-provider handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	var encoded [4]byte
	check(t, backend.ReadMemory(0x06100128, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340IdleExtendedObject {
		t.Fatalf("W340 IdleApp extended provider = %#08x", got)
	}
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != samsungW340IdleExtendedObject {
		t.Fatalf("W340 IdleApp extended-provider register = %#08x", got)
	}
	var result [0x10]byte
	check(t, backend.ReadMemory(0x0610082c, result[:]))
	if result != [0x10]byte{} {
		t.Fatalf("W340 IdleApp extended-provider offline result = %x", result)
	}
	for _, offset := range []uint32{0x4c, 0xc4, 0x194} {
		check(t, backend.ReadMemory(samsungW340IdleExtendedVTable+offset, encoded[:]))
		if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceNoop|1 {
			t.Fatalf("W340 IdleApp extended-provider method +%#x = %#08x", offset, got)
		}
	}
	check(t, backend.ReadMemory(samsungW340IdleExtendedVTable+0x08, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceIdentity|1 {
		t.Fatalf("W340 IdleApp extended-provider identity method = %#08x", got)
	}
}

func TestSCHW340IdlePrimaryNotificationBridgesRetainedSecondaryKind(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-idle-notification", 0x06100000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], samsungW340IdleSecondaryNotification)
	check(t, backend.WriteMemory(0x06100100, encoded[:]))
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x06100100))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340IdlePrimaryNotification]
	if handler == nil {
		t.Fatal("W340 IdleApp primary-notification handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	got, err := backend.ReadRegister(cpu.RegisterR3)
	check(t, err)
	if got != samsungW340IdleSecondaryNotification {
		t.Fatalf("W340 IdleApp bridged notification kind = %#08x", got)
	}
}

func TestSCHW340StartupPrimaryInterfaceSuppliesMissingResult(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-startup-primary-object", 0x07fd1000, 0x1000))
	check(t, bus.MapRAM("w340-startup-primary-stack", 0x06100000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterSP, 0x06100100))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340StartupPrimaryInterface]
	if handler == nil {
		t.Fatal("W340 startup primary-interface handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	var encoded [4]byte
	check(t, backend.ReadMemory(0x06100128, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340StartupPrimaryObject {
		t.Fatalf("W340 startup primary result = %#08x", got)
	}
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != samsungW340StartupPrimaryObject {
		t.Fatalf("W340 startup primary register = %#08x", got)
	}
	for _, offset := range []uint32{0x00, 0xc4, 0x194} {
		check(t, backend.ReadMemory(samsungW340StartupPrimaryVTable+offset, encoded[:]))
		if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceNoop|1 {
			t.Fatalf("W340 startup primary method +%#x = %#08x", offset, got)
		}
	}
	check(t, backend.ReadMemory(samsungW340StartupPrimaryVTable+0x08, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceIdentity|1 {
		t.Fatalf("W340 startup primary identity method = %#08x", got)
	}
}

func TestSCHW340StartupSecondaryInterfaceSuppliesMissingResult(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-startup-secondary-object", 0x07fd1000, 0x1000))
	check(t, bus.MapRAM("w340-startup-secondary-stack", 0x06100000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterSP, 0x06100100))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340StartupSecondaryInterface]
	if handler == nil {
		t.Fatal("W340 startup secondary-interface handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend}))
	var encoded [4]byte
	check(t, backend.ReadMemory(0x06100124, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340StartupSecondaryObject {
		t.Fatalf("W340 startup secondary result = %#08x", got)
	}
	got, err := backend.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != samsungW340StartupSecondaryObject {
		t.Fatalf("W340 startup secondary register = %#08x", got)
	}
	for _, offset := range []uint32{0x00, 0xc4, 0x194} {
		check(t, backend.ReadMemory(samsungW340StartupSecondaryVTable+offset, encoded[:]))
		if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceNoop|1 {
			t.Fatalf("W340 startup secondary method +%#x = %#08x", offset, got)
		}
	}
	check(t, backend.ReadMemory(samsungW340StartupSecondaryVTable+0x08, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RetainedServiceIdentity|1 {
		t.Fatalf("W340 startup secondary identity method = %#08x", got)
	}
}

func TestAddressBitSevenPanelAliasesLowAddressLines(t *testing.T) {
	bus := system.NewBus()
	panel := system.NewParallelPanelInterface()
	ports := system.ParallelPanelPortProfile{
		CommandAddress: 0x20000000,
		DataAddress:    0x20000080,
		AliasSpan:      0x80,
	}
	check(t, mapSparsePanelPorts(bus, "aliased-panel", ports, panel))

	var command [2]byte
	binary.LittleEndian.PutUint16(command[:], 0x45)
	check(t, bus.Write(0x20000054, command[:], cpu.PermissionWrite))
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], 0x1234)
	check(t, bus.Write(0x200000d4, data[:], cpu.PermissionWrite))
	if panel.CurrentCommand() != 0x45 || panel.LastData() != 0x1234 {
		t.Fatalf("aliased panel state = command %#x data %#x", panel.CurrentCommand(), panel.LastData())
	}
}

func TestSCHW340AMSSBulkZeroRestoresRetainedIRQHandler(t *testing.T) {
	const base = uint32(0x03c50000)
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-amss-bss", base, 0x10000))
	check(t, bus.MapRAM("w340-interrupt-table", samsungW340InterruptTable, 0x2000))
	check(t, bus.MapRAM("w340-sd-script-store", 0x02569000, 0x1000))
	check(t, bus.MapRAM("w340-nv-buffer-store", 0x02394000, 0x1000))
	check(t, bus.MapRAM("w340-device-table", 0x02397000, 0x1000))
	check(t, bus.MapRAM("w340-ui-initialization", 0x02575000, 0x1000))
	check(t, bus.MapRAM("w340-lsm-callbacks", 0x02455000, 0x1000))
	check(t, bus.MapRAM("w340-diag-buffer-store", 0x02396000, 0x1000))
	check(t, bus.MapRAM("w340-radio-state-store", 0x0261e000, 0x1000))
	check(t, bus.MapRAM("w340-mgp-queue-pool-store", 0x02692000, 0x1000))
	check(t, bus.MapRAM("w340-retained-scratch", 0x07fc0000, 0x2000))
	check(t, bus.MapRAM("w340-retained-objects", 0x07fd0000, 0x2000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteMemory(base, bytes.Repeat([]byte{0xa5}, 0x10000)))
	check(t, backend.WriteRegister(cpu.RegisterR0, base))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x10000))
	handler := samsungQualcommMachineHLEHandlers(
		nil, nil, system.SCHW340DC18BoardProfile(), samsung.ProgressiveELF{}, nil, nil,
	)[system.HLEContractSamsungAMSSBulkZero]
	if handler == nil {
		t.Fatal("W340 AMSS bulk-zero handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	var encoded [4]byte
	check(t, backend.ReadMemory(samsungW340IRQHandlerStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340IRQHandler {
		t.Fatalf("W340 post-zero IRQ handler = %#08x", got)
	}
	check(t, backend.ReadMemory(samsungW340TimeTickInterruptEntry, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != 0x01041147 {
		t.Fatalf("W340 post-zero time-tick callback = %#08x", got)
	}
	check(t, backend.ReadMemory(samsungW340TimeTickInterruptEntry+0x1c, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != 0x80000434 {
		t.Fatalf("W340 post-zero time-tick enable pointer = %#08x", got)
	}
	check(t, backend.ReadMemory(base, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != 0 {
		t.Fatalf("W340 bulk-zero prefix = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x02569000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340SDSSScriptStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340SDSSScriptBacking {
		t.Fatalf("W340 post-zero SD script-state pointer = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x02394000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340NVBufferStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340NVBufferBacking {
		t.Fatalf("W340 post-zero NV buffer pointer = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x02397000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340DeviceTable+4, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340DeviceObjectBacking {
		t.Fatalf("W340 post-zero device object pointer = %#08x", got)
	}
	check(t, backend.ReadMemory(samsungW340DeviceTable+8, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340DeviceCommonCallback {
		t.Fatalf("W340 post-zero device constructor = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x02575000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	var flag [1]byte
	check(t, backend.ReadMemory(samsungW340UIInitializationFlag, flag[:]))
	if flag[0] != 1 {
		t.Fatalf("W340 post-zero UI initialization flag = %#02x", flag[0])
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x02455000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340LSMCallbackTableStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340LSMCallbackTable {
		t.Fatalf("W340 post-zero LSM callback table = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x02396000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340DIAGBufferStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340DIAGBufferBacking {
		t.Fatalf("W340 post-zero DIAG buffer pointer = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x0261e000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340RadioStateStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RadioStateBacking {
		t.Fatalf("W340 post-zero radio-state pointer = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0x02692000))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x1000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340MGPQueuePoolStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340MGPQueuePoolBacking {
		t.Fatalf("W340 post-zero MGP queue-pool pointer = %#08x", got)
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, samsungW340InterruptTable))
	check(t, backend.WriteRegister(cpu.RegisterR1, 0x2000))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340RadioWorkspace+8, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340RadioWorkspaceBuffer {
		t.Fatalf("W340 post-zero radio-workspace buffer = %#08x", got)
	}
	check(t, backend.ReadMemory(samsungW340DefaultDispatchObject, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340BCXUnavailableCallback {
		t.Fatalf("W340 post-zero default dispatch callback = %#08x", got)
	}
}

func TestSCHW340BCXFirmwareIdentityHandlerPublishesNativeResult(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-bcx-identity", samsungW340BCXIdentityState, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteMemory(
		samsungW340BCXIdentityState, bytes.Repeat([]byte{0xa5}, 0x20),
	))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340BCXFirmwareIdentity]
	if handler == nil {
		t.Fatal("W340 BCX firmware-identity handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	data := make([]byte, 0x20)
	check(t, backend.ReadMemory(samsungW340BCXIdentityState, data))
	if data[6] != 1 || data[7] != 1 ||
		!bytes.Equal(data[0x0c:0x0c+len(samsungW340BCXFirmwareIdentity)], samsungW340BCXFirmwareIdentity[:]) {
		t.Fatalf("W340 BCX firmware identity state = %x", data)
	}
	if result, err := backend.ReadRegister(cpu.RegisterR0); err != nil || result != 1 {
		t.Fatalf("W340 BCX firmware identity return = %#08x/%v", result, err)
	}
}

func TestSCHW340PMICADCConversionHandlerReturnsIdleSample(t *testing.T) {
	const output = uint32(0x1000)
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-pmic-adc", output, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteMemory(output, []byte{0xa5, 0xa5}))
	check(t, backend.WriteRegister(cpu.RegisterR1, output))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340PMICADCConversion]
	if handler == nil {
		t.Fatal("W340 PMIC ADC conversion handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	var sample [2]byte
	check(t, backend.ReadMemory(output, sample[:]))
	if sample != [2]byte{} {
		t.Fatalf("W340 PMIC ADC sample = %x", sample)
	}
	if result, err := backend.ReadRegister(cpu.RegisterR0); err != nil || result != 0 {
		t.Fatalf("W340 PMIC ADC return = %#08x/%v", result, err)
	}
}

func TestSCHW340ImageResourceOffsetHandlerFallsBackToFontTable(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-image-resource-offset", samsungW340BREWResourceOffsetsStore&^0xfff, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340ImageResourceOffset]
	if handler == nil {
		t.Fatal("W340 image-resource offset handler is missing")
	}

	var encoded [4]byte
	check(t, backend.WriteRegister(cpu.RegisterR0, 0))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	var offsets [16]byte
	check(t, backend.ReadMemory(samsungW340BREWResourceOffsetsStore, offsets[:]))
	if got := binary.LittleEndian.Uint32(offsets[0:]); got != samsungW340BREWUTFOffset {
		t.Fatalf("W340 absent UTF-resource offset = %#08x", got)
	}
	if got := binary.LittleEndian.Uint32(offsets[4:]); got != samsungW340BREWDictionaryOffset {
		t.Fatalf("W340 absent dictionary-resource offset = %#08x", got)
	}
	check(t, backend.ReadMemory(samsungW340BREWTableOffsetStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != samsungW340BREWTableOffset {
		t.Fatalf("W340 absent image-resource offset = %#08x", got)
	}

	const resolved = uint32(0x00123456)
	check(t, backend.WriteRegister(cpu.RegisterR0, resolved))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(samsungW340BREWTableOffsetStore, encoded[:]))
	if got := binary.LittleEndian.Uint32(encoded[:]); got != resolved {
		t.Fatalf("W340 resolved image-resource offset = %#08x", got)
	}
}

func TestSCHW340DisplayColorHandlerRestoresDC18Palette(t *testing.T) {
	const output = uint32(0x1000)
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-display-color", output, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340DisplayColor]
	if handler == nil {
		t.Fatal("W340 display-color handler is missing")
	}
	for selector, want := range samsungW340DisplayDefaultColors {
		check(t, backend.WriteRegister(cpu.RegisterR1, uint32(selector+1)))
		check(t, backend.WriteRegister(cpu.RegisterR2, output))
		check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
		var encoded [4]byte
		check(t, backend.ReadMemory(output, encoded[:]))
		if got := binary.LittleEndian.Uint32(encoded[:]); got != want {
			t.Fatalf("W340 display color %d = %#08x, want %#08x", selector+1, got, want)
		}
		if result, err := backend.ReadRegister(cpu.RegisterR0); err != nil || result != 0 {
			t.Fatalf("W340 display color %d return = %#08x/%v", selector+1, result, err)
		}
	}
	check(t, backend.WriteRegister(cpu.RegisterR1, uint32(len(samsungW340DisplayDefaultColors)+1)))
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	if result, err := backend.ReadRegister(cpu.RegisterR0); err != nil || result != 0x14 {
		t.Fatalf("W340 unsupported display color return = %#08x/%v", result, err)
	}
}

func TestSCHW340FontMetricsHandlerSuppliesRetainedDefault(t *testing.T) {
	const output = uint32(0x1000)
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-font-metrics", output, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterR2, output))
	check(t, backend.WriteRegister(cpu.RegisterR3, output+4))
	handler := samsungQualcommHLEHandlers()[system.HLEContractSamsungW340FontMetrics]
	if handler == nil {
		t.Fatal("W340 font-metrics handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	var metrics [8]byte
	check(t, backend.ReadMemory(output, metrics[:]))
	if ascent, descent := binary.LittleEndian.Uint32(metrics[0:]), binary.LittleEndian.Uint32(metrics[4:]); ascent != 12 || descent != 4 {
		t.Fatalf("W340 font metrics = %d/%d", ascent, descent)
	}
	if result, err := backend.ReadRegister(cpu.RegisterR0); err != nil || result != 16 {
		t.Fatalf("W340 font metrics return = %#08x/%v", result, err)
	}
}

func TestSCHW340RetainedFontHandlersMatchAEEDispABI(t *testing.T) {
	const (
		base       = uint32(0x1000)
		text       = base + 0x100
		stack      = base + 0x200
		countOut   = base + 0x300
		widthOut   = base + 0x304
		metricsOut = base + 0x400
	)
	bus := system.NewBus()
	check(t, bus.MapRAM("w340-retained-font", base, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteMemory(text, []byte{'A', 0, 'B', 0, 'C', 0, 'D', 0, 0, 0}))
	var outputs [8]byte
	binary.LittleEndian.PutUint32(outputs[0:], countOut)
	binary.LittleEndian.PutUint32(outputs[4:], widthOut)
	check(t, backend.WriteMemory(stack, outputs[:]))
	check(t, backend.WriteRegister(cpu.RegisterR1, text))
	check(t, backend.WriteRegister(cpu.RegisterR2, ^uint32(0)))
	check(t, backend.WriteRegister(cpu.RegisterR3, 24))
	check(t, backend.WriteRegister(cpu.RegisterSP, stack))
	handlers := samsungQualcommHLEHandlers()
	measure := handlers[system.HLEContractSamsungW340FontMeasure]
	if measure == nil {
		t.Fatal("W340 retained-font measure handler is missing")
	}
	check(t, measure.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	check(t, backend.ReadMemory(countOut, outputs[:]))
	if count, width := binary.LittleEndian.Uint32(outputs[0:]), binary.LittleEndian.Uint32(outputs[4:]); count != 3 || width != 24 {
		t.Fatalf("W340 retained-font extent = %d/%d", count, width)
	}

	info := handlers[system.HLEContractSamsungW340FontInfo]
	if info == nil {
		t.Fatal("W340 retained-font info handler is missing")
	}
	check(t, backend.WriteRegister(cpu.RegisterR1, metricsOut))
	check(t, info.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	var metrics [4]byte
	check(t, backend.ReadMemory(metricsOut, metrics[:]))
	if ascent, descent := binary.LittleEndian.Uint16(metrics[0:]), binary.LittleEndian.Uint16(metrics[2:]); ascent != 12 || descent != 4 {
		t.Fatalf("W340 retained-font info = %d/%d", ascent, descent)
	}
	for _, contract := range []string{system.HLEContractSamsungW340FontDraw, system.HLEContractSamsungW340FontInfo} {
		if result, err := backend.ReadRegister(cpu.RegisterR0); err != nil || result != 0 {
			t.Fatalf("W340 retained-font handler %q return = %#08x/%v", contract, result, err)
		}
	}
}

func TestSPHW4200SharedDirectoryMatchesAMSSContract(t *testing.T) {
	seed := samsungW4200SharedDirectorySeed()
	if seed.Address != samsungW4200SharedDirectory || uint32(len(seed.Bytes)) != samsungW4200SharedDirectorySize {
		t.Fatalf("W4200 shared directory = %#08x/%#x", seed.Address, len(seed.Bytes))
	}
	word := func(offset uint32) uint32 {
		return binary.LittleEndian.Uint32(seed.Bytes[offset : offset+4])
	}
	for offset, want := range map[uint32]uint32{
		0x000: 0xa1b2c3d5,
		0x010: samsungW4200SharedDirectory + 0x18,
		0x014: samsungW4200SharedDirectory + 0x884,
		0x22c: 0xa1b2c3dc,
		0x230: 0x0000061c,
		0x23c: 0x103b5d7f,
		0x240: 0x00000001,
		0x848: 0xa1b2c3d7,
		0x85c: 0xa1b2c3d6,
	} {
		if got := word(offset); got != want {
			t.Fatalf("W4200 shared directory[%#x] = %#08x, want %#08x", offset, got, want)
		}
	}
}

func TestSPHW4200SharedDirectoryHandlerPublishesPostBSSState(t *testing.T) {
	bus := system.NewBus()
	check(t, bus.MapRAM("shared", samsungW4200SharedDirectory, 0x4000))
	check(t, bus.MapRAM("loader-globals", 0x08008000, 0x1000))
	check(t, bus.MapRAM("runtime-globals", 0x08871000, 0x1000))
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterR0, samsungW4200SharedDirectory))
	check(t, backend.WriteRegister(cpu.RegisterR1, samsungW4200SharedDirectorySize))
	featureConfig := make([]byte, samsungW4200FeatureConfigSize)
	for index, pair := range [][2]uint32{
		{0x108, 0x40},
		{0x10e, 0},
		{0x11f, 0},
		{0x120, 9},
		{0x121, 0x12c0},
		{0x130, 2},
		{0x131, 0},
	} {
		binary.LittleEndian.PutUint32(featureConfig[index*8:], pair[0])
		binary.LittleEndian.PutUint32(featureConfig[index*8+4:], pair[1])
	}
	handler := samsungQualcommMachineHLEHandlers(
		nil, nil, system.SPHW4200DC17BoardProfile(), samsung.ProgressiveELF{}, nil, featureConfig,
	)[system.HLEContractSamsungSharedDirectoryInit]
	if handler == nil {
		t.Fatal("W4200 shared-directory handler is missing")
	}
	check(t, handler.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	for address, want := range map[uint32]uint32{
		samsungW4200SharedDirectory:         0xa1b2c3d5,
		samsungW4200SharedDirectory + 0x10:  samsungW4200SharedDirectory + 0x18,
		samsungW4200SharedDirectory + 0x22c: 0xa1b2c3dc,
		samsungW4200SharedDirectory + 0x240: 0x00000001,
		samsungW4200SharedDirectory + 0x244: 0x00000108,
		samsungW4200SharedDirectory + 0x248: 0x00000040,
		samsungW4200SharedDirectory + 0x2f8: 0x00000120,
		samsungW4200SharedDirectory + 0x2fc: 0x00000009,
		samsungW4200SharedDirectory + 0x300: 0x00000121,
		samsungW4200SharedDirectoryStore:    samsungW4200SharedDirectory,
	} {
		var encoded [4]byte
		check(t, bus.ReadMemory(address, encoded[:], cpu.PermissionRead))
		if got := binary.LittleEndian.Uint32(encoded[:]); got != want {
			t.Fatalf("W4200 shared state[%#x] = %#08x, want %#08x", address, got, want)
		}
	}
	attach := samsungQualcommMachineHLEHandlers(
		nil, nil, system.SPHW4200DC17BoardProfile(), samsung.ProgressiveELF{}, nil, featureConfig,
	)[system.HLEContractSamsungSharedDirectoryAttach]
	if attach == nil {
		t.Fatal("W4200 shared-directory attach handler is missing")
	}
	check(t, attach.InvokeHLE(system.HLECallContext{CPU: backend, Bus: bus}))
	var runtimePointer [4]byte
	check(t, backend.ReadMemory(samsungW4200RuntimeDirectoryStore, runtimePointer[:]))
	if got := binary.LittleEndian.Uint32(runtimePointer[:]); got != samsungW4200SharedDirectory {
		t.Fatalf("W4200 runtime shared-directory pointer = %#08x", got)
	}
}

func TestInterpreterBackendModeSelection(t *testing.T) {
	for _, test := range []struct {
		mode     CPUBackendMode
		wantName string
	}{
		{mode: "", wantName: interpreter.BackendName},
		{mode: CPUBackendPrecise, wantName: interpreter.BackendName},
		{mode: CPUBackendJIT, wantName: interpreter.BackendName + "-jit"},
		{mode: CPUBackendJITLoops, wantName: interpreter.BackendName + "-jit-loops"},
	} {
		backend, err := newInterpreterBackend(test.mode, interpreter.CompatibilityOptions{})
		check(t, err)
		if got := backend.Identity().Name; got != test.wantName {
			t.Fatalf("mode %q backend = %q, want %q", test.mode, got, test.wantName)
		}
		check(t, backend.Close())
	}
	if _, err := newInterpreterBackend("unknown", interpreter.CompatibilityOptions{}); err == nil {
		t.Fatal("unknown CPU backend mode was accepted")
	}
}
