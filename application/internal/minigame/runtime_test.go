package minigame

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestClearActionDiscardsGameplayDrawLists(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, bytes.Repeat([]byte{0x7a}, 16)); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{cpu: backend, screenPixels: 0x1000, width: 2, height: 2}

	runtime.beginInputFrame(clearActionKey)
	runtime.presentFrame()
	if !runtime.clearFrames || !runtime.clearOnDraw {
		t.Fatal("clear action did not arm the discard-style gameplay surface")
	}
	if err := runtime.putPixel(1, 0, 0x00123456); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 16)
	if err := backend.ReadMemory(0x1000, raw); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 0, 0x56, 0x34, 0x12, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(raw, want) {
		t.Fatalf("discarded draw list = %x, want %x", raw, want)
	}
	if runtime.clearOnDraw {
		t.Fatal("first gameplay draw did not consume the pending clear")
	}

	runtime.presentFrame()
	if !runtime.clearOnDraw || runtime.presentCount != 2 || runtime.tickMS != 32 {
		t.Fatalf("next presentation did not arm a clear: count=%d tick=%d clear=%t",
			runtime.presentCount, runtime.tickMS, runtime.clearOnDraw)
	}

	retained := &Runtime{}
	retained.beginInputFrame(0x13)
	retained.presentFrame()
	if retained.clearFrames || retained.clearOnDraw {
		t.Fatal("ordinary menu input changed the retained drawing surface")
	}
}

func TestFrameEventResumesAfterInstructionBudget(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x30, // adds r0, #1
		0xfd, 0xe7, // b 0x1000
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterPC, guest.ReturnSentinel); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		cpu:   backend,
		entry: 0x1001,
		stage: 5,
	}

	first, completed, err := runtime.StepFrame(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if completed || first.Instructions != 4 || len(runtime.Stats.Events) != 1 {
		t.Fatalf("first frame slice = %+v, completed=%t", first, completed)
	}
	if got, err := backend.ReadRegister(cpu.RegisterR0); err != nil || got != FrameEvent+2 {
		t.Fatalf("r0 after first slice = %d, err=%v; want %d", got, err, FrameEvent+2)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, 99); err != nil {
		t.Fatal(err)
	}

	second, completed, err := runtime.StepFrame(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if completed || second.Instructions != 4 || len(runtime.Stats.Events) != 2 {
		t.Fatalf("second frame slice = %+v, completed=%t", second, completed)
	}
	if got, err := backend.ReadRegister(cpu.RegisterR0); err != nil || got != FrameEvent+4 {
		t.Fatalf("r0 after resumed slice = %d, err=%v; want %d", got, err, FrameEvent+4)
	}
	if got, err := backend.ReadRegister(cpu.RegisterR1); err != nil || got != 99 {
		t.Fatalf("r1 after resumed slice = %d, err=%v; want 99", got, err)
	}
}

// The save format refuses a longer event log than maxSavedEADSEvents, and a
// frame that needs several budget slices records one result per slice, so the
// log must stay bounded no matter how long a title runs.
func TestEventResultLogStaysWithinTheSaveFormatLimit(t *testing.T) {
	runtime := &Runtime{}
	for index := 0; index < maxSavedEADSEvents+16; index++ {
		runtime.appendEventResult(EADSEventResult{Instructions: uint64(index)})
	}
	if len(runtime.Stats.Events) != maxSavedEADSEvents {
		t.Fatalf("retained %d results, want %d",
			len(runtime.Stats.Events), maxSavedEADSEvents)
	}
	newest := runtime.Stats.Events[len(runtime.Stats.Events)-1]
	if newest.Instructions != uint64(maxSavedEADSEvents+15) {
		t.Fatalf("newest result = %d, want %d",
			newest.Instructions, maxSavedEADSEvents+15)
	}
	count := uint64(maxSavedEADSEvents + 16)
	if got, want := runtime.Stats.TotalInstructions, count*(count-1)/2; got != want {
		t.Fatalf("rolling event log lost cumulative instructions: %d, want %d", got, want)
	}
}
