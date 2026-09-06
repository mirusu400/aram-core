package interpreter

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestExecutionContextRoundTripPreservesWarmTranslations(t *testing.T) {
	backend := NewJIT()
	defer backend.Close()
	check(t, backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	))
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x30, // adds r0, #1
		0xfd, 0xe7, // b 0x1000
	}); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(t.Context(), 0x1000, cpu.ModeThumb, 64)
	if result.Err != nil || result.Reason != cpu.StopBudget {
		t.Fatalf("warm run = %+v", result)
	}
	if len(backend.jitBlocks) == 0 {
		t.Fatal("warm run produced no translated blocks")
	}

	backend.regs[cpu.RegisterR0] = 0x11223344
	backend.regs[cpu.RegisterSP] = 0x7fff0100
	check(t, backend.WriteRegister(
		cpu.RegisterCPSR,
		flagN|flagC|cpu.StatusThumb,
	))
	backend.banks.fiq[0] = 0x55667788
	backend.banks.irq[1] = 0x99aabbcc
	backend.spsr.supervisor = 0xddeeff00
	backend.mode = cpu.ModeThumb
	wantRegs := backend.regs
	wantBanks := backend.banks
	wantSPSR := backend.spsr
	wantMode := backend.mode
	warmBlocks := len(backend.jitBlocks)
	warmGeneration := backend.jitGen
	before := backend.ExecutionStatistics()

	saved, err := backend.SaveExecutionContext(nil)
	check(t, err)
	backend.regs = [17]uint32{}
	backend.banks = bankedRegisters{}
	backend.spsr = savedProgramStatus{}
	backend.mode = cpu.ModeARM
	check(t, backend.RestoreExecutionContext(saved))

	if backend.regs != wantRegs || backend.banks != wantBanks ||
		backend.spsr != wantSPSR || backend.mode != wantMode {
		t.Fatalf(
			"execution state mismatch: regs=%#v banks=%#v spsr=%#v mode=%d",
			backend.regs,
			backend.banks,
			backend.spsr,
			backend.mode,
		)
	}
	if len(backend.jitBlocks) != warmBlocks || backend.jitGen != warmGeneration {
		t.Fatalf(
			"fast restore invalidated warm translations: blocks %d->%d generation %d->%d",
			warmBlocks,
			len(backend.jitBlocks),
			warmGeneration,
			backend.jitGen,
		)
	}
	after := backend.ExecutionStatistics()
	if after.FastContextSaves-before.FastContextSaves != 1 ||
		after.FastContextRestores-before.FastContextRestores != 1 ||
		after.TranslationInvalidations != before.TranslationInvalidations {
		t.Fatalf("execution statistics = before %+v after %+v", before, after)
	}

	serialized, err := backend.MarshalExecutionContext(saved, nil)
	check(t, err)
	portable, err := backend.SaveContext()
	check(t, err)
	if !bytes.Equal(serialized, portable) {
		t.Fatal("marshaled execution context changed the portable save-state format")
	}
}

func TestExecutionContextSaveReusesItsAllocation(t *testing.T) {
	backend := New()
	defer backend.Close()
	saved, err := backend.SaveExecutionContext(nil)
	check(t, err)
	allocations := testing.AllocsPerRun(1000, func() {
		saved, err = backend.SaveExecutionContext(saved)
		if err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("reused execution-context save = %g allocs, want 0", allocations)
	}
}

func TestExecutionContextRejectsWholeSystemState(t *testing.T) {
	backend := New()
	defer backend.Close()
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	check(t, backend.AttachSystemBus(bus))
	if _, err := backend.SaveExecutionContext(nil); !errors.Is(
		err,
		cpu.ErrExecutionContextUnavailable,
	) {
		t.Fatalf("whole-system execution context error = %v", err)
	}
}

func TestExecutionContextKeepsSMCInvalidationActive(t *testing.T) {
	backend := NewJIT()
	defer backend.Close()
	check(t, backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	))
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x30, // adds r0, #1
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	check(t, backend.WriteRegister(cpu.RegisterLR, 0x1002|1))
	if result := backend.Run(t.Context(), 0x1000, cpu.ModeThumb, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	saved, err := backend.SaveExecutionContext(nil)
	check(t, err)
	check(t, backend.RestoreExecutionContext(saved))
	generation := backend.jitGen
	check(t, backend.WriteMemory(0x1000, []byte{0x02, 0x30}))
	if backend.jitGen == generation {
		t.Fatal("self-modifying write did not invalidate the translated range")
	}
	check(t, backend.WriteRegister(cpu.RegisterR0, 0))
	if result := backend.Run(t.Context(), 0x1000, cpu.ModeThumb, 1); result.Err != nil {
		t.Fatal(result.Err)
	}
	if got, err := backend.ReadRegister(cpu.RegisterR0); err != nil || got != 2 {
		t.Fatalf("modified instruction result = %d, %v; want 2", got, err)
	}
}

func BenchmarkExecutionContextSwitch(b *testing.B) {
	backend := NewJIT()
	b.Cleanup(func() { _ = backend.Close() })
	contexts := make([]cpu.ExecutionContext, 4)
	for index := range contexts {
		check(b, backend.WriteRegister(cpu.RegisterR0, uint32(index)))
		var err error
		contexts[index], err = backend.SaveExecutionContext(nil)
		check(b, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		current := index & 3
		var err error
		contexts[current], err = backend.SaveExecutionContext(contexts[current])
		check(b, err)
		check(b, backend.RestoreExecutionContext(contexts[(current+1)&3]))
	}
}
