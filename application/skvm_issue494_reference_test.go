package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The last hash press arrives when the game worker has yielded midway through
// drawing the previous menu. The worker must finish its ready slice before the
// input callback clears the menu widgets.
func TestSKVMIssue494InputAfterWorkerQuantum(t *testing.T) {
	const digest = "d1dce4a3614196a6255c898bcdf121a7c4dd656e25925bdd9f8be8768a2ce6d0"
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
		222: {Control: "hash", Pressed: true},
		243: {Control: "hash", Pressed: false},
		253: {Control: "num4", Pressed: true},
		254: {Control: "num4", Pressed: false},
		263: {Control: "ok", Pressed: true},
		264: {Control: "ok", Pressed: false},
		290: {Control: "send", Pressed: true},
		291: {Control: "send", Pressed: false},
		292: {Control: "hash", Pressed: true},
		293: {Control: "hash", Pressed: false},
	}
	for frame := 0; frame < 350; frame++ {
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
