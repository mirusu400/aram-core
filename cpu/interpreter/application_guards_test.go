package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// An application guest runs unprivileged and reports an invalid CPSR mode
// field, so the architectural User-mode masking cannot protect it. A control
// byte MSR must stay undefined there instead of banking its live stack pointer
// away, while a flags-only MSR is harmless in every mode.
func TestApplicationModeRejectsControlProgramStatusWrites(t *testing.T) {
	backend := New()
	defer backend.Close()
	mapARMInstructions(t, backend,
		0xe321f0d3, // MSR CPSR_c, #0xd3
	)
	if err := backend.WriteRegister(cpu.RegisterSP, 0x1234); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Reason != cpu.StopFault || result.Err == nil {
		t.Fatalf("control MSR result = %+v, want a fault", result)
	}
	if got := register(t, backend, cpu.RegisterSP); got != 0x1234 {
		t.Fatalf("stack pointer = %#x after a rejected MSR, want %#x", got, 0x1234)
	}
	if backend.currentProcessorMode() == processorModeSupervisor {
		t.Fatal("a rejected MSR still switched processor mode")
	}
}

func TestApplicationModeAcceptsFlagsOnlyProgramStatusWrites(t *testing.T) {
	backend := New()
	defer backend.Close()
	mapARMInstructions(t, backend,
		0xe328f102, // MSR CPSR_f, #0x80000000
		0xe10f0000, // MRS r0, CPSR
	)
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("flags MSR result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got&0x80000000 == 0 {
		t.Fatalf("CPSR after MSR CPSR_f = %#x, want N set", got)
	}
}

// An application guest must not be able to reach the CP15 system registers:
// enabling the MMU against an empty translation table would fault every
// following access.
func TestApplicationModeRejectsCP15SystemRegisters(t *testing.T) {
	backend := New()
	defer backend.Close()
	mapARMInstructions(t, backend,
		0xee010f10, // MCR p15, 0, r0, c1, c0, 0
	)
	if err := backend.WriteRegister(cpu.RegisterR0, 1); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Reason != cpu.StopFault || result.Err == nil {
		t.Fatalf("CP15 control write result = %+v, want a fault", result)
	}
	if backend.cp15.control != 0 || backend.mmuEnabled() {
		t.Fatalf("application guest changed CP15 control to %#x", backend.cp15.control)
	}
}

// Cache maintenance stays a retiring no-op for an application guest, and must
// not discard translated blocks: guest writes are already tracked by SMC
// invalidation, so a flush here only reintroduces a retranslation treadmill.
func TestApplicationModeCacheMaintenanceKeepsTranslatedBlocks(t *testing.T) {
	backend := NewJIT()
	defer backend.Close()
	if err := backend.Map(0x1000, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x30, // adds r0, #1
		0xfe, 0xe7, // b .-4
	}); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 64)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("Thumb warmup result = %+v", result)
	}
	if len(backend.jitBlocks) == 0 {
		t.Fatal("no translated blocks after warmup")
	}
	blocks, generation := len(backend.jitBlocks), backend.jitGen

	if err := backend.WriteMemory(0x2000-4, []byte{0x15, 0x0f, 0x07, 0xee}); err != nil {
		t.Fatal(err) // MCR p15, 0, r0, c7, c5, 0: invalidate I-cache
	}
	maintenance := backend.Run(context.Background(), 0x1ffc, cpu.ModeARM, 1)
	if maintenance.Err != nil || maintenance.Instructions != 1 {
		t.Fatalf("I-cache invalidate result = %+v", maintenance)
	}
	if len(backend.jitBlocks) != blocks || backend.jitGen != generation {
		t.Fatalf("cache maintenance dropped translated blocks: %d->%d, generation %d->%d",
			blocks, len(backend.jitBlocks), generation, backend.jitGen)
	}
}

// The whole-system Thumb loop drives the batch loop one instruction at a time
// so the phone's asynchronous checks stay off the application path. It has to
// stop at a branch-exchange exactly as the batch loop does: continuing would
// decode ARM words through the Thumb decoder and derail the guest a long way
// from the instruction that actually switched mode.
func TestSystemBusThumbStopsAtBranchExchangeIntoARM(t *testing.T) {
	backend := New()
	defer backend.Close()
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	bus.writeRaw(0x1000, []byte{
		0x01, 0x48, // ldr r0, [pc, #4] -> 0x1008
		0x00, 0x47, // bx r0
	})
	bus.writeU32(0x1008, 0x2000) // ARM entry, bit 0 clear
	bus.writeU32(0x2000, 0xe2911001)
	bus.writeU32(0x2004, 0xe2911001)
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 4)
	if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 4 {
		t.Fatalf("interworking run result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 2 {
		t.Fatalf("r1 = %d after two ARM adds, want 2", got)
	}
}
