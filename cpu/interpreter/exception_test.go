package interpreter

import (
	"context"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestIRQEntryAndDataProcessingExceptionReturn(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	bus.writeU32(vectorIRQ, 0xe25ef004) // SUBS pc, lr, #4
	bus.writeU32(0x1000, 0xe3a00007)    // MOV r0, #7
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	originalStatus := flagC | uint32(processorModeSystem)
	if err := backend.WriteRegister(cpu.RegisterCPSR, originalStatus); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 1 || result.PC != 0x1000 {
		t.Fatalf("IRQ return result = %+v", result)
	}
	if status := register(t, backend, cpu.RegisterCPSR); status != originalStatus {
		t.Fatalf("restored CPSR = %#x, want %#x", status, originalStatus)
	}
	if backend.spsr.irq != originalStatus || backend.banks.irq[1] != 0x1004 {
		t.Fatalf("IRQ saved state = SPSR %#x LR %#x", backend.spsr.irq, backend.banks.irq[1])
	}
	if err := backend.SetInterruptLine(cpu.InterruptIRQ, false); err != nil {
		t.Fatal(err)
	}
	result = backend.Run(context.Background(), result.PC, cpu.ModeARM, 1)
	if result.Err != nil || result.PC != 0x1004 || register(t, backend, cpu.RegisterR0) != 7 {
		t.Fatalf("post-IRQ execution = %+v r0=%d", result, register(t, backend, cpu.RegisterR0))
	}
}

func TestInterruptEntryUsesFIQPriorityAndHighVectors(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	bus.writeU32(0xffff0000+vectorIRQ, 0xe3a00001) // MOV r0, #1
	bus.writeU32(0xffff0000+vectorFIQ, 0xe3a00002) // MOV r0, #2
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.control = 1 << 13
	originalStatus := flagN | uint32(processorModeSystem)
	if err := backend.WriteRegister(cpu.RegisterCPSR, originalStatus); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetInterruptLine(cpu.InterruptFIQ, true); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x2000, cpu.ModeARM, 1)
	if result.Err != nil || result.PC != 0xffff0020 || register(t, backend, cpu.RegisterR0) != 2 {
		t.Fatalf("FIQ high-vector result = %+v r0=%d", result, register(t, backend, cpu.RegisterR0))
	}
	status := register(t, backend, cpu.RegisterCPSR)
	if processorMode(status&processorModeMask) != processorModeFIQ ||
		status&(statusIRQDisable|statusFIQDisable) != statusIRQDisable|statusFIQDisable {
		t.Fatalf("FIQ CPSR = %#x", status)
	}
	if backend.spsr.fiq != originalStatus || register(t, backend, cpu.RegisterLR) != 0x2004 {
		t.Fatalf("FIQ saved state = SPSR %#x LR %#x", backend.spsr.fiq, register(t, backend, cpu.RegisterLR))
	}
}

func TestMaskedInterruptWaitsAndThumbIRQPreservesReturnState(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	bus.writeU32(vectorIRQ, 0xe25ef004) // SUBS pc, lr, #4
	bus.writeRaw(0x1000, []byte{0x07, 0x20})
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	maskedStatus := uint32(processorModeSystem) | cpu.StatusThumb | statusIRQDisable
	if err := backend.WriteRegister(cpu.RegisterCPSR, maskedStatus); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
	if result.Err != nil || result.PC != 0x1002 || register(t, backend, cpu.RegisterR0) != 7 {
		t.Fatalf("masked Thumb result = %+v r0=%d", result, register(t, backend, cpu.RegisterR0))
	}

	unmaskedStatus := maskedStatus &^ statusIRQDisable
	if err := backend.WriteRegister(cpu.RegisterCPSR, unmaskedStatus); err != nil {
		t.Fatal(err)
	}
	result = backend.Run(context.Background(), 0x1002, cpu.ModeThumb, 1)
	if result.Err != nil || result.PC != 0x1002 || register(t, backend, cpu.RegisterCPSR) != unmaskedStatus {
		t.Fatalf("Thumb IRQ return result = %+v CPSR=%#x", result, register(t, backend, cpu.RegisterCPSR))
	}
	if backend.banks.irq[1] != 0x1006 {
		t.Fatalf("Thumb IRQ LR = %#x", backend.banks.irq[1])
	}
}

func TestSystemSWIEntersSupervisorVector(t *testing.T) {
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	bus.writeU32(0x1000, 0xef000042)         // SWI #0x42
	bus.writeU32(vectorSoftware, 0xe3a0002a) // MOV r0, #42
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	originalStatus := flagV | uint32(processorModeSystem)
	if err := backend.WriteRegister(cpu.RegisterCPSR, originalStatus); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.PC != vectorSoftware+4 || register(t, backend, cpu.RegisterR0) != 42 {
		t.Fatalf("SWI result = %+v r0=%d", result, register(t, backend, cpu.RegisterR0))
	}
	status := register(t, backend, cpu.RegisterCPSR)
	if processorMode(status&processorModeMask) != processorModeSupervisor || status&statusIRQDisable == 0 {
		t.Fatalf("SWI CPSR = %#x", status)
	}
	if backend.spsr.supervisor != originalStatus || register(t, backend, cpu.RegisterLR) != 0x1004 {
		t.Fatalf("SWI saved state = SPSR %#x LR %#x", backend.spsr.supervisor, register(t, backend, cpu.RegisterLR))
	}
}

func TestMMUFaultsEnterPrefetchAndDataAbortVectors(t *testing.T) {
	t.Run("prefetch", func(t *testing.T) {
		bus := &testSystemBus{memory: make(map[uint32]byte)}
		const tableBase = uint32(0x4000)
		bus.writeU32(tableBase, 3<<10|2) // VA 0 -> PA 0, manager domain
		bus.writeU32(vectorPrefetchAbort, 0xe3a0000c)
		backend := New()
		if err := backend.AttachSystemBus(bus); err != nil {
			t.Fatal(err)
		}
		backend.cp15.translationTableBase = tableBase
		backend.cp15.domainAccessControl = 3
		backend.cp15.control = 1
		if err := backend.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)); err != nil {
			t.Fatal(err)
		}
		result := backend.Run(context.Background(), 0x90000000, cpu.ModeARM, 1)
		if result.Err != nil || result.PC != vectorPrefetchAbort+4 || register(t, backend, cpu.RegisterR0) != 0x0c {
			t.Fatalf("prefetch-abort result = %+v r0=%#x", result, register(t, backend, cpu.RegisterR0))
		}
		if backend.cp15.instructionFaultStatus != 5 || backend.cp15.faultAddress != 0x90000000 ||
			register(t, backend, cpu.RegisterLR) != 0x90000004 {
			t.Fatalf("prefetch fault state = IFSR %#x FAR %#x LR %#x", backend.cp15.instructionFaultStatus, backend.cp15.faultAddress, register(t, backend, cpu.RegisterLR))
		}
	})

	t.Run("data", func(t *testing.T) {
		bus := &testSystemBus{memory: make(map[uint32]byte)}
		const tableBase = uint32(0x4000)
		bus.writeU32(tableBase, 3<<10|2) // VA 0 -> PA 0
		bus.writeU32(tableBase+(0x80000000>>20)*4, 0x00100000|3<<10|2)
		bus.writeU32(0x00101000, 0xe5910000)      // LDR r0, [r1]
		bus.writeU32(vectorDataAbort, 0xe3a00010) // MOV r0, #16
		backend := New()
		if err := backend.AttachSystemBus(bus); err != nil {
			t.Fatal(err)
		}
		backend.cp15.translationTableBase = tableBase
		backend.cp15.domainAccessControl = 3
		backend.cp15.control = 1
		if err := backend.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)); err != nil {
			t.Fatal(err)
		}
		if err := backend.WriteRegister(cpu.RegisterR1, 0x90000000); err != nil {
			t.Fatal(err)
		}
		result := backend.Run(context.Background(), 0x80001000, cpu.ModeARM, 1)
		if result.Err != nil || result.PC != vectorDataAbort+4 || register(t, backend, cpu.RegisterR0) != 0x10 {
			t.Fatalf("data-abort result = %+v r0=%#x", result, register(t, backend, cpu.RegisterR0))
		}
		if backend.cp15.dataFaultStatus != 5 || backend.cp15.faultAddress != 0x90000000 ||
			register(t, backend, cpu.RegisterLR) != 0x80001008 {
			t.Fatalf("data fault state = DFSR %#x FAR %#x LR %#x", backend.cp15.dataFaultStatus, backend.cp15.faultAddress, register(t, backend, cpu.RegisterLR))
		}
	})
}

func TestUnmappedPhysicalAccessEntersPreciseExternalDataAbort(t *testing.T) {
	const (
		tableBase      = uint32(0x4000)
		instructionVA  = uint32(0x80001000)
		instructionPA  = uint32(0x00101000)
		faultVA        = uint32(0x90031c00)
		faultPA        = uint32(0x68931c00)
		externalStatus = uint32(0x8)
	)
	bus := &externalAbortSystemBus{
		testSystemBus: testSystemBus{memory: make(map[uint32]byte)},
		abortAddress:  faultPA,
	}
	bus.writeU32(tableBase, 3<<10|2) // VA 0 -> PA 0, manager domain
	bus.writeU32(tableBase+(instructionVA>>20)*4, 0x00100000|3<<10|2)
	bus.writeU32(tableBase+(faultVA>>20)*4, 0x68900000|3<<10|2)
	bus.writeU32(instructionPA, 0xe5910000)   // LDR r0, [r1]
	bus.writeU32(vectorDataAbort, 0xe3a00010) // MOV r0, #16
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	backend.cp15.translationTableBase = tableBase
	backend.cp15.domainAccessControl = 3
	backend.cp15.control = 1
	if err := backend.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, faultVA); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), instructionVA, cpu.ModeARM, 1)
	if result.Err != nil || result.PC != vectorDataAbort+4 || register(t, backend, cpu.RegisterR0) != 0x10 {
		t.Fatalf("external data-abort result = %+v r0=%#x", result, register(t, backend, cpu.RegisterR0))
	}
	if backend.cp15.dataFaultStatus != externalStatus || backend.cp15.faultAddress != faultVA ||
		register(t, backend, cpu.RegisterLR) != instructionVA+8 {
		t.Fatalf("external data-abort state = DFSR %#x FAR %#x LR %#x",
			backend.cp15.dataFaultStatus, backend.cp15.faultAddress, register(t, backend, cpu.RegisterLR))
	}
}

func TestUnmappedPhysicalAccessWithoutMMUEntersPreciseExternalDataAbort(t *testing.T) {
	const (
		instructionAddress = uint32(0x1000)
		faultAddress       = uint32(0x68931c00)
		externalStatus     = uint32(0x8)
	)
	bus := &externalAbortSystemBus{
		testSystemBus: testSystemBus{memory: make(map[uint32]byte)},
		abortAddress:  faultAddress,
	}
	bus.writeU32(instructionAddress, 0xe5910000) // LDR r0, [r1]
	bus.writeU32(vectorDataAbort, 0xe3a00010)    // MOV r0, #16
	backend := New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, faultAddress); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), instructionAddress, cpu.ModeARM, 1)
	if result.Err != nil || result.PC != vectorDataAbort+4 || register(t, backend, cpu.RegisterR0) != 0x10 {
		t.Fatalf("external data-abort result = %+v r0=%#x", result, register(t, backend, cpu.RegisterR0))
	}
	if backend.cp15.dataFaultStatus != externalStatus || backend.cp15.faultAddress != faultAddress ||
		register(t, backend, cpu.RegisterLR) != instructionAddress+8 {
		t.Fatalf("external data-abort state = DFSR %#x FAR %#x LR %#x",
			backend.cp15.dataFaultStatus, backend.cp15.faultAddress, register(t, backend, cpu.RegisterLR))
	}
}

type externalAbortSystemBus struct {
	testSystemBus
	abortAddress uint32
}

func (b *externalAbortSystemBus) Read(
	address uint32,
	destination []byte,
	permission cpu.Permissions,
) error {
	if address == b.abortAddress {
		return externalAbortTestError{}
	}
	return b.testSystemBus.Read(address, destination, permission)
}

type externalAbortTestError struct{}

func (externalAbortTestError) Error() string       { return "test external abort" }
func (externalAbortTestError) ExternalAbort() bool { return true }

func TestInterruptLineValidationAndClosedState(t *testing.T) {
	backend := New()
	if err := backend.SetInterruptLine(cpu.InterruptLine(2), true); !errors.Is(err, cpu.ErrInvalidAddress) {
		t.Fatalf("invalid interrupt line error = %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetInterruptLine(cpu.InterruptIRQ, true); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("closed interrupt line error = %v", err)
	}
}
