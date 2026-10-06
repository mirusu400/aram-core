package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The SKT episode 2 package starts a new game with its first active sprite at
// the map edge. Its tile-marker probe previously crashed on row -1 at frame 821.
func TestSKVMIssue491AstoniaEP2StartsNewGameAtMapEdge(t *testing.T) {
	const digest = "08aa799c11b0d97d4f3b03fc1c2ce7fb28136009227ae6442e0d5ca105fe6ede"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	if err := created.Start(ctx); err != nil {
		t.Fatal(err)
	}
	events := map[int]machinecore.InputEvent{
		308: {Control: "num2", Pressed: true},
		345: {Control: "num2", Pressed: false},
		697: {Control: "num2", Pressed: true},
		710: {Control: "num2", Pressed: false},
		803: {Control: "ok", Pressed: true},
		804: {Control: "ok", Pressed: false},
	}
	for frame := 0; frame < 1200; frame++ {
		if event, ok := events[frame]; ok {
			if err := created.QueueInput(event); err != nil {
				t.Fatalf("input at frame %d: %v", frame, err)
			}
		}
		if err := created.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
