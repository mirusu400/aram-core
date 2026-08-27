package cpu_test

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func warmJITBackend(t *testing.T) *interpreter.Backend {
	t.Helper()
	backend := interpreter.NewJIT()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x30, // adds r0, #1
		0xfd, 0xe7, // b 0x1000
	}); err != nil {
		t.Fatal(err)
	}
	if result := backend.Run(t.Context(), 0x1000, cpu.ModeThumb, 64); result.Err != nil {
		t.Fatalf("warm run = %+v", result)
	}
	return backend
}

// A host callback re-enters the guest and returns to the address space it left,
// so the return must not retire translations the guest is about to run again.
func TestScopedContextRoundTripKeepsTranslations(t *testing.T) {
	backend := warmJITBackend(t)
	if err := backend.WriteRegister(cpu.RegisterR0, 0x11223344); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x7fff0100); err != nil {
		t.Fatal(err)
	}
	before := backend.ExecutionStatistics()

	saved, err := cpu.SaveScopedContext(backend, cpu.ScopedContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if err := saved.Restore(backend); err != nil {
		t.Fatal(err)
	}

	value, err := backend.ReadRegister(cpu.RegisterR0)
	if err != nil || value != 0x11223344 {
		t.Fatalf("r0 after restore = 0x%08x, err=%v", value, err)
	}
	after := backend.ExecutionStatistics()
	if after.TranslationInvalidations != before.TranslationInvalidations {
		t.Fatalf("scoped restore flushed translations: %d -> %d",
			before.TranslationInvalidations, after.TranslationInvalidations)
	}
	if after.SerializedContextSaves != before.SerializedContextSaves ||
		after.SerializedContextRestores != before.SerializedContextRestores {
		t.Fatal("scoped save/restore fell back to the portable context")
	}
}

// A nested call happens many times per frame, so repeating it must not allocate.
func TestScopedContextReuseAllocatesNothing(t *testing.T) {
	backend := warmJITBackend(t)
	reuse, err := cpu.SaveScopedContext(backend, cpu.ScopedContext{})
	if err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(500, func() {
		saved, err := cpu.SaveScopedContext(backend, reuse)
		if err != nil {
			panic(err)
		}
		reuse = saved
		if err := saved.Restore(backend); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("scoped save/restore allocations = %.2f, want 0", allocations)
	}
}

// A backend with no reusable context still round-trips through the portable one.
func TestScopedContextFallsBackToThePortableContext(t *testing.T) {
	backend := interpreter.New()
	defer backend.Close()
	if err := backend.WriteRegister(cpu.RegisterR4, 0xfeedface); err != nil {
		t.Fatal(err)
	}
	saved, err := cpu.SaveScopedContext(portableOnly{backend}, cpu.ScopedContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR4, 0); err != nil {
		t.Fatal(err)
	}
	if err := saved.Restore(portableOnly{backend}); err != nil {
		t.Fatal(err)
	}
	value, err := backend.ReadRegister(cpu.RegisterR4)
	if err != nil || value != 0xfeedface {
		t.Fatalf("r4 after portable restore = 0x%08x, err=%v", value, err)
	}
}

// portableOnly hides the reusable-context capability so the fallback is
// exercised on a backend that does implement it.
type portableOnly struct{ cpu.Backend }
