package wipi

import (
	"bytes"
	"image"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// The guarded screen's pixels start inside their allocation, after the leading
// guard rows. A state holding such a screen has to load again.
func TestStateAcceptsScreenPixelsBehindLeadingGuardRows(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.Frame = image.NewRGBA(image.Rect(0, 0, 16, 32))
	runtime.ScreenPixelGuardRows = 24
	handle, err := runtime.EnsureScreenFramebuffer()
	check(t, err)
	screen := runtime.Framebuffers[handle]
	if _, starts := runtime.Heap.Allocations[screen.Pixels]; starts {
		t.Fatal("test setup: the screen has no leading guard rows")
	}

	var buffer bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, guest.NewStateWriter(&buffer)))
	saved, err := ParseState(runtime, &guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())})
	check(t, err)
	check(t, runtime.RestoreState(saved))
	if got := runtime.Framebuffers[handle]; got.Pixels != screen.Pixels || got.Height != 32 {
		t.Fatalf("restored screen = 0x%08x, %d rows; want 0x%08x, 32 rows", got.Pixels, got.Height, screen.Pixels)
	}
}

func TestStatePixelRoomStopsAtTheAllocationEnd(t *testing.T) {
	allocations := []guest.Block{{Address: 0x1000, Size: 0x100}}
	sizes := map[uint32]uint32{0x1000: 0x100}
	for _, test := range []struct {
		name   string
		pixels uint32
		owns   bool
		want   uint32
	}{
		{"allocation start", 0x1000, false, 0x100},
		{"inside the screen allocation", 0x1040, false, 0xc0},
		{"owned pixels must start an allocation", 0x1040, true, 0},
		{"past the allocation", 0x1100, false, 0},
		{"before the allocation", 0xfff, false, 0},
	} {
		if got := framebufferPixelRoom(Framebuffer{Pixels: test.pixels, owns: test.owns}, allocations, sizes); got != test.want {
			t.Errorf("%s: room = %#x, want %#x", test.name, got, test.want)
		}
	}
}
