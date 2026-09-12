package ktf

import (
	"context"
	"testing"
)

// The private media provider allocates its 96-byte clip through MC_knlCalloc.
// Clet libraries dereference the returned memory ID before requesting playback
// (issue #279), so a zero-filled direct allocation is not a valid clip handle.
func TestKTFWIPICMediaUsesIndirectMemoryHandle(t *testing.T) {
	r := newTestRuntime(t)
	handle := newKTFTestMediaClip(t, r, 4096)
	allocation, ok := r.wipicMemory[handle]
	if !ok {
		t.Fatal("media create returned a direct allocation, want a kernel memory ID")
	}
	head, err := r.ReadU32(handle)
	check(t, err)
	size, err := r.ReadU32(handle + 4)
	check(t, err)
	if head == 0 || head != allocation.base || size != 96 || allocation.size != 96 || allocation.data != head+8 {
		t.Fatalf("invalid indirect clip: head=%08x size=%d allocation=%+v", head, size, allocation)
	}
	payload := make([]byte, 96)
	check(t, r.CPU.ReadMemory(head+8, payload))
	for _, b := range payload {
		if b != 0 {
			t.Fatal("clip allocation was not cleared")
		}
	}
	putKTFTestMediaData(t, r, handle, []byte("MMMD-test"))
	setKTFWIPICCallArguments(t, r, []uint32{handle, 0})
	result, err := ktfWIPICMediaPlay(context.Background(), r)
	check(t, err)
	if result != 0 {
		t.Fatalf("play returned %08x", result)
	}
	for _, handler := range []ktfHostHandler{ktfWIPICMediaStop, ktfWIPICMediaClearData} {
		setKTFWIPICCallArguments(t, r, []uint32{handle})
		_, err := handler(context.Background(), r)
		check(t, err)
		got, err := r.ReadU32(handle)
		check(t, err)
		if got != head {
			t.Fatalf("media operation changed the indirect header: %08x", got)
		}
	}
	setKTFWIPICCallArguments(t, r, []uint32{handle})
	_, err = ktfWIPICMediaDestroy(context.Background(), r)
	check(t, err)
	if _, ok := r.wipicMemory[handle]; ok {
		t.Fatal("destroy retained the kernel memory ID")
	}
	if r.Heap.Release(allocation.base) {
		t.Fatal("destroy leaked the backing allocation")
	}
	if r.Heap.Release(handle) {
		t.Fatal("destroy leaked the handle allocation")
	}
}
