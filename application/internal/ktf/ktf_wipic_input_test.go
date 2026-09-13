package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

func TestKTFWIPICInputSupportedModes(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	count, err := ktfWIPICHandler(ktfWIPICMasterInput, 3)(
		context.Background(),
		runtime,
	)
	check(t, err)
	if count != uint32(len(ktfWIPICInputModes)) {
		t.Fatalf("supported mode count = %d, want %d", count, len(ktfWIPICInputModes))
	}
	address, err := ktfWIPICHandler(ktfWIPICMasterInput, 4)(
		context.Background(),
		runtime,
	)
	check(t, err)
	if address == 0 {
		t.Fatal("supported mode table is null")
	}
	for index, want := range ktfWIPICInputModes {
		pointer := readU32(t, runtime, address+uint32(index*4))
		got, err := runtime.readCString(pointer, 16)
		check(t, err)
		if got != want {
			t.Fatalf("supported mode %d = %q, want %q", index, got, want)
		}
	}
	again, err := ktfWIPICInputGetSupportedModes(context.Background(), runtime)
	check(t, err)
	if again != address {
		t.Fatalf("second supported mode table = 0x%08x, want 0x%08x", again, address)
	}
	runtime.collectJavaHeap()
	if _, ok := runtime.Heap.Root().Allocations[address]; !ok {
		t.Fatal("supported mode table was collected while host still referenced it")
	}
}

func TestKTFWIPICInputModesSurviveStateRoundTrip(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	address, err := ktfWIPICInputGetSupportedModes(context.Background(), runtime)
	check(t, err)

	var buffer bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&buffer)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	runtime.wipicInputModes = 0
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	if runtime.wipicInputModes != address {
		t.Fatalf("restored mode table = 0x%08x, want 0x%08x", runtime.wipicInputModes, address)
	}

	// A schema-7 save has the task-thread block but no input-mode pointer.
	legacy := append([]byte(nil), buffer.Bytes()[:buffer.Len()-4-44]...)
	binary.LittleEndian.PutUint32(legacy[4:8], ktfStateSchemaV7)
	decoder = guest.StateDecoder{Reader: bytes.NewReader(legacy)}
	saved, err = ParseState(runtime, &decoder)
	check(t, err)
	if saved.wipicInputModes != 0 {
		t.Fatalf("legacy mode table = 0x%08x, want zero", saved.wipicInputModes)
	}
}
