package raptor

import (
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// TestRaptorJavaTaskStacksDoNotOverlap pins that every scheduled thread gets
// its own stack. They all started at the same stack pointer, which only stayed
// hidden while a second thread could never be scheduled: as soon as
// 현영맞고2006's TimeChecker ran alongside its game loop the two wrote over each
// other's frames and the guest faulted on a smashed local (issue #79).
func TestRaptorJavaTaskStacksDoNotOverlap(t *testing.T) {
	seen := map[uint32]int{}
	previous := uint32(0)
	for index := 0; index < raptorJavaTaskStackMax; index++ {
		stack := RaptorJavaTaskStack(index)
		if other, clash := seen[stack]; clash {
			t.Fatalf("thread %d shares thread %d's stack 0x%08x", index, other, stack)
		}
		seen[stack] = index
		if index > 0 && previous-stack != raptorJavaTaskStackSize {
			t.Fatalf(
				"thread %d stack 0x%08x is %d bytes below thread %d's, want %d",
				index, stack, previous-stack, index-1, raptorJavaTaskStackSize,
			)
		}
		previous = stack
	}
	// Every stack has to stay inside the guest stack mapping.
	lowest := RaptorJavaTaskStack(raptorJavaTaskStackMax - 1)
	if lowest < guest.DefaultStackBase {
		t.Fatalf("lowest thread stack 0x%08x is below the guest stack", lowest)
	}
	// Threads past the supported count share the last stack rather than
	// wandering out of the mapping.
	if got := RaptorJavaTaskStack(raptorJavaTaskStackMax + 3); got != lowest {
		t.Fatalf("overflow thread stack = 0x%08x, want 0x%08x", got, lowest)
	}
	if got := RaptorJavaTaskStack(-1); got != RaptorJavaTaskStack(0) {
		t.Fatalf("negative index stack = 0x%08x", got)
	}
}

// TestRaptorJavaThreadRunFallsBackToTheGuestVTable pins how a started thread's
// body is found when the module publishes no metadata for its class.
// 현영맞고2006's TimeChecker carries neither a method nor a field table, so
// resolving run() by name found nothing and the thread was never scheduled.
func TestRaptorJavaThreadRunFallsBackToTheGuestVTable(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java := &JavaRuntime{
		classes:     map[uint32]*raptorJavaClass{},
		ClassByName: map[string]*raptorJavaClass{},
	}

	vtable, err := public.Heap.Allocate(raptorJavaThreadRunSlot+8, true)
	if err != nil {
		t.Fatal(err)
	}
	const runBody = uint32(0x00083005)
	if err := public.WriteU32(vtable+raptorJavaThreadRunSlot, runBody); err != nil {
		t.Fatal(err)
	}
	object, err := public.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	holder := uint32(0x01403600)
	if err := public.WriteU32(object+4, holder); err != nil {
		t.Fatal(err)
	}
	class := &raptorJavaClass{
		Holder:      holder,
		Name:        "TimeChecker",
		parentName:  "java/lang/Thread",
		guestVTable: vtable,
	}
	java.classes[holder] = class
	java.ClassByName[class.Name] = class

	if got := runtime.raptorJavaThreadRun(java, object); got != runBody {
		t.Fatalf("run body = 0x%08x, want 0x%08x", got, runBody)
	}

	// A class that does publish its metadata is still resolved by name, and
	// the declared body wins over the vtable slot.
	const declared = uint32(0x0000ad05)
	class.methods = []raptorJavaDeclaredMethod{
		{Name: "run", descriptor: "()V", Body: declared},
	}
	if got := runtime.raptorJavaThreadRun(java, object); got != declared {
		t.Fatalf("declared run body = 0x%08x, want 0x%08x", got, declared)
	}
}
