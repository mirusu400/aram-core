package pxo

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestHostTracksHandlersPropertiesAndMenu(t *testing.T) {
	backend := interpreter.New()
	defer backend.Close()
	host := NewHost(backend)
	runtime := &Runtime{CPU: backend}

	dispatch := func(call Call) uint32 {
		t.Helper()
		value, err := host.Dispatch(context.Background(), runtime, call)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	dispatch(Call{Group: 0x40, Ordinal: 0x145, Args: []uint32{2, 0x1235}})
	if got := host.Handler(2); got != 0x1235 {
		t.Fatalf("handler 2 = %#x, want %#x", got, 0x1235)
	}
	dispatch(Call{Group: 0x40, Ordinal: 0x141, Args: []uint32{2}})
	if got := host.Active(); got != 2 {
		t.Fatalf("active handler = %d, want 2", got)
	}
	dispatch(Call{Group: 0x48, Ordinal: 0x32a, Args: []uint32{0x9d, 1}})
	if got := dispatch(Call{Group: 0x48, Ordinal: 0x2e2, Args: []uint32{0x9d}}); got != 1 {
		t.Fatalf("property 0x9d = %d, want 1", got)
	}
	dispatch(Call{Group: 0x44, Ordinal: 0x5f, Args: []uint32{0, 3}})
	if got := dispatch(Call{Group: 0x44, Ordinal: 0x127}); got != 1 {
		t.Fatalf("menu selection = %d, want 1", got)
	}
	dispatch(Call{Group: 0x44, Ordinal: 0x5f, Args: []uint32{0, 2}})
	if got := host.Selection(); got != 0 {
		t.Fatalf("menu selection = %d, want 0", got)
	}
}

func TestHostMapsWorkMemoryAndFramebuffer(t *testing.T) {
	backend := interpreter.New()
	defer backend.Close()
	host := NewHost(backend)
	runtime := &Runtime{CPU: backend}

	first, err := host.Dispatch(context.Background(), runtime, Call{Group: 0x48, Ordinal: 0x165, Args: []uint32{5}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := host.Dispatch(context.Background(), runtime, Call{Group: 0x48, Ordinal: 0x165, Args: []uint32{9}})
	if err != nil {
		t.Fatal(err)
	}
	if first != GuestHeapBase || second != GuestHeapBase+8 {
		t.Fatalf("allocations = (%#x, %#x), want (%#x, %#x)", first, second, GuestHeapBase, GuestHeapBase+8)
	}
	framebuffer, err := host.Dispatch(context.Background(), runtime, Call{Group: 0x40, Ordinal: 0xc0})
	if err != nil {
		t.Fatal(err)
	}
	if framebuffer != FramebufferBase || !host.FramebufferMapped() {
		t.Fatalf("framebuffer = %#x mapped=%t", framebuffer, host.FramebufferMapped())
	}
	if err := backend.WriteMemory(framebuffer+FramebufferByteSize-1, []byte{0x7f}); err != nil {
		t.Fatalf("write last framebuffer byte: %v", err)
	}
}
