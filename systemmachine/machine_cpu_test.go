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
		if err := backend.Map(region.address, region.size, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
			t.Fatal(err)
		}
	}
	qcsbl := make([]byte, samsungW320QCSBLUsedSize)
	for index := range qcsbl {
		qcsbl[index] = byte(index*17 + 3)
	}
	if err := backend.WriteMemory(samsungW320QCSBLLoadAddress, qcsbl); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(samsungW320PBLVerifiedStatus, []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	handler := samsungQualcommHLEHandlers()[system.HLEContractQualcommPBLVerifiedLoaderState]
	if handler == nil {
		t.Fatal("verified PBL loader-state handler is missing")
	}
	if err := handler.InvokeHLE(system.HLECallContext{CPU: backend}); err != nil {
		t.Fatal(err)
	}
	if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != 0x10 {
		t.Fatalf("verified PBL loader-state result = %#x, error %v", value, err)
	}
	verifiedCopy := make([]byte, len(qcsbl))
	if err := backend.ReadMemory(samsungW320PBLVerifiedCopy, verifiedCopy); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(verifiedCopy, qcsbl) {
		t.Fatal("verified PBL QCSBL copy differs from its exact input")
	}
	record := make([]byte, 6+sha512.Size)
	if err := backend.ReadMemory(samsungW320PBLVerifiedRecord, record); err != nil {
		t.Fatal(err)
	}
	digest := sha512.Sum512(qcsbl)
	if binary.BigEndian.Uint32(record[:4]) != samsungW320QCSBLUsedSize ||
		record[4] != 0 || record[5] != 0 || !bytes.Equal(record[6:], digest[:]) {
		t.Fatal("verified PBL record does not describe the exact QCSBL")
	}
	status := []byte{0xff}
	if err := backend.ReadMemory(samsungW320PBLVerifiedStatus, status); err != nil {
		t.Fatal(err)
	}
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
	if err := appendSamsungW320VerifiedPBLState(&handoff, qcsbl); err != nil {
		t.Fatal(err)
	}
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
	if err := backend.WriteRegister(cpu.RegisterR0, 0xffffffff); err != nil {
		t.Fatal(err)
	}
	if err := handler.InvokeHLE(system.HLECallContext{CPU: backend}); err != nil {
		t.Fatal(err)
	}
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
	if err := backend.WriteRegister(cpu.RegisterR0, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := handler.InvokeHLE(system.HLECallContext{CPU: backend}); err != nil {
		t.Fatal(err)
	}
	if value, err := backend.ReadRegister(cpu.RegisterR0); err != nil || value != sentinel {
		t.Fatalf("resident boot callback result = %#x, error %v", value, err)
	}
}

func TestSamsungQualcommRetainedPBLNANDHandlers(t *testing.T) {
	flash, err := system.NewErasedCOWFlash(0x8000, 0x4000, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	page := bytes.Repeat([]byte{0x5a}, 0x200)
	if err := flash.ProgramAt(page, 0x400); err != nil {
		t.Fatal(err)
	}
	board := system.BoardProfile{
		NANDPageSize: 0x200, NANDEraseBlockSize: 0x4000,
		NANDFactoryBadBlocks: []uint32{1},
	}
	handlers := samsungQualcommMachineHLEHandlers(flash, board)
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(0x2000, 0x2000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: 2,
		cpu.RegisterR1: 0x200,
		cpu.RegisterR2: 2,
		cpu.RegisterR3: 0x2000,
		cpu.RegisterSP: 0x3000,
	} {
		if err := backend.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], 1)
	if err := backend.WriteMemory(0x3000, count[:]); err != nil {
		t.Fatal(err)
	}
	readHandler := handlers[system.HLEContractQualcommPBLNANDRead]
	if readHandler == nil {
		t.Fatal("retained PBL NAND read handler is missing")
	}
	if err := readHandler.InvokeHLE(system.HLECallContext{CPU: backend}); err != nil {
		t.Fatal(err)
	}
	output := make([]byte, len(page))
	if err := backend.ReadMemory(0x2000, output); err != nil {
		t.Fatal(err)
	}
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
		if err := backend.WriteRegister(cpu.RegisterR0, block); err != nil {
			t.Fatal(err)
		}
		if err := badBlockHandler.InvokeHLE(system.HLECallContext{CPU: backend}); err != nil {
			t.Fatal(err)
		}
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
		backend, err := newInterpreterBackend(test.mode)
		if err != nil {
			t.Fatal(err)
		}
		if got := backend.Identity().Name; got != test.wantName {
			t.Fatalf("mode %q backend = %q, want %q", test.mode, got, test.wantName)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := newInterpreterBackend("unknown"); err == nil {
		t.Fatal("unknown CPU backend mode was accepted")
	}
}
