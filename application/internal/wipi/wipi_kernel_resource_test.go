package wipi

import "testing"

// A guest asks whether its save exists by comparing the resource lookup
// against M_E_NOENT exactly. 메이플스토리 시그너스기사단 compiles
//
//	MC_knlGetResourceID(name, &size) != -12
//
// into `eor r2, r0, #-12; rsbs r3, r2, #0; orrs r3, r2; lsr r0, r3, #31`, so
// any other failure code told it the save was there. It then allocated the
// zero size it had been handed, wrote the save into that empty block, and
// destroyed the header of the next block in its own heap; a few calls later
// its allocator answered NULL and the title dereferenced it (issue #141).
func TestGetResourceIDAnswersNoEntryForAMissingResource(t *testing.T) {
	runtime := newPublicRuntime(t)
	name, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(name, []byte("bb"), -1); err != nil {
		t.Fatal(err)
	}
	size, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(size, 0x5a5a5a5a); err != nil {
		t.Fatal(err)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlGetResourceID",
		name,
		size,
	).Low); got != -12 {
		t.Fatalf("MC_knlGetResourceID for a missing resource = %d, want -12", got)
	}
	// The size has to be cleared as well: a guest that ignores the result and
	// allocates what it was handed must not be handed a stale number.
	if got, err := runtime.ReadU32(size); err != nil || got != 0 {
		t.Fatalf("resource size = %d, %v; want 0", got, err)
	}
}
