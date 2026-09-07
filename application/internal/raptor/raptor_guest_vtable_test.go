package raptor

import "testing"

// A class the module built its own dispatch table for gets its own slots
// from that table, not from the descriptor words at +0x2c, which in that SDK
// are the class's static helpers. SD한국전쟁's script-command class l has one
// own method - the command-kind getter at +0x2c, returning 4 - and no +0x20
// table pointer; the inline pass placed its <clinit> (a bare `bx lr`) in the
// slot, so the getter answered with the receiver, the command loop never
// advanced past the command, and the paint after it cast the command to the
// wrong class and read a null String (issue #157).
func TestRaptorGuestVTableOverridesDescriptorHelpers(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	const (
		parentKind = uint32(0x00001b21)
		childKind  = uint32(0x00002ef5)
		kindSlot   = uint32(0x2c)
		// The slot count java/lang/Object plus one own method occupy.
		slotCount = uint32(11)
	)
	parent := newRaptorTestClass(t, runtime, java, "f", "", nil)
	child := newRaptorTestClass(t, runtime, java, "l", parent.Name, nil)
	inheritor := newRaptorTestClass(t, runtime, java, "c", parent.Name, nil)
	installRaptorGuestVTable(t, runtime, parent, slotCount, parentKind)
	installRaptorGuestVTable(t, runtime, child, slotCount, childKind)
	// The inheritor declares nothing of its own: its slot is zero in the
	// module's table and must come from the parent.
	installRaptorGuestVTable(t, runtime, inheritor, slotCount, 0)
	java.flatVirtual = []raptorJavaMethod{
		{className: "app/Unrelated", Name: "tick", descriptor: "()V"},
	}

	for _, class := range []*raptorJavaClass{parent, child, inheritor} {
		check(t, runtime.buildRaptorJavaVTable(java, class, uint32(len(java.flatVirtual))))
	}

	slot, err := public.ReadU32(child.vtable + kindSlot)
	check(t, err)
	if slot != childKind {
		t.Fatalf("child slot +0x2c = 0x%08x, want the module's body 0x%08x", slot, childKind)
	}
	// The word after the table's last slot is the next class's descriptor,
	// not a method: the slot count keeps it out, and the backstop fills the
	// slot instead.
	beyond, err := public.ReadU32(child.vtable + kindSlot + 4)
	check(t, err)
	if beyond != java.noopStub {
		t.Fatalf("child slot +0x30 = 0x%08x, want the no-op backstop 0x%08x", beyond, java.noopStub)
	}
	inherited, err := public.ReadU32(inheritor.vtable + kindSlot)
	check(t, err)
	if inherited != parentKind {
		t.Fatalf("inheritor slot +0x2c = 0x%08x, want the parent's body 0x%08x", inherited, parentKind)
	}
}

// installRaptorGuestVTable gives a test class the module-built dispatch table
// shape: descriptor+0x24 carries the slot count, +0x2c..+0x34 hold the
// class's three static helpers, and descriptor+0x0c (kept as guestVTable)
// names a table of holder, Object slots, the one own slot, then the next
// class's first descriptor word.
func installRaptorGuestVTable(
	t *testing.T,
	runtime *Runtime,
	class *raptorJavaClass,
	slots uint32,
	ownBody uint32,
) {
	t.Helper()
	const (
		helperClinit  = uint32(0x00002e59)
		helperEnsure  = uint32(0x00002e99)
		helperResolve = uint32(0x00002e61)
		nextDescWord  = uint32(0x00000031)
	)
	table, err := runtime.Public.Heap.Allocate(4+slots*4+4, true)
	if err != nil || table == 0 {
		t.Fatalf("allocate guest vtable = 0x%08x, %v", table, err)
	}
	check(t, runtime.Public.WriteU32(table, class.Holder))
	check(t, runtime.Public.WriteU32(table+slots*4, ownBody))
	check(t, runtime.Public.WriteU32(table+4+slots*4, nextDescWord))
	check(t, runtime.Public.WriteU32(class.descriptor+0x0c, table))
	check(t, runtime.Public.WriteU32(class.descriptor+0x24, slots<<16|9))
	check(t, runtime.Public.WriteU32(class.descriptor+0x2c, helperClinit))
	check(t, runtime.Public.WriteU32(class.descriptor+0x30, helperEnsure))
	check(t, runtime.Public.WriteU32(class.descriptor+0x34, helperResolve))
	class.guestVTable = table
}
