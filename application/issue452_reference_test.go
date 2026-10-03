package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The reference package is supplied through ARAM_TEST_DATA; no game assets are
// included in the repository. This path reaches play after the tutorial dialogs.
func TestIssue452TimeAndTalesEntersPlay(t *testing.T) {
	const archiveSHA256 = "92a4eed51e5717de5fd13f3cd0c9ab3633ec6daaaac48abbe285fa216d195e53"
	path, data := findAuthorizedPackage(t, archiveSHA256)
	factory := NewFactory()
	var err error
	factory.NewCPU, err = ResolveCPUBackend("fastest")
	if err != nil {
		t.Fatal(err)
	}
	factory.RunBudget = DefaultHandsetRunBudget
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame <= 5400; frame++ {
		if frame == 30 || frame >= 900 && (frame-900)%150 == 0 {
			if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}); err != nil {
				t.Fatal(err)
			}
		}
		if frame == 32 || frame >= 902 && (frame-902)%150 == 0 {
			if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: false}); err != nil {
				t.Fatal(err)
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	if len(machine.raptor.Java.Tasks) < 3 || machine.raptor.Java.Tasks[2].Procedure != 0x0005ba2c {
		t.Fatalf("game-start Runnable was not scheduled at its run() body: %+v", machine.raptor.Java.Tasks)
	}
	const stalledSplashSHA256 = "b623a49671edef6c9f7aa5f1d3bb68647d200839570d33bb26c3119dc1f6be44"
	if got := fmt.Sprintf("%x", sha256.Sum256(machine.frame.Pix)); got == stalledSplashSHA256 {
		t.Fatal("game remained on the blue Time & Tales splash after the tutorial")
	}
}
