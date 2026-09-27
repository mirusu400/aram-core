package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

const cowsbladeSHA256 = "e2df58252214bef8ccb3f7355cbf9dcdc092260e97c5d215dac11be5d787fa91"

// The game's CSound class asks for package-relative MMF resources. A root-only
// Class.getResourceAsStream returned null, so the title never created a clip.
func TestKTFIssue352CowsbladeTitleMusic(t *testing.T) {
	path, data := findAuthorizedPackage(t, cowsbladeSHA256)
	factory := NewFactory()
	factory.NewCPU = newJITCPU
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	nonzero := 0
	for frame := 0; frame < 300; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		for _, sample := range machine.DrainPublishedAudio().PCM16 {
			if sample != 0 {
				nonzero++
			}
		}
	}
	if clips := len(machine.ktf.Services.Media.Snapshot().Clips); clips == 0 {
		t.Fatal("title never loaded an MMF clip")
	}
	if nonzero < 1_000 {
		t.Fatalf("title rendered only %d nonzero audio samples", nonzero)
	}
}
