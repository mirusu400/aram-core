package wipi

import (
	"bytes"
	"testing"
)

// Under the indirect convention a memory ID is the address of a cell holding
// the data pointer. MC_knlAlloc hands such IDs out, MC_knlGetResource takes
// one as its destination, and a framebuffer handle is an ID for a descriptor
// in the standard field order whose pixels are named by an ID as well.
func TestIndirectMemoryIDs(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.IndirectMemoryIDs = true

	id := dispatchPublicAPI(t, runtime, "MC_knlCalloc", 16).Low
	pointer, err := runtime.ReadU32(id)
	check(t, err)
	if id == 0 || pointer == 0 || pointer == id {
		t.Fatalf("memory ID %#x holds pointer %#x", id, pointer)
	}

	resource := runtime.RegisterResource("note.txt", []byte("resource"))
	if got := int32(dispatchPublicAPI(t, runtime, "MC_knlGetResource", uint32(resource), id, 16).Low); got != 0 {
		t.Fatalf("MC_knlGetResource = %d", got)
	}
	data := make([]byte, 8)
	check(t, runtime.CPU.ReadMemory(pointer, data))
	if !bytes.Equal(data, []byte("resource")) {
		t.Fatalf("resource was read to %q; the ID's own cell must stay intact", data)
	}
	if again, err := runtime.ReadU32(id); err != nil || again != pointer {
		t.Fatalf("memory ID cell = %#x, %v; want %#x", again, err, pointer)
	}
	dispatchPublicAPI(t, runtime, "MC_knlFree", id)
	if _, live := runtime.Heap.Allocations[id]; live {
		t.Fatal("MC_knlFree left the block allocated")
	}

	handle := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	descriptor, err := runtime.ReadU32(handle)
	check(t, err)
	field := func(index uint32) uint32 {
		value, err := runtime.ReadU32(descriptor + index*4)
		check(t, err)
		return value
	}
	bounds := runtime.Frame.Bounds()
	framebuffer := runtime.Framebuffers[handle]
	if field(0) != uint32(bounds.Dx()) || field(1) != uint32(bounds.Dy()) ||
		field(2) != uint32(bounds.Dx()*framebuffer.BitsPerPixel/8) ||
		field(3) != uint32(framebuffer.BitsPerPixel) {
		t.Fatalf("descriptor = %d x %d, %d bytes per line, %d bits",
			field(0), field(1), field(2), field(3))
	}
	pixels, err := runtime.ReadU32(field(4))
	check(t, err)
	if pixels != framebuffer.Pixels {
		t.Fatalf("pixel ID resolves to %#x, want %#x", pixels, framebuffer.Pixels)
	}
}
