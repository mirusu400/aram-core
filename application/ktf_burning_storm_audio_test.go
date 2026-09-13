package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// TestKTFBurningStormPublishesAudio is an optional authorized-corpus regression.
// Only the package identity is retained here, never proprietary package bytes.
func TestKTFBurningStormPublishesAudio(t *testing.T) {
	path, data := findAuthorizedPackage(t, "7c4e5ee6dcc43ec7e426c59e3dbbeba98e8efc9c4bb7eb7f988c21b36042cce0")
	ctx := context.Background()
	factory := NewFactory()
	factory.NewCPU = func() cpu.Backend { return interpreter.New() }
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Drain each frame through the same publication boundary used by a frontend.
	// Empty buffers and allocated but entirely silent PCM must both fail.
	stage := func(name string, frames int, wantAudio bool) {
		t.Helper()
		samples, nonzero := 0, 0
		for frame := 0; frame < frames; frame++ {
			if err := machine.StepFrame(ctx); err != nil {
				t.Fatalf("%s frame %d: %v", name, frame, err)
			}
			chunk := machine.DrainPublishedAudio()
			samples += len(chunk.PCM16)
			for _, sample := range chunk.PCM16 {
				if sample != 0 {
					nonzero++
				}
			}
		}
		t.Logf("%s: frames=%d samples=%d nonzero=%d", name, frames, samples, nonzero)
		if wantAudio && nonzero == 0 {
			t.Errorf("%s: no nonzero published PCM in %d frames", name, frames)
		}
	}
	fire := func() {
		t.Helper()
		if err := machine.QueueInput(machinecore.InputEvent{Control: "fire", Pressed: true}); err != nil {
			t.Fatal(err)
		}
		stage("fire held", 10, false)
		if err := machine.QueueInput(machinecore.InputEvent{Control: "fire", Pressed: false}); err != nil {
			t.Fatal(err)
		}
	}
	stage("title", 180, true)
	fire()
	stage("menu", 600, true)
	fire()
	stage("gameplay-start", 300, true)
	fire()
	stage("gameplay", 600, true)
}
