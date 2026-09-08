package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestRaptorImplicitCheckThrowIsRecorded pins that the module-100 helpers a
// Raptor Clet uses to raise Java's implicit runtime checks are recognized.
//
// They used to fall through to the anonymous unimplemented-import path, which
// resumed the guest with r0 = 0 under the label "RAPTOR.module100#34". The
// guest then ran the operation the check had just rejected: 아빠와나 dispatched
// a virtual call through a null reference and branched to address 0, and the
// machine failed with "ARM fetch at 0x00000000" naming nothing about the
// exception that had been thrown and lost (issues #164/#195/#207/#215).
func TestRaptorImplicitCheckThrowIsRecorded(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	const throwSite = uint32(0x00001b91)
	check(t, public.CPU.WriteRegister(cpu.RegisterLR, throwSite))
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, 0x0badc0de))
	runtime.BeginGuestSlice()

	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 34},
	))

	name := RaptorJavaThrowName("java/lang/NullPointerException")
	if got := public.Stats.LastUnimplemented; got != name {
		t.Fatalf("last unimplemented API = %q, want %q", got, name)
	}
	if got := public.Unimplemented[name]; got != 1 {
		t.Fatalf("unimplemented count for %q = %d, want 1", name, got)
	}
	if got := public.Unimplemented["RAPTOR.module100#34"]; got != 0 {
		t.Fatalf("throw was also recorded as an anonymous import %d times", got)
	}
	if runtime.LastJavaThrow != "java/lang/NullPointerException thrown at 0x00001b90" {
		t.Fatalf("LastJavaThrow = %q", runtime.LastJavaThrow)
	}

	// The guest still resumes, so a title whose swallowed throw is harmless
	// keeps running; the exception stays takeable for the slice so a fault in
	// its aftermath is attributed to it instead of to an address-zero branch.
	pc, err := public.CPU.ReadRegister(cpu.RegisterPC)
	check(t, err)
	if pc != throwSite&^1 {
		t.Fatalf("resume PC = 0x%08x, want 0x%08x", pc, throwSite&^1)
	}
	description, undelivered := runtime.TakeUndeliveredJavaThrow()
	if !undelivered || description != runtime.LastJavaThrow {
		t.Fatalf("TakeUndeliveredJavaThrow = %q, %t", description, undelivered)
	}
	if _, again := runtime.TakeUndeliveredJavaThrow(); again {
		t.Fatal("the same throw was reported twice")
	}
}

// TestRaptorUndeliveredThrowIsSliceScoped keeps a fault in a later slice from
// being blamed on an exception an earlier slice swallowed.
func TestRaptorUndeliveredThrowIsSliceScoped(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	runtime.recordRaptorJavaThrow("java/lang/ClassCastException", 0x1234)
	runtime.BeginGuestSlice()
	if description, undelivered := runtime.TakeUndeliveredJavaThrow(); undelivered {
		t.Fatalf("throw survived into the next slice as %q", description)
	}
}
