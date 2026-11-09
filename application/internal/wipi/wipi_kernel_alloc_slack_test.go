package wipi

import "testing"

// A string builder in a title checks only that the next character fits before
// it appends it with MC_knlSprintk(buf+len, "%c", c). When the buffer is
// exactly full, the terminating NUL lands one byte past its end. A handset's C
// heap keeps a block header or rounding slack there. ARAM packed blocks flush
// against each other, so the NUL zeroed the low byte of the next object's
// class-descriptor pointer, and the title's next virtual call on that object
// jumped into data (issue #577).
func TestKernelAllocKeepsSpareByteAfterRequest(t *testing.T) {
	runtime := newPublicRuntime(t)
	format, err := runtime.Heap.Allocate(4, true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(format, []byte("%c\x00")))
	for _, name := range []string{"MC_knlAlloc", "MC_knlCalloc"} {
		buffer := dispatchPublicAPI(t, runtime, name, 8).Low
		object := dispatchPublicAPI(t, runtime, name, 8).Low
		if buffer == 0 || object == 0 {
			t.Fatalf("%s(8) = 0x%08x, 0x%08x", name, buffer, object)
		}
		const descriptor = 0x014024b0
		check(t, runtime.WriteU32(object, descriptor))
		dispatchPublicAPI(t, runtime, "MC_knlSprintk", buffer+7, format, 'x')
		if got, err := runtime.ReadU32(object); err != nil || got != descriptor {
			t.Fatalf("%s: next block header = 0x%08x after a full-buffer append, want 0x%08x: %v", name, got, descriptor, err)
		}
	}
}
