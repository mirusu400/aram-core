package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// TestKTFRegisterMNClassesStopsAtUnrelocatedSentinelRecord reproduces the
// gamearchive corpus crash: a module's class list does not always end at a
// null pointer. Some builds (every KTF WIPI1.1-era title probed) close it
// with a record whose fields were never relocated - a small offset sits
// where a class descriptor pointer belongs. Dereferencing it read guest
// address 0x508 and crashed the whole bootstrap. The walk has to recognize
// that shape and stop instead.
func TestKTFRegisterMNClassesStopsAtUnrelocatedSentinelRecord(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     make([]byte, 4096),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.CPU.Close() })
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	name, err := runtime.allocateBytes(append([]byte("Real"), 0), true)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := runtime.AllocateWords(9)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(descriptor, []uint32{
		name, 0, 0, 0, 0, 0, 0, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}

	// Two class records laid out in the image the way a module carries them:
	// object := record - 4, and the record itself is read as five words
	// starting at record. The real class comes first; the sentinel follows
	// it, the same order the corpus titles carry it in.
	const (
		object      = ImageBase + 0x100
		record      = object + 4
		sentinel    = ImageBase + 0x200
		sentinelRec = sentinel + 4
		// Past every mapped region: the corpus sentinel's own "next" field,
		// which must end the walk rather than be dereferenced.
		outsideImage = 0x00080000
		// Not a pointer to anything: the corpus sentinel's own descriptor
		// field, which must not be dereferenced either.
		unrelocatedOffset = 0x500
	)
	moduleTable := uint32(record - mnModuleTableClasses)

	fields := []struct {
		address uint32
		value   uint32
	}{
		{object, 0x19},             // tag (unused by the walk itself)
		{object + 4, 0},            // record's own first word
		{object + 8, descriptor},   // class descriptor pointer
		{object + 12, 0},           // reserved
		{object + 16, 0},           // flags
		{object + 20, sentinelRec}, // next record

		{sentinel, 0x80000000},            // sentinel tag
		{sentinel + 4, 0},                 // record's own first word
		{sentinel + 8, unrelocatedOffset}, // never a real descriptor
		{sentinel + 12, 0},
		{sentinel + 16, 0},
		{sentinel + 20, outsideImage}, // next: outside every mapped region
	}
	for _, field := range fields {
		if err := runtime.WriteU32(field.address, field.value); err != nil {
			t.Fatal(err)
		}
	}

	if err := runtime.registerMNClasses(moduleTable, 0); err != nil {
		t.Fatalf("registerMNClasses returned %v, want the sentinel skipped and the real class registered", err)
	}
}
