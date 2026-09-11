package raptor

import "testing"

// The compact Runnable special case must not shift normal Thread dispatch or
// infer a sole run method for Object subclasses with more than one own slot.
func TestRaptorJavaThreadRunOtherLayoutsUnchanged(t *testing.T) {
	for _, tt := range []struct {
		name   string
		parent string
		slots  uint32
	}{
		{name: "Thread_subclass", parent: "java/lang/Thread", slots: 12},
		{name: "Object_two_own_slots", parent: "java/lang/Object", slots: 12},
		{name: "Object_three_own_slots", parent: "java/lang/Object", slots: 13},
	} {
		t.Run(tt.name, func(t *testing.T) {
			public := newPublicRuntime(t)
			runtime := &Runtime{CPU: public.CPU, Public: public}
			java, err := runtime.ensureJavaRuntime()
			check(t, err)
			class := newRaptorTestClass(t, runtime, java, "app/Worker", tt.parent, nil)
			const firstOwnBody = uint32(0x00002401)
			const expectedRunBody = uint32(0x00002801)
			table, err := public.Heap.Allocate(4+tt.slots*4, true)
			check(t, err)
			check(t, public.WriteU32(table, class.Holder))
			check(t, public.WriteU32(table+0x2c, firstOwnBody))
			check(t, public.WriteU32(table+raptorJavaThreadRunSlot, expectedRunBody))
			check(t, public.WriteU32(class.descriptor+0x24, tt.slots<<16|9))
			class.guestVTable = table
			object, err := public.Heap.Allocate(12, true)
			check(t, err)
			check(t, public.WriteU32(object+4, class.Holder))
			if got := runtime.raptorJavaThreadRun(java, object); got != expectedRunBody {
				t.Fatalf("existing fallback body = %#x, want %#x", got, expectedRunBody)
			}
		})
	}
}

// Runnable targets passed to Thread are not necessarily Thread subclasses.
// This normal compact Runnable layout has Object's ten slots and one own
// run() slot. It must resolve that body rather than Thread's later run slot.
func TestRaptorJavaSingleMethodRunnableTarget(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	class := newRaptorTestClass(t, runtime, java, "app/Worker", "java/lang/Object", nil)
	const runBody = uint32(0x00002401)
	const slots = uint32(11)
	table, err := public.Heap.Allocate(4+slots*4, true)
	check(t, err)
	check(t, public.WriteU32(table, class.Holder))
	check(t, public.WriteU32(table+slots*4, runBody))
	check(t, public.WriteU32(class.descriptor+0x24, slots<<16|9))
	class.guestVTable = table
	object, err := public.Heap.Allocate(12, true)
	check(t, err)
	check(t, public.WriteU32(object+4, class.Holder))
	if got := runtime.raptorJavaThreadRun(java, object); got != runBody {
		t.Fatalf("Runnable run body = %#x, want %#x", got, runBody)
	}
	// Published method metadata always takes precedence over compact layout.
	const declaredBody = uint32(0x00002801)
	class.methods = []raptorJavaDeclaredMethod{{Name: "run", descriptor: "()V", Body: declaredBody}}
	if got := runtime.raptorJavaThreadRun(java, object); got != declaredBody {
		t.Fatalf("declared Runnable run body = %#x, want %#x", got, declaredBody)
	}
}
